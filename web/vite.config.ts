/// <reference types="vitest" />
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// The SPA is embedded into the nipad binary via go:embed, so the production
// build must land in web/server/dist (outDir below). During development,
// frontend requests are proxied to a running nipad server (default
// http://localhost:6745, override with NIPA_SERVER_URL).
const API_TARGET = process.env.NIPA_SERVER_URL ?? 'http://localhost:6745'

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: 'server/dist',
    emptyOutDir: true,
  },
  server: {
    port: 5173,
    proxy: {
      '/auth': API_TARGET,
      '/docs': API_TARGET,
      '/api': API_TARGET,
    },
  },
  test: {
    environment: 'jsdom',
    server: {
      deps: {
        inline: [/@primer\//, /octicons/],
      },
    },
  },
})