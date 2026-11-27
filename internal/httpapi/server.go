// Package httpapi 是传输层：路由、会话中间件、JSON 编解码、静态托管。
// 两个入口（后台会话 / CI 令牌）在中间件层收敛为统一的 Principal 语义。
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/lyzz0612/deploy-center/internal/app"
)

// SessionCookie 会话 cookie 名。
const SessionCookie = "sg_session"

// Deps 组装依赖（main 是唯一 new 实现的地方）。
type Deps struct {
	Version       string
	SchemaVersion int
	Login         *app.LoginService
	Secrets       *app.SecretService
	Identity      *app.IdentityService
	Providers     *app.ProviderService
	Users         *app.UserService
	UserGroups    *app.UserGroupService
	Grants        *app.GrantService
	Export        *app.ExportService
	Static        fs.FS // web/dist

	// 登录限流（测试可注入）；零值 = 每 IP 每分钟 10 次
	LoginRateLimit  int
	LoginRateWindow time.Duration
}

type server struct {
	deps    Deps
	limiter *loginLimiter
	now     func() time.Time
}

// New 构建完整 HTTP 处理器。
func New(d Deps) http.Handler {
	limit, window := d.LoginRateLimit, d.LoginRateWindow
	if limit <= 0 {
		limit = 10
	}
	if window <= 0 {
		window = time.Minute
	}
	s := &server{
		deps:    d,
		limiter: newLoginLimiter(limit, window),
		now:     time.Now,
	}
	return s.middleware(s.routes())
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.handleHealthz)

	// 登录 / 会话
	mux.HandleFunc("POST /admin/api/login", s.handleLogin)
	mux.HandleFunc("POST /admin/api/logout", s.requireSession(s.handleLogout))
	mux.HandleFunc("GET /admin/api/me", s.requireSession(s.handleMe))

	// 总览与状态
	mux.HandleFunc("GET /admin/api/overview", s.requireSession(s.handleOverview))
	mux.HandleFunc("GET /admin/api/status", s.requireSession(s.handleStatus))

	// 密钥分组
	mux.HandleFunc("GET /admin/api/groups", s.requireSession(s.handleListGroups))
	mux.HandleFunc("POST /admin/api/groups", s.requireSession(s.handleCreateGroup))
	mux.HandleFunc("PATCH /admin/api/groups/{id}", s.requireSession(s.handleRenameGroup))
	mux.HandleFunc("DELETE /admin/api/groups/{id}", s.requireSession(s.handleDeleteGroup))

	// 密钥（明文只在 reveal 出现）
	mux.HandleFunc("GET /admin/api/groups/{id}/secrets", s.requireSession(s.handleListSecrets))
	mux.HandleFunc("POST /admin/api/groups/{id}/secrets", s.requireSession(s.handlePutSecret))
	mux.HandleFunc("PUT /admin/api/groups/{id}/secrets/{key}", s.requireSession(s.handleUpdateSecret))
	mux.HandleFunc("GET /admin/api/groups/{id}/secrets/{key}/reveal", s.requireSession(s.handleRevealSecret))
	mux.HandleFunc("DELETE /admin/api/groups/{id}/secrets/{key}", s.requireSession(s.handleDeleteSecret))

	// 插件类型 / Provider 实例
	mux.HandleFunc("GET /admin/api/plugins", s.requireSession(s.handleListPlugins))
	mux.HandleFunc("GET /admin/api/providers", s.requireSession(s.handleListProviders))
	mux.HandleFunc("POST /admin/api/providers", s.requireSession(s.handleCreateProvider))
	mux.HandleFunc("PATCH /admin/api/providers/{id}", s.requireSession(s.handleUpdateProvider))
	mux.HandleFunc("DELETE /admin/api/providers/{id}", s.requireSession(s.handleDeleteProvider))
	mux.HandleFunc("POST /admin/api/providers/{id}/test", s.requireSession(s.handleTestProvider))

	// 用户
	mux.HandleFunc("GET /admin/api/users", s.requireSession(s.handleListUsers))
	mux.HandleFunc("GET /admin/api/users/{id}", s.requireSession(s.handleGetUser))
	mux.HandleFunc("PATCH /admin/api/users/{id}", s.requireSession(s.handlePatchUser))
	mux.HandleFunc("POST /admin/api/users/{id}/approve", s.requireSession(s.handleApproveUser))
	mux.HandleFunc("DELETE /admin/api/users/{id}", s.requireSession(s.handleDeleteUser))
	mux.HandleFunc("GET /admin/api/users/{id}/grants", s.requireSession(s.handleUserGrants))

	// 用户组
	mux.HandleFunc("GET /admin/api/user-groups", s.requireSession(s.handleListUserGroups))
	mux.HandleFunc("POST /admin/api/user-groups", s.requireSession(s.handleCreateUserGroup))
	mux.HandleFunc("DELETE /admin/api/user-groups/{id}", s.requireSession(s.handleDeleteUserGroup))
	mux.HandleFunc("GET /admin/api/user-groups/{id}/members", s.requireSession(s.handleUserGroupMembers))
	mux.HandleFunc("PUT /admin/api/user-groups/{id}/members", s.requireSession(s.handleSetUserGroupMembers))

	// 授权
	mux.HandleFunc("GET /admin/api/grants", s.requireSession(s.handleListGrants))
	mux.HandleFunc("POST /admin/api/grants", s.requireSession(s.handleCreateGrant))
	mux.HandleFunc("DELETE /admin/api/grants/{id}", s.requireSession(s.handleDeleteGrant))

	// 被拒记录
	mux.HandleFunc("GET /admin/api/rejections", s.requireSession(s.handleRejections))

	// CI 侧导出
	mux.HandleFunc("GET /v1/export", s.handleExport)

	// SPA
	mux.HandleFunc("GET /admin", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusPermanentRedirect)
	})
	mux.Handle("GET /admin/", s.spaHandler())
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusPermanentRedirect)
	})

	return mux
}

// middleware：非 GET 的 Origin 校验（防跨站写）、API 响应 no-store、API 访问日志。
func (s *server) middleware(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		isAPI := strings.HasPrefix(r.URL.Path, "/admin/api") || strings.HasPrefix(r.URL.Path, "/v1")

		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if origin := r.Header.Get("Origin"); origin != "" {
				scheme := "http"
				if r.TLS != nil {
					scheme = "https"
				}
				if fp := r.Header.Get("X-Forwarded-Proto"); fp != "" {
					scheme = fp
				}
				if origin != scheme+"://"+r.Host {
					writeErr(w, http.StatusForbidden, "跨站请求被拒绝")
					return
				}
			}
		}
		if isAPI {
			w.Header().Set("Cache-Control", "no-store")
		}

		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		h.ServeHTTP(sw, r)

		if isAPI || r.URL.Path == "/healthz" {
			log.Printf("%s %s -> %d (%s)", r.Method, r.URL.Path, sw.status, time.Since(s.now()).Round(time.Microsecond))
		}
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// requireSession 后台会话校验。
func (s *server) requireSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(SessionCookie)
		if err != nil || !s.deps.Login.VerifySession(c.Value, s.now()) {
			writeErr(w, http.StatusUnauthorized, "未登录或会话已过期")
			return
		}
		next(w, r)
	}
}

// ---------- 登录限流 ----------

type loginLimiter struct {
	mu     sync.Mutex
	hits   map[string][]time.Time
	limit  int
	window time.Duration
}

func newLoginLimiter(limit int, window time.Duration) *loginLimiter {
	return &loginLimiter{hits: map[string][]time.Time{}, limit: limit, window: window}
}

func (l *loginLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	kept := l.hits[ip][:0]
	for _, t := range l.hits[ip] {
		if now.Sub(t) < l.window {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.limit {
		l.hits[ip] = kept
		return false
	}
	l.hits[ip] = append(kept, now)
	return true
}

// ---------- JSON 工具 ----------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("请求体不合法: %w", err)
	}
	return nil
}

// mapErr 把用例层错误映射为 HTTP 状态码；未知错误记日志、对外不泄漏细节。
func mapErr(w http.ResponseWriter, err error) {
	switch {
	case err == nil:
	case errors.Is(err, app.ErrInvalid):
		writeErr(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, app.ErrNotFound):
		writeErr(w, http.StatusNotFound, err.Error())
	case errors.Is(err, app.ErrConflict):
		writeErr(w, http.StatusConflict, err.Error())
	case errors.Is(err, app.ErrKeyMismatch):
		writeErr(w, http.StatusInternalServerError, err.Error())
	default:
		log.Printf("httpapi: %v", err)
		writeErr(w, http.StatusInternalServerError, "内部错误")
	}
}
