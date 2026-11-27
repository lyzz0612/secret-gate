// Package oidc 是各认证插件共用的 OIDC 令牌验真：
// RS256 验签 + OIDC discovery 定位 JWKS + 公钥缓存 + 时间窗。
//
// 一条铁律：预期签发者（issuer）只能来自代码常量或实例配置，
// **绝不来自令牌自带的 iss**——否则令牌能把服务引向任意 URL。
package oidc

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
)

const (
	jwksCacheTTL     = time.Hour
	discoveryTTL     = time.Hour
	jwksMinRefetch   = 10 * time.Second // 未知 kid 触发重拉的最小间隔，防 JWKS 放大攻击
	clockLeeway      = 60 * time.Second
	maxTokenBodySize = 64 * 1024
	maxDiscoverySize = 1 << 20
)

// Verifier 验签器：公钥与 discovery 都按 URL 缓存，可被多个插件类型共用（并发安全）。
type Verifier struct {
	http *http.Client
	now  func() time.Time

	mu   sync.Mutex
	jwks map[string]*jwksEntry
	disc map[string]*discEntry
}

type jwksEntry struct {
	keys           map[string]*rsa.PublicKey
	fetched        time.Time
	lastRefetchTry time.Time
}

type discEntry struct {
	jwksURL string
	fetched time.Time
}

// NewVerifier 默认超时 10s 的验签器。
func NewVerifier() *Verifier {
	return &Verifier{
		http: &http.Client{Timeout: 10 * time.Second},
		now:  time.Now,
		jwks: map[string]*jwksEntry{},
		disc: map[string]*discEntry{},
	}
}

// NewVerifierWith 测试用：注入 http 客户端与时钟。
func NewVerifierWith(client *http.Client, now func() time.Time) *Verifier {
	v := NewVerifier()
	if client != nil {
		v.http = client
	}
	if now != nil {
		v.now = now
	}
	return v
}

// Verify 校验令牌并返回字符串化后的 claims。
//   - issuer：预期签发者（代码常量或实例配置），会与令牌的 iss 整串比对；
//   - jwksURL：显式公钥地址；为空时走 OIDC discovery（{issuer}/.well-known/openid-configuration）。
func (v *Verifier) Verify(ctx context.Context, rawToken, issuer, jwksURL string) (map[string]string, error) {
	issuer = strings.TrimRight(issuer, "/")
	if issuer == "" {
		return nil, errors.New("实例未配置 issuer（签发者）")
	}
	if rawToken == "" || len(rawToken) > maxTokenBodySize {
		return nil, errors.New("缺少 Bearer 令牌")
	}
	parts := strings.Split(rawToken, ".")
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
	// 显式拒绝：三家平台都只用 RS256
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

	// 先核对 iss 再拉公钥：令牌里的 iss 不能决定我们去访问哪个 URL。
	iss, _ := claims["iss"].(string)
	if strings.TrimRight(iss, "/") != issuer {
		return nil, fmt.Errorf("签发者 %q 不是 %s", iss, issuer)
	}

	keyURL := jwksURL
	if keyURL == "" {
		var err error
		keyURL, err = v.discover(ctx, issuer)
		if err != nil {
			return nil, err
		}
	}
	pub, err := v.publicKey(ctx, keyURL, hdr.Kid)
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

	now := v.now()
	exp, ok := numFloat(claims["exp"])
	if !ok {
		return nil, errors.New("令牌缺少 exp")
	}
	if now.After(time.Unix(int64(exp), 0).Add(clockLeeway)) {
		return nil, errors.New("令牌已过期")
	}
	if nbf, ok := numFloat(claims["nbf"]); ok {
		if time.Unix(int64(nbf), 0).After(now.Add(clockLeeway)) {
			return nil, errors.New("令牌尚未生效")
		}
	}
	return stringify(claims), nil
}

// stringify 把 claims 统一成字符串（数字保精度，数组取首元素）。
func stringify(claims map[string]any) map[string]string {
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
	return out
}

func numFloat(v any) (float64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	f, err := n.Float64()
	return f, err == nil
}

// ---------- discovery ----------

// discover 取 {issuer}/.well-known/openid-configuration 里的 jwks_uri。
// GitHub / GitLab / Vercel 都发布该文档；老版本自建 GitLab 缺失时可在实例里手填 jwks_url。
func (v *Verifier) discover(ctx context.Context, issuer string) (string, error) {
	v.mu.Lock()
	if e := v.disc[issuer]; e != nil && time.Since(e.fetched) < discoveryTTL {
		url := e.jwksURL
		v.mu.Unlock()
		return url, nil
	}
	v.mu.Unlock()

	u := issuer + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	resp, err := v.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("拉取 OIDC discovery: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("OIDC discovery 返回 %d", resp.StatusCode)
	}
	var doc struct {
		Issuer  string `json:"issuer"`
		JwksURI string `json:"jwks_uri"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, maxDiscoverySize)).Decode(&doc); err != nil {
		return "", fmt.Errorf("解析 OIDC discovery: %w", err)
	}
	if doc.JwksURI == "" {
		return "", fmt.Errorf("OIDC discovery 未提供 jwks_uri（可在实例配置里手填公钥地址）")
	}

	v.mu.Lock()
	v.disc[issuer] = &discEntry{jwksURL: doc.JwksURI, fetched: time.Now()}
	v.mu.Unlock()
	return doc.JwksURI, nil
}

// ---------- JWKS 缓存 ----------

func (v *Verifier) publicKey(ctx context.Context, url, kid string) (*rsa.PublicKey, error) {
	v.mu.Lock()
	e := v.jwks[url]
	if e != nil {
		if k, ok := e.keys[kid]; ok && time.Since(e.fetched) < jwksCacheTTL {
			v.mu.Unlock()
			return k, nil
		}
		// 未知 kid 或缓存过期：节流后重拉
		if time.Since(e.lastRefetchTry) < jwksMinRefetch {
			v.mu.Unlock()
			if k, ok := e.keys[kid]; ok {
				return k, nil // 重拉窗口内先用旧值
			}
			return nil, fmt.Errorf("kid %q 未知且重拉过于频繁", kid)
		}
	}
	v.mu.Unlock()

	keys, err := fetchJWKS(ctx, v.http, url)
	if err != nil {
		return nil, err
	}

	v.mu.Lock()
	if v.jwks[url] == nil {
		v.jwks[url] = &jwksEntry{}
	}
	v.jwks[url].keys = keys
	v.jwks[url].fetched = time.Now()
	v.jwks[url].lastRefetchTry = time.Now()
	v.mu.Unlock()

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
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, maxDiscoverySize)).Decode(&doc); err != nil {
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
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: e}, nil
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

// Bearer 提取 Authorization: Bearer <token>（各插件共用，避免重复实现）。
func Bearer(r *http.Request) string {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return strings.TrimSpace(h[len(prefix):])
	}
	return ""
}
