// HTTP transport middleware. CORS exists for the desktop topology: the
// Tauri webview serves the frontend from its own origin
// (tauri://localhost on macOS/Linux, http(s)://tauri.localhost on
// Windows) and calls the sidecar on http://127.0.0.1:7612 — cross-origin.
// Same-origin deployments (vite dev proxy, Go-embedded web mode, reverse
// proxies) never send an Origin header and are unaffected.
//
// The personal sidecar binds loopback only, so the allowlist is limited
// to the Tauri origins and local dev servers (any localhost port).
package httpapi

import (
	"net/http"
	"strings"
)

// allowedCORSOrigin reports whether origin may call the API from a
// browser. Tauri's fixed origins plus loopback dev servers (vite: 5173,
// the embedded-UI mode on any port).
func allowedCORSOrigin(origin string) bool {
	switch origin {
	case "tauri://localhost",
		"http://tauri.localhost",
		"https://tauri.localhost":
		return true
	}
	// http://localhost:<port> / http://127.0.0.1:<port> (also [::1]).
	if strings.HasPrefix(origin, "http://localhost:") ||
		strings.HasPrefix(origin, "http://127.0.0.1:") ||
		strings.HasPrefix(origin, "http://[::1]:") {
		return true
	}
	return false
}

// CORS wraps a handler with cross-origin support for the desktop shell.
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && allowedCORSOrigin(origin) {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Vary", "Origin")
			h.Add("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
			h.Add("Access-Control-Allow-Headers", "Content-Type, Authorization")
			h.Set("Access-Control-Max-Age", "86400")
		}
		// Preflight: answer directly, never reach business handlers.
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
