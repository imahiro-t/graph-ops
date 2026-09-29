import { mergeConfig, defineConfig } from 'vitest/config';
import viteConfig from './vite.config.ts';

// Merges onto vite.config.ts (left untouched) rather than duplicating its
// plugins/server config -- this file only adds the `test` block Vitest
// itself needs. `globals: false` is intentional: tests import
// describe/it/expect/vi explicitly from 'vitest' instead of relying on
// injected globals, so src/test/setup.ts has to import
// '@testing-library/jest-dom/vitest' and call afterEach(cleanup) itself
// (see that file).
//
// defineConfig/mergeConfig come from 'vitest/config' (not 'vite') so the
// `test` block type-checks: that defineConfig's UserConfig carries
// Vitest's `test` property, which tsconfig.node.json checks at build time.
// The '.ts' extension on the vite.config import is explicit because Vite
// warns that extensionless imports won't resolve under its future native
// config loader.
export default mergeConfig(
  viteConfig,
  defineConfig({
    test: {
      environment: 'jsdom',
      setupFiles: ['./src/test/setup.ts'],
      // Raised from Vitest's 5 s default. Under heavy load (a load average
      // above 200, e.g. while autopilot runs several reviews in parallel)
      // 40-56 async UI tests (App.project, ReviewGatesEditor, settings/...)
      // went past 5 s and failed, then passed once the load dropped
      // (observed in DFLT-00270's reviews); CI and normal load never hit
      // it. This only raises the ceiling, so a passing test takes exactly
      // as long as before -- only a test that really hangs is reported up
      // to 30 s later. hookTimeout is raised too because the beforeEach
      // setup (building fake backends, rendering) slows down the same way.
      // Lowering the worker count was not chosen: it would slow every run
      // at normal load. Load beyond what 30 s absorbs is out of scope.
      testTimeout: 30_000,
      hookTimeout: 30_000
    }
  })
);
