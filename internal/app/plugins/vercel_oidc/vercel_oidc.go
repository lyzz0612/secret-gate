// Package vercel_oidc 是 Vercel OIDC Federation 令牌认证插件类型。
// issuer 形如 https://oidc.vercel.com/<team>（team 模式）或 https://oidc.vercel.com（global 模式），
// 因团队而异，**必须由实例配置**。
package vercel_oidc

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/lyzz0612/deploy-center/internal/app/plugins"
	"github.com/lyzz0612/deploy-center/internal/domain"
	"github.com/lyzz0612/deploy-center/internal/infra/oidc"
)

const typeName = "vercel_oidc"

var errMissingProjectID = errors.New("令牌缺少 project_id")

// Plugin 实现 plugins.PluginType。
type Plugin struct {
	v *oidc.Verifier
}

var _ plugins.PluginType = (*Plugin)(nil)

func New() *Plugin { return &Plugin{v: oidc.NewVerifier()} }

func (p *Plugin) Type() string  { return typeName }
func (p *Plugin) Label() string { return "Vercel (OIDC Federation)" }
func (p *Plugin) Builtin() bool { return false }

// FindUser 实例型插件不适用（用户由过准入后自动登记）。
func (p *Plugin) FindUser(ctx context.Context, f plugins.UserFinder, p2 *plugins.Parsed) (*domain.User, error) {
	return nil, nil
}

func (p *Plugin) Match(r *http.Request) bool {
	tok := oidc.Bearer(r)
	return tok != "" && strings.Count(tok, ".") == 2
}

func (p *Plugin) ConfigFields() []plugins.Field {
	return []plugins.Field{
		{
			Key: "issuer", Label: "Vercel 签发者（issuer）", Type: "text", Required: true,
			Placeholder: "https://oidc.vercel.com/acme",
			Help:        "team 模式填 https://oidc.vercel.com/<team>；global 模式填 https://oidc.vercel.com。请求方自定义 aud 时 iss 会变成 team 形态",
		},
		{
			Key: "jwks_url", Label: "公钥地址（可选）", Type: "text",
			Placeholder: "https://oidc.vercel.com/acme/.well-known/jwks",
			Help:        "留空则按 OIDC discovery 自动发现",
		},
	}
}

func (p *Plugin) ClaimFields() []plugins.ClaimField {
	return []plugins.ClaimField{
		{Key: "aud", Label: "Audience（默认 https://vercel.com/<team>）", Example: "https://vercel.com/acme"},
		{Key: "owner", Label: "团队 slug", Example: "acme"},
		{Key: "owner_id", Label: "团队 ID", Example: "team_7Gw5ZMzpQA8h90F832KGp7nwbuh3"},
		{Key: "project", Label: "项目名", Example: "acme_website"},
		{Key: "project_id", Label: "项目 ID（prj_…，全局唯一）", Example: "prj_7Gw5ZMBpQA8h9GF832KGp7nwbuh3"},
		{Key: "environment", Label: "环境（production/preview/development/自定义）", Example: "production"},
		{Key: "custom_environment_id", Label: "自定义环境 ID", Example: "env_9f2a"},
		{Key: "user_id", Label: "用户 ID（仅 development 环境）", Example: "usr_9f2a"},
		{Key: "sub", Label: "Subject（含 environment，别当稳定键）", Example: "owner:acme:project:acme_website:environment:production"},
	}
}

// Parse 验签 + 时间窗，取出 project_id 作为身份键（Vercel 全局唯一，稳定于改名）。
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
	disp := claims["owner"] + "/" + claims["project"]
	if disp == "/" {
		disp = projID
	}
	return &plugins.Parsed{ExtID: projID, Disp: disp, Kind: "machine", Claims: claims}, nil
}

// Accept 实例级核对已并入 Parse（issuer 来自实例配置）与准入规则。
func (p *Plugin) Accept(ctx context.Context, parsed *plugins.Parsed, cfg map[string]any) error {
	return nil
}
