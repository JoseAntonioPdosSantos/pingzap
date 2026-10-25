import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// The build goes into the Go package that embeds it. In development
// (npm run dev), API calls are proxied to pingzap on port 8080.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../internal/web/dist',
    emptyOutDir: true,
  },
  server: {
    proxy: { '/api': 'http://localhost:8080' },
  },
})
