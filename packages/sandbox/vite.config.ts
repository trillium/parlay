import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { fileURLToPath } from 'node:url'

// The sandbox imports the REAL input wrapper from source rather than a built
// dist: `packages/input` is the one implementation of the input protocol, and a
// second copy vendored here would be exactly the "preview that cannot fire" the
// sandbox exists to avoid. Vite compiles the TS itself, so no build step and no
// publish is involved.
//
// The dev server PROXIES `/api` to the parlay server. That is not a convenience:
// it is what keeps this page inside the guard. The browser sees one origin
// (http://localhost:5173) for both the page and the API, the proxy forwards the
// page's own Origin and Host unchanged, and the server's origin rule — "Origin's
// host:port must equal the request's Host" — therefore accepts it as same-origin.
// No new allowed origin, no PARLAY_ALLOWED_ORIGINS, no weakened check.
const server = process.env.PARLAY_SERVER ?? 'http://127.0.0.1:4242'

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      'parlay-input': fileURLToPath(new URL('../input/src/index.ts', import.meta.url)),
    },
  },
  server: {
    proxy: {
      '/api': { target: server, changeOrigin: false, ws: false },
    },
  },
  build: {
    // The build is served by the go-server's existing static mount, so the
    // output has to be a self-contained bundle with relative asset URLs.
    outDir: 'dist',
    emptyOutDir: true,
  },
})
