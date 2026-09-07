// Package webui embeds the built React frontend (frontend/dist) into the
// Go binary so the personal sidecar stays ONE artifact: it serves both the
// JSON API and the static UI from http://127.0.0.1:7612 with zero external
// files — double-click the desktop app (or run the sidecar directly) and
// the whole platform is reachable in a browser.
//
// The Tauri desktop shell embeds the same bundle itself (frontendDist in
// tauri.conf.json); there the handler below is simply never hit. It exists
// for (a) the no-Tauri "web mode" of the personal binary, (b) dev/testing
// without a Node toolchain running, and (c) small_business deployments.
//
// Build wiring: scripts/build-frontend.sh runs `npm run build` and copies
// frontend/dist over this package's dist/ directory BEFORE go build. A
// fresh clone contains only dist/.gitkeep (dist/ must exist for go:embed),
// so the binary always compiles; Dist() then serves placeholder.html — a
// self-contained "UI not built" notice — instead of the app. The API is
// unaffected; only the browser UI is a stub until the bundle is synced.
package webui

import (
	"embed"
	"io/fs"
	"testing/fstest"
)

//go:embed all:dist
var distFS embed.FS

//go:embed placeholder.html
var placeholderHTML string

// Dist returns the embedded frontend bundle as a filesystem rooted at the
// dist directory (so open("index.html") / open("assets/...") work directly).
// If no real bundle has been synced into dist/ (fresh clone without running
// scripts/build-frontend.sh), it returns an in-memory filesystem holding
// only the placeholder notice page — callers mount the result either way.
func Dist() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err == nil {
		if f, openErr := sub.Open("index.html"); openErr == nil {
			_ = f.Close()
			return sub
		}
	}
	return fstest.MapFS{
		"index.html": {Data: []byte(placeholderHTML)},
	}
}
