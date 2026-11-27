package httpapi

import (
	"bytes"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"time"
)

// spaHandler 托管 web/dist：/admin/* 下的静态资源与 SPA 入口。
// hash 资产走 FileServer（长缓存）；index.html 手动写响应——
// 绝不能交给 FileServer，它会把 /index.html 规范化重定向成 ./，造成 /admin/ 死循环。
func (s *server) spaHandler() http.HandlerFunc {
	sub, err := fs.Sub(s.deps.Static, "dist")
	if err != nil {
		panic("httpapi: 静态目录损坏: " + err.Error())
	}
	index, indexErr := fs.ReadFile(sub, "index.html")
	fileServer := http.FileServer(http.FS(sub))

	serveIndex := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(index))
	}

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeErr(w, http.StatusMethodNotAllowed, "只支持 GET")
			return
		}
		if indexErr != nil {
			// dist 里只有占位文件：前端未构建
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			fmt.Fprintln(w, "前端未构建。请先执行: npm --prefix web run build（或直接运行 scripts/dev.bat）")
			return
		}
		p := strings.TrimPrefix(r.URL.Path, "/admin")
		p = strings.TrimPrefix(p, "/")
		if p == "" {
			serveIndex(w, r)
			return
		}
		if st, err := fs.Stat(sub, p); err == nil && !st.IsDir() {
			// 带 hash 的资产：一年 immutable 缓存
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/" + p
			fileServer.ServeHTTP(w, r2)
			return
		}
		// 其余路径（SPA 路由、目录、不存在）回 index.html
		serveIndex(w, r)
	}
}
