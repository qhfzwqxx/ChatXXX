import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  server: {
    host: '0.0.0.0',
    port: 5178,
    hmr: false,
    allowedHosts: ['chatxxx.l-kx.cn'],
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:8007',
        changeOrigin: true
      }
    }
  }
});
