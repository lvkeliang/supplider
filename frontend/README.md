# Supplider Frontend

React + TypeScript + Tailwind CSS, built with Vite. This is the **shared
frontend code** for both the web build and the Tauri 2.0 desktop shell
(个人版 MVP) — the same bundle runs in both.

## Develop

Start the Go sidecar first (serves the HTTP API on `127.0.0.1:7612`):

```bash
(cd ../backend && go run -tags personal ./cmd/suppliderd -data-dir ~/.supplider)
```

Then start Vite (it proxies `/api` → `127.0.0.1:7612`):

```bash
npm install
npm run dev        # http://localhost:5173
```

## Build

```bash
npm run build      # type-checks (tsc) then bundles to dist/
npm run preview    # serve the production bundle locally
```

The production bundle is ~170 KB JS / 16 KB CSS (~55 KB gzipped) — small
enough to embed in the ~15 MB Tauri installer.

## Tauri desktop (later loop)

Tauri 2.0 embeds `dist/` and launches `suppliderd` as a sidecar. For that
target the app talks to the sidecar directly (not via the Vite proxy), so
build with:

```bash
VITE_API_BASE=http://127.0.0.1:7612 npm run build
```

> **Follow-up for the Tauri loop:** the sidecar serves on `127.0.0.1` and
> the Tauri origin is `tauri://localhost`, so the Go HTTP layer needs a
> permissive localhost CORS policy (or the Tauri HTTP adapter) for the
> embedded bundle to call it. That is wired when the Rust shell lands.

## Structure

- `src/types.ts` — TS mirrors of the Go `domain` model + feature matrix.
- `src/api.ts` — typed HTTP client for `/api/v1` (the only place fetch is used).
- `src/components/SupplierList.tsx` — keyword search + structured filters
  (地域/品类/资质/评分) + keyset "load more" (summary rows only).
- `src/components/SupplierForm.tsx` — manual entry/edit: fixed core fields +
  dynamic qualification/product/performance rows + **free-form custom
  fields** (name + type + value, no predefined schema).
- `src/components/SupplierDetail.tsx` — 文档卡片式档案: each facet is a
  collapsible `DocumentCard`; custom fields render generically.

AI entry points are driven by `GET /api/v1/features` and are hidden entirely
when `ai_enabled` is false (no API key configured) — the platform is fully
usable with no AI.
