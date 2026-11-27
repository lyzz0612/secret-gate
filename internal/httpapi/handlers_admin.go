package httpapi

import (
	"net"
	"net/http"
	"strconv"

	"github.com/lyzz0612/deploy-center/internal/app"
	"github.com/lyzz0612/deploy-center/internal/domain"
)
// ---------- 登录 / 会话 ----------

func (s *server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	if !s.limiter.allow(ip) {
		writeErr(w, http.StatusTooManyRequests, "尝试过于频繁，请 1 分钟后再试")
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.deps.Login.Check(body.Password) {
		writeErr(w, http.StatusUnauthorized, "口令错误")
		return
	}
	token, exp := s.deps.Login.NewSession(s.now())
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    token,
		Path:     "/admin",
		Expires:  exp,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    "",
		Path:     "/admin",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "role": "admin"})
}

// ---------- 健康与状态 ----------

func (s *server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"version": s.deps.Version,
		"schema":  s.deps.SchemaVersion,
	})
}

func (s *server) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"version": s.deps.Version,
		"schema":  s.deps.SchemaVersion,
	})
}

func (s *server) handleOverview(w http.ResponseWriter, r *http.Request) {
	c, err := s.deps.Secrets.Overview(r.Context())
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// ---------- 分组 ----------

type groupDTO struct {
	ID        int64  `json:"id"`
	Path      string `json:"path"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
}

func (s *server) handleListGroups(w http.ResponseWriter, r *http.Request) {
	gs, err := s.deps.Secrets.ListGroups(r.Context())
	if err != nil {
		mapErr(w, err)
		return
	}
	out := make([]groupDTO, 0, len(gs))
	for _, g := range gs {
		out = append(out, groupDTO{ID: g.ID, Path: g.Path, Name: g.Name, CreatedAt: g.CreatedAt})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	g, err := s.deps.Secrets.CreateGroup(r.Context(), body.Path, body.Name)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, groupDTO{ID: g.ID, Path: g.Path, Name: g.Name, CreatedAt: g.CreatedAt})
}

func (s *server) handleRenameGroup(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "id 不合法")
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.deps.Secrets.RenameGroup(r.Context(), id, body.Name); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) handleDeleteGroup(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "id 不合法")
		return
	}
	if err := s.deps.Secrets.DeleteGroup(r.Context(), id); err != nil {
		mapErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- 密钥 ----------

type secretMetaDTO struct {
	ID        int64  `json:"id"`
	GroupID   int64  `json:"group_id"`
	Key       string `json:"key"`
	UpdatedAt string `json:"updated_at"`
}

func (s *server) handleListSecrets(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "id 不合法")
		return
	}
	ms, err := s.deps.Secrets.ListSecrets(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return
	}
	out := make([]secretMetaDTO, 0, len(ms))
	for _, m := range ms {
		out = append(out, secretMetaDTO{ID: m.ID, GroupID: m.GroupID, Key: m.Key, UpdatedAt: m.UpdatedAt})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) handlePutSecret(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "id 不合法")
		return
	}
	var body struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.deps.Secrets.PutSecret(r.Context(), id, body.Key, body.Value); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]bool{"ok": true})
}

func (s *server) handleUpdateSecret(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "id 不合法")
		return
	}
	var body struct {
		Value string `json:"value"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	key := r.PathValue("key")
	// 先确认存在（PUT 到不存在的 key 是 404 而不是静默创建）
	if _, err := s.deps.Secrets.RevealSecret(r.Context(), id, key); err != nil {
		mapErr(w, err)
		return
	}
	if err := s.deps.Secrets.PutSecret(r.Context(), id, key, body.Value); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) handleRevealSecret(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "id 不合法")
		return
	}
	v, err := s.deps.Secrets.RevealSecret(r.Context(), id, r.PathValue("key"))
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"value": v})
}

func (s *server) handleDeleteSecret(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "id 不合法")
		return
	}
	if err := s.deps.Secrets.DeleteSecret(r.Context(), id, r.PathValue("key")); err != nil {
		mapErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- 插件类型 / Provider 实例 ----------

func (s *server) handleListPlugins(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.deps.Providers.ListTypes())
}

func (s *server) handleListProviders(w http.ResponseWriter, r *http.Request) {
	ps, err := s.deps.Providers.List(r.Context())
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ps)
}

type providerBody struct {
	Type   string          `json:"type"`
	Name   string          `json:"name"`
	Enabled *bool          `json:"enabled"`
	Config map[string]any  `json:"config"`
	Rules  []domain.Rule   `json:"rules"`
}

func (s *server) handleCreateProvider(w http.ResponseWriter, r *http.Request) {
	var body providerBody
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	p, err := s.deps.Providers.Create(r.Context(), body.Type, body.Name, body.Config, body.Rules)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (s *server) handleUpdateProvider(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "id 不合法")
		return
	}
	var body providerBody
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	enabled := body.Enabled == nil || *body.Enabled
	if err := s.deps.Providers.Update(r.Context(), id, body.Name, enabled, body.Config, body.Rules); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) handleDeleteProvider(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "id 不合法")
		return
	}
	if err := s.deps.Providers.Delete(r.Context(), id); err != nil {
		mapErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleTestProvider(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "id 不合法")
		return
	}
	var body struct {
		Claims map[string]string `json:"claims"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := s.deps.Providers.Test(r.Context(), id, body.Claims)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// ---------- 用户 ----------

func (s *server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	us, err := s.deps.Users.List(r.Context())
	if err != nil {
		mapErr(w, err)
		return
	}
	out := make([]map[string]any, 0, len(us))
	for _, u := range us {
		out = append(out, userDTO(u))
	}
	writeJSON(w, http.StatusOK, out)
}

func userDTO(u domain.User) map[string]any {
	return map[string]any{
		"id": u.ID, "provider_type": u.ProviderType, "external_id": u.ExternalID,
		"display": u.Display, "kind": u.Kind, "role": u.Role,
		"cred_kind": u.CredKind,
		"builtin":   u.CredKind != "",
		"source_id": u.SourceID, "created_at": u.CreatedAt, "last_seen_at": u.LastSeenAt,
	}
}

// handleCreateUser 建 appkey / 账号密码用户（内置型；实例型用户由准入自动登记）。
// 响应带凭证明文（appkey / 口令，创建即可见，后台随时可 reveal）。
func (s *server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Type       string `json:"type"`
		ExternalID string `json:"external_id"`
		Display    string `json:"display"`
		Password   string `json:"password"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	u, plain, err := s.deps.Users.Create(r.Context(), app.CreateUserReq{
		Type: body.Type, ExternalID: body.ExternalID, Display: body.Display, Password: body.Password,
	})
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"user": userDTO(u), "credential": plain})
}

func (s *server) handleGetUser(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "id 不合法")
		return
	}
	u, err := s.deps.Users.Get(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return
	}
	grants, _ := s.deps.Users.GrantsOf(r.Context(), id)
	groups, _ := s.deps.Users.GroupsOf(r.Context(), id)
	writeJSON(w, http.StatusOK, map[string]any{
		"user": userDTO(*u), "grants": grants, "user_groups": groups,
	})
}

func (s *server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "id 不合法")
		return
	}
	if err := s.deps.Users.Delete(r.Context(), id); err != nil {
		mapErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSetUserRole 切换 guest / normal。
func (s *server) handleSetUserRole(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "id 不合法")
		return
	}
	var body struct {
		Role string `json:"role"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.deps.Users.SetRole(r.Context(), id, body.Role); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleRevealCredential 解密内置用户的凭证（appkey / 口令明文）。
func (s *server) handleRevealCredential(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "id 不合法")
		return
	}
	plain, err := s.deps.Users.RevealCredential(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"value": plain})
}

// handleRotateCredential 轮换凭证：appkey 生成新 key；password 可带新口令（留空随机）。
// 响应带新明文；旧值立即失效。
func (s *server) handleRotateCredential(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "id 不合法")
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	plain, err := s.deps.Users.RotateCredential(r.Context(), id, body.Password)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"value": plain})
}

// ---------- 运行时设置 ----------

func (s *server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	ttl := s.deps.Settings.TokenTTL(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"token_ttl_minutes": int(ttl.Minutes())})
}

func (s *server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TokenTTLMinutes int `json:"token_ttl_minutes"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.deps.Settings.SetTokenTTLMinutes(r.Context(), body.TokenTTLMinutes); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) handleUserGrants(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "id 不合法")
		return
	}
	gs, err := s.deps.Users.GrantsOf(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, gs)
}

// ---------- 授权 ----------

func (s *server) handleListGrants(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var subjectID int64
	if v := q.Get("subject_id"); v != "" {
		var err error
		subjectID, err = strconv.ParseInt(v, 10, 64)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "subject_id 不合法")
			return
		}
	}
	gs, err := s.deps.Grants.List(r.Context(), q.Get("subject_kind"), subjectID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, gs)
}

func (s *server) handleCreateGrant(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SubjectKind string `json:"subject_kind"`
		SubjectID   int64  `json:"subject_id"`
		TargetKind  string `json:"target_kind"`
		TargetID    int64  `json:"target_id"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	g, err := s.deps.Grants.Create(r.Context(), body.SubjectKind, body.SubjectID, body.TargetKind, body.TargetID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, g)
}

func (s *server) handleDeleteGrant(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "id 不合法")
		return
	}
	if err := s.deps.Grants.Delete(r.Context(), id); err != nil {
		mapErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- 被拒记录 ----------

func (s *server) handleRejections(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.deps.Identity.Rejections())
}

// ---------- 用户组 ----------

func (s *server) handleListUserGroups(w http.ResponseWriter, r *http.Request) {
	gs, err := s.deps.UserGroups.List(r.Context())
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, gs)
}

func (s *server) handleCreateUserGroup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	g, err := s.deps.UserGroups.Create(r.Context(), body.Name, body.Description)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, g)
}

func (s *server) handleDeleteUserGroup(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "id 不合法")
		return
	}
	if err := s.deps.UserGroups.Delete(r.Context(), id); err != nil {
		mapErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleUserGroupMembers(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "id 不合法")
		return
	}
	ms, err := s.deps.UserGroups.Members(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ms)
}

func (s *server) handleSetUserGroupMembers(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "id 不合法")
		return
	}
	var body struct {
		UserIDs []int64 `json:"user_ids"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.deps.UserGroups.SetMembers(r.Context(), id, body.UserIDs); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "count": len(body.UserIDs)})
}
