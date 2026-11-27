package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/lyzz0612/deploy-center/internal/app/plugins"
	"github.com/lyzz0612/deploy-center/internal/domain"
)

// MaxAutoGroups 一个实例最多配多少个"新用户默认组"。
const MaxAutoGroups = 20

// nameRe 接入标识：CI 侧要把它拼进 URL（POST /v1/auth/{name}），所以只收 URL 安全字符。
var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ProviderService 实例管理：后台可建任意多个，名字即接入标识。
type ProviderService struct {
	repo     Repo
	registry *plugins.Registry
	ts       func() string
}

func NewProviderService(repo Repo, registry *plugins.Registry) *ProviderService {
	return &ProviderService{repo: repo, registry: registry, ts: func() string { return nowRFC3339() }}
}

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }

// TypeSummary 给前端的类型元数据（配置字段 + 可匹配字段 + 是否内置）。
type TypeSummary struct {
	Type         string               `json:"type"`
	Label        string               `json:"label"`
	Builtin      bool                 `json:"builtin"`
	ConfigFields []plugins.Field      `json:"config_fields"`
	ClaimFields  []plugins.ClaimField `json:"claim_fields"`
}

// InstanceView 实例 + 类型元数据合并视图。
type InstanceView struct {
	domain.ProviderInstance
	TypeLabel string `json:"type_label"`
}

// ListTypes 全部已注册类型。列表字段永远是非空数组
// （nil 切片会序列化成 null，前端 .length 直接崩整页——新建实例黑屏的教训）。
func (s *ProviderService) ListTypes() []TypeSummary {
	out := make([]TypeSummary, 0)
	for _, p := range s.registry.List() {
		cfg := p.ConfigFields()
		if cfg == nil {
			cfg = []plugins.Field{}
		}
		claims := p.ClaimFields()
		if claims == nil {
			claims = []plugins.ClaimField{}
		}
		out = append(out, TypeSummary{
			Type:         p.Type(),
			Label:        p.Label(),
			Builtin:      p.Builtin(),
			ConfigFields: cfg,
			ClaimFields:  claims,
		})
	}
	return out
}

func (s *ProviderService) List(ctx context.Context) ([]InstanceView, error) {
	ps, err := s.repo.ListProviders(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]InstanceView, 0, len(ps))
	for _, p := range ps {
		label := p.Type
		if pt, ok := s.registry.Get(p.Type); ok {
			label = pt.Label()
		} else {
			label += "（类型已不存在）"
		}
		if p.AutoGroupIDs == nil {
			p.AutoGroupIDs = []int64{}
		}
		out = append(out, InstanceView{ProviderInstance: p, TypeLabel: label})
	}
	return out, nil
}

// Create 校验类型存在、非内置（内置型凭证挂用户行，没有实例）、必填配置齐全、
// 规则可编译、接入标识可用，然后落库。
func (s *ProviderService) Create(ctx context.Context, typ, name string, config map[string]any,
	rules []domain.Rule, defaultRole string, autoGroupIDs []int64) (domain.ProviderInstance, error) {
	name = strings.TrimSpace(name)
	if err := s.validateName(ctx, name, 0); err != nil {
		return domain.ProviderInstance{}, err
	}
	pt, ok := s.registry.Get(typ)
	if !ok {
		return domain.ProviderInstance{}, fmt.Errorf("%w: 未知插件类型 %q", ErrInvalid, typ)
	}
	if pt.Builtin() {
		return domain.ProviderInstance{}, fmt.Errorf("%w: %q 是内置用户类型——去「用户」页建用户，不建实例", ErrInvalid, typ)
	}
	configJSON, err := validateConfig(pt, config)
	if err != nil {
		return domain.ProviderInstance{}, err
	}
	rulesJSON, err := validateRules(rules)
	if err != nil {
		return domain.ProviderInstance{}, err
	}
	role, groups, err := s.validateProvisioning(ctx, defaultRole, autoGroupIDs)
	if err != nil {
		return domain.ProviderInstance{}, err
	}
	return s.repo.CreateProvider(ctx, domain.ProviderInstance{
		Type: typ, Name: name, ConfigJSON: configJSON, RulesJSON: rulesJSON,
		DefaultRole: role, AutoGroupIDs: groups,
	}, s.ts())
}

// Update 全量更新（name/enabled/config/rules/自动登记一起给）。
func (s *ProviderService) Update(ctx context.Context, id int64, name string, enabled bool, config map[string]any,
	rules []domain.Rule, defaultRole string, autoGroupIDs []int64) error {
	name = strings.TrimSpace(name)
	if err := s.validateName(ctx, name, id); err != nil {
		return err
	}
	inst, err := s.repo.ProviderByID(ctx, id)
	if err != nil {
		return err
	}
	pt, ok := s.registry.Get(inst.Type)
	if !ok {
		return fmt.Errorf("%w: 类型 %q 已不存在，只能删除该实例", ErrInvalid, inst.Type)
	}
	configJSON, err := validateConfig(pt, config)
	if err != nil {
		return err
	}
	rulesJSON, err := validateRules(rules)
	if err != nil {
		return err
	}
	role, groups, err := s.validateProvisioning(ctx, defaultRole, autoGroupIDs)
	if err != nil {
		return err
	}
	inst.Name = name
	inst.Enabled = enabled
	inst.ConfigJSON = configJSON
	inst.RulesJSON = rulesJSON
	inst.DefaultRole = role
	inst.AutoGroupIDs = groups
	return s.repo.UpdateProvider(ctx, *inst, s.ts())
}

func (s *ProviderService) Delete(ctx context.Context, id int64) error {
	return s.repo.DeleteProvider(ctx, id)
}

// Test 用一段 claims 试跑该实例的准入规则，逐条返回命中情况；
// 顺带把"通过后会登记成什么"（分级 + 自动加入的组名）一起回给前端。
type TestResult struct {
	Allowed     bool                `json:"allowed"`
	Results     []domain.RuleResult `json:"results"`
	DefaultRole string              `json:"default_role"`
	AutoGroups  []domain.UserGroup  `json:"auto_groups"`
}

func (s *ProviderService) Test(ctx context.Context, id int64, claims map[string]string) (*TestResult, error) {
	inst, err := s.repo.ProviderByID(ctx, id)
	if err != nil {
		return nil, err
	}
	rules, err := domain.Compile(decodeRules(inst.RulesJSON))
	if err != nil {
		return nil, fmt.Errorf("%w: 规则编译失败: %v", ErrInvalid, err)
	}
	out := &TestResult{DefaultRole: inst.DefaultRole, AutoGroups: []domain.UserGroup{}}
	out.Allowed, out.Results = rules.Match(claims)
	all, err := s.repo.ListUserGroups(ctx)
	if err != nil {
		return nil, err
	}
	want := map[int64]bool{}
	for _, g := range inst.AutoGroupIDs {
		want[g] = true
	}
	for _, g := range all {
		if want[g.ID] {
			out.AutoGroups = append(out.AutoGroups, g)
		}
	}
	return out, nil
}

// ---------- 校验 ----------

// validateName 接入标识：字符集 + 全局唯一（改自己时排除自己）。
func (s *ProviderService) validateName(ctx context.Context, name string, selfID int64) error {
	if name == "" {
		return fmt.Errorf("%w: 实例名（接入标识）不能为空", ErrInvalid)
	}
	if !nameRe.MatchString(name) {
		return fmt.Errorf("%w: 接入标识只能用小写/大写字母、数字、点、下划线、连字符，且不超过 64 字符（CI 侧要拼进 URL）", ErrInvalid)
	}
	ps, err := s.repo.ListProviders(ctx)
	if err != nil {
		return err
	}
	for _, p := range ps {
		if p.Name == name && p.ID != selfID {
			return fmt.Errorf("%w: 接入标识 %q 已被实例使用", ErrConflict, name)
		}
	}
	return nil
}

// validateProvisioning 校验"验过之后怎么落地"：分级枚举 + 组必须存在。
func (s *ProviderService) validateProvisioning(ctx context.Context, role string, groupIDs []int64) (string, []int64, error) {
	if role == "" {
		role = domain.RoleGuest
	}
	if role != domain.RoleGuest && role != domain.RoleNormal {
		return "", nil, fmt.Errorf("%w: 默认分级只能是 guest / normal", ErrInvalid)
	}
	if len(groupIDs) > MaxAutoGroups {
		return "", nil, fmt.Errorf("%w: 自动加入的用户组超过 %d 个", ErrInvalid, MaxAutoGroups)
	}
	out := make([]int64, 0, len(groupIDs))
	seen := map[int64]bool{}
	for _, gid := range groupIDs {
		if seen[gid] {
			continue
		}
		seen[gid] = true
		if _, err := s.repo.UserGroupByID(ctx, gid); err != nil {
			if errors.Is(err, ErrNotFound) {
				return "", nil, fmt.Errorf("%w: 用户组 %d 不存在", ErrInvalid, gid)
			}
			return "", nil, err
		}
		out = append(out, gid)
	}
	return role, out, nil
}

func validateConfig(pt plugins.PluginType, config map[string]any) (string, error) {
	if config == nil {
		config = map[string]any{}
	}
	declared := map[string]bool{}
	for _, f := range pt.ConfigFields() {
		declared[f.Key] = true
	}
	for k := range config {
		if !declared[k] {
			return "", fmt.Errorf("%w: 插件 %s 没有配置项 %q（实例不接受的键）", ErrInvalid, pt.Type(), k)
		}
	}
	for _, f := range pt.ConfigFields() {
		v, ok := config[f.Key]
		if !f.Required {
			continue
		}
		s, _ := v.(string)
		if !ok || s == "" {
			return "", fmt.Errorf("%w: 缺少必填配置 %s（%s）", ErrInvalid, f.Key, f.Label)
		}
	}
	b, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("%w: 配置序列化失败: %v", ErrInvalid, err)
	}
	return string(b), nil
}

func validateRules(rules []domain.Rule) (string, error) {
	if _, err := domain.Compile(rules); err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	b, err := json.Marshal(rules)
	if err != nil {
		return "", fmt.Errorf("%w: 规则序列化失败: %v", ErrInvalid, err)
	}
	return string(b), nil
}
