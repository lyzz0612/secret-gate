package httpapi

import (
	"net"
	"net/http"
	"strconv"
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

// ---------- 插件类型 / Provider 实例（M5 实现，先占位） ----------

func (s *server) handleListPlugins(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, []any{})
}

func (s *server) handleListProviders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, []any{})
}
