// Module supplider — supplier resource management platform.
//
// ONE Go module backs all three product tiers:
//   - personal       (//go:build personal)        Tauri sidecar + SQLite, zero external deps
//   - small-business (//go:build small-business)  Docker Compose monolith
//   - enterprise     (//go:build enterprise)      K8s microservices
//
// Business logic (services, handlers, validation, search ranking) MUST compile
// identically under every tag. Tier differences are allowed ONLY in:
//   1. infrastructure adapter packages (storage / search / queue / object / AI)
//   2. //go:build tag files that wire adapters (storefactory, tier)
//   3. feature-flag configuration
//
// Business logic depends on the Go standard library only. The two external
// dependencies are both pure-Go (no cgo) and confined to infrastructure
// adapters, so the personal binary stays a single zero-dependency artifact
// that cross-compiles for the Tauri sidecar:
//   - modernc.org/sqlite — cgo-free SQLite driver, used ONLY by
//     datamodel/sqlite (personal + small-business storage);
//   - xuri/excelize/v2 — .xlsx reader/writer, used ONLY by importer
//     (Excel batch import / template; later also xlsx export).
module github.com/supplider/supplider/backend

go 1.25.0

require (
	github.com/mozillazg/go-pinyin v0.21.0
	github.com/xuri/excelize/v2 v2.11.0
	modernc.org/sqlite v1.58.0
)

require (
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/richardlehane/mscfb v1.0.7 // indirect
	github.com/richardlehane/msoleps v1.0.6 // indirect
	github.com/tiendc/go-deepcopy v1.7.2 // indirect
	github.com/xuri/efp v0.0.1 // indirect
	github.com/xuri/nfp v0.0.2-0.20250530014748-2ddeb826f9a9 // indirect
	golang.org/x/crypto v0.53.0 // indirect
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.38.0 // indirect
	modernc.org/libc v1.75.6 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
)
