// Package plugins 定义认证插件契约（类型），以及编译期注册表。
// 一个"类型"是一段代码：怎么验真、声明哪些配置字段与可匹配字段；
// 一个"实例"(Provider) 是一份数据：配置 + 准入规则，后台可建任意多个。
package plugins

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/lyzz0612/deploy-center/internal/domain"
)

// Field 实例的配置字段声明（后台动态渲染表单用）。
type Field struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Help        string   `json:"help,omitempty"`
	Type        string   `json:"type"` // text | number | bool | enum | list
	Options     []string `json:"options,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Secret      bool     `json:"secret,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
}

// ClaimField 准入规则编辑器能选的可匹配字段。
type ClaimField struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Example string `json:"example,omitempty"`
}

// Parsed 类型级验真后的中间产物：claims 已字符串化，但还没核对实例配置。
type Parsed struct {
	Type   string            // 插件类型名（由 Authenticate 填）
	ExtID  string            // 稳定唯一外部 ID；内置型 = 凭证查找键（appkey 为其 sha256 hex）
	Disp   string            // 展示名
	Kind   string            // machine | human
	Claims map[string]string // 全部字符串化（含 iss/aud）
	Secret string            // 口令类插件的凭证；绝不进 claims、绝不写日志
}

// UserFinder 内置类型定位用户行的仓储视图（app.Repo 由 app 层适配后传入）。
// 语义约定：没找到时返回 (nil, nil)——ErrNotFound 之类的哨兵由适配层吞掉。
type UserFinder interface {
	UserByKey(ctx context.Context, typ, extID string) (*domain.User, error)
	UserByCredLookup(ctx context.Context, lookup string) (*domain.User, error)
}

// PluginType 认证插件类型。
// 两种形态：
//   - 实例型（Builtin=false）：Parse 每请求一次；Accept 对照 Provider 实例配置；
//     过准入规则自动登记用户（github_oidc / gitlab_oidc / vercel_oidc）。
//   - 内置型（Builtin=true）：没有实例，凭证挂后台创建的用户行；FindUser 按 Parse
//     产物定位用户行，Accept 对照用户行的凭证（appkey / password）。
//
// cfg 是"这次请求该用哪份配置"：
//   - 实例型：胜出（或按名字指定）的那条 Provider 的 config_json——OIDC 插件从中读 issuer；
//   - 内置型：Parse 时为 nil，Accept 时是用户行上的凭证。
type PluginType interface {
	Type() string
	Label() string
	// Builtin 是否内置用户类型（不能也不需要创建 Provider 实例）。
	Builtin() bool
	Match(req *http.Request) bool
	ConfigFields() []Field
	ClaimFields() []ClaimField
	Parse(ctx context.Context, req *http.Request, cfg map[string]any) (*Parsed, error)
	Accept(ctx context.Context, p *Parsed, cfg map[string]any) error
	// FindUser 内置类型按 Parse 产物定位用户行；无此用户返回 (nil, nil)。
	// 实例类型返回 (nil, nil)，不会被调用。
	FindUser(ctx context.Context, f UserFinder, p *Parsed) (*domain.User, error)
}

// Registry 编译期注册表。加插件 = 一个包 + main 里一行 Register。
type Registry struct {
	mu    sync.RWMutex
	types map[string]PluginType
	order []string
}

func NewRegistry() *Registry {
	return &Registry{types: map[string]PluginType{}}
}

func (r *Registry) Register(p PluginType) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.types[p.Type()]; dup {
		panic("plugins: 重复注册 " + p.Type())
	}
	r.types[p.Type()] = p
	r.order = append(r.order, p.Type())
}

func (r *Registry) Get(typ string) (PluginType, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.types[typ]
	return p, ok
}

// List 按注册顺序返回全部类型。
func (r *Registry) List() []PluginType {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]PluginType, 0, len(r.order))
	for _, t := range r.order {
		out = append(out, r.types[t])
	}
	return out
}

// MatchTypes 按注册顺序返回声称能处理该请求的类型。
func (r *Registry) MatchTypes(req *http.Request) []PluginType {
	var out []PluginType
	for _, p := range r.List() {
		if p.Match(req) {
			out = append(out, p)
		}
	}
	return out
}

// ConfigString 读实例配置里的字符串项（缺失或类型不符都当空串）。
func ConfigString(cfg map[string]any, key string) string {
	if cfg == nil {
		return ""
	}
	s, _ := cfg[key].(string)
	return strings.TrimSpace(s)
}

// SortedNames 类型名列表（稳定排序，给下拉框用）。
func (r *Registry) SortedNames() []string {
	names := make([]string, 0, len(r.order))
	for _, p := range r.List() {
		names = append(names, p.Type())
	}
	sort.Strings(names)
	return names
}
