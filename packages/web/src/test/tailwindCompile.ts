// Shared helper for the tests that compile the app's index.css with Tailwind
// (DFLT-00323): tailwindVariants.test.ts builds CSS for given classes with the
// compiler, tailwindSources.test.ts reads the sources index.css declares.
// Both load index.css the same way, through this one function, so a change
// to how its imports resolve is made in one place.
import fs from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';
import { compile } from 'tailwindcss';

export const INDEX_CSS = path.resolve(__dirname, '..', 'index.css');

const require = createRequire(import.meta.url);

// Compiles index.css, resolving `@import 'tailwindcss'` to the package's own
// stylesheet and any other import relative to the importing file.
export function compileIndexCss() {
  return compile(fs.readFileSync(INDEX_CSS, 'utf8'), {
    base: path.dirname(INDEX_CSS),
    async loadStylesheet(id: string, base: string) {
      const file = id === 'tailwindcss' ? require.resolve('tailwindcss/index.css') : path.resolve(base, id);
      return { path: file, base: path.dirname(file), content: fs.readFileSync(file, 'utf8') };
    }
  });
}
