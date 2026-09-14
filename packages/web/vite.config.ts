import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  server: {
    port: 3000,
    // 127.0.0.1 rather than "localhost" on purpose: the API server binds the
    // IPv4 loopback address by default (see
    // packages/core-go/internal/runtimeconfig.DefaultHost), and naming the
    // address directly avoids depending on how this machine happens to
    // resolve "localhost" (::1 first, on some setups).
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:49173',
        changeOrigin: true
      },
      '/artifacts-static': {
        target: 'http://127.0.0.1:49173',
        changeOrigin: true
      }
    }
  }
});
