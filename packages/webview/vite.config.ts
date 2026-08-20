import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig(({ mode }) => ({
  plugins: [react()],
  // In production the app is served from /fleet/ on the go-server; in dev
  // the Vite dev server uses / so the proxy below works without path rewriting.
  base: mode === 'production' ? '/fleet/' : '/',
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://127.0.0.1:4242',
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
}))
