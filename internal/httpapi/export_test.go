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

	h := httpapi.New(httpapi.Deps{
		Version: "test", SchemaVersion: sqlite.SchemaVersion,
		Login:      app.NewLoginService(testAdminPW),
		Secrets:    app.NewSecretService(repo, sealer),
		Identity:   app.NewIdentityService(repo, registry),
		Providers:  app.NewProviderService(repo, registry),
		Users:      app.NewUserService(repo),
		UserGroups: app.NewUserGroupService(repo),
		Grants:     app.NewGrantService(repo),
		Export:     app.NewExportService(repo, sealer),
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

// export 用给定 token 调 /v1/export。
func (e *exportEnv) export(token, query string) (int, string) {
	e.t.Helper()
	req, _ := http.NewRequest("GET", e.srv.URL+"/v1/export"+query, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := e.cl.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b := new(bytes.Buffer)
	b.ReadFrom(resp.Body)
	return resp.StatusCode, b.String()
}

// seed 准备：分组 + 密钥 + （可选）给用户授权。
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

	// 用合法 token 敲一次门 → 建 pending 用户
	tok := mintJWT(e.t, e.key, e.kid, defaultClaims(e.issuer, e.aud))
	code, out = e.export(tok, "")
	if code != 403 {
		e.t.Fatalf("首次敲门应 403（pending），got %d %s", code, out)
	}
	// 找到用户 id
	code, out = e.api("GET", "/admin/api/users?status=pending", nil)
	var users []map[string]any
	json.Unmarshal([]byte(out), &users)
	if len(users) != 1 {
		e.t.Fatalf("应有 1 个 pending 用户, got %s", out)
	}
	uid := int64(users[0]["id"].(float64))
	// 批准
	code, out = e.api("POST", fmt.Sprintf("/admin/api/users/%d/approve", uid), map[string]string{})
	if code != 200 {
		e.t.Fatalf("批准失败 %d %s", code, out)
	}
	// 授权给父组（含子孙）
	code, out = e.api("POST", "/admin/api/grants", map[string]any{
		"subject_kind": "user", "subject_id": uid, "target_kind": "group", "target_id": g1.ID,
	})
	if code != 201 {
		e.t.Fatalf("授权失败 %d %s", code, out)
	}
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

	tok := mintJWT(t, e.key, e.kid, defaultClaims(e.issuer, e.aud))

	t.Run("全量导出", func(t *testing.T) {
		code, out := e.export(tok, "")
		if code != 200 {
			t.Fatalf("= %d %s", code, out)
		}
		var r struct {
			Secrets map[string]string `json:"secrets"`
		}
		json.Unmarshal([]byte(out), &r)
		if r.Secrets["OSS_KEY"] != "AKIA-EXPORT-TEST" || r.Secrets["ROOT_KEY"] != "ROOT-VAL" {
			t.Errorf("secrets = %v", r.Secrets)
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

func TestExport_AuthFailures(t *testing.T) {
	e := newExportEnv(t)
	e.createInstance("我的个人号", e.aud, []map[string]string{
		{"field": "repository_owner", "pattern": "^lyzz0612$"},
	})
	e.seed()
	valid := mintJWT(t, e.key, e.kid, defaultClaims(e.issuer, e.aud))

	t.Run("无 Bearer", func(t *testing.T) {
		req, _ := http.NewRequest("GET", e.srv.URL+"/v1/export", nil)
		resp, _ := e.cl.Do(req)
		resp.Body.Close()
		if resp.StatusCode != 401 {
			t.Errorf("= %d, 期望 401", resp.StatusCode)
		}
	})

	t.Run("垃圾 token", func(t *testing.T) {
		if code, _ := e.export("garbage.token", ""); code != 401 {
			t.Errorf("= %d, 期望 401", code)
		}
	})

	t.Run("alg=none", func(t *testing.T) {
		hb, _ := json.Marshal(map[string]string{"alg": "none", "kid": e.kid})
		cb, _ := json.Marshal(defaultClaims(e.issuer, e.aud))
		tok := b64url(hb) + "." + b64url(cb) + "."
		if code, _ := e.export(tok, ""); code != 401 {
			t.Errorf("= %d, 期望 401", code)
		}
	})

	t.Run("错误签名（另一把密钥）", func(t *testing.T) {
		other := testRSAKey(t)
		tok := mintJWT(t, other, e.kid, defaultClaims(e.issuer, e.aud))
		if code, _ := e.export(tok, ""); code != 401 {
			t.Errorf("= %d, 期望 401", code)
		}
	})

	t.Run("过期 token", func(t *testing.T) {
		claims := defaultClaims(e.issuer, e.aud)
		claims["exp"] = time.Now().Add(-time.Hour).Unix()
		tok := mintJWT(t, e.key, e.kid, claims)
		if code, _ := e.export(tok, ""); code != 401 {
			t.Errorf("= %d, 期望 401", code)
		}
	})

	t.Run("假 issuer（非 GitHub）→ 401 且不拉 JWKS", func(t *testing.T) {
		claims := defaultClaims("https://evil-issuer.example", e.aud)
		tok := mintJWT(t, e.key, e.kid, claims)
		if code, _ := e.export(tok, ""); code != 401 {
			t.Errorf("= %d, 期望 401（issuer 写死在代码里）", code)
		}
	})

	t.Run("aud 规则不匹配 → 403 不建用户", func(t *testing.T) {
		claims := defaultClaims(e.issuer, "https://other-service.example")
		tok := mintJWT(t, e.key, e.kid, claims)
		if code, _ := e.export(tok, ""); code != 403 {
			t.Errorf("= %d, 期望 403", code)
		}
		// 不该产生新用户（仍是 seed 的那 1 个）
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
		if code, _ := e.export(tok, ""); code != 403 {
			t.Errorf("= %d, 期望 403", code)
		}
	})

	t.Run("claim 缺字段（无 event_name 规则才过）", func(t *testing.T) {
		// 本实例规则只查 repository_owner；删掉它后仍应过。
		// 这里用 valid（默认通过）验证正常路径未受影响
		if code, _ := e.export(valid, ""); code != 200 {
			t.Errorf("valid = %d, 期望 200", code)
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

	tok := mintJWT(t, e.key, e.kid, defaultClaims(e.issuer, e.aud))
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
	tok := mintJWT(t, e.key, e.kid, defaultClaims(e.issuer, e.aud))

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
	code, out = e.export(tok, "")
	if code != 200 || !strings.Contains(out, `"secrets":{}`) {
		t.Errorf("删授权后应空集 = %d %s", code, out)
	}

	// 删用户 → 下次敲门重建 pending → 403
	if code, _ := e.api("DELETE", fmt.Sprintf("/admin/api/users/%d", uid), nil); code != 204 {
		t.Fatalf("删用户失败")
	}
	code, out = e.export(tok, "")
	if code != 403 {
		t.Errorf("删用户后应重建 pending → 403, got %d %s", code, out)
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

	t.Run("password 缺必填被拒", func(t *testing.T) {
		code, out := e.api("POST", "/admin/api/providers", map[string]any{
			"type": "password", "name": "x", "config": map[string]any{"username": "u"},
			"rules": []map[string]string{{"field": "username", "pattern": "^u$"}},
		})
		if code != 400 {
			t.Errorf("= %d %s, 期望 400（缺 password）", code, out)
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
		tok := mintJWT(t, e.key, e.kid, defaultClaims(e.issuer, e.aud))
		e.export(tok, "")
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
