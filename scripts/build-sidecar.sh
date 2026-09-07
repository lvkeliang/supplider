#!/usr/bin/env bash
# Cross-compile the Go sidecar for every Tauri desktop target and place
# the binaries where Tauri resolves externalBin:
#   src-tauri/binaries/suppliderd-<rust-target-triple>[.exe]
#
# The sidecar is PURE Go (modernc.org/sqlite — no cgo), so CGO_ENABLED=0
# cross builds need no C toolchain and run from any host OS.
#
# -ldflags="-s -w" strips debug info to keep the installer small
# (~15MB target; the stripped linux binary is ~11MB).
set -euo pipefail
cd "$(dirname "$0")/.."
OUT=src-tauri/binaries
mkdir -p "$OUT"

build() { # $1=GOOS $2=GOARCH $3=Rust triple
  local goos=$1 goarch=$2 triple=$3 ext=""
  [ "$goos" = "windows" ] && ext=".exe"
  echo "==> suppliderd-$triple$ext"
  (cd backend && CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build -tags personal -ldflags="-s -w" \
    -o "../$OUT/suppliderd-$triple$ext" ./cmd/suppliderd)
}

build linux   amd64 x86_64-unknown-linux-gnu
build linux   arm64 aarch64-unknown-linux-gnu
build windows amd64 x86_64-pc-windows-msvc
build darwin  amd64 x86_64-apple-darwin
build darwin  arm64 aarch64-apple-darwin

echo "==> sidecars in $OUT:"
ls -lh "$OUT" | awk 'NR>1 {print $9, $5}'
