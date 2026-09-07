/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** Sidecar base URL for the Tauri production build (e.g. http://127.0.0.1:7612). Empty = same origin (dev proxy). */
  readonly VITE_API_BASE?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
