package httpapi_test

import (
	"encoding/json"
	"strings"
	"testing"
)

// ---------- 后台手动建用户（预配置用） ----------

func TestCreateUserAPI(t *testing.T) {
	e := newExportEnv(t)

	t.Run("创建成功", func(t *testing.T) {
		code, out := e.api("POST", "/admin/api/users", map[string]string{
			"provider_type": "github_oidc", "external_id": "555001", "display": "lyzz0612/deploy-center",
		})
		if code != 201 || !strings.Contains(out, `"external_id":"555001"`) {
			t.Fatalf("= %d %s", code, out)
		}
		// display 省缺时取 external_id
		code, out = e.api("POST", "/admin/api/users", map[string]string{
			"provider_type": "password", "external_id": "manual-user",
		})
		if code != 201 || !strings.Contains(out, `"display":"manual-user"`) {
			t.Fatalf("display 省缺 = %d %s", code, out)
		}
	})

	t.Run("重复身份键 409", func(t *testing.T) {
		code, out := e.api("POST", "/admin/api/users", map[string]string{
			"provider_type": "github_oidc", "external_id": "555001",
		})
		if code != 409 {
			t.Errorf("= %d %s, 期望 409", code, out)
		}
	})

	t.Run("未知类型 400", func(t *testing.T) {
		code, out := e.api("POST", "/admin/api/users", map[string]string{
			"provider_type": "nope", "external_id": "1",
		})
		if code != 400 {
			t.Errorf("= %d %s, 期望 400", code, out)
		}
	})

	t.Run("空外部 ID 400", func(t *testing.T) {
		code, out := e.api("POST", "/admin/api/users", map[string]string{
			"provider_type": "github_oidc", "external_id": "",
		})
		if code != 400 {
			t.Errorf("= %d %s, 期望 400", code, out)
		}
	})

	t.Run("手动用户可被授权", func(t *testing.T) {
		// 建 group + 手动用户 + 授权
		_, out := e.api("POST", "/admin/api/groups", map[string]string{"path": "pre", "name": "预配置"})
		var g struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal([]byte(out), &g); err != nil {
			t.Fatal(err)
		}
		_, out = e.api("POST", "/admin/api/users", map[string]string{
			"provider_type": "github_oidc", "external_id": "777", "display": "pre-provisioned",
		})
		var u struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal([]byte(out), &u); err != nil {
			t.Fatal(err)
		}
		code, out := e.api("POST", "/admin/api/grants", map[string]any{
			"subject_kind": "user", "subject_id": u.ID, "target_kind": "group", "target_id": g.ID,
		})
		if code != 201 {
			t.Fatalf("授权手动用户 = %d %s", code, out)
		}
	})

	t.Run("同键敲门命中同一条记录", func(t *testing.T) {
		// 手动预建 external_id=987654321（defaultClaims 的 repository_id），敲门后不新建
		code, out := e.api("POST", "/admin/api/users", map[string]string{
			"provider_type": "github_oidc", "external_id": "987654321", "display": "预建身份",
		})
		if code != 201 {
			t.Fatalf("预建 = %d %s", code, out)
		}

		e.createInstance("我的个人号", e.aud, []map[string]string{
			{"field": "repository_owner", "pattern": "^lyzz0612$"},
		})
		tok := mintJWT(e.t, e.key, e.kid, defaultClaims(e.issuer, e.aud))
		if code, _ := e.export(tok, ""); code != 200 {
			t.Fatalf("敲门应 200 = %d", code)
		}
		// 敲门后：预建记录被命中（last_seen 刷新）且不产生第二条同键用户
		_, out = e.api("GET", "/admin/api/users", nil)
		if n := strings.Count(out, `"external_id":"987654321"`); n != 1 {
			t.Errorf("应命中预建用户不新建（同键用户数 = %d）: %s", n, out)
		}
		if !strings.Contains(out, `"display":"预建身份"`) {
			t.Errorf("预建记录应保留: %s", out)
		}
	})
}
