// Package github_oidc 是 GitHub Actions OIDC 认证插件类型。
// runner 自己向 GitHub 换取一次性 JWT，本插件只做验签与声明提取。
// issuer 是代码级常量（GitHub 公有云只有一个签发者）；要接 GHES 派生一个换掉它的类型。
package github_oidc

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/lyzz0612/deploy-center/internal/app/plugins"
	"github.com/lyzz0612/deploy-center/internal/domain"
	"github.com/lyzz0612/deploy-center/internal/infra/oidc"
)

var errMissingRepoID = errors.New("令牌缺少 repository_id")

// DefaultIssuer GitHub 公有云 OIDC 签发者，代码写死。
const DefaultIssuer = "https://token.actions.githubusercontent.com"

const typeName = "github_oidc"

// Plugin 实现 plugins.PluginType。
type Plugin struct {
	issuer string
	v      *oidc.Verifier
}

var _ plugins.PluginType = (*Plugin)(nil)

// New GitHub 公有云插件。
func New() *Plugin { return NewWithIssuer(DefaultIssuer) }

// NewWithIssuer 指定签发者（测试与 GHES 派生插件用；后台实例不可配）。
func NewWithIssuer(issuer string) *Plugin {
	return &Plugin{issuer: issuer, v: oidc.NewVerifier()}
}

func (p *Plugin) Type() string  { return typeName }
func (p *Plugin) Label() string { return "GitHub Actions (OIDC)" }
func (p *Plugin) Builtin() bool { return false }

// FindUser 实例型插件不适用（用户由过准入后自动登记）。
func (p *Plugin) FindUser(ctx context.Context, f plugins.UserFinder, p2 *plugins.Parsed) (*domain.User, error) {
	return nil, nil
}

func (p *Plugin) Match(r *http.Request) bool {
	tok := oidc.Bearer(r)
	return tok != "" && strings.Count(tok, ".") == 2
}

// ConfigFields 空：issuer 写死、aud 是普通 claim 由准入规则匹配，实例无配置项。
func (p *Plugin) ConfigFields() []plugins.Field {
	return nil
}

func (p *Plugin) ClaimFields() []plugins.ClaimField {
	return []plugins.ClaimField{
		{Key: "aud", Label: "Audience（令牌铸给谁）", Example: "https://secrets.example.com"},
		{Key: "repository_owner", Label: "仓库所属者", Example: "lyzz0612"},
		{Key: "repository", Label: "仓库全名", Example: "lyzz0612/secret-gate"},
		{Key: "repository_id", Label: "仓库 ID（数字，改名不变）", Example: "987654321"},
		{Key: "ref", Label: "触发分支/标签", Example: "refs/heads/main"},
		{Key: "ref_type", Label: "引用类型", Example: "branch"},
		{Key: "event_name", Label: "触发事件", Example: "push"},
		{Key: "environment", Label: "GHA environment", Example: "production"},
		{Key: "workflow", Label: "workflow 名称", Example: "release"},
		{Key: "actor", Label: "触发人", Example: "lyzz0612"},
		{Key: "repository_visibility", Label: "仓库可见性", Example: "private"},
		{Key: "runner_environment", Label: "runner 类型", Example: "github-hosted"},
	}
}

// Parse 验签 + 时间窗，取出 repository_id 作为身份键。
// cfg 对 GitHub 无意义（实例无配置项），保留参数以统一契约。
func (p *Plugin) Parse(ctx context.Context, r *http.Request, cfg map[string]any) (*plugins.Parsed, error) {
	claims, err := p.v.Verify(ctx, oidc.Bearer(r), p.issuer, "")
	if err != nil {
		return nil, err
	}
	repoID, ok := claims["repository_id"]
	if !ok || repoID == "" {
		return nil, errMissingRepoID
	}
	disp := claims["repository"]
	if disp == "" {
		disp = repoID
	}
	return &plugins.Parsed{ExtID: repoID, Disp: disp, Kind: "machine", Claims: claims}, nil
}

// Accept 实例级核对已并入 Parse（issuer 写死）与准入规则（aud 是普通 claim）。
func (p *Plugin) Accept(ctx context.Context, parsed *plugins.Parsed, cfg map[string]any) error {
	return nil
}
