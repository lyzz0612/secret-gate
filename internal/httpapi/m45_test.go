package httpapi_test

import (
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

	t.Run("成员通过组授权导出", func(t *testing.T) {
		tok := e.jwtV1Token()
		code, out := e.export(tok, "")
		if code != 200 || !strings.Contains(out, "AKIA-EXPORT-TEST") {
			t.Errorf("= %d %s", code, out)
		}
	})

	t.Run("移出成员立即失去访问", func(t *testing.T) {
		e.api("PUT", fmt.Sprintf("/admin/api/user-groups/%d/members", ug.ID), map[string]any{"user_ids": []int64{}})
		tok := e.jwtV1Token()
		code, out := e.export(tok, "")
		if code != 200 || !strings.Contains(out, `"secrets":[]`) {
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
		tok := e.jwtV1Token()
		code, out := e.export(tok, "")
		if code != 200 || !strings.Contains(out, `"secrets":[]`) {
			t.Errorf("删组后应空集 = %d %s", code, out)
		}
	})
}

// ---------- 插件类型元数据 ----------

func TestPluginsListing(t *testing.T) {
	e := newExportEnv(t)
	code, out := e.api("GET", "/admin/api/plugins", nil)
	if code != 200 {
		t.Fatalf("= %d %s", code, out)
	}
	if !strings.Contains(out, "github_oidc") || !strings.Contains(out, "password") || !strings.Contains(out, "appkey") {
		t.Errorf("插件清单应含三种类型 = %s", out)
	}
	// 内置标记：前端据此过滤（Providers 下拉去掉内置，Users 下拉只留内置）
	if !strings.Contains(out, `"builtin":true`) || !strings.Contains(out, `"builtin":false`) {
		t.Errorf("应带 builtin 标记 = %s", out)
	}
	// 回归：nil 切片序列化成 null 会让前端 .length 崩整页（新建实例黑屏）
	if !strings.Contains(out, `"config_fields":[]`) {
		t.Errorf("无配置字段时应输出空数组而不是 null = %s", out)
	}
	if !strings.Contains(out, `"claim_fields":[`) {
		t.Errorf("claim_fields 应是数组 = %s", out)
	}
}

// ---------- 被拒记录：第一跳失败进面板 ----------

func TestRejectionsAfterAuthFailures(t *testing.T) {
	e := newExportEnv(t)
	e.createInstance("我的个人号", e.aud, []map[string]string{
		{"field": "repository_owner", "pattern": "^lyzz0612$"},
	})

	// appkey 错 key + 错口令 → 都应出现在被拒面板
	e.authAppKey("ak_wrong-key-0000000000000000000000000")
	e.authBasic("nobody", "bad-pass")

	code, out := e.api("GET", "/admin/api/rejections", nil)
	if code != 200 {
		t.Fatalf("= %d %s", code, out)
	}
	if !strings.Contains(out, `"type":"appkey"`) || !strings.Contains(out, `"type":"password"`) {
		t.Errorf("被拒记录应含 appkey / password 尝试: %s", out)
	}
}

// ---------- 限流：/v1/auth 也受 limiter 保护 ----------

func TestV1AuthRateLimit(t *testing.T) {
	// 独立小限流环境（每 IP 每窗口 3 次）
	cl := newClient(t, newServer(t, 3))
	_ = cl

	// 用 appkey 撞 4 次（凭错凭证即可）：第 4 次应 429
	hdr := func(r *http.Request) { r.Header.Set("X-App-Key", "ak_rate_limit_probe_000000000000") }
	for i := 0; i < 3; i++ {
		req, _ := http.NewRequest("POST", cl.base+"/v1/auth", nil)
		hdr(req)
		resp, err := cl.c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode == 429 {
			t.Fatalf("第 %d 次不应 429", i+1)
		}
	}
	req, _ := http.NewRequest("POST", cl.base+"/v1/auth", nil)
	hdr(req)
	resp, err := cl.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 429 {
		t.Errorf("第 4 次应 429, got %d", resp.StatusCode)
	}
}
