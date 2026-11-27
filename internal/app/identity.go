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
	// ErrUnknownProvider 接入标识（实例名）不存在或已停用——调用方配置错，直接点名。
	ErrUnknownProvider = errors.New("未知接入标识")
)

// Rejection 一次被拒的认证尝试（内存环形，只留最近 50 条，重启即清）。
type Rejection struct {
	TS       time.Time `json:"ts"`
	Type     string    `json:"type"`
	ExtID    string    `json:"ext_id"`
	Display  string    `json:"display"`
	Instance string    `json:"instance"`
	Reason   string    `json:"reason"`
	SourceIP string    `json:"source_ip"`
}

// AuthOutcome 一次成功通过认证 + 准入的结果。
// 内置型（appkey/password）：User 直接命中（凭证在用户行上，无需登记）。
// 实例型（OIDC）：User 为 nil，由 ResolveUser 自动登记。
type AuthOutcome struct {
	User     *domain.User
	Instance domain.ProviderInstance
	Parsed   *plugins.Parsed
	Results  []domain.RuleResult
}

// IdentityService 认证编排。
// 两条路径：
//   - 带接入标识（POST /v1/auth/{name}）：只认那一条实例，用它配置里的 issuer 验签；
//   - 不带：类型匹配 → 逐实例试（每个实例各自的 issuer），第一个全过的胜出。
//
// 后者保留给存量 GitHub 调用方，也让"忘带标识"的请求仍有机会被接纳。
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

// Authenticate 走完整认证链。providerName 非空时走"指名"路径。
// 没有任何通过 → ErrNotAdmitted（403），令牌解析/验签失败 → ErrTokenInvalid（401）。
func (s *IdentityService) Authenticate(ctx context.Context, r *http.Request, providerName string) (*AuthOutcome, error) {
	if providerName != "" {
		return s.authenticateNamed(ctx, r, providerName)
	}
	return s.authenticateAny(ctx, r)
}

// authenticateNamed 指名路径：只认这一条实例。
func (s *IdentityService) authenticateNamed(ctx context.Context, r *http.Request, name string) (*AuthOutcome, error) {
	ip := sourceIP(r)
	inst, err := s.repo.ProviderByName(ctx, name)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			s.recordRejection(Rejection{
				TS: s.now(), Instance: name, Reason: "未知接入标识", SourceIP: ip,
			})
			return nil, fmt.Errorf("%w: %q", ErrUnknownProvider, name)
		}
		return nil, err
	}
	if !inst.Enabled {
		s.recordRejection(Rejection{
			TS: s.now(), Type: inst.Type, Instance: inst.Name, Reason: "实例已停用", SourceIP: ip,
		})
		return nil, fmt.Errorf("%w: %q 已停用", ErrUnknownProvider, name)
	}
	pt, ok := s.registry.Get(inst.Type)
	if !ok {
		return nil, fmt.Errorf("实例 %q 的类型 %q 已不存在", inst.Name, inst.Type)
	}

	cfg, err := decodeConfig(inst.ConfigJSON)
	if err != nil {
		s.recordRejection(Rejection{
			TS: s.now(), Type: inst.Type, Instance: inst.Name, Reason: "实例配置损坏", SourceIP: ip,
		})
		return nil, fmt.Errorf("实例 %q 的配置损坏: %w", inst.Name, err)
	}
	parsed, err := pt.Parse(ctx, r, cfg)
	if err != nil {
		s.recordRejection(Rejection{
			TS: s.now(), Type: inst.Type, Instance: inst.Name, Reason: err.Error(), SourceIP: ip,
		})
		return nil, fmt.Errorf("%w: %v", ErrTokenInvalid, err)
	}
	parsed.Type = pt.Type()

	rules, err := domain.Compile(decodeRules(inst.RulesJSON))
	if err != nil {
		return nil, fmt.Errorf("实例 %q 的准入规则编译失败: %w", inst.Name, err)
	}
	okRule, results := rules.Match(parsed.Claims)
	if !okRule {
		reason := ruleMissReason(results)
		s.recordRejection(Rejection{
			TS: s.now(), Type: pt.Type(), ExtID: parsed.ExtID, Display: parsed.Disp,
			Instance: inst.Name, Reason: reason, SourceIP: ip,
		})
		return nil, fmt.Errorf("%w: %s", ErrNotAdmitted, reason)
	}
	return &AuthOutcome{Instance: *inst, Parsed: parsed, Results: results}, nil
}

// authenticateAny 未指名：类型匹配 → 该类型逐实例试（每个实例用自己的 issuer 验签）。
func (s *IdentityService) authenticateAny(ctx context.Context, r *http.Request) (*AuthOutcome, error) {
	types := s.registry.MatchTypes(r)
	if len(types) == 0 {
		return nil, ErrNoPlugin
	}
	instances, err := s.repo.ListProviders(ctx)
	if err != nil {
		return nil, err
	}

	ip := sourceIP(r)
	var lastParseErr error
	parsedAny := false

	for _, typ := range types {
		// 内置型：凭证挂用户行，没有实例。
		if typ.Builtin() {
			parsed, err := typ.Parse(ctx, r, nil)
			if err != nil {
				lastParseErr = err
				s.recordRejection(Rejection{
					TS: s.now(), Type: typ.Type(), Reason: err.Error(), SourceIP: ip,
				})
				continue
			}
			parsed.Type = typ.Type()
			out, err := s.authBuiltin(ctx, typ, parsed, ip)
			if err != nil {
				return nil, err
			}
			if out != nil {
				return out, nil
			}
			continue
		}

		// 实例型：该类型的启用实例按 id 顺序逐个试
		tried := 0
		var lastReason string
		for i := range instances {
			inst := instances[i]
			if inst.Type != typ.Type() || !inst.Enabled {
				continue
			}
			tried++
			cfg, err := decodeConfig(inst.ConfigJSON)
			if err != nil {
				lastReason = "实例配置损坏"
				continue
			}
			parsed, err := typ.Parse(ctx, r, cfg)
			if err != nil {
				lastReason = err.Error()
				lastParseErr = err
				continue
			}
			parsedAny = true
			parsed.Type = typ.Type()
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
			TS: s.now(), Type: typ.Type(), Instance: typ.Type(),
			Reason: lastReason + "（未指定接入标识）", SourceIP: ip,
		})
	}

	if !parsedAny && lastParseErr != nil {
		return nil, fmt.Errorf("%w: %v", ErrTokenInvalid, lastParseErr)
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

func decodeConfig(configJSON string) (map[string]any, error) {
	var cfg map[string]any
	if configJSON == "" {
		return map[string]any{}, nil
	}
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return nil, err
	}
	if cfg == nil {
		cfg = map[string]any{}
	}
	return cfg, nil
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
// 实例型（OIDC）：过准入即自动登记，无待批准状态；身份键 = (类型, external_id)，与实例无关。
//
// 自动登记只在此刻写一次：默认分级 = 该实例的 default_role；
// 初始组 = 该实例的 auto_group_ids。之后以后台为准——管理员降级 / 移出组不会被
// 下一次请求静默改回去。
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

	role := domain.RoleGuest
	var groups []int64
	if out.Instance.ID != 0 {
		if out.Instance.DefaultRole != "" {
			role = out.Instance.DefaultRole
		}
		if len(out.Instance.AutoGroupIDs) > 0 {
			groups, err = s.existingGroupIDs(ctx, out.Instance.AutoGroupIDs)
			if err != nil {
				return domain.User{}, false, err
			}
		}
	}

	created, err := s.repo.CreateUserWithGroups(ctx, domain.User{
		ProviderType: out.Parsed.Type,
		ExternalID:   out.Parsed.ExtID,
		Display:      out.Parsed.Disp,
		Kind:         out.Parsed.Kind,
		Role:         role,
		SourceID:     out.Instance.ID,
		CreatedAt:    now,
		LastSeenAt:   now,
	}, groups)
	if err != nil {
		// 并发下可能撞唯一键：再读一次
		if u2, err2 := s.repo.UserByKey(ctx, out.Parsed.Type, out.Parsed.ExtID); err2 == nil {
			return *u2, false, nil
		}
		return domain.User{}, false, err
	}
	return created, true, nil
}

// existingGroupIDs 过滤掉已被删除的组（正常路径下删组会被引用校验拦下，这里只是兜底）。
func (s *IdentityService) existingGroupIDs(ctx context.Context, ids []int64) ([]int64, error) {
	all, err := s.repo.ListUserGroups(ctx)
	if err != nil {
		return nil, err
	}
	alive := map[int64]bool{}
	for _, g := range all {
		alive[g.ID] = true
	}
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if alive[id] {
			out = append(out, id)
		}
	}
	return out, nil
}
