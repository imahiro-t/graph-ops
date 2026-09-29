import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';

export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    rolldownOptions: {
      output: {
        // Splits third-party code out of the app's own chunk so that no
        // chunk goes over Vite's 500 kB warning limit (one bundle was ~765
        // kB). chunkSizeWarningLimit is deliberately left at its default:
        // the warning should keep catching real growth rather than be
        // silenced. React itself (react / react-dom / scheduler) gets its
        // own chunk because it changes least often and so stays cached
        // across releases; every other dependency (react-markdown /
        // remark-gfm, i18next, lucide-react, ...) goes to `vendor`. The
        // `vendor` chunk imports `vendor-react`, never the other way round,
        // so their load order is fixed. [\\/] matches both path separators.
        // If the app's own chunk (index-*.js, ~320 kB now) ever nears 500
        // kB, lazy-load screens that the first view does not need (for
        // example ArtifactPreviewPage with React.lazy) instead of raising
        // the limit.
        codeSplitting: {
          groups: [
            {
              name: 'vendor-react',
              test: /[\\/]node_modules[\\/](react|react-dom|scheduler)[\\/]/,
              priority: 20
            },
            {
              name: 'vendor',
              test: /[\\/]node_modules[\\/]/,
              priority: 10
            }
          ]
        }
      }
    }
  },
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
      }
    }
  }
});
