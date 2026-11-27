// Package plugins 定义认证插件契约（类型），以及编译期注册表。
// 一个"类型"是一段代码：怎么验真、声明哪些配置字段与可匹配字段；
// 一个"实例"(Provider) 是一份数据：配置 + 准入规则，后台可建任意多个。
package plugins

import (
	"context"
	"net/http"
	"sort"
	"sync"
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
	ExtID  string            // 稳定唯一外部 ID
	Disp   string            // 展示名
	Kind   string            // machine | human
	Claims map[string]string // 全部字符串化（含 iss/aud）
	Secret string            // 口令类插件的凭证；绝不进 claims、绝不写日志
}

// PluginType 认证插件类型。
// Parse 每请求一次（验签等重活）；Accept 每实例一次（只比对配置项）。
type PluginType interface {
	Type() string
	Label() string
	Match(req *http.Request) bool
	ConfigFields() []Field
	ClaimFields() []ClaimField
	Parse(ctx context.Context, req *http.Request) (*Parsed, error)
	Accept(ctx context.Context, p *Parsed, cfg map[string]any) error
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

// SortedNames 类型名列表（稳定排序，给下拉框用）。
func (r *Registry) SortedNames() []string {
	names := make([]string, 0, len(r.order))
	for _, p := range r.List() {
		names = append(names, p.Type())
	}
	sort.Strings(names)
	return names
}
