#!/usr/bin/env bash
# Build the React frontend and sync it into BOTH consumers:
#   1. frontend/dist/           — read directly by Tauri (frontendDist)
#   2. backend/internal/webui/dist/ — go:embed into suppliderd (web mode)
#
# VITE_API_BASE points the Tauri bundle at the loopback sidecar (its
# webview origin is tauri://localhost, so calls are cross-origin and the
# sidecar answers with CORS). When the UI is served by suppliderd itself
# (web mode) the absolute URL is same-origin and also works.
set -euo pipefail
cd "$(dirname "$0")/.."

echo "==> building frontend (VITE_API_BASE=${VITE_API_BASE:-http://127.0.0.1:7612})"
(cd frontend && VITE_API_BASE="${VITE_API_BASE:-http://127.0.0.1:7612}" npm run build)

echo "==> syncing bundle into backend/internal/webui/dist"
mkdir -p backend/internal/webui/dist
# Replace everything except nothing — the placeholder index.html is
# overwritten by the real build.
rm -rf backend/internal/webui/dist/assets
cp -r frontend/dist/. backend/internal/webui/dist/

echo "==> done: $(du -sh frontend/dist | cut -f1) bundle embedded"
