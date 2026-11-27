package httpapi

import (
	"errors"
	"net"
	"net/http"
	"sort"
	"strings"

	"github.com/lyzz0612/deploy-center/internal/app"
)

// ---------- 第一跳：换票 ----------

// handleV1Auth 凭证换短期 token。
// 认证链：插件 Parse（401）→ 内置型查用户行 / 实例型准入规则（403，不建用户）
// → 自动登记用户 → 按设置里的 TTL 签发 token。
// 三种凭证形态：X-App-Key 头（appkey） / Basic（账密） / Bearer（github OIDC）。
func (s *server) handleV1Auth(w http.ResponseWriter, r *http.Request) {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	if !s.limiter.allow(ip) {
		writeErr(w, http.StatusTooManyRequests, "尝试过于频繁，请 1 分钟后再试")
		return
	}

	outcome, err := s.deps.Identity.Authenticate(r.Context(), r)
	if err != nil {
		switch {
		case errors.Is(err, app.ErrNoPlugin), errors.Is(err, app.ErrTokenInvalid):
			writeErr(w, http.StatusUnauthorized, "凭证无效")
		case errors.Is(err, app.ErrNotAdmitted):
			writeErr(w, http.StatusForbidden, "凭证无效")
		default:
			mapErr(w, err)
		}
		return
	}

	u, _, err := s.deps.Identity.ResolveUser(r.Context(), outcome)
	if err != nil {
		mapErr(w, err)
		return
	}

	ttl := s.deps.Settings.TokenTTL(r.Context())
	token, exp := s.deps.Tokens.Issue(u.ID, ttl, s.now())
	writeJSON(w, http.StatusOK, map[string]any{
		"token":       token,
		"expires_in":  int(ttl.Seconds()),
		"expires_at":  exp.UTC().Format("2006-01-02T15:04:05Z"),
		"role":        u.Role,
		"display":     u.Display,
		"token_type":  "Bearer",
	})
}

// ---------- 第二跳：导出 ----------

// handleExport CI 侧唯一取密钥入口：只认 /v1/auth 换发的短期 token。
// 认证：验 HMAC + exp（401）→ 按 uid 查用户行（用户已删 → 403）
// → 授权实时展开（无授权 = 空集 200）→ normal 解密 / guest 脱敏。
// ?group=path 只导该 path 及子孙；?format=env 输出 KEY=VALUE 行。
func (s *server) handleExport(w http.ResponseWriter, r *http.Request) {
	uid, err := s.deps.Tokens.Verify(bearerToken(r), s.now())
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "令牌无效或已过期")
		return
	}

	u, err := s.deps.Users.Get(r.Context(), uid)
	if err != nil {
		// token 有效但用户行没了（被删）：票随人走，人没了票也没意义
		writeErr(w, http.StatusForbidden, "用户不存在或已被删除")
		return
	}

	groupPath := strings.TrimSpace(r.URL.Query().Get("group"))
	entries, masked, err := s.deps.Export.Export(r.Context(), *u, groupPath)
	if err != nil {
		mapErr(w, err)
		return
	}

	if r.URL.Query().Get("format") == "env" {
		// env 是扁平 namespace，装不下跨组同名 key：拒绝并点名冲突组
		// （与「含换行的值无法用 env 格式导出」同一先例——装不下就明说，不静默丢）
		owners := map[string][]string{}
		for _, e := range entries {
			owners[e.Key] = append(owners[e.Key], e.Group)
		}
		for k, gs := range owners {
			if len(gs) > 1 {
				sort.Strings(gs)
				writeErr(w, http.StatusUnprocessableEntity, "密钥 "+k+" 在多个分组出现（"+
					strings.Join(gs, ", ")+"），env 格式无法承载同名 key——用 ?group= 精确过滤")
				return
			}
		}
		var b strings.Builder
		if masked {
			// # 是 env 注释，source 无害；脚本可 grep 到被降级的信号
			b.WriteString("# guest: values are masked\n")
		}
		for _, e := range entries {
			// env 行格式无法承载换行：含换行的值直接拒绝，避免静默截断
			if strings.ContainsAny(e.Value, "\r\n") {
				writeErr(w, http.StatusUnprocessableEntity, "密钥 "+e.Key+" 含换行，无法用 env 格式导出")
				return
			}
			b.WriteString(e.Key)
			b.WriteByte('=')
			b.WriteString(e.Value)
			b.WriteByte('\n')
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(b.String()))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"secrets": entries, "masked": masked})
}

// bearerToken 提取 Authorization: Bearer <token>。
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return strings.TrimSpace(h[len(prefix):])
	}
	return ""
}
