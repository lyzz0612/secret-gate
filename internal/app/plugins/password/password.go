// Package password 是内置的账号口令用户类型：后台创建用户（username + 口令），
// 凭证加密挂在用户行上，调 /v1/auth 用 Basic auth 换短期 token。
// 没有 Provider 实例；口令只做常量时间比对，绝不落日志。
package password

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/lyzz0612/deploy-center/internal/app/plugins"
	"github.com/lyzz0612/deploy-center/internal/domain"
)

const (
	typeName    = "password"
	basicPrefix = "Basic "
)

// Plugin 实现 plugins.PluginType。
type Plugin struct{}

var _ plugins.PluginType = (*Plugin)(nil)

// New 返回账号口令插件。
func New() *Plugin { return &Plugin{} }

func (p *Plugin) Type() string   { return typeName }
func (p *Plugin) Label() string  { return "账号口令 (Basic Auth)" }
func (p *Plugin) Builtin() bool  { return true }

func (p *Plugin) Match(r *http.Request) bool {
	return basicUserPass(r) != nil
}

// ConfigFields 内置类型没有实例配置（凭证在用户行上，由后台用户管理维护）。
func (p *Plugin) ConfigFields() []plugins.Field {
	return nil
}

func (p *Plugin) ClaimFields() []plugins.ClaimField {
	return nil
}

func basicUserPass(r *http.Request) *[2]string {
	h := r.Header.Get("Authorization")
	if len(h) <= len(basicPrefix) || !strings.EqualFold(h[:len(basicPrefix)], basicPrefix) {
		return nil
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(h[len(basicPrefix):]))
	if err != nil {
		return nil
	}
	pair := strings.SplitN(string(raw), ":", 2)
	if len(pair) != 2 {
		return nil
	}
	return &[2]string{pair[0], pair[1]}
}

// Parse 从 Basic 头提取用户名与口令。用户名进身份键，口令放 Secret（不进 claims）。
func (p *Plugin) Parse(ctx context.Context, r *http.Request) (*plugins.Parsed, error) {
	cred := basicUserPass(r)
	if cred == nil {
		return nil, errors.New("缺少 Basic 认证头")
	}
	if cred[0] == "" {
		return nil, errors.New("用户名为空")
	}
	return &plugins.Parsed{
		ExtID:  cred[0],
		Disp:   cred[0],
		Kind:   "human",
		Claims: map[string]string{"username": cred[0]},
		Secret: cred[1],
	}, nil
}

// FindUser 按用户名定位用户行（FindUser 语义：没找到返回 nil, nil）。
func (p *Plugin) FindUser(ctx context.Context, f plugins.UserFinder, parsed *plugins.Parsed) (*domain.User, error) {
	return f.UserByKey(ctx, typeName, parsed.ExtID)
}

// Accept 常量时间比对用户名与口令。cfg 来自用户行（app 层解密凭证后构造）。
func (p *Plugin) Accept(ctx context.Context, parsed *plugins.Parsed, cfg map[string]any) error {
	wantUser, _ := cfg["username"].(string)
	wantPass, _ := cfg["password"].(string)
	if wantUser == "" || wantPass == "" {
		return errors.New("用户行缺少凭证配置")
	}
	if parsed.Claims["username"] != wantUser {
		return fmt.Errorf("用户名不匹配")
	}
	if !constEq(parsed.Secret, wantPass) {
		return errors.New("口令不匹配")
	}
	return nil
}

// constEq 先各自 SHA-256 等长化再做常量时间比较（避免比时泄漏长度）。
func constEq(a, b string) bool {
	ha := sha256.Sum256([]byte(a))
	hb := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1
}
