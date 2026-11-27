// Package password 是账号口令认证插件类型：给非 GitHub 的调用方
// （其他 CI、脚本、人）用 Basic auth 取密钥。实例配置 username + password，
// 准入规则照常可匹配 username。凭证只做常量时间比对，绝不落日志。
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

func (p *Plugin) Type() string  { return typeName }
func (p *Plugin) Label() string { return "账号口令 (Basic Auth)" }

func (p *Plugin) Match(r *http.Request) bool {
	return basicUserPass(r) != nil
}

func (p *Plugin) ConfigFields() []plugins.Field {
	return []plugins.Field{
		{
			Key:      "username",
			Label:    "用户名",
			Help:     "调用方在 Basic auth 里用的用户名",
			Type:     "text",
			Required: true,
		},
		{
			Key:      "password",
			Label:    "口令",
			Help:     "调用方在 Basic auth 里用的口令；只做常量时间比对",
			Type:     "text",
			Required: true,
			Secret:   true,
		},
	}
}

func (p *Plugin) ClaimFields() []plugins.ClaimField {
	return []plugins.ClaimField{
		{Key: "username", Label: "用户名", Example: "ci-runner"},
	}
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

// Accept 常量时间比对用户名与口令。
func (p *Plugin) Accept(ctx context.Context, parsed *plugins.Parsed, cfg map[string]any) error {
	wantUser, _ := cfg["username"].(string)
	wantPass, _ := cfg["password"].(string)
	if wantUser == "" || wantPass == "" {
		return errors.New("实例缺少 username / password 配置")
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
