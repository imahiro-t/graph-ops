import { mergeConfig, defineConfig } from 'vite';
import viteConfig from './vite.config';

// Merges onto vite.config.ts (left untouched) rather than duplicating its
// plugins/server config -- this file only adds the `test` block Vitest
// itself needs. `globals: false` is intentional: tests import
// describe/it/expect/vi explicitly from 'vitest' instead of relying on
// injected globals, so src/test/setup.ts has to import
// '@testing-library/jest-dom/vitest' and call afterEach(cleanup) itself
// (see that file).
export default mergeConfig(
  viteConfig,
  defineConfig({
    test: {
      environment: 'jsdom',
      setupFiles: ['./src/test/setup.ts']
    }
  })
);
