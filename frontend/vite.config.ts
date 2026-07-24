import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// Vite configuration for the ensp-lab frontend.
//
// During development the dev server proxies /api and /static requests to
// the Go backend on :8080 so that the React app and the REST API can
// share a single origin. In production `npm run build` emits static
// assets under ./dist which the Go binary embeds and serves from /static.
export default defineConfig({
  plugins: [react()],
  // 使用相对路径，使构建产物可通过 /static/dist/index.html 直接访问，
  // 资源引用为 ./assets/... 而非 /assets/...，便于 Go 后端 /static 静态托管。
  base: './',
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
      },
      '/static': {
        target: 'http://localhost:8080',
        changeOrigin: true,
      },
    },
  },
});
