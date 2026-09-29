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
import { createRequire } from 'node:module';
import { compile } from 'tailwindcss';
import { Scanner } from '@tailwindcss/oxide';

const SRC = __dirname;
const INDEX_CSS = path.join(SRC, 'index.css');
const require = createRequire(import.meta.url);

async function declaredSources() {
  const compiler = await compile(fs.readFileSync(INDEX_CSS, 'utf8'), {
    base: SRC,
    async loadStylesheet(id: string, base: string) {
      const file = id === 'tailwindcss' ? require.resolve('tailwindcss/index.css') : path.resolve(base, id);
      return { path: file, base: path.dirname(file), content: fs.readFileSync(file, 'utf8') };
    }
  });
  return compiler.sources;
}

async function scan() {
  // What @tailwindcss/vite scans: the stylesheet's directory, automatically,
  // minus the sources index.css leaves out.
  const scanner = new Scanner({ sources: [{ base: SRC, pattern: '**/*', negated: false }, ...(await declaredSources())] });
  const candidates = new Set(scanner.scan());
  return { files: scanner.files.map(f => path.relative(SRC, f)), candidates };
}

const isTestFile = (rel: string) => /\.test\.tsx?$/.test(rel) || rel.split(path.sep)[0] === 'test';

// Written only in test files (this one among them, and in the prose of
// others: Tailwind takes candidates from any text), never in the app's code.
const TEST_ONLY_CLASSES = ['select-all', 'break-words'];

describe('the files Tailwind scans for classes (DFLT-00323)', () => {
  it('index.css leaves the test files out', async () => {
    const negated = (await declaredSources()).filter(s => s.negated).map(s => path.join(path.relative(SRC, s.base), s.pattern));
    expect(negated).toEqual(expect.arrayContaining(['**/*.test.ts', '**/*.test.tsx', 'test']));
  });

  it('scans the app code and no test file', async () => {
    const { files } = await scan();
    for (const rel of ['App.tsx', path.join('components', 'TicketItem.tsx'), path.join('lib', 'autopilotApi.ts')]) {
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
        else if (isTestFile(path.relative(SRC, full)) && full !== __filename) testFiles.push(full);
      }
    };
    walk(SRC);
    for (const cls of TEST_ONLY_CLASSES) {
      expect(testFiles.some(f => fs.readFileSync(f, 'utf8').includes(cls))).toBe(true);
    }
  });
});
