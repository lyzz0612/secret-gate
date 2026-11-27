package httpapi_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// ---------- M4：用户组授权并集 ----------

func TestUserGroupGrants(t *testing.T) {
	e := newExportEnv(t)
	e.createInstance("我的个人号", e.aud, []map[string]string{
		{"field": "repository_owner", "pattern": "^lyzz0612$"},
	})
	uid, groupID := e.seed()

	// 建用户组并放入该用户
	code, out := e.api("POST", "/admin/api/user-groups", map[string]string{"name": "CI 集群", "description": "所有构建机"})
	if code != 201 {
		t.Fatalf("建用户组 = %d %s", code, out)
	}
	var ug struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &ug); err != nil {
		t.Fatal(err)
	}
	code, out = e.api("PUT", fmt.Sprintf("/admin/api/user-groups/%d/members", ug.ID), map[string]any{"user_ids": []int64{uid}})
	if code != 200 {
		t.Fatalf("加成员 = %d %s", code, out)
	}

	// 用户当前的个人授权已在 seed 里建立（user → group minigames）。
	// 移除个人授权，改用组授权验证"并集"语义
	_, out = e.api("GET", fmt.Sprintf("/admin/api/users/%d/grants", uid), nil)
	var grants []map[string]any
	json.Unmarshal([]byte(out), &grants)
	for _, g := range grants {
		e.api("DELETE", fmt.Sprintf("/admin/api/grants/%d", int64(g["id"].(float64))), nil)
	}

	// 授权给用户组（subject_kind=group）
	code, out = e.api("POST", "/admin/api/grants", map[string]any{
		"subject_kind": "group", "subject_id": ug.ID, "target_kind": "group", "target_id": groupID,
	})
	if code != 201 {
		t.Fatalf("组授权 = %d %s", code, out)
	}

	tok := mintJWT(e.t, e.key, e.kid, defaultClaims(e.issuer, e.aud))

	t.Run("成员通过组授权导出", func(t *testing.T) {
		code, out := e.export(tok, "")
		if code != 200 || !strings.Contains(out, "AKIA-EXPORT-TEST") {
			t.Errorf("= %d %s", code, out)
		}
	})

	t.Run("移出成员立即失去访问", func(t *testing.T) {
		e.api("PUT", fmt.Sprintf("/admin/api/user-groups/%d/members", ug.ID), map[string]any{"user_ids": []int64{}})
		code, out := e.export(tok, "")
		if code != 200 || !strings.Contains(out, `"secrets":{}`) {
			t.Errorf("移出后应空集 = %d %s", code, out)
		}
	})

	t.Run("删用户组连带清组授权", func(t *testing.T) {
		// 重新加入成员 + 组授权
		e.api("PUT", fmt.Sprintf("/admin/api/user-groups/%d/members", ug.ID), map[string]any{"user_ids": []int64{uid}})
		e.api("POST", "/admin/api/grants", map[string]any{
			"subject_kind": "group", "subject_id": ug.ID, "target_kind": "group", "target_id": groupID,
		})
		if code, _ := e.api("DELETE", fmt.Sprintf("/admin/api/user-groups/%d", ug.ID), nil); code != 204 {
			t.Fatalf("删用户组失败")
		}
		code, out := e.export(tok, "")
		if code != 200 || !strings.Contains(out, `"secrets":{}`) {
			t.Errorf("删组后应空集 = %d %s", code, out)
		}
	})
}

// ---------- M5：账号口令插件 ----------

func basicHeader(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

func (e *exportEnv) exportBasic(user, pass, query string) (int, string) {
	e.t.Helper()
	req, _ := http.NewRequest("GET", e.srv.URL+"/v1/export"+query, nil)
	req.Header.Set("Authorization", basicHeader(user, pass))
	resp, err := e.cl.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b := new(bytes.Buffer)
	b.ReadFrom(resp.Body)
	return resp.StatusCode, b.String()
}

func TestPasswordPlugin(t *testing.T) {
	e := newExportEnv(t)

	// 建账号口令实例
	code, out := e.api("POST", "/admin/api/providers", map[string]any{
		"type": "password", "name": "CI 脚本通道",
		"config": map[string]any{"username": "ci-runner", "password": "s3cret-pass"},
		"rules":  []map[string]string{{"field": "username", "pattern": "^ci-runner$"}},
	})
	if code != 201 {
		t.Fatalf("建实例 = %d %s", code, out)
	}

	// 敲门：正确口令 → pending 用户（password:ci-runner）
	code, out = e.exportBasic("ci-runner", "s3cret-pass", "")
	if code != 403 || !strings.Contains(out, "待批准") {
		t.Fatalf("正确口令首次应 403 待批准 = %d %s", code, out)
	}
	_, out = e.api("GET", "/admin/api/users?status=pending", nil)
	var users []map[string]any
	json.Unmarshal([]byte(out), &users)
	if len(users) != 1 || users[0]["provider_type"] != "password" {
		t.Fatalf("应有一个 password 用户 = %s", out)
	}
	uid := int64(users[0]["id"].(float64))

	// 错口令 → 403，不建新用户
	if code, _ := e.exportBasic("ci-runner", "wrong-pass", ""); code != 403 {
		t.Errorf("错口令应 403")
	}
	_, out = e.api("GET", "/admin/api/users", nil)
	users = nil
	json.Unmarshal([]byte(out), &users)
	if len(users) != 1 {
		t.Errorf("错口令不应建用户, = %s", out)
	}

	// 准备密钥 + 批准 + 授权
	_, gID := e.seed()
	// seed 会建一个 github 用户并敲门 —— 它用的是 JWT；这里给 password 用户授权
	e.api("POST", fmt.Sprintf("/admin/api/users/%d/approve", uid), map[string]string{})
	e.api("POST", "/admin/api/grants", map[string]any{
		"subject_kind": "user", "subject_id": uid, "target_kind": "group", "target_id": gID,
	})

	t.Run("正确口令导出", func(t *testing.T) {
		code, out := e.exportBasic("ci-runner", "s3cret-pass", "")
		if code != 200 || !strings.Contains(out, "AKIA-EXPORT-TEST") {
			t.Errorf("= %d %s", code, out)
		}
	})

	t.Run("错用户名 403", func(t *testing.T) {
		if code, _ := e.exportBasic("other-user", "s3cret-pass", ""); code != 403 {
			t.Errorf("= %d, 期望 403", code)
		}
	})

	t.Run("环境格式", func(t *testing.T) {
		code, out := e.exportBasic("ci-runner", "s3cret-pass", "?format=env")
		if code != 200 || !strings.Contains(out, "OSS_KEY=AKIA-EXPORT-TEST") {
			t.Errorf("= %d %s", code, out)
		}
	})
}

// ---------- M5：插件类型元数据 ----------

func TestPluginsListing(t *testing.T) {
	e := newExportEnv(t)
	code, out := e.api("GET", "/admin/api/plugins", nil)
	if code != 200 {
		t.Fatalf("= %d %s", code, out)
	}
	if !strings.Contains(out, "github_oidc") || !strings.Contains(out, "password") {
		t.Errorf("插件清单应含两种类型 = %s", out)
	}
	if !strings.Contains(out, `"secret":true`) {
		t.Errorf("password 字段应标记 secret = %s", out)
	}
}
