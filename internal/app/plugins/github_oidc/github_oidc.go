// Package github_oidc 是 GitHub Actions OIDC 认证插件类型。
// runner 自己向 GitHub 换取一次性 JWT，本插件只做验签与声明提取。
// Parse：RS256 验签 + JWKS + 时间窗（每请求一次）
// Accept：iss / aud 与实例配置比对（每实例一次）
package github_oidc

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/lyzz0612/deploy-center/internal/app/plugins"
)

// DefaultIssuer GitHub 公有云 OIDC 签发者，代码写死。
const DefaultIssuer = "https://token.actions.githubusercontent.com"

const (
	jwksCacheTTL     = time.Hour
	jwksMinRefetch   = 10 * time.Second // 未知 kid 触发重拉的最小间隔，防 JWKS 放大攻击
	clockLeeway      = 60 * time.Second
	typeName         = "github_oidc"
	bearerPrefix     = "Bearer "
	maxTokenBodySize = 64 * 1024
)

// Plugin 实现 plugins.PluginType。
type Plugin struct {
	// issuer 是代码级常量（默认 GitHub 公有云）。
	// 实例不配置签发者；要接 GitHub Enterprise Server 就写一个换掉它的插件类型。
	issuer string

	httpClient *http.Client
	now        func() time.Time
	jwks       *jwksCache
}

var _ plugins.PluginType = (*Plugin)(nil)

// New GitHub 公有云插件。
func New() *Plugin { return NewWithIssuer(DefaultIssuer) }

// NewWithIssuer 指定签发者（测试与 GHES 派生插件用；后台实例不可配）。
func NewWithIssuer(issuer string) *Plugin {
	return &Plugin{
		issuer:     strings.TrimRight(issuer, "/"),
		httpClient: &http.Client{Timeout: 10 * time.Second},
		now:        time.Now,
		jwks:       newJWKSCache(),
	}
}

func (p *Plugin) Type() string  { return typeName }
func (p *Plugin) Label() string  { return "GitHub Actions (OIDC)" }
func (p *Plugin) Match(r *http.Request) bool {
	tok := bearer(r)
	return strings.Count(tok, ".") == 2
}

// ConfigFields 空：issuer 写死、aud 是普通 claim 由准入规则匹配，实例无配置项。
func (p *Plugin) ConfigFields() []plugins.Field {
	return nil
}

func (p *Plugin) ClaimFields() []plugins.ClaimField {
	return []plugins.ClaimField{
		{Key: "aud", Label: "Audience（令牌铸给谁）", Example: "https://secrets.example.com"},
		{Key: "repository_owner", Label: "仓库所属者", Example: "lyzz0612"},
		{Key: "repository", Label: "仓库全名", Example: "lyzz0612/minigames"},
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

// bearer 提取 Authorization: Bearer <token>。
func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > len(bearerPrefix) && strings.EqualFold(h[:len(bearerPrefix)], bearerPrefix) {
		return strings.TrimSpace(h[len(bearerPrefix):])
	}
	return ""
}

// ---------- Parse：验签 + 时间窗 ----------

func (p *Plugin) Parse(ctx context.Context, r *http.Request) (*plugins.Parsed, error) {
	tok := bearer(r)
	if tok == "" || len(tok) > maxTokenBodySize {
		return nil, errors.New("缺少 Bearer 令牌")
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return nil, errors.New("令牌格式非法")
	}

	var hdr struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
		Typ string `json:"typ"`
	}
	if err := decodeSegment(parts[0], &hdr); err != nil {
		return nil, fmt.Errorf("令牌头解析失败: %w", err)
	}
	// 显式拒绝：GitHub 只用 RS256
	if hdr.Alg != "RS256" {
		return nil, fmt.Errorf("算法 %q 不被接受（仅 RS256）", hdr.Alg)
	}
	if hdr.Kid == "" {
		return nil, errors.New("令牌缺少 kid")
	}

	var claims map[string]any
	if err := decodeSegmentNum(parts[1], &claims); err != nil {
		return nil, fmt.Errorf("令牌负载解析失败: %w", err)
	}

	// 签发者核对：iss 必须等于插件写死的值。
	// 先核对再拉 JWKS——否则令牌里的 iss 能把服务引向任意 URL。
	iss, _ := claims["iss"].(string)
	if iss != p.issuer {
		return nil, fmt.Errorf("签发者 %q 不是 %s", iss, p.issuer)
	}
	jwksURL := p.issuer + "/.well-known/jwks"
	pub, err := p.jwks.publicKey(ctx, p.httpClient, jwksURL, hdr.Kid)
	if err != nil {
		return nil, fmt.Errorf("获取签名公钥失败: %w", err)
	}

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("签名解码失败: %w", err)
	}
	signingInput := parts[0] + "." + parts[1]
	digest := sha256.Sum256([]byte(signingInput))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig); err != nil {
		return nil, errors.New("签名验证失败")
	}

	now := p.now()
	// exp 必须存在且未过期（带时钟偏移）
	exp, ok := numFloat(claims["exp"])
	if !ok {
		return nil, errors.New("令牌缺少 exp")
	}
	if now.After(time.Unix(int64(exp), 0).Add(clockLeeway)) {
		return nil, errors.New("令牌已过期")
	}
	// nbf（如有）不能晚于当前时间太久
	if nbf, ok := numFloat(claims["nbf"]); ok {
		if time.Unix(int64(nbf), 0).After(now.Add(clockLeeway)) {
			return nil, errors.New("令牌尚未生效")
		}
	}

	// claims 字符串化（数字用 json.Number 保精度）
	out := make(map[string]string, len(claims))
	for k, v := range claims {
		switch x := v.(type) {
		case string:
			out[k] = x
		case json.Number:
			out[k] = x.String()
		case bool:
			if x {
				out[k] = "true"
			} else {
				out[k] = "false"
			}
		case []any: // aud 等数组：取第一个元素
			if len(x) > 0 {
				if s, ok := x[0].(string); ok {
					out[k] = s
				}
			}
		case nil:
			// 跳过
		}
	}

	repoID, ok := out["repository_id"]
	if !ok || repoID == "" {
		return nil, errors.New("令牌缺少 repository_id")
	}
	disp := out["repository"]
	if disp == "" {
		disp = repoID
	}
	return &plugins.Parsed{
		ExtID:  repoID,
		Disp:   disp,
		Kind:   "machine",
		Claims: out,
	}, nil
}

// Accept 实例级核对已并入 Parse（issuer 写死）与准入规则（aud 是普通 claim）。
// 保留空实现以满足插件契约。
func (p *Plugin) Accept(ctx context.Context, parsed *plugins.Parsed, cfg map[string]any) error {
	return nil
}

func numFloat(v any) (float64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	f, err := n.Float64()
	return f, err == nil
}

// ---------- 工具 ----------

func decodeSegment(seg string, v any) error {
	b, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func decodeSegmentNum(seg string, v any) error {
	b, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.UseNumber()
	return dec.Decode(v)
}

// ---------- JWKS 缓存 ----------

type jwksCache struct {
	mu      sync.Mutex
	entries map[string]*jwksEntry
}

type jwksEntry struct {
	keys           map[string]*rsa.PublicKey
	fetched        time.Time
	lastRefetchTry time.Time
}

func newJWKSCache() *jwksCache {
	return &jwksCache{entries: map[string]*jwksEntry{}}
}

func (c *jwksCache) publicKey(ctx context.Context, client *http.Client, url, kid string) (*rsa.PublicKey, error) {
	c.mu.Lock()
	e := c.entries[url]
	if e != nil {
		if k, ok := e.keys[kid]; ok && time.Since(e.fetched) < jwksCacheTTL {
			c.mu.Unlock()
			return k, nil
		}
		// 未知 kid 或缓存过期：节流后重拉
		if time.Since(e.lastRefetchTry) < jwksMinRefetch {
			c.mu.Unlock()
			if k, ok := e.keys[kid]; ok {
				return k, nil // 重拉窗口内先用旧值
			}
			return nil, fmt.Errorf("kid %q 未知且重拉过于频繁", kid)
		}
	}
	c.mu.Unlock()

	keys, err := fetchJWKS(ctx, client, url)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	if c.entries[url] == nil {
		c.entries[url] = &jwksEntry{}
	}
	c.entries[url].keys = keys
	c.entries[url].fetched = time.Now()
	c.entries[url].lastRefetchTry = time.Now()
	c.mu.Unlock()

	if k, ok := keys[kid]; ok {
		return k, nil
	}
	return nil, fmt.Errorf("JWKS 中不存在 kid %q", kid)
}

type jwkKey struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
	// RSA 公钥字段由 x509 解出
	X5c []string `json:"x5c"`
}

func fetchJWKS(ctx context.Context, client *http.Client, url string) (map[string]*rsa.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("拉取 JWKS: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("JWKS 返回 %d", resp.StatusCode)
	}
	var doc struct {
		Keys []jwkKey `json:"keys"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 1<<20)).Decode(&doc); err != nil {
		return nil, fmt.Errorf("解析 JWKS: %w", err)
	}
	out := map[string]*rsa.PublicKey{}
	for _, k := range doc.Keys {
		if k.Kty != "RSA" || k.N == "" || k.E == "" {
			continue
		}
		pub, err := rsaPublicKeyFromJWK(k.N, k.E)
		if err != nil {
			continue
		}
		out[k.Kid] = pub
	}
	if len(out) == 0 {
		return nil, errors.New("JWKS 不含可用 RSA 公钥")
	}
	return out, nil
}

func rsaPublicKeyFromJWK(nB64, eB64 string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(nB64)
	if err != nil {
		return nil, err
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(eB64)
	if err != nil {
		return nil, err
	}
	if len(eBytes) > 8 || len(eBytes) == 0 {
		return nil, errors.New("e 长度非法")
	}
	e := 0
	for _, b := range eBytes {
		e = e<<8 | int(b)
	}
	if e < 3 || e%2 == 0 {
		return nil, errors.New("e 数值非法")
	}
	return &rsa.PublicKey{
		N: new(big.Int).SetBytes(nBytes),
		E: e,
	}, nil
}
