package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The Tauri webview is a cross-origin caller of the loopback sidecar. The
// fetch()+Blob downloads (TR-02) read the server filename from
// Content-Disposition, which cross-origin JavaScript can only see when the
// CORS layer explicitly exposes the header.
func TestCORSExposesContentDisposition(t *testing.T) {
	h := CORS(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename*=UTF-8''a%E4%B8%AD.xlsx`)
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/export", nil)
	req.Header.Set("Origin", "tauri://localhost")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "tauri://localhost" {
		t.Fatalf("Allow-Origin = %q, want tauri://localhost", got)
	}
	if got := rec.Header().Get("Access-Control-Expose-Headers"); got != "Content-Disposition" {
		t.Fatalf("Expose-Headers = %q, want Content-Disposition", got)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

// Requests without an Origin header (same-origin embedded web / CLI) must be
// served unchanged with no CORS headers attached.
func TestCORSNoOriginPassesThrough(t *testing.T) {
	h := CORS(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/readyz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("unexpected CORS header for Origin-less request: %q", got)
	}
}

// Preflight never reaches the business handler.
func TestCORSPreflightShortCircuits(t *testing.T) {
	reached := false
	h := CORS(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		reached = true
	}))
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/suppliers", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", rec.Code)
	}
	if reached {
		t.Fatal("preflight reached the business handler")
	}
}
