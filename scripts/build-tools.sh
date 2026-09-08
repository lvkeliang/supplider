#!/usr/bin/env bash
# Cross-compile the standalone command-line tools for every desktop target
# and place them in dist/tools/ for release:
#
#   srm-mcp-<rust-target-triple>[.exe]  — MCP stdio server (Tools/Resources/
#       Prompts for external AI agents: Claude Desktop, Cursor, ...).
#       Opens the SAME SQLite library the desktop app uses (WAL; works with
#       the app open or closed); zero external services.
#   srm-cli-<rust-target-triple>[.exe]  — terminal client (search/add/list/
#       info/export/compare/dedup/merge/risk/expiring/visibility ...),
#       talks HTTP to a running sidecar/daemon (SRM_API_ADDR).
#
# Both are PURE Go (modernc.org/sqlite — no cgo), so CGO_ENABLED=0 cross
# builds need no C toolchain and run from any host OS.
#
# -ldflags="-s -w" strips debug info; each binary is ~11-16MB.
# sha256sums.txt covers every artifact for release verification.
set -euo pipefail
cd "$(dirname "$0")/.."
OUT=dist/tools
mkdir -p "$OUT"

build() { # $1=GOOS $2=GOARCH $3=Rust triple
  local goos=$1 goarch=$2 triple=$3 ext=""
  [ "$goos" = "windows" ] && ext=".exe"
  for tool in srm-mcp srm-cli; do
    echo "==> $tool-$triple$ext"
    (cd backend && CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
      go build -tags personal -ldflags="-s -w" \
      -o "../$OUT/$tool-$triple$ext" ./cmd/$tool)
  done
}

build linux   amd64 x86_64-unknown-linux-gnu
build linux   arm64 aarch64-unknown-linux-gnu
build windows amd64 x86_64-pc-windows-msvc
build darwin  amd64 x86_64-apple-darwin
build darwin  arm64 aarch64-apple-darwin

# Checksums (release verification). Use the platform-agnostic SHA format.
( cd "$OUT" && sha256sum srm-* > sha256sums.txt )

echo "==> tools in $OUT:"
ls -lh "$OUT" | awk 'NR>1 {print $9, $5}'
