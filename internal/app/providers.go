package app

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/lyzz0612/deploy-center/internal/app/plugins"
	"github.com/lyzz0612/deploy-center/internal/domain"
)

// ProviderService 实例管理：后台可建任意多个，同类型按 id 顺序择优。
type ProviderService struct {
	repo     Repo
	registry *plugins.Registry
	ts       func() string
}

func NewProviderService(repo Repo, registry *plugins.Registry) *ProviderService {
	return &ProviderService{repo: repo, registry: registry, ts: func() string { return nowRFC3339() }}
}

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }

// TypeSummary 给前端的类型元数据（配置字段 + 可匹配字段）。
type TypeSummary struct {
	Type         string           `json:"type"`
	Label        string           `json:"label"`
	ConfigFields []plugins.Field  `json:"config_fields"`
	ClaimFields  []plugins.ClaimField `json:"claim_fields"`
}

// InstanceView 实例 + 类型元数据合并视图。
type InstanceView struct {
	domain.ProviderInstance
	TypeLabel string `json:"type_label"`
}

// ListTypes 全部已注册类型。
func (s *ProviderService) ListTypes() []TypeSummary {
	out := []TypeSummary{}
	for _, p := range s.registry.List() {
		out = append(out, TypeSummary{
			Type: p.Type(), Label: p.Label(),
			ConfigFields: p.ConfigFields(), ClaimFields: p.ClaimFields(),
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
		out = append(out, InstanceView{ProviderInstance: p, TypeLabel: label})
	}
	return out, nil
}

// Create 校验类型存在、必填配置齐全、规则可编译，然后落库。
func (s *ProviderService) Create(ctx context.Context, typ, name string, config map[string]any, rules []domain.Rule) (domain.ProviderInstance, error) {
	if err := validateName(name); err != nil {
		return domain.ProviderInstance{}, err
	}
	pt, ok := s.registry.Get(typ)
	if !ok {
		return domain.ProviderInstance{}, fmt.Errorf("%w: 未知插件类型 %q", ErrInvalid, typ)
	}
	configJSON, err := validateConfig(pt, config)
	if err != nil {
		return domain.ProviderInstance{}, err
	}
	rulesJSON, err := validateRules(rules)
	if err != nil {
		return domain.ProviderInstance{}, err
	}
	return s.repo.CreateProvider(ctx, typ, name, configJSON, rulesJSON, s.ts())
}

// Update 全量更新（name/enabled/config/rules 一起给）。
func (s *ProviderService) Update(ctx context.Context, id int64, name string, enabled bool, config map[string]any, rules []domain.Rule) error {
	if err := validateName(name); err != nil {
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
	return s.repo.UpdateProvider(ctx, id, name, configJSON, rulesJSON, enabled, s.ts())
}

func (s *ProviderService) Delete(ctx context.Context, id int64) error {
	return s.repo.DeleteProvider(ctx, id)
}

// Test 用一段 claims 试跑该实例的准入规则，逐条返回命中情况。
// issuer 的核对在验签阶段写死；aud 就是普通规则字段，claims 里带上即可测。
type TestResult struct {
	Allowed    bool                `json:"allowed"`
	Results    []domain.RuleResult `json:"results"`
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
	out := &TestResult{}
	out.Allowed, out.Results = rules.Match(claims)
	return out, nil
}

// ---------- 校验 ----------

func validateName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: 实例名不能为空", ErrInvalid)
	}
	if len(name) > 64 {
		return fmt.Errorf("%w: 实例名超过 64 字符", ErrInvalid)
	}
	return nil
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
