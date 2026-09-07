package httpapi

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// MountWebUI serves an embedded static frontend bundle at "/" (the API
// routes under /api and /readyz are more specific ServeMux patterns and
// always win). Unknown non-asset paths fall back to index.html so the
// single-page app boots on any route; genuinely missing /assets/* files
// still 404 (a stale hashed bundle reference, not a client-side route).
//
// Pass nil to disable (API-only binary). The Tauri shell does not need
// this — it embeds the bundle itself; web/dev mode does.
func (s *Server) MountWebUI(fsys fs.FS) *Server {
	if fsys == nil {
		return s
	}
	fileServer := http.FileServerFS(fsys)
	s.Mux.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p != "" && p != "index.html" && !strings.HasPrefix(p, "assets/") {
			if f, err := fsys.Open(p); err != nil {
				// No such static file: hand the SPA its entry point.
				r.URL.Path = "/"
			} else {
				_ = f.Close()
			}
		}
		fileServer.ServeHTTP(w, r)
	}))
	return s
}
