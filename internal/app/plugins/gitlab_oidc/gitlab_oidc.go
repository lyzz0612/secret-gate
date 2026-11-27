// Package gitlab_oidc 是 GitLab CI ID token 认证插件类型。
// issuer 是 GitLab 实例的域名（gitlab.com 或自建），**必须由实例配置**——
// 自建实例的签发者各不相同，写不进代码常量。
package gitlab_oidc

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/lyzz0612/deploy-center/internal/app/plugins"
	"github.com/lyzz0612/deploy-center/internal/domain"
	"github.com/lyzz0612/deploy-center/internal/infra/oidc"
)

const typeName = "gitlab_oidc"

var errMissingProjectID = errors.New("令牌缺少 project_id")

// Plugin 实现 plugins.PluginType。
type Plugin struct {
	v *oidc.Verifier
}

var _ plugins.PluginType = (*Plugin)(nil)

func New() *Plugin { return &Plugin{v: oidc.NewVerifier()} }

func (p *Plugin) Type() string  { return typeName }
func (p *Plugin) Label() string { return "GitLab CI (ID Token)" }
func (p *Plugin) Builtin() bool { return false }

// FindUser 实例型插件不适用（用户由过准入后自动登记）。
func (p *Plugin) FindUser(ctx context.Context, f plugins.UserFinder, p2 *plugins.Parsed) (*domain.User, error) {
	return nil, nil
}

func (p *Plugin) Match(r *http.Request) bool {
	tok := oidc.Bearer(r)
	return tok != "" && strings.Count(tok, ".") == 2
}

// ConfigFields issuer 必填；jwks_url 只在老版本自建实例不发 OIDC discovery 时需要。
func (p *Plugin) ConfigFields() []plugins.Field {
	return []plugins.Field{
		{
			Key: "issuer", Label: "GitLab 地址（issuer）", Type: "text", Required: true,
			Placeholder: "https://gitlab.example.com",
			Help:        "令牌 iss 必须整串等于它；公钥由 {issuer}/.well-known/openid-configuration 自动发现",
		},
		{
			Key: "jwks_url", Label: "公钥地址（可选）", Type: "text",
			Placeholder: "https://gitlab.example.com/oauth/discovery/keys",
			Help:        "仅当实例不发布 OIDC discovery 时手填；留空自动发现",
		},
	}
}

func (p *Plugin) ClaimFields() []plugins.ClaimField {
	return []plugins.ClaimField{
		{Key: "aud", Label: "Audience（id_tokens 里配的值）", Example: "https://secrets.example.com"},
		{Key: "project_id", Label: "项目 ID（数字，改名不变）", Example: "20"},
		{Key: "project_path", Label: "项目路径", Example: "my-group/my-project"},
		{Key: "namespace_id", Label: "命名空间 ID", Example: "72"},
		{Key: "namespace_path", Label: "命名空间路径", Example: "my-group"},
		{Key: "ref", Label: "分支/标签名（不含 refs/）", Example: "main"},
		{Key: "ref_path", Label: "完整引用路径", Example: "refs/heads/main"},
		{Key: "ref_type", Label: "引用类型", Example: "branch"},
		{Key: "ref_protected", Label: "是否受保护分支", Example: "true"},
		{Key: "pipeline_source", Label: "流水线来源", Example: "merge_request_event"},
		{Key: "environment", Label: "环境（仅声明了 environment 的 job）", Example: "production"},
		{Key: "environment_protected", Label: "环境是否受保护", Example: "true"},
		{Key: "deployment_tier", Label: "环境层级", Example: "production"},
		{Key: "job_id", Label: "job ID", Example: "302"},
		{Key: "pipeline_id", Label: "流水线 ID", Example: "574"},
		{Key: "user_login", Label: "触发人", Example: "sample-user"},
		{Key: "runner_environment", Label: "runner 类型", Example: "self-hosted"},
		{Key: "project_visibility", Label: "项目可见性", Example: "private"},
		{Key: "sub", Label: "Subject（项目可自定义，别当稳定键）", Example: "project_path:my-group/my-project:ref_type:branch:ref:main"},
	}
}

// Parse 验签 + 时间窗，取出 project_id 作为身份键。
// 身份键带 issuer 主机前缀：自建与 gitlab.com 的 project_id 各自独立编号，
// 不带前缀会在「同类型两个实例」时把两个不同项目撞成同一个用户。
func (p *Plugin) Parse(ctx context.Context, r *http.Request, cfg map[string]any) (*plugins.Parsed, error) {
	issuer := plugins.ConfigString(cfg, "issuer")
	claims, err := p.v.Verify(ctx, oidc.Bearer(r), issuer, plugins.ConfigString(cfg, "jwks_url"))
	if err != nil {
		return nil, err
	}
	projID := claims["project_id"]
	if projID == "" {
		return nil, errMissingProjectID
	}
	extID := projID
	if host := issuerHost(issuer); host != "" {
		extID = host + "#" + projID
	}
	disp := claims["project_path"]
	if disp == "" {
		disp = projID
	}
	return &plugins.Parsed{ExtID: extID, Disp: disp, Kind: "machine", Claims: claims}, nil
}

// Accept 实例级核对已并入 Parse（issuer 来自实例配置）与准入规则。
func (p *Plugin) Accept(ctx context.Context, parsed *plugins.Parsed, cfg map[string]any) error {
	return nil
}

// issuerHost 取签发者的主机名（含端口），用作身份键的命名空间。
func issuerHost(issuer string) string {
	u, err := url.Parse(issuer)
	if err != nil {
		return ""
	}
	if u.Host != "" {
		return u.Host
	}
	return strings.TrimRight(issuer, "/")
}
