// @vitest-environment node
// DFLT-00323: Tailwind picks class candidates out of the files it scans, and
// used to scan the test files under src/ too, so the strings in them (class
// lists a test asserts are absent, old class names kept for comparison, ...)
// added unused rules to the app's CSS. index.css now leaves the test files
// out with `@source not`. This checks that it does, the way the build does:
// the sources index.css declares, then the files and candidates Tailwind's
// scanner (@tailwindcss/oxide, which @tailwindcss/vite uses) finds with them.
import { describe, expect, it } from 'vitest';
import fs from 'node:fs';
import path from 'node:path';
import { Scanner } from '@tailwindcss/oxide';
import { compileIndexCss } from './test/tailwindCompile';

const SRC = __dirname;
// The Vite root: vite.config.ts sets no `root`, so it is packages/web, the
// directory Vite is run from (the npm scripts run in it).
const ROOT = path.resolve(SRC, '..');

async function declaredSources() {
  return (await compileIndexCss()).sources;
}

async function scan() {
  // What @tailwindcss/vite scans: index.css does not set a source root, so
  // it scans the whole Vite root (packages/web, not only src/)
  // automatically, plus the sources index.css adds and minus the ones it
  // leaves out. The scanner skips what .gitignore lists (node_modules,
  // dist).
  const scanner = new Scanner({ sources: [{ base: ROOT, pattern: '**/*', negated: false }, ...(await declaredSources())] });
  const candidates = new Set(scanner.scan());
  return { files: scanner.files.map(f => path.relative(ROOT, f)), candidates };
}

// Paths relative to ROOT: any test or spec file wherever it is, and the
// shared test helpers under src/test/.
const isTestFile = (rel: string) =>
  /\.(test|spec)\.[cm]?[jt]sx?$/.test(rel) || rel.startsWith(path.join('src', 'test') + path.sep);

// Written only in test files (this one among them, and in the prose of
// others: Tailwind takes candidates from any text), never in the app's code.
const TEST_ONLY_CLASSES = ['select-all', 'break-words'];

describe('the files Tailwind scans for classes (DFLT-00323)', () => {
  it('index.css leaves the test files out', async () => {
    const negated = (await declaredSources()).filter(s => s.negated).map(s => path.join(path.relative(ROOT, s.base), s.pattern));
    expect(negated).toEqual(expect.arrayContaining([path.join('src', '**/*.test.ts'), path.join('src', '**/*.test.tsx'), path.join('src', 'test')]));
  });

  it('scans the app code and no test file', async () => {
    const { files } = await scan();
    for (const rel of ['index.html', path.join('src', 'App.tsx'), path.join('src', 'components', 'TicketItem.tsx'), path.join('src', 'lib', 'autopilotApi.ts')]) {
      expect(files).toContain(rel);
    }
    expect(files.filter(isTestFile)).toEqual([]);
  });

  it('finds the app\'s classes and not the ones only the tests write', async () => {
    const { candidates } = await scan();
    for (const cls of ['flex', 'below-80rem:wrap-anywhere', 'sm:cq-from-16rem:border-l']) expect(candidates).toContain(cls);
    for (const cls of TEST_ONLY_CLASSES) expect(candidates).not.toContain(cls);
  });

  it('checks classes some test file does write', () => {
    // Otherwise the check above could pass with the test files scanned.
    const testFiles: string[] = [];
    const walk = (dir: string) => {
      for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
        const full = path.join(dir, entry.name);
        if (entry.isDirectory()) walk(full);
        else if (isTestFile(path.relative(ROOT, full)) && full !== __filename) testFiles.push(full);
      }
    };
    walk(SRC);
    for (const cls of TEST_ONLY_CLASSES) {
      expect(testFiles.some(f => fs.readFileSync(f, 'utf8').includes(cls))).toBe(true);
    }
  });
});
