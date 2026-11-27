package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lyzz0612/deploy-center/internal/app"
	"github.com/lyzz0612/deploy-center/internal/httpapi"
	"github.com/lyzz0612/deploy-center/internal/infra/crypto"
	"github.com/lyzz0612/deploy-center/internal/infra/sqlite"
	"github.com/lyzz0612/deploy-center/web"
)

const (
	testMaster  = "test-master-key-32-bytes-abcdefg" // 32 字节
	testAdminPW = "dev-password"
)

func newServer(t *testing.T, loginRateLimit int) http.Handler {
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
	return httpapi.New(httpapi.Deps{
		Version:         "test",
		SchemaVersion:   sqlite.SchemaVersion,
		Login:           app.NewLoginService(testAdminPW),
		Secrets:         app.NewSecretService(repo, sealer),
		Static:          web.Dist,
		LoginRateLimit:  loginRateLimit,
		LoginRateWindow: time.Hour,
	})
}

type client struct {
	t    *testing.T
	base string
	c    *http.Client
}

func newClient(t *testing.T, h http.Handler) *client {
	jar, _ := cookiejar.New(nil)
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return &client{t: t, base: ts.URL, c: &http.Client{Jar: jar}}
}

func (cl *client) do(method, path string, body any) (int, string, http.Header) {
	cl.t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			cl.t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, cl.base+path, rd)
	if err != nil {
		cl.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := cl.c.Do(req)
	if err != nil {
		cl.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

func TestHealthz(t *testing.T) {
	cl := newClient(t, newServer(t, 0))
	code, body, _ := cl.do("GET", "/healthz", nil)
	if code != 200 || !strings.Contains(body, `"ok":true`) {
		t.Errorf("healthz = %d %s", code, body)
	}
}

func TestLoginFlow(t *testing.T) {
	cl := newClient(t, newServer(t, 1000))

	// 未登录访问受保护接口
	if code, _, _ := cl.do("GET", "/admin/api/me", nil); code != 401 {
		t.Errorf("未登录 me 应 401, got %d", code)
	}
	// 错口令
	if code, body, _ := cl.do("POST", "/admin/api/login", map[string]string{"password": "wrong"}); code != 401 {
		t.Errorf("错口令应 401, got %d %s", code, body)
	}
	// 正确口令
	if code, _, _ := cl.do("POST", "/admin/api/login", map[string]string{"password": testAdminPW}); code != 200 {
		t.Fatalf("登录应 200")
	}
	// 已登录
	if code, _, _ := cl.do("GET", "/admin/api/me", nil); code != 200 {
		t.Errorf("登录后 me 应 200, got %d", code)
	}
	// 未知字段拒绝
	if code, _, _ := cl.do("POST", "/admin/api/login", map[string]string{"password": "x", "foo": "y"}); code != 400 {
		t.Errorf("未知字段应 400, got %d", code)
	}
	// 登出后再访问
	if code, _, _ := cl.do("POST", "/admin/api/logout", nil); code != 200 {
		t.Fatalf("登出应 200")
	}
	if code, _, _ := cl.do("GET", "/admin/api/me", nil); code != 401 {
		t.Errorf("登出后 me 应 401, got %d", code)
	}
}

func TestLoginRateLimit(t *testing.T) {
	cl := newClient(t, newServer(t, 3))
	for i := 0; i < 3; i++ {
		if code, _, _ := cl.do("POST", "/admin/api/login", map[string]string{"password": "wrong"}); code != 401 {
			t.Fatalf("第 %d 次应仍是 401", i+1)
		}
	}
	if code, body, _ := cl.do("POST", "/admin/api/login", map[string]string{"password": testAdminPW}); code != 429 {
		t.Errorf("第 4 次应 429, got %d %s", code, body)
	}
}

func TestOriginCheck(t *testing.T) {
	ts := httptest.NewServer(newServer(t, 1000))
	defer ts.Close()

	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	// 登录拿 cookie
	req, _ := http.NewRequest("POST", ts.URL+"/admin/api/login", strings.NewReader(`{"password":"`+testAdminPW+`"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// 带 Origin 的 POST：origin 与 host 不一致 → 403
	req2, _ := http.NewRequest("POST", ts.URL+"/admin/api/logout", nil)
	req2.Header.Set("Origin", "https://evil.example.com")
	resp2, err := c.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != 403 {
		t.Errorf("跨站 Origin 应 403, got %d", resp2.StatusCode)
	}

	// 同源 Origin 放行
	req3, _ := http.NewRequest("POST", ts.URL+"/admin/api/logout", nil)
	req3.Header.Set("Origin", ts.URL)
	resp3, err := c.Do(req3)
	if err != nil {
		t.Fatal(err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != 200 {
		t.Errorf("同源 Origin 应放行, got %d", resp3.StatusCode)
	}
}

func TestGroupsAndSecretsAPI(t *testing.T) {
	cl := newClient(t, newServer(t, 1000))
	cl.do("POST", "/admin/api/login", map[string]string{"password": testAdminPW})

	// ---- 分组 ----
	code, body, _ := cl.do("POST", "/admin/api/groups", map[string]string{"path": "minigames", "name": "小游戏"})
	if code != 201 || !strings.Contains(body, `"path":"minigames"`) {
		t.Fatalf("建组 = %d %s", code, body)
	}
	if code, _, _ := cl.do("POST", "/admin/api/groups", map[string]string{"path": "minigames"}); code != 409 {
		t.Errorf("重复建组应 409, got %d", code)
	}
	if code, _, _ := cl.do("POST", "/admin/api/groups", map[string]string{"path": "A/B/C"}); code != 400 {
		t.Errorf("非法 path 应 400, got %d", code)
	}
	cl.do("POST", "/admin/api/groups", map[string]string{"path": "minigames/prod"})

	_, body, _ = cl.do("GET", "/admin/api/groups", nil)
	var gs []map[string]any
	json.Unmarshal([]byte(body), &gs)
	if len(gs) != 2 {
		t.Fatalf("分组数 = %d, body %s", len(gs), body)
	}

	// ---- 密钥 ----
	if code, body, _ := cl.do("POST", "/admin/api/groups/1/secrets", map[string]string{"key": "OSS_KEY", "value": "AKIA-test-123"}); code != 201 {
		t.Fatalf("存密钥 = %d %s", code, body)
	}
	if code, _, _ := cl.do("POST", "/admin/api/groups/1/secrets", map[string]string{"key": "1BAD", "value": "v"}); code != 400 {
		t.Errorf("非法 key 应 400, got %d", code)
	}
	if code, _, _ := cl.do("POST", "/admin/api/groups/1/secrets", map[string]string{"key": "K", "value": ""}); code != 400 {
		t.Errorf("空值应 400, got %d", code)
	}

	// 列表：不含 value
	_, body, _ = cl.do("GET", "/admin/api/groups/1/secrets", nil)
	if strings.Contains(body, "AKIA-test-123") || strings.Contains(body, `"value"`) {
		t.Errorf("列表不应含明文: %s", body)
	}
	if !strings.Contains(body, "OSS_KEY") {
		t.Errorf("列表应含 key 名: %s", body)
	}

	// reveal
	if code, body, _ := cl.do("GET", "/admin/api/groups/1/secrets/OSS_KEY/reveal", nil); code != 200 || !strings.Contains(body, "AKIA-test-123") {
		t.Errorf("reveal = %d %s", code, body)
	}
	if code, _, _ := cl.do("GET", "/admin/api/groups/1/secrets/NOPE/reveal", nil); code != 404 {
		t.Errorf("reveal 不存在应 404, got %d", code)
	}

	// 修改
	if code, _, _ := cl.do("PUT", "/admin/api/groups/1/secrets/OSS_KEY", map[string]string{"value": "UPDATED"}); code != 200 {
		t.Errorf("改值应 200, got %d", code)
	}
	_, body, _ = cl.do("GET", "/admin/api/groups/1/secrets/OSS_KEY/reveal", nil)
	if !strings.Contains(body, "UPDATED") {
		t.Errorf("改值后 reveal = %s", body)
	}
	if code, _, _ := cl.do("PUT", "/admin/api/groups/1/secrets/NOPE", map[string]string{"value": "v"}); code != 404 {
		t.Errorf("改不存在的 key 应 404, got %d", code)
	}

	// 删除
	if code, _, _ := cl.do("DELETE", "/admin/api/groups/1/secrets/OSS_KEY", nil); code != 204 {
		t.Errorf("删密钥应 204, got %d", code)
	}
	if code, _, _ := cl.do("GET", "/admin/api/groups/1/secrets/OSS_KEY/reveal", nil); code != 404 {
		t.Errorf("删除后 reveal 应 404, got %d", code)
	}

	// ---- 删父组连带删子组 ----
	cl.do("POST", "/admin/api/groups/1/secrets", map[string]string{"key": "K2", "value": "v2"})
	if code, _, _ := cl.do("DELETE", "/admin/api/groups/1", nil); code != 204 {
		t.Errorf("删组应 204, got %d", code)
	}
	_, body, _ = cl.do("GET", "/admin/api/groups", nil)
	if strings.Contains(body, "minigames") {
		t.Errorf("删父组应连带删子组, 剩 %s", body)
	}
	var gs2 []map[string]any
	json.Unmarshal([]byte(body), &gs2)
	if len(gs2) != 0 {
		t.Errorf("两组都应被删除, 剩 %d 个", len(gs2))
	}
}

func TestAPIHeadersAndNoStore(t *testing.T) {
	cl := newClient(t, newServer(t, 1000))
	cl.do("POST", "/admin/api/login", map[string]string{"password": testAdminPW})
	_, _, h := cl.do("GET", "/admin/api/overview", nil)
	if v := h.Get("Cache-Control"); v != "no-store" {
		t.Errorf("API 响应 Cache-Control = %q, 期望 no-store", v)
	}
	_, _, h = cl.do("GET", "/admin/api/status", nil)
	if v := h.Get("Cache-Control"); v != "no-store" {
		t.Errorf("status Cache-Control = %q", v)
	}
}

func TestOverview(t *testing.T) {
	cl := newClient(t, newServer(t, 1000))
	cl.do("POST", "/admin/api/login", map[string]string{"password": testAdminPW})
	code, body, _ := cl.do("GET", "/admin/api/overview", nil)
	if code != 200 || !strings.Contains(body, `"users":0`) || strings.Contains(body, "pending") {
		t.Errorf("overview = %d %s", code, body)
	}
}

func TestRootRedirectAndSPA(t *testing.T) {
	cl := newClient(t, newServer(t, 1000))
	// GET / → 308 /admin/（禁用自动跟随，直接断言重定向本身）
	noRedirect := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := noRedirect.Get(cl.base + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 308 || resp.Header.Get("Location") != "/admin/" {
		t.Errorf("根路径应 308 → /admin/, got %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	// /admin/：构建了前端时返回 SPA 入口；未构建时返回提示（dist 只有 .gitkeep）
	code, body, _ := cl.do("GET", "/admin/", nil)
	if code != 200 {
		t.Errorf("/admin/ = %d, 期望 200", code)
	}
	if !strings.Contains(body, "前端未构建") && !strings.Contains(body, `id="root"`) {
		t.Errorf("/admin/ 内容异常: %.80s", body)
	}
}

func TestUnauthorizedAPIDenied(t *testing.T) {
	cl := newClient(t, newServer(t, 1000)) // 未登录
	for _, tc := range []struct {
		m, p string
		want int
	}{
		{"GET", "/admin/api/overview", 401},
		{"GET", "/admin/api/groups", 401},
		{"POST", "/admin/api/groups", 401},
		{"GET", "/admin/api/providers", 401},
	} {
		if code, _, _ := cl.do(tc.m, tc.p, map[string]string{"path": "x"}); code != tc.want {
			t.Errorf("%s %s 未登录应 %d, got %d", tc.m, tc.p, tc.want, code)
		}
	}
}
