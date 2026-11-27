package httpapi_test

import (
	"bytes"
	stdcrypto "crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lyzz0612/deploy-center/internal/app"
	"github.com/lyzz0612/deploy-center/internal/app/plugins"
	"github.com/lyzz0612/deploy-center/internal/app/plugins/appkey"
	"github.com/lyzz0612/deploy-center/internal/app/plugins/github_oidc"
	"github.com/lyzz0612/deploy-center/internal/app/plugins/password"
	"github.com/lyzz0612/deploy-center/internal/httpapi"
	"github.com/lyzz0612/deploy-center/internal/infra/crypto"
	"github.com/lyzz0612/deploy-center/internal/infra/sqlite"
	"github.com/lyzz0612/deploy-center/web"
)

// ---------- 测试工具：RSA 密钥 / 假 JWKS / JWT 铸造 ----------

func testRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// fakeOIDC 起一个假签发者：/.well-known/jwks 返回给定公钥。
func fakeOIDC(t *testing.T, key *rsa.PrivateKey, kid string) *httptest.Server {
	t.Helper()
	jwks := map[string]any{
		"keys": []map[string]any{{
			"kty": "RSA", "kid": kid, "alg": "RS256", "use": "sig",
			"n":   b64url(key.N.Bytes()),
			"e":   b64url(big.NewInt(int64(key.E)).Bytes()),
		}},
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/jwks" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(jwks)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts
}

// mintJWT 用给定密钥签一个 RS256 JWT。
func mintJWT(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	header := map[string]string{"alg": "RS256", "typ": "JWT", "kid": kid}
	hb, _ := json.Marshal(header)
	cb, _ := json.Marshal(claims)
	signingInput := b64url(hb) + "." + b64url(cb)
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, stdcrypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return signingInput + "." + b64url(sig)
}

func defaultClaims(issuer, aud string) map[string]any {
	return map[string]any{
		"iss": issuer, "aud": aud,
		"exp": time.Now().Add(5 * time.Minute).Unix(),
		"iat": time.Now().Unix(),
		"sub": "repo:lyzz0612/minigames:ref:refs/heads/main",
		"repository":           "lyzz0612/minigames",
		"repository_id":        987654321,
		"repository_owner":     "lyzz0612",
		"repository_owner_id":  12345,
		"event_name":           "push",
		"ref":                  "refs/heads/main",
		"workflow":             "release",
		"actor":                "lyzz0612",
		"repository_visibility": "private",
	}
}

// ---------- 全装配测试环境 ----------

type exportEnv struct {
	t      *testing.T
	srv    *httptest.Server
	cl     *http.Client // 带 cookie 的后台客户端
	issuer string
	aud    string
	key    *rsa.PrivateKey
	kid    string
}

func newExportEnv(t *testing.T) *exportEnv {
	t.Helper()
	repo, err := sqlite.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	sealer, err := crypto.New([]byte(testMaster))
	if err != nil {
		t.Fatal(err)
	}
	if err := app.EnsureSentinel(repo, sealer); err != nil {
		t.Fatal(err)
	}

	// 假签发者（github_oidc 的 issuer 是代码级常量，测试用 NewWithIssuer 指向它）
	key := testRSAKey(t)
	kid := "test-key-1"
	issuer := fakeOIDC(t, key, kid).URL

	registry := plugins.NewRegistry()
	registry.Register(github_oidc.NewWithIssuer(issuer))
	registry.Register(password.New())
	registry.Register(appkey.New())

	tokens, err := app.NewTokenService([]byte(testMaster))
	if err != nil {
		t.Fatal(err)
	}

	h := httpapi.New(httpapi.Deps{
		Version: "test", SchemaVersion: sqlite.SchemaVersion,
		Login:      app.NewLoginService(testAdminPW),
		Secrets:    app.NewSecretService(repo, sealer),
		Identity:   app.NewIdentityService(repo, registry, sealer),
		Providers:  app.NewProviderService(repo, registry),
		Users:      app.NewUserService(repo, registry, sealer),
		UserGroups: app.NewUserGroupService(repo),
		Grants:     app.NewGrantService(repo),
		Export:     app.NewExportService(repo, sealer),
		Tokens:     tokens,
		Settings:   app.NewSettingsService(repo),
		Static:     web.Dist,
		LoginRateLimit: 1000,
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	jar, _ := cookiejar.New(nil)
	cl := &http.Client{Jar: jar}

	env := &exportEnv{
		t: t, srv: srv, cl: cl,
		aud: "https://secrets.test.example",
		key: key, kid: kid, issuer: issuer,
	}

	// 后台登录
	login := func() {
		resp, err := cl.Post(srv.URL+"/admin/api/login", "application/json",
			strings.NewReader(`{"password":"`+testAdminPW+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("登录失败 %d", resp.StatusCode)
		}
	}
	login()
	return env
}

func (e *exportEnv) api(method, path string, body any) (int, string) {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := e.cl.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b := new(bytes.Buffer)
	b.ReadFrom(resp.Body)
	return resp.StatusCode, b.String()
}

// createInstance 建一个 github_oidc 实例。
// github_oidc 无实例配置项；aud 作为普通 claim 由规则匹配（自动注入一条 aud 规则，
// 等价于真实使用时用户手写的防重放规则）。
func (e *exportEnv) createInstance(name, aud string, rules []map[string]string) int64 {
	e.t.Helper()
	all := append([]map[string]string{{"field": "aud", "pattern": "^" + aud + "$"}}, rules...)
	body := map[string]any{
		"type": "github_oidc", "name": name,
		"config": map[string]any{},
		"rules":  all,
	}
	code, out := e.api("POST", "/admin/api/providers", body)
	if code != 201 {
		e.t.Fatalf("建实例失败 %d %s", code, out)
	}
	var p struct {
		ID int64 `json:"id"`
	}
	json.Unmarshal([]byte(out), &p)
	return p.ID
}

// ---------- 两跳：/v1/auth 换票 + /v1/export 导出 ----------

func (e *exportEnv) doReq(method, path string, setHdr func(*http.Request)) (int, string) {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, nil)
	if setHdr != nil {
		setHdr(req)
	}
	resp, err := e.cl.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b := new(bytes.Buffer)
	b.ReadFrom(resp.Body)
	return resp.StatusCode, b.String()
}

// auth 用任意凭证形态调 POST /v1/auth。
func (e *exportEnv) auth(setHdr func(*http.Request)) (int, string) {
	return e.doReq("POST", "/v1/auth", setHdr)
}

func (e *exportEnv) authJWT(jwt string) (int, string) {
	return e.auth(func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+jwt) })
}

func (e *exportEnv) authBasic(user, pass string) (int, string) {
	return e.auth(func(r *http.Request) {
		r.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(user+":"+pass)))
	})
}

func (e *exportEnv) authAppKey(key string) (int, string) {
	return e.auth(func(r *http.Request) { r.Header.Set(appkey.AppKeyHeader, key) })
}

// v1Token 从 auth 响应里取 token（失败即 Fatal）。
func (e *exportEnv) v1Token(code int, out string) string {
	e.t.Helper()
	if code != 200 {
		e.t.Fatalf("换票失败 %d %s", code, out)
	}
	var r struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil || r.Token == "" {
		e.t.Fatalf("响应缺 token: %s", out)
	}
	return r.Token
}

// jwtV1Token 铸一个合法 JWT → 换票（常用捷径）。
func (e *exportEnv) jwtV1Token() string {
	e.t.Helper()
	tok := mintJWT(e.t, e.key, e.kid, defaultClaims(e.issuer, e.aud))
	return e.v1Token(e.authJWT(tok))
}

// export 用换来的短期 token 调 /v1/export。
func (e *exportEnv) export(token, query string) (int, string) {
	return e.doReq("GET", "/v1/export"+query, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+token)
	})
}

// createBuiltinUser 后台建内置用户（appkey/password），返回用户 id 与凭证明文。
func (e *exportEnv) createBuiltinUser(typ, externalID, pw string) (int64, string) {
	e.t.Helper()
	code, out := e.api("POST", "/admin/api/users", map[string]string{
		"type": typ, "external_id": externalID, "password": pw,
	})
	if code != 201 {
		e.t.Fatalf("建用户失败 %d %s", code, out)
	}
	var r struct {
		User       map[string]any `json:"user"`
		Credential string         `json:"credential"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		e.t.Fatal(err)
	}
	return int64(r.User["id"].(float64)), r.Credential
}

// setRole 切换用户分级。
func (e *exportEnv) setRole(uid int64, role string) {
	e.t.Helper()
	code, out := e.api("PATCH", fmt.Sprintf("/admin/api/users/%d/role", uid), map[string]string{"role": role})
	if code != 200 {
		e.t.Fatalf("切分级失败 %d %s", code, out)
	}
}

// grant 授权用户看某密钥组。
func (e *exportEnv) grant(uid, groupID int64) {
	e.t.Helper()
	code, out := e.api("POST", "/admin/api/grants", map[string]any{
		"subject_kind": "user", "subject_id": uid, "target_kind": "group", "target_id": groupID,
	})
	if code != 201 {
		e.t.Fatalf("授权失败 %d %s", code, out)
	}
}

// seed 准备：分组 + 密钥 + github 用户敲门登记 + 授权 + 提级 normal。
func (e *exportEnv) seed() (userID int64, groupID int64) {
	e.t.Helper()
	code, out := e.api("POST", "/admin/api/groups", map[string]string{"path": "minigames", "name": "小游戏"})
	if code != 201 {
		e.t.Fatalf("建组 %d %s", code, out)
	}
	var g1 struct {
		ID int64 `json:"id"`
	}
	json.Unmarshal([]byte(out), &g1)
	code, out = e.api("POST", "/admin/api/groups", map[string]string{"path": "minigames/prod"})
	var g2 struct {
		ID int64 `json:"id"`
	}
	json.Unmarshal([]byte(out), &g2)
	if code != 201 {
		e.t.Fatalf("建子组 %d %s", code, out)
	}
	e.api("POST", fmt.Sprintf("/admin/api/groups/%d/secrets", g2.ID), map[string]string{"key": "OSS_KEY", "value": "AKIA-EXPORT-TEST"})
	e.api("POST", fmt.Sprintf("/admin/api/groups/%d/secrets", g1.ID), map[string]string{"key": "ROOT_KEY", "value": "ROOT-VAL"})

	// 合法 JWT 敲门 → 过准入即自动登记（guest 无授权 = 空集 200）
	jwt := mintJWT(e.t, e.key, e.kid, defaultClaims(e.issuer, e.aud))
	v1tok := e.v1Token(e.authJWT(jwt))
	code, out = e.export(v1tok, "")
	if code != 200 || !strings.Contains(out, `"secrets":{}`) {
		e.t.Fatalf("首次敲门应 200 空集, got %d %s", code, out)
	}
	// 找到本次登记的 github 用户（按身份键，不数总数——调用方可能已有别的用户）
	code, out = e.api("GET", "/admin/api/users", nil)
	var users []map[string]any
	json.Unmarshal([]byte(out), &users)
	var uid int64 = -1
	for _, u := range users {
		if u["provider_type"] == "github_oidc" && fmt.Sprint(u["external_id"]) == "987654321" {
			uid = int64(u["id"].(float64))
		}
	}
	if uid == -1 {
		e.t.Fatalf("github 用户未自动登记, users = %s", out)
	}
	// 授权给父组（含子孙）+ 提级 normal（默认 guest 只能拿 key 名）
	e.grant(uid, g1.ID)
	e.setRole(uid, "normal")
	return uid, g1.ID
}

// ---------- 用例 ----------

func TestExport_FullFlow(t *testing.T) {
	e := newExportEnv(t)
	e.createInstance("我的个人号", e.aud, []map[string]string{
		{"field": "repository_owner", "pattern": "^lyzz0612$"},
		{"field": "event_name", "pattern": "^(push|workflow_dispatch|release)$"},
	})
	_, _ = e.seed()

	tok := e.jwtV1Token()

	t.Run("全量导出", func(t *testing.T) {
		code, out := e.export(tok, "")
		if code != 200 {
			t.Fatalf("= %d %s", code, out)
		}
		var r struct {
			Secrets map[string]string `json:"secrets"`
			Masked  bool              `json:"masked"`
		}
		json.Unmarshal([]byte(out), &r)
		if r.Secrets["OSS_KEY"] != "AKIA-EXPORT-TEST" || r.Secrets["ROOT_KEY"] != "ROOT-VAL" {
			t.Errorf("secrets = %v", r.Secrets)
		}
		if r.Masked {
			t.Errorf("normal 不应 masked")
		}
	})

	t.Run("group 过滤（含子孙）", func(t *testing.T) {
		code, out := e.export(tok, "?group=minigames/prod")
		if code != 200 {
			t.Fatalf("= %d %s", code, out)
		}
		if !strings.Contains(out, "AKIA-EXPORT-TEST") || strings.Contains(out, "ROOT_KEY") {
			t.Errorf("过滤结果 = %s", out)
		}
	})

	t.Run("format=env", func(t *testing.T) {
		code, out := e.export(tok, "?format=env")
		if code != 200 {
			t.Fatalf("= %d %s", code, out)
		}
		if !strings.Contains(out, "OSS_KEY=AKIA-EXPORT-TEST") {
			t.Errorf("env 输出 = %s", out)
		}
	})

	t.Run("no-store 头", func(t *testing.T) {
		req, _ := http.NewRequest("GET", e.srv.URL+"/v1/export", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := e.cl.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if v := resp.Header.Get("Cache-Control"); v != "no-store" {
			t.Errorf("Cache-Control = %q", v)
		}
	})
}

// 两跳下的 token 侧失败（插件链失败见 TestV1Auth_PluginFailures）。
func TestExport_TokenFailures(t *testing.T) {
	e := newExportEnv(t)
	e.createInstance("我的个人号", e.aud, []map[string]string{
		{"field": "repository_owner", "pattern": "^lyzz0612$"},
	})
	e.seed()

	t.Run("无 Bearer", func(t *testing.T) {
		code, out := e.doReq("GET", "/v1/export", nil)
		if code != 401 {
			t.Errorf("= %d %s, 期望 401", code, out)
		}
	})

	t.Run("垃圾 token", func(t *testing.T) {
		if code, _ := e.export("garbage", ""); code != 401 {
			t.Errorf("= %d, 期望 401", code)
		}
	})

	t.Run("篡改 token（换 uid）", func(t *testing.T) {
		tok := e.jwtV1Token()
		// v1.<uid>.<exp>.<sig>：把 uid 段改掉
		parts := strings.Split(tok, ".")
		parts[1] = "999"
		if code, _ := e.export(strings.Join(parts, "."), ""); code != 401 {
			t.Errorf("= %d, 期望 401", code)
		}
	})

	t.Run("删除用户后旧票失效", func(t *testing.T) {
		tok := e.jwtV1Token()
		_, out := e.api("GET", "/admin/api/users", nil)
		var users []map[string]any
		json.Unmarshal([]byte(out), &users)
		for _, u := range users {
			if u["provider_type"] == "github_oidc" {
				uid := int64(u["id"].(float64))
				if code, _ := e.api("DELETE", fmt.Sprintf("/admin/api/users/%d", uid), nil); code != 204 {
					t.Fatalf("删用户失败")
				}
			}
		}
		// 票还在有效期内，但用户行没了 → 403
		if code, _ := e.export(tok, ""); code != 403 {
			t.Errorf("= %d, 期望 403（用户已删）", code)
		}
		// 重新敲门 → 重新登记 → 新票正常（无授权 = 空集）
		tok2 := e.jwtV1Token()
		code, out := e.export(tok2, "")
		if code != 200 || !strings.Contains(out, `"secrets":{}`) {
			t.Errorf("重新登记后应 200 空集 = %d %s", code, out)
		}
	})
}

// 插件认证链的失败：全部发生在第一跳 POST /v1/auth。
func TestV1Auth_PluginFailures(t *testing.T) {
	e := newExportEnv(t)
	e.createInstance("我的个人号", e.aud, []map[string]string{
		{"field": "repository_owner", "pattern": "^lyzz0612$"},
	})
	e.seed()

	t.Run("无凭证头", func(t *testing.T) {
		if code, _ := e.auth(nil); code != 401 {
			t.Errorf("= %d, 期望 401", code)
		}
	})

	t.Run("垃圾 JWT", func(t *testing.T) {
		if code, _ := e.authJWT("garbage.token"); code != 401 {
			t.Errorf("= %d, 期望 401", code)
		}
	})

	t.Run("alg=none", func(t *testing.T) {
		hb, _ := json.Marshal(map[string]string{"alg": "none", "kid": e.kid})
		cb, _ := json.Marshal(defaultClaims(e.issuer, e.aud))
		tok := b64url(hb) + "." + b64url(cb) + "."
		if code, _ := e.authJWT(tok); code != 401 {
			t.Errorf("= %d, 期望 401", code)
		}
	})

	t.Run("错误签名（另一把密钥）", func(t *testing.T) {
		other := testRSAKey(t)
		tok := mintJWT(t, other, e.kid, defaultClaims(e.issuer, e.aud))
		if code, _ := e.authJWT(tok); code != 401 {
			t.Errorf("= %d, 期望 401", code)
		}
	})

	t.Run("过期 JWT", func(t *testing.T) {
		claims := defaultClaims(e.issuer, e.aud)
		claims["exp"] = time.Now().Add(-time.Hour).Unix()
		tok := mintJWT(t, e.key, e.kid, claims)
		if code, _ := e.authJWT(tok); code != 401 {
			t.Errorf("= %d, 期望 401", code)
		}
	})

	t.Run("假 issuer（非 GitHub）→ 401 且不拉 JWKS", func(t *testing.T) {
		claims := defaultClaims("https://evil-issuer.example", e.aud)
		tok := mintJWT(t, e.key, e.kid, claims)
		if code, _ := e.authJWT(tok); code != 401 {
			t.Errorf("= %d, 期望 401（issuer 写死在代码里）", code)
		}
	})

	t.Run("aud 规则不匹配 → 403 不建用户", func(t *testing.T) {
		claims := defaultClaims(e.issuer, "https://other-service.example")
		tok := mintJWT(t, e.key, e.kid, claims)
		if code, _ := e.authJWT(tok); code != 403 {
			t.Errorf("= %d, 期望 403", code)
		}
		_, out := e.api("GET", "/admin/api/users", nil)
		var users []map[string]any
		json.Unmarshal([]byte(out), &users)
		if len(users) != 1 {
			t.Errorf("aud 不匹配不应建用户, users = %s", out)
		}
	})

	t.Run("准入规则不匹配 → 403", func(t *testing.T) {
		claims := defaultClaims(e.issuer, e.aud)
		claims["repository_owner"] = "someone-else"
		tok := mintJWT(t, e.key, e.kid, claims)
		if code, _ := e.authJWT(tok); code != 403 {
			t.Errorf("= %d, 期望 403", code)
		}
	})
}

func TestExport_InstanceSelection(t *testing.T) {
	e := newExportEnv(t)
	// 两个实例：id 小的 audience 不匹配，id 大的匹配 —— 应选中第二个
	e.createInstance("错误实例", "https://wrong.example", []map[string]string{
		{"field": "repository_owner", "pattern": "^lyzz0612$"},
	})
	e.createInstance("正确实例", e.aud, []map[string]string{
		{"field": "repository_owner", "pattern": "^lyzz0612$"},
	})
	e.seed()

	tok := e.jwtV1Token()
	code, out := e.export(tok, "")
	if code != 200 {
		t.Fatalf("多实例择优失败 = %d %s", code, out)
	}
	if !strings.Contains(out, "AKIA-EXPORT-TEST") {
		t.Errorf("= %s", out)
	}
}

func TestExport_RevokeAndUserDelete(t *testing.T) {
	e := newExportEnv(t)
	e.createInstance("我的个人号", e.aud, []map[string]string{
		{"field": "repository_owner", "pattern": "^lyzz0612$"},
	})
	uid, _ := e.seed()

	// 删掉授权 → 空集
	code, out := e.api("GET", fmt.Sprintf("/admin/api/users/%d/grants", uid), nil)
	var grants []map[string]any
	json.Unmarshal([]byte(out), &grants)
	if len(grants) != 1 {
		t.Fatalf("grants = %s", out)
	}
	gid := int64(grants[0]["id"].(float64))
	if code, _ := e.api("DELETE", fmt.Sprintf("/admin/api/grants/%d", gid), nil); code != 204 {
		t.Fatalf("删授权失败")
	}
	tok := e.jwtV1Token()
	code, out = e.export(tok, "")
	if code != 200 || !strings.Contains(out, `"secrets":{}`) {
		t.Errorf("删授权后应空集 = %d %s", code, out)
	}

	// 删用户 → 旧票 403；重新敲门重新登记（无授权 = 空集 200）
	if code, _ := e.api("DELETE", fmt.Sprintf("/admin/api/users/%d", uid), nil); code != 204 {
		t.Fatalf("删用户失败")
	}
	if code, _ := e.export(tok, ""); code != 403 {
		t.Errorf("删用户后旧票应 403 = %d", code)
	}
	tok2 := e.jwtV1Token()
	code, out = e.export(tok2, "")
	if code != 200 || !strings.Contains(out, `"secrets":{}`) {
		t.Errorf("删用户后应重新登记且空集 = %d %s", code, out)
	}
}

func TestProviderValidation(t *testing.T) {
	e := newExportEnv(t)

	t.Run("未知类型", func(t *testing.T) {
		code, out := e.api("POST", "/admin/api/providers", map[string]any{
			"type": "nope", "name": "x", "config": map[string]any{}, "rules": []map[string]string{{"field": "a", "pattern": "b"}},
		})
		if code != 400 {
			t.Errorf("= %d %s, 期望 400", code, out)
		}
	})

	t.Run("内置类型不能建实例", func(t *testing.T) {
		for _, typ := range []string{"password", "appkey"} {
			code, out := e.api("POST", "/admin/api/providers", map[string]any{
				"type": typ, "name": "x", "config": map[string]any{}, "rules": []map[string]string{{"field": "a", "pattern": "b"}},
			})
			if code != 400 {
				t.Errorf("%s 建实例 = %d %s, 期望 400（内置用户类型）", typ, code, out)
			}
		}
	})

	t.Run("github_oidc 不接受任何配置键", func(t *testing.T) {
		code, out := e.api("POST", "/admin/api/providers", map[string]any{
			"type": "github_oidc", "name": "x", "config": map[string]any{"audience": "https://x"},
			"rules": []map[string]string{{"field": "a", "pattern": "b"}},
		})
		if code != 400 {
			t.Errorf("= %d %s, 期望 400（issuer/audience 已并入代码与规则）", code, out)
		}
		// 空配置应成功（无配置项）
		code, out = e.api("POST", "/admin/api/providers", map[string]any{
			"type": "github_oidc", "name": "无配置实例",
			"config": map[string]any{},
			"rules":  []map[string]string{{"field": "aud", "pattern": "^https://x$"}},
		})
		if code != 201 {
			t.Errorf("空配置实例 = %d %s, 期望 201", code, out)
		}
	})

	t.Run("规则不可编译", func(t *testing.T) {
		code, out := e.api("POST", "/admin/api/providers", map[string]any{
			"type": "github_oidc", "name": "x",
			"config": map[string]any{"audience": "https://x"},
			"rules":  []map[string]string{{"field": "a", "pattern": "(bad"}},
		})
		if code != 400 {
			t.Errorf("= %d %s, 期望 400", code, out)
		}
	})

	t.Run("试一下", func(t *testing.T) {
		id := e.createInstance("t", e.aud, []map[string]string{
			{"field": "repository_owner", "pattern": "^lyzz0612$"},
		})
		claims := map[string]string{
			"iss": e.issuer, "aud": e.aud,
			"repository_owner": "lyzz0612", "repository_id": "1", "event_name": "push",
		}
		code, out := e.api("POST", fmt.Sprintf("/admin/api/providers/%d/test", id), map[string]any{"claims": claims})
		if code != 200 || !strings.Contains(out, `"allowed":true`) {
			t.Errorf("test = %d %s", code, out)
		}
		claims["repository_owner"] = "other"
		code, out = e.api("POST", fmt.Sprintf("/admin/api/providers/%d/test", id), map[string]any{"claims": claims})
		if code != 200 || strings.Contains(out, `"allowed":true`) {
			t.Errorf("test(不匹配) = %d %s", code, out)
		}
	})

	t.Run("删除实例不影响用户", func(t *testing.T) {
		id := e.createInstance("删除我", e.aud, []map[string]string{
			{"field": "repository_owner", "pattern": "^lyzz0612$"},
		})
		// 敲门建用户
		e.jwtV1Token()
		_, out := e.api("GET", "/admin/api/users", nil)
		var users []map[string]any
		json.Unmarshal([]byte(out), &users)
		if len(users) != 1 {
			t.Fatalf("users = %s", out)
		}
		if code, _ := e.api("DELETE", fmt.Sprintf("/admin/api/providers/%d", id), nil); code != 204 {
			t.Fatalf("删实例失败")
		}
		_, out = e.api("GET", "/admin/api/users", nil)
		users = nil
		json.Unmarshal([]byte(out), &users)
		if len(users) != 1 {
			t.Errorf("删实例不应动用户, users = %s", out)
		}
	})
}

// ---------- 两跳新链路：appkey / 账密用户 + guest 分级 ----------

func TestAppKeyFlow(t *testing.T) {
	e := newExportEnv(t)
	e.createInstance("我的个人号", e.aud, []map[string]string{
		{"field": "repository_owner", "pattern": "^lyzz0612$"},
	})
	_, gID := e.seed() // 顺带建好密钥

	uid, key := e.createBuiltinUser("appkey", "ci-app", "")
	if !strings.HasPrefix(key, "ak_") {
		t.Fatalf("appkey 应带 ak_ 前缀: %q", key)
	}
	e.grant(uid, gID)

	t.Run("新用户默认 guest", func(t *testing.T) {
		_, out := e.api("GET", "/admin/api/users", nil)
		if !strings.Contains(out, `"role":"guest"`) {
			t.Errorf("应有 guest 用户: %s", out)
		}
	})

	t.Run("换票返回 role 与 TTL", func(t *testing.T) {
		code, out := e.authAppKey(key)
		if code != 200 {
			t.Fatalf("= %d %s", code, out)
		}
		if !strings.Contains(out, `"role":"guest"`) || !strings.Contains(out, `"expires_in":120`) {
			t.Errorf("auth 响应缺 role/expires_in: %s", out)
		}
	})

	t.Run("guest 导出：key 名 + fake_value + masked", func(t *testing.T) {
		tok := e.v1Token(e.authAppKey(key))
		code, out := e.export(tok, "")
		if code != 200 {
			t.Fatalf("= %d %s", code, out)
		}
		if !strings.Contains(out, `"masked":true`) || !strings.Contains(out, `"OSS_KEY":"fake_value"`) {
			t.Errorf("guest 应脱敏: %s", out)
		}
		if strings.Contains(out, "AKIA-EXPORT-TEST") {
			t.Errorf("guest 不应拿到真值: %s", out)
		}
	})

	t.Run("guest env 格式：假值 + 注释行", func(t *testing.T) {
		tok := e.v1Token(e.authAppKey(key))
		code, out := e.export(tok, "?format=env")
		if code != 200 {
			t.Fatalf("= %d %s", code, out)
		}
		if !strings.Contains(out, "# guest: values are masked") || !strings.Contains(out, "OSS_KEY=fake_value") {
			t.Errorf("guest env 应带注释与假值: %s", out)
		}
	})

	t.Run("提级 normal 后拿真值", func(t *testing.T) {
		e.setRole(uid, "normal")
		tok := e.v1Token(e.authAppKey(key))
		code, out := e.export(tok, "")
		if code != 200 || !strings.Contains(out, "AKIA-EXPORT-TEST") {
			t.Errorf("= %d %s", code, out)
		}
		if !strings.Contains(out, `"masked":false`) {
			t.Errorf("normal 应 masked:false: %s", out)
		}
	})

	t.Run("错 appkey 403", func(t *testing.T) {
		if code, _ := e.authAppKey("ak_not-the-right-one-at-all-1234567890"); code != 403 {
			t.Errorf("= %d, 期望 403", code)
		}
	})

	t.Run("reveal 凭证", func(t *testing.T) {
		code, out := e.api("GET", fmt.Sprintf("/admin/api/users/%d/credential", uid), nil)
		if code != 200 || !strings.Contains(out, key) {
			t.Errorf("reveal = %d %s", code, out)
		}
	})

	t.Run("轮换后旧 key 立即失效", func(t *testing.T) {
		code, out := e.api("POST", fmt.Sprintf("/admin/api/users/%d/credential/rotate", uid), map[string]string{})
		if code != 200 {
			t.Fatalf("rotate = %d %s", code, out)
		}
		var r struct {
			Value string `json:"value"`
		}
		json.Unmarshal([]byte(out), &r)
		if r.Value == key || !strings.HasPrefix(r.Value, "ak_") {
			t.Fatalf("应换发新 key: %s", out)
		}
		if code, _ := e.authAppKey(key); code != 403 {
			t.Errorf("旧 key 应 403, got %d", code)
		}
		if code, _ := e.authAppKey(r.Value); code != 200 {
			t.Errorf("新 key 应 200, got %d", code)
		}
	})
}

func TestPasswordUserFlow(t *testing.T) {
	e := newExportEnv(t)
	e.createInstance("我的个人号", e.aud, []map[string]string{
		{"field": "repository_owner", "pattern": "^lyzz0612$"},
	})
	_, gID := e.seed()

	uid, _ := e.createBuiltinUser("password", "ci-runner", "s3cret-pass")
	e.grant(uid, gID)

	t.Run("正确口令换票 → guest 脱敏", func(t *testing.T) {
		code, out := e.authBasic("ci-runner", "s3cret-pass")
		if code != 200 {
			t.Fatalf("= %d %s", code, out)
		}
		tok := e.v1Token(200, out)
		code, out = e.export(tok, "")
		if code != 200 || !strings.Contains(out, `"masked":true`) {
			t.Errorf("默认 guest 应脱敏 = %d %s", code, out)
		}
	})

	t.Run("错口令 403", func(t *testing.T) {
		if code, _ := e.authBasic("ci-runner", "wrong-pass"); code != 403 {
			t.Errorf("= %d, 期望 403", code)
		}
	})

	t.Run("错用户名 403", func(t *testing.T) {
		if code, _ := e.authBasic("other-user", "s3cret-pass"); code != 403 {
			t.Errorf("= %d, 期望 403", code)
		}
	})

	t.Run("提级 + 轮换口令（自定义）", func(t *testing.T) {
		e.setRole(uid, "normal")
		code, out := e.api("POST", fmt.Sprintf("/admin/api/users/%d/credential/rotate", uid),
			map[string]string{"password": "new-pass-42"})
		if code != 200 {
			t.Fatalf("rotate = %d %s", code, out)
		}
		if code, _ := e.authBasic("ci-runner", "s3cret-pass"); code != 403 {
			t.Errorf("旧口令应 403")
		}
		tok := e.v1Token(e.authBasic("ci-runner", "new-pass-42"))
		code, out = e.export(tok, "")
		if code != 200 || !strings.Contains(out, "AKIA-EXPORT-TEST") {
			t.Errorf("= %d %s", code, out)
		}
	})

	t.Run("环境格式", func(t *testing.T) {
		tok := e.v1Token(e.authBasic("ci-runner", "new-pass-42"))
		code, out := e.export(tok, "?format=env")
		if code != 200 || !strings.Contains(out, "OSS_KEY=AKIA-EXPORT-TEST") {
			t.Errorf("= %d %s", code, out)
		}
	})
}

// TTL 设置：默认 2 分钟，可改，auth 响应的 expires_in 跟随。
func TestTokenTTLSetting(t *testing.T) {
	e := newExportEnv(t)

	t.Run("默认 2 分钟", func(t *testing.T) {
		uid, key := e.createBuiltinUser("appkey", "ttl-test", "")
		_ = uid
		code, out := e.authAppKey(key)
		if code != 200 || !strings.Contains(out, `"expires_in":120`) {
			t.Errorf("= %d %s, 期望 expires_in 120", code, out)
		}
	})

	t.Run("改成 5 分钟后生效", func(t *testing.T) {
		if code, out := e.api("PUT", "/admin/api/settings", map[string]int{"token_ttl_minutes": 5}); code != 200 {
			t.Fatalf("改设置 = %d %s", code, out)
		}
		code, out := e.api("GET", "/admin/api/settings", nil)
		if code != 200 || !strings.Contains(out, `"token_ttl_minutes":5`) {
			t.Fatalf("读设置 = %d %s", code, out)
		}
		_, key := e.createBuiltinUser("appkey", "ttl-test-2", "")
		code, out = e.authAppKey(key)
		if code != 200 || !strings.Contains(out, `"expires_in":300`) {
			t.Errorf("= %d %s, 期望 expires_in 300", code, out)
		}
	})

	t.Run("越界值 400", func(t *testing.T) {
		for _, bad := range []int{0, -1, 1441} {
			if code, _ := e.api("PUT", "/admin/api/settings", map[string]int{"token_ttl_minutes": bad}); code != 400 {
				t.Errorf("ttl=%d 应 400", bad)
			}
		}
	})
}
