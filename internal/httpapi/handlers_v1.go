package httpapi

import (
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/lyzz0612/deploy-center/internal/app"
)

// handleExport CI 侧唯一取密钥入口。
// 认证链：插件 Parse（401）→ 实例 Accept + 准入规则（403，不建用户）
// → 用户 pending 拒绝（403）→ 授权展开 → 解密。
// ?group=path 只导该 path 及子孙；?format=env 输出 KEY=VALUE 行。
func (s *server) handleExport(w http.ResponseWriter, r *http.Request) {
	outcome, err := s.deps.Identity.Authenticate(r.Context(), r)
	if err != nil {
		switch {
		case errors.Is(err, app.ErrNoPlugin), errors.Is(err, app.ErrTokenInvalid):
			writeErr(w, http.StatusUnauthorized, "令牌无效")
		case errors.Is(err, app.ErrNotAdmitted):
			writeErr(w, http.StatusForbidden, "身份未被接受")
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
	if u.Status != "active" {
		writeErr(w, http.StatusForbidden, "身份待批准")
		return
	}

	groupPath := strings.TrimSpace(r.URL.Query().Get("group"))
	secrets, _, err := s.deps.Export.Export(r.Context(), u, groupPath)
	if err != nil {
		mapErr(w, err)
		return
	}

	if r.URL.Query().Get("format") == "env" {
		keys := make([]string, 0, len(secrets))
		for k := range secrets {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		for _, k := range keys {
			v := secrets[k]
			// env 行格式无法承载换行：含换行的值直接拒绝，避免静默截断
			if strings.ContainsAny(v, "\r\n") {
				writeErr(w, http.StatusUnprocessableEntity, "密钥 "+k+" 含换行，无法用 env 格式导出")
				return
			}
			b.WriteString(k)
			b.WriteByte('=')
			b.WriteString(v)
			b.WriteByte('\n')
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(b.String()))
		return
	}

	writeJSON(w, http.StatusOK, map[string]map[string]string{"secrets": secrets})
}
