package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/lyzz0612/deploy-center/internal/app/plugins"
	"github.com/lyzz0612/deploy-center/internal/domain"
)

// 认证链错误：httpapi 据此区分 401 / 403。
var (
	ErrNoPlugin     = errors.New("没有插件能处理该请求")
	ErrTokenInvalid = errors.New("令牌无效")
	ErrNotAdmitted  = errors.New("身份未被接受")
)

// Rejection 一次被拒的认证尝试（内存环形，只留最近 50 条，重启即清）。
type Rejection struct {
	TS        time.Time `json:"ts"`
	Type      string    `json:"type"`
	ExtID     string    `json:"ext_id"`
	Display   string    `json:"display"`
	Instance  string    `json:"instance"`
	Reason    string    `json:"reason"`
	SourceIP  string    `json:"source_ip"`
}

// AuthOutcome 一次成功通过认证 + 准入的结果。
// 内置型（appkey/password）：User 直接命中（凭证在用户行上，无需登记）。
// 实例型（github_oidc）：User 为 nil，由 ResolveUser 自动登记。
type AuthOutcome struct {
	User     *domain.User
	Instance domain.ProviderInstance
	Parsed   *plugins.Parsed
	Results  []domain.RuleResult
}

// IdentityService 认证编排：类型匹配 → Parse → 内置型直查用户行 / 实例型择优。
type IdentityService struct {
	repo     Repo
	registry *plugins.Registry
	creds    credentials
	now      func() time.Time

	mu         sync.Mutex
	rejections []Rejection
}

func NewIdentityService(repo Repo, registry *plugins.Registry, seal Sealer) *IdentityService {
	return &IdentityService{repo: repo, registry: registry, creds: credentials{seal: seal}, now: time.Now}
}

func (s *IdentityService) recordRejection(r Rejection) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rejections = append(s.rejections, r)
	if len(s.rejections) > 50 {
		s.rejections = s.rejections[len(s.rejections)-50:]
	}
}

// Rejections 最近被拒记录（新→旧）。
func (s *IdentityService) Rejections() []Rejection {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Rejection, len(s.rejections))
	copy(out, s.rejections)
	// 反转成新→旧
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func sourceIP(r *http.Request) string {
	// 只作记录用途，拿 RemoteAddr 就够了；反代场景可被伪造，不影响安全判定
	if i := lastIndexByte(r.RemoteAddr, ':'); i > 0 {
		return r.RemoteAddr[:i]
	}
	return r.RemoteAddr
}

func lastIndexByte(s string, b byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// Authenticate 走完整认证链。内置类型直查用户行（查到即通过）；
// 实例类型遍历该类型的启用实例（Accept + 准入规则），返回第一个通过的实例。
// 没有任何通过 → ErrNotAdmitted（403），token 解析失败 → ErrTokenInvalid（401）。
func (s *IdentityService) Authenticate(ctx context.Context, r *http.Request) (*AuthOutcome, error) {
	types := s.registry.MatchTypes(r)
	if len(types) == 0 {
		return nil, ErrNoPlugin
	}

	instances, err := s.repo.ListProviders(ctx)
	if err != nil {
		return nil, err
	}

	ip := sourceIP(r)
	for _, typ := range types {
		parsed, err := typ.Parse(ctx, r)
		if err != nil {
			// 解析/验签失败 = 401 级别，不落入实例择优
			return nil, fmt.Errorf("%w: %v", ErrTokenInvalid, err)
		}
		parsed.Type = typ.Type()

		// 内置型：凭证挂用户行，没有实例。定位 + 比对。
		if typ.Builtin() {
			out, err := s.authBuiltin(ctx, typ, parsed, ip)
			if err != nil {
				return nil, err
			}
			if out != nil {
				return out, nil
			}
			continue
		}

		// 该类型的启用实例按 id 顺序逐个试
		var lastReason string
		tried := 0
		for _, inst := range instances {
			if inst.Type != typ.Type() || !inst.Enabled {
				continue
			}
			tried++
			var cfg map[string]any
			if err := json.Unmarshal([]byte(inst.ConfigJSON), &cfg); err != nil {
				lastReason = "实例配置损坏"
				continue
			}
			if err := typ.Accept(ctx, parsed, cfg); err != nil {
				lastReason = err.Error()
				continue
			}
			rules, err := domain.Compile(decodeRules(inst.RulesJSON))
			if err != nil {
				lastReason = "准入规则编译失败: " + err.Error()
				continue
			}
			ok, results := rules.Match(parsed.Claims)
			if !ok {
				lastReason = ruleMissReason(results)
				continue
			}
			return &AuthOutcome{Instance: inst, Parsed: parsed, Results: results}, nil
		}
		if tried == 0 {
			lastReason = "该类型没有启用的实例"
		}
		s.recordRejection(Rejection{
			TS: s.now(), Type: typ.Type(), ExtID: parsed.ExtID, Display: parsed.Disp,
			Instance: typ.Type(), Reason: lastReason, SourceIP: ip,
		})
	}
	return nil, fmt.Errorf("%w: %s", ErrNotAdmitted, lastNoInstanceReason(instances))
}

// authBuiltin 内置类型的用户行认证：FindUser 定位（appkey 按哈希索引命中即通过；
// password 按用户名找行）→ 需要比对凭证的（password）解密后交给 Accept。
// 返回 nil 表示该类型没通过（已记入被拒面板），调用方继续试下一类型。
func (s *IdentityService) authBuiltin(ctx context.Context, typ plugins.PluginType, parsed *plugins.Parsed, ip string) (*AuthOutcome, error) {
	u, err := typ.FindUser(ctx, builtinUserFinder{repo: s.repo}, parsed)
	if err != nil {
		return nil, err
	}
	reject := func(reason string) *AuthOutcome {
		s.recordRejection(Rejection{
			TS: s.now(), Type: typ.Type(), ExtID: parsed.ExtID, Display: parsed.Disp,
			Instance: typ.Type(), Reason: reason, SourceIP: ip,
		})
		return nil
	}
	if u == nil {
		return reject("凭证无效（无此用户或口令不匹配）"), nil
	}
	cfg := map[string]any{"username": u.ExternalID}
	if len(u.CredCipher) > 0 {
		plain, err := s.creds.open(u)
		if err != nil {
			return nil, err
		}
		cfg[u.CredKind] = plain
	}
	if err := typ.Accept(ctx, parsed, cfg); err != nil {
		return reject("凭证无效（" + err.Error() + "）"), nil
	}
	return &AuthOutcome{User: u, Parsed: parsed}, nil
}

// builtinUserFinder 把 Repo 适配成 plugins.UserFinder（ErrNotFound → nil, nil）。
type builtinUserFinder struct{ repo Repo }

func (f builtinUserFinder) UserByKey(ctx context.Context, typ, extID string) (*domain.User, error) {
	u, err := f.repo.UserByKey(ctx, typ, extID)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	return u, err
}

func (f builtinUserFinder) UserByCredLookup(ctx context.Context, lookup string) (*domain.User, error) {
	u, err := f.repo.UserByCredLookup(ctx, lookup)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	return u, err
}

func decodeRules(rulesJSON string) []domain.Rule {
	var rules []domain.Rule
	_ = json.Unmarshal([]byte(rulesJSON), &rules)
	return rules
}

func ruleMissReason(results []domain.RuleResult) string {
	for _, r := range results {
		if r.Missing {
			return "claim 缺少字段 " + r.Field
		}
	}
	for _, r := range results {
		if !r.Hit {
			return "规则不匹配: " + r.Field + " ~ /" + r.Pattern + "/"
		}
	}
	return "规则不匹配"
}

func lastNoInstanceReason(instances []domain.ProviderInstance) string {
	if len(instances) == 0 {
		return "没有任何 Provider 实例"
	}
	return "全部实例拒绝"
}

// ResolveUser 找到或创建用户并刷新 last_seen。
// 内置型（appkey/password）：Authenticate 已命中用户行，直接返回（后台不建就不存在）。
// 实例型（github_oidc）：过准入即自动登记，无待批准状态；
// 身份键 = (类型, external_id)，与实例无关：同仓库被两个实例接纳也只有一个用户。
func (s *IdentityService) ResolveUser(ctx context.Context, out *AuthOutcome) (domain.User, bool, error) {
	now := s.now().UTC().Format(time.RFC3339)
	if out.User != nil {
		_ = s.repo.TouchUser(ctx, out.User.ID, now)
		return *out.User, false, nil
	}
	u, err := s.repo.UserByKey(ctx, out.Parsed.Type, out.Parsed.ExtID)
	if err == nil {
		_ = s.repo.TouchUser(ctx, u.ID, now)
		return *u, false, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return domain.User{}, false, err
	}
	created, err := s.repo.CreateUser(ctx, domain.User{
		ProviderType: out.Parsed.Type,
		ExternalID:   out.Parsed.ExtID,
		Display:      out.Parsed.Disp,
		Kind:         out.Parsed.Kind,
		Role:         domain.RoleGuest, // 自动登记一律最小权限，要真值去后台提级
		SourceID:     out.Instance.ID,
		CreatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		// 并发下可能撞唯一键：再读一次
		if u2, err2 := s.repo.UserByKey(ctx, out.Parsed.Type, out.Parsed.ExtID); err2 == nil {
			return *u2, false, nil
		}
		return domain.User{}, false, err
	}
	return created, true, nil
}
