import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// Vite configuration for the ensp-lab frontend.
//
// During development the dev server proxies /api and /static requests to
// the Go backend. The backend port is read from ENS_API_PORT (default 8080)
// so it follows the configured port. In production `npm run build` emits static
// assets under ./dist which the Go binary embeds and serves from /static.
const apiPort = process.env.ENS_API_PORT || '8080';

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
    // 开发模式专用：代理目标端口跟随 ENS_API_PORT（默认 8080），
    // 不影响生产嵌入产物。
    proxy: {
      '/api': {
        target: `http://localhost:${apiPort}`,
        changeOrigin: true,
      },
      '/static': {
        target: `http://localhost:${apiPort}`,
        changeOrigin: true,
      },
    },
  },
});
