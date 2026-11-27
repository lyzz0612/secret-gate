package httpapi_test

import (
	"encoding/json"
	"strings"
	"testing"
)

// ---------- 后台建内置用户（appkey / 账号密码） ----------

func TestCreateUserAPI(t *testing.T) {
	e := newExportEnv(t)

	t.Run("appkey 用户：返回生成的凭证", func(t *testing.T) {
		code, out := e.api("POST", "/admin/api/users", map[string]string{
			"type": "appkey", "external_id": "ci-app",
		})
		if code != 201 {
			t.Fatalf("= %d %s", code, out)
		}
		var r struct {
			User       map[string]any `json:"user"`
			Credential string         `json:"credential"`
		}
		if err := json.Unmarshal([]byte(out), &r); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(r.Credential, "ak_") {
			t.Errorf("credential 应是 ak_ 开头: %q", r.Credential)
		}
		if r.User["role"] != "guest" || r.User["display"] != "ci-app" {
			t.Errorf("默认 guest + display 省缺取标识名: %s", out)
		}
	})

	t.Run("password 用户：回显口令 + role 默认 guest", func(t *testing.T) {
		code, out := e.api("POST", "/admin/api/users", map[string]string{
			"type": "password", "external_id": "manual-user", "password": "pw-123",
		})
		if code != 201 || !strings.Contains(out, `"credential":"pw-123"`) || !strings.Contains(out, `"role":"guest"`) {
			t.Fatalf("= %d %s", code, out)
		}
	})

	t.Run("password 缺口令 400", func(t *testing.T) {
		code, _ := e.api("POST", "/admin/api/users", map[string]string{
			"type": "password", "external_id": "no-pw",
		})
		if code != 400 {
			t.Errorf("= %d, 期望 400", code)
		}
	})

	t.Run("实例型（github_oidc）不手建 400", func(t *testing.T) {
		code, out := e.api("POST", "/admin/api/users", map[string]string{
			"type": "github_oidc", "external_id": "555001",
		})
		if code != 400 {
			t.Errorf("= %d %s, 期望 400（实例型由准入自动登记）", code, out)
		}
	})

	t.Run("未知类型 400", func(t *testing.T) {
		code, _ := e.api("POST", "/admin/api/users", map[string]string{
			"type": "nope", "external_id": "1",
		})
		if code != 400 {
			t.Errorf("= %d, 期望 400", code)
		}
	})

	t.Run("空标识名 400", func(t *testing.T) {
		code, _ := e.api("POST", "/admin/api/users", map[string]string{
			"type": "appkey", "external_id": "",
		})
		if code != 400 {
			t.Errorf("= %d, 期望 400", code)
		}
	})

	t.Run("重复身份键 409", func(t *testing.T) {
		e.api("POST", "/admin/api/users", map[string]string{"type": "appkey", "external_id": "dup"})
		code, _ := e.api("POST", "/admin/api/users", map[string]string{"type": "appkey", "external_id": "dup"})
		if code != 409 {
			t.Errorf("= %d, 期望 409", code)
		}
	})

	t.Run("内置用户可被授权", func(t *testing.T) {
		_, out := e.api("POST", "/admin/api/groups", map[string]string{"path": "pre", "name": "预配置"})
		var g struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal([]byte(out), &g); err != nil {
			t.Fatal(err)
		}
		uid, _ := e.createBuiltinUser("appkey", "grant-me", "")
		code, out := e.api("POST", "/admin/api/grants", map[string]any{
			"subject_kind": "user", "subject_id": uid, "target_kind": "group", "target_id": g.ID,
		})
		if code != 201 {
			t.Fatalf("授权内置用户 = %d %s", code, out)
		}
	})

	t.Run("实例型用户 reveal 凭证 400", func(t *testing.T) {
		// github 用户先自动登记
		e.createInstance("github-me", e.aud, []map[string]string{
			{"field": "repository_owner", "pattern": "^lyzz0612$"},
		})
		e.jwtV1Token()
		_, out := e.api("GET", "/admin/api/users", nil)
		var users []map[string]any
		json.Unmarshal([]byte(out), &users)
		var gid int64 = -1
		for _, u := range users {
			if u["provider_type"] == "github_oidc" {
				gid = int64(u["id"].(float64))
			}
		}
		if gid == -1 {
			t.Fatalf("github 用户未登记: %s", out)
		}
		if code, _ := e.api("GET", "/admin/api/users/"+jsonNum(gid)+"/credential", nil); code != 400 {
			t.Errorf("实例型用户 reveal 应 400, got %d", code)
		}
	})
}

func jsonNum(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
