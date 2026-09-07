import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// The frontend is shared by the web build and the Tauri 2.0 desktop shell.
// In `vite dev` we proxy /api and /readyz to the Go sidecar (suppliderd),
// so the app calls same-origin relative URLs. In the Tauri production build
// the static bundle is embedded and talks to the sidecar directly; set
// VITE_API_BASE=http://127.0.0.1:7612 at build time for that target.
const SIDECAR = process.env.VITE_SIDECAR_URL ?? 'http://127.0.0.1:7612'

export default defineConfig({
  plugins: [react()],
  // Tauri expects a fixed port and relative asset paths.
  server: {
    port: 5173,
    strictPort: false,
    proxy: {
      '/api': { target: SIDECAR, changeOrigin: true },
      '/readyz': { target: SIDECAR, changeOrigin: true },
    },
  },
  build: {
    // Produce a small bundle; Tauri embeds dist/.
    target: 'es2021',
    outDir: 'dist',
  },
})
