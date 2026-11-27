package httpapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// ---------- 平台插件（gitlab_oidc / vercel_oidc）+ 接入标识 + 自动登记 ----------

// createPlatformInstance 建一条任意类型的实例（可带默认分级与自动加入的组）。
func (e *exportEnv) createPlatformInstance(typ, name string, cfg map[string]any,
	rules []map[string]string, role string, groupIDs []int64) int64 {
	e.t.Helper()
	if cfg == nil {
		cfg = map[string]any{}
	}
	if role == "" {
		role = "guest"
	}
	if groupIDs == nil {
		groupIDs = []int64{}
	}
	body := map[string]any{
		"type": typ, "name": name, "config": cfg, "rules": rules,
		"default_role": role, "auto_group_ids": groupIDs,
	}
	code, out := e.api("POST", "/admin/api/providers", body)
	if code != 201 {
		e.t.Fatalf("建实例失败 %d %s", code, out)
	}
	var p struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &p); err != nil {
		e.t.Fatal(err)
	}
	return p.ID
}

// updatePlatformInstance 全量更新实例（后台 PATCH 语义）。
func (e *exportEnv) updatePlatformInstance(id int64, typ, name string, enabled bool, cfg map[string]any,
	rules []map[string]string, role string, groupIDs []int64) {
	e.t.Helper()
	if cfg == nil {
		cfg = map[string]any{}
	}
	if groupIDs == nil {
		groupIDs = []int64{}
	}
	body := map[string]any{
		"type": typ, "name": name, "enabled": enabled, "config": cfg, "rules": rules,
		"default_role": role, "auto_group_ids": groupIDs,
	}
	code, out := e.api("PATCH", fmt.Sprintf("/admin/api/providers/%d", id), body)
	if code != 200 {
		e.t.Fatalf("改实例失败 %d %s", code, out)
	}
}

// authNamed 带接入标识换票：POST /v1/auth/{name}。
func (e *exportEnv) authNamed(name, jwt string) (int, string) {
	return e.doReq("POST", "/v1/auth/"+name, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+jwt)
	})
}

// userByExternalID 从用户列表里按 (类型, 外部 ID) 找一行。
func (e *exportEnv) userByExternalID(typ, extID string) map[string]any {
	e.t.Helper()
	code, out := e.api("GET", "/admin/api/users", nil)
	if code != 200 {
		e.t.Fatalf("列用户 = %d %s", code, out)
	}
	var us []map[string]any
	if err := json.Unmarshal([]byte(out), &us); err != nil {
		e.t.Fatal(err)
	}
	for _, u := range us {
		if u["provider_type"] == typ && u["external_id"] == extID {
			return u
		}
	}
	return nil
}

// userGroupsOf 用户详情里的用户组列表。
func (e *exportEnv) userGroupsOf(userID int64) []map[string]any {
	e.t.Helper()
	code, out := e.api("GET", fmt.Sprintf("/admin/api/users/%d", userID), nil)
	if code != 200 {
		e.t.Fatalf("用户详情 = %d %s", code, out)
	}
	var r struct {
		UserGroups []map[string]any `json:"user_groups"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		e.t.Fatal(err)
	}
	return r.UserGroups
}

// createUserGroup 建用户组，返回 id。
func (e *exportEnv) createUserGroup(name string) int64 {
	e.t.Helper()
	code, out := e.api("POST", "/admin/api/user-groups", map[string]string{"name": name, "description": ""})
	if code != 201 {
		e.t.Fatalf("建用户组失败 %d %s", code, out)
	}
	var g struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &g); err != nil {
		e.t.Fatal(err)
	}
	return g.ID
}

func userID(u map[string]any) int64 {
	f, _ := u["id"].(float64)
	return int64(f)
}

// gitlabClaims 按 GitLab 官方文档的 ID token 形态造一段 claims。
func gitlabClaims(issuer, aud string, projectID int, projectPath string) map[string]any {
	return map[string]any{
		"iss": issuer, "aud": aud,
		"exp":        time.Now().Add(5 * time.Minute).Unix(),
		"iat":        time.Now().Unix(),
		"nbf":        time.Now().Add(-time.Minute).Unix(),
		"jti":        "235b3a54-b797-45c7-ae9a-f72d7bc6ef5b",
		"sub":        "project_path:" + projectPath + ":ref_type:branch:ref:main",
		"project_id": projectID, "project_path": projectPath,
		"namespace_id": 72, "namespace_path": "my-group",
		"ref": "main", "ref_type": "branch", "ref_path": "refs/heads/main", "ref_protected": "true",
		"pipeline_source": "push", "pipeline_id": 574, "job_id": 302,
		"user_login": "sample-user", "runner_environment": "self-hosted",
		"project_visibility": "private",
	}
}

// vercelClaims 按 Vercel OIDC Federation 文档的形态造一段 claims。
func vercelClaims(issuer, aud, owner, project, projectID, env string) map[string]any {
	return map[string]any{
		"iss": issuer, "aud": aud,
		"exp":   time.Now().Add(time.Hour).Unix(),
		"iat":   time.Now().Unix(),
		"nbf":   time.Now().Add(-time.Minute).Unix(),
		"sub":   "owner:" + owner + ":project:" + project + ":environment:" + env,
		"owner": owner, "owner_id": "team_7Gw5ZMzpQA8h90F832KGp7nwbuh3",
		"project": project, "project_id": projectID, "environment": env,
	}
}

func gitlabRules(aud string, projectID int) []map[string]string {
	return []map[string]string{
		{"field": "aud", "pattern": "^" + aud + "$"},
		{"field": "project_id", "pattern": fmt.Sprintf("^%d$", projectID)},
	}
}

// TestGitLabOIDC_NamedAuth 自建 GitLab：issuer 来自实例配置，令牌指名实例换票。
func TestGitLabOIDC_NamedAuth(t *testing.T) {
	e := newExportEnv(t)
	e.createPlatformInstance("gitlab_oidc", "gitlab-m68",
		map[string]any{"issuer": e.issuer}, gitlabRules(e.aud, 20), "", nil)

	jwt := mintJWT(t, e.key, e.kid, gitlabClaims(e.issuer, e.aud, 20, "my-group/my-project"))
	code, out := e.authNamed("gitlab-m68", jwt)
	if code != 200 {
		t.Fatalf("换票 = %d %s", code, out)
	}

	host := strings.TrimPrefix(strings.TrimPrefix(e.issuer, "http://"), "https://")
	u := e.userByExternalID("gitlab_oidc", host+"#20")
	if u == nil {
		t.Fatalf("应自动登记 %s#20，实际用户列表里没有", host)
	}
	if u["display"] != "my-group/my-project" {
		t.Errorf("展示名 = %v，期望项目路径", u["display"])
	}
	if u["role"] != "guest" {
		t.Errorf("默认分级 = %v，期望 guest（实例未配置时）", u["role"])
	}

	// 未指名也能走：类型匹配 → 逐实例用各自的 issuer 验签
	code, out = e.authJWT(mintJWT(t, e.key, e.kid, gitlabClaims(e.issuer, e.aud, 20, "my-group/my-project")))
	if code != 200 {
		t.Fatalf("不带接入标识换票 = %d %s", code, out)
	}
}

// TestVercelOIDC_NamedAuth Vercel：issuer 是 team 形态，身份键用全局唯一的 project_id。
func TestVercelOIDC_NamedAuth(t *testing.T) {
	e := newExportEnv(t)
	e.createPlatformInstance("vercel_oidc", "vercel-acme",
		map[string]any{"issuer": e.issuer},
		[]map[string]string{
			{"field": "aud", "pattern": "^" + e.aud + "$"},
			{"field": "project_id", "pattern": "^prj_test_1$"},
			{"field": "environment", "pattern": "^production$"},
		}, "", nil)

	jwt := mintJWT(t, e.key, e.kid, vercelClaims(e.issuer, e.aud, "acme", "acme_website", "prj_test_1", "production"))
	code, out := e.authNamed("vercel-acme", jwt)
	if code != 200 {
		t.Fatalf("换票 = %d %s", code, out)
	}
	u := e.userByExternalID("vercel_oidc", "prj_test_1")
	if u == nil {
		t.Fatal("应自动登记 prj_test_1")
	}
	if u["display"] != "acme/acme_website" {
		t.Errorf("展示名 = %v，期望 team/project", u["display"])
	}

	// environment 不符合规则 → 403（准入唯一门槛）
	devJWT := mintJWT(t, e.key, e.kid, vercelClaims(e.issuer, e.aud, "acme", "acme_website", "prj_test_1", "development"))
	if code, out := e.authNamed("vercel-acme", devJWT); code != 403 {
		t.Errorf("environment=development 应 403，实际 %d %s", code, out)
	}
}

// TestNamedAuthFailures 未知标识 / 已停用 / 签发者不符。
func TestNamedAuthFailures(t *testing.T) {
	e := newExportEnv(t)
	id := e.createPlatformInstance("gitlab_oidc", "gitlab-m68",
		map[string]any{"issuer": e.issuer}, gitlabRules(e.aud, 20), "", nil)

	jwt := mintJWT(t, e.key, e.kid, gitlabClaims(e.issuer, e.aud, 20, "my-group/my-project"))
	code, out := e.authNamed("no-such-instance", jwt)
	if code != 401 || !strings.Contains(out, "未知接入标识") {
		t.Errorf("未知标识 = %d %s，期望 401 且点名", code, out)
	}

	// 签发者不符：令牌是真的、但不是这个实例的
	wrongJWT := mintJWT(t, e.key, e.kid, gitlabClaims("https://gitlab.example.com", e.aud, 20, "my-group/my-project"))
	if code, out := e.authNamed("gitlab-m68", wrongJWT); code != 401 {
		t.Errorf("签发者不符 = %d %s，期望 401", code, out)
	}

	// 停用后指名 → 401 且说明已停用
	e.updatePlatformInstance(id, "gitlab_oidc", "gitlab-m68", false,
		map[string]any{"issuer": e.issuer}, gitlabRules(e.aud, 20), "", nil)
	code, out = e.authNamed("gitlab-m68", jwt)
	if code != 401 || !strings.Contains(out, "已停用") {
		t.Errorf("停用实例 = %d %s，期望 401 且说明停用", code, out)
	}
}

// TestAutoProvisionRoleAndGroups 首次登记按实例配置落地，之后不被静默改写。
func TestAutoProvisionRoleAndGroups(t *testing.T) {
	e := newExportEnv(t)
	g1 := e.createUserGroup("ci-frontend")
	g2 := e.createUserGroup("ci-ops")
	rules := []map[string]string{
		{"field": "aud", "pattern": "^" + e.aud + "$"},
		{"field": "repository_owner", "pattern": "^lyzz0612$"},
	}
	id := e.createPlatformInstance("github_oidc", "gh-auto", nil, rules, "normal", []int64{g1, g2})

	jwt := mintJWT(t, e.key, e.kid, defaultClaims(e.issuer, e.aud))
	if code, out := e.authNamed("gh-auto", jwt); code != 200 {
		t.Fatalf("换票 = %d %s", code, out)
	}
	u := e.userByExternalID("github_oidc", "987654321")
	if u == nil {
		t.Fatal("应自动登记 987654321")
	}
	if u["role"] != "normal" {
		t.Errorf("首次登记分级 = %v，期望 normal", u["role"])
	}
	if gs := e.userGroupsOf(userID(u)); len(gs) != 2 {
		t.Errorf("首次登记应进 2 个组，实际 %d", len(gs))
	}

	// 实例改成 guest + 只留一个组：已存在的用户不受影响
	e.updatePlatformInstance(id, "github_oidc", "gh-auto", true, nil, rules, "guest", []int64{g1})
	if code, out := e.authNamed("gh-auto", jwt); code != 200 {
		t.Fatalf("二次换票 = %d %s", code, out)
	}
	u = e.userByExternalID("github_oidc", "987654321")
	if u["role"] != "normal" {
		t.Errorf("二次通过后分级被改成 %v——不该静默覆盖管理员的决定", u["role"])
	}
	if gs := e.userGroupsOf(userID(u)); len(gs) != 2 {
		t.Errorf("二次通过后组数 = %d，期望仍为 2", len(gs))
	}

	// 新身份按新配置落地
	claims := defaultClaims(e.issuer, e.aud)
	claims["repository_id"] = 555000111
	if code, out := e.authNamed("gh-auto", mintJWT(t, e.key, e.kid, claims)); code != 200 {
		t.Fatalf("新身份换票 = %d %s", code, out)
	}
	u2 := e.userByExternalID("github_oidc", "555000111")
	if u2 == nil {
		t.Fatal("应自动登记 555000111")
	}
	if u2["role"] != "guest" {
		t.Errorf("新身份分级 = %v，期望 guest", u2["role"])
	}
	if gs := e.userGroupsOf(userID(u2)); len(gs) != 1 {
		t.Errorf("新身份应进 1 个组，实际 %d", len(gs))
	}
}

// TestUserGroupDeleteBlockedByProvider 被实例当作默认组时拒绝删除，解除后可删。
func TestUserGroupDeleteBlockedByProvider(t *testing.T) {
	e := newExportEnv(t)
	g := e.createUserGroup("被引用的组")
	rules := []map[string]string{{"field": "aud", "pattern": "^" + e.aud + "$"}}
	id := e.createPlatformInstance("github_oidc", "gh-ref", nil, rules, "guest", []int64{g})

	code, out := e.api("DELETE", fmt.Sprintf("/admin/api/user-groups/%d", g), nil)
	if code != 409 || !strings.Contains(out, "gh-ref") {
		t.Fatalf("删被引用的组 = %d %s，期望 409 且点名实例", code, out)
	}

	e.updatePlatformInstance(id, "github_oidc", "gh-ref", true, nil, rules, "guest", nil)
	if code, out := e.api("DELETE", fmt.Sprintf("/admin/api/user-groups/%d", g), nil); code != 204 {
		t.Errorf("解除引用后删除 = %d %s，期望 204", code, out)
	}
}

// TestProviderNameIsAccessKey 实例名即接入标识：URL 安全字符 + 全局唯一。
func TestProviderNameIsAccessKey(t *testing.T) {
	e := newExportEnv(t)
	rules := []map[string]string{{"field": "aud", "pattern": "^" + e.aud + "$"}}

	cases := []struct {
		name string
		code int
	}{
		{"我的个人号", 400},
		{"has space", 400},
		{"-leading-dash", 400},
		{"gh.prod_1-x", 201},
	}
	for _, c := range cases {
		code, out := e.api("POST", "/admin/api/providers", map[string]any{
			"type": "github_oidc", "name": c.name, "config": map[string]any{},
			"rules": rules, "default_role": "guest", "auto_group_ids": []int64{},
		})
		if code != c.code {
			t.Errorf("%q = %d %s，期望 %d", c.name, code, out, c.code)
		}
	}
	// 同名再建一次 → 409
	code, out := e.api("POST", "/admin/api/providers", map[string]any{
		"type": "github_oidc", "name": "gh.prod_1-x", "config": map[string]any{},
		"rules": rules, "default_role": "guest", "auto_group_ids": []int64{},
	})
	if code != 409 {
		t.Errorf("重名 = %d %s，期望 409", code, out)
	}
}

// TestProviderAutoGroupMustExist 配了不存在的组 → 400 而不是等到首次登记才炸。
func TestProviderAutoGroupMustExist(t *testing.T) {
	e := newExportEnv(t)
	code, out := e.api("POST", "/admin/api/providers", map[string]any{
		"type": "github_oidc", "name": "gh-bad-group", "config": map[string]any{},
		"rules":          []map[string]string{{"field": "aud", "pattern": "^" + e.aud + "$"}},
		"default_role":   "guest",
		"auto_group_ids": []int64{9999},
	})
	if code != 400 || !strings.Contains(out, "用户组 9999 不存在") {
		t.Errorf("不存在的组 = %d %s，期望 400", code, out)
	}
}

// TestProviderTestPreview 「试一下」带出"通过后会登记成什么"。
func TestProviderTestPreview(t *testing.T) {
	e := newExportEnv(t)
	g := e.createUserGroup("preview-group")
	rules := []map[string]string{{"field": "aud", "pattern": "^" + e.aud + "$"}}
	id := e.createPlatformInstance("github_oidc", "gh-preview", nil, rules, "normal", []int64{g})

	code, out := e.api("POST", fmt.Sprintf("/admin/api/providers/%d/test", id), map[string]any{
		"claims": map[string]string{"aud": e.aud},
	})
	if code != 200 {
		t.Fatalf("试一下 = %d %s", code, out)
	}
	var r struct {
		Allowed     bool             `json:"allowed"`
		DefaultRole string           `json:"default_role"`
		AutoGroups  []map[string]any `json:"auto_groups"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatal(err)
	}
	if !r.Allowed || r.DefaultRole != "normal" || len(r.AutoGroups) != 1 {
		t.Errorf("预览 = %s，期望 放行/normal/1 组", out)
	}
	if r.AutoGroups[0]["name"] != "preview-group" {
		t.Errorf("预览组名 = %v", r.AutoGroups[0]["name"])
	}
}
