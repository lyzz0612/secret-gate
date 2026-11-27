// Package appkey 是内置的 AppKey 用户类型：后台创建用户时自动生成
// `ak_` 开头的长随机凭证（可随时查看与轮换），调用方拿它调 /v1/auth 换短期 token。
// 认证按 sha256(appkey) 命中用户行唯一索引——命中即通过，不需要比对明文。
package appkey

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"

	"github.com/lyzz0612/deploy-center/internal/app/plugins"
	"github.com/lyzz0612/deploy-center/internal/domain"
)

const (
	typeName = "appkey"
	// AppKeyHeader 独立认证头，与 Bearer JWT / Basic 井水不河水。
	AppKeyHeader = "X-App-Key"
	keyPrefix    = "ak_"
	// keyBodyLen 随机部分字节数（base64 无 padding 后 43 字符）。
	keyBodyLen = 32
	// minKeyLen 参与认证的 key 最短长度（前缀 + 32B base64 = 46）。
	minKeyLen = 20
)

// Plugin 实现 plugins.PluginType。
type Plugin struct{}

var _ plugins.PluginType = (*Plugin)(nil)

func New() *Plugin { return &Plugin{} }

func (p *Plugin) Type() string  { return typeName }
func (p *Plugin) Label() string { return "AppKey（应用密钥）" }
func (p *Plugin) Builtin() bool { return true }

func (p *Plugin) Match(r *http.Request) bool {
	return r.Header.Get(AppKeyHeader) != ""
}

// ConfigFields 内置类型没有实例配置。
func (p *Plugin) ConfigFields() []plugins.Field {
	return nil
}

func (p *Plugin) ClaimFields() []plugins.ClaimField {
	return nil
}

// Parse 提取 AppKey。ExtID 放 sha256 hex（用户行 cred_lookup 按它命中），
// 原始 key 绝不进 claims、不写日志。
func (p *Plugin) Parse(ctx context.Context, r *http.Request) (*plugins.Parsed, error) {
	key := r.Header.Get(AppKeyHeader)
	if key == "" {
		return nil, errors.New("缺少 " + AppKeyHeader + " 头")
	}
	if len(key) < minKeyLen || len(key) > 256 {
		return nil, errors.New("AppKey 长度非法")
	}
	return &plugins.Parsed{
		ExtID:  HashKey(key),
		Kind:   "machine",
		Claims: map[string]string{},
	}, nil
}

// FindUser 按凭证哈希索引定位用户行（命中 = 验证通过；nil, nil = 无此 key）。
func (p *Plugin) FindUser(ctx context.Context, f plugins.UserFinder, parsed *plugins.Parsed) (*domain.User, error) {
	return f.UserByCredLookup(ctx, parsed.ExtID)
}

// Accept 无事可做：FindUser 按唯一索引命中已经完成验证（拿到 hash 等价于拿到正确 key）。
func (p *Plugin) Accept(ctx context.Context, parsed *plugins.Parsed, cfg map[string]any) error {
	return nil
}

// HashKey 凭证哈希（用户行 cred_lookup 与 Parse 的 ExtID 共用）。
func HashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// Generate 生成一个新 AppKey：`ak_` + 32 字节随机（base64 无 padding，43 字符）。
func Generate() (plain, lookup string, err error) {
	buf := make([]byte, keyBodyLen)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	plain = keyPrefix + base64.RawStdEncoding.EncodeToString(buf)
	return plain, HashKey(plain), nil
}

// RandomPassword 轮换账号口令时生成随机密码（22 字符，无易混字符集外符号）。
func RandomPassword() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
