// DFLT-00270: guards for the Tailwind v4 migration's v3-compatibility layer.
//
// index.css restores v3's space-y-* / divide-* behaviour (gap and border on
// every child but the first, specificity 0,3,0) value by value. A value it
// does not list silently gets v4's behaviour instead (a child's own mt-*
// adds to the gap, a divider moves by a pixel), and nothing else -- build,
// lint or the component tests -- would notice. So every space-* / divide-*
// class the sources use, variants included, has to have both its
// zero-specificity reset (`:where(...) > *`) and its v3 rule
// (`.<class> > :not([hidden]) ~ :not([hidden])`) in index.css.
//
// It also checks that no element carries both wrap-break-word and
// wrap-anywhere as plain classes: on v4 wrap-break-word is emitted after
// wrap-anywhere and wins, undoing the anywhere break the reflow fixes rely
// on (on v3 the arbitrary [overflow-wrap:anywhere] came last and won).
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';
import { describe, expect, it } from 'vitest';

// process.cwd() is the Vitest root, i.e. packages/web.
const WEB_ROOT = process.cwd();
const SRC = join(WEB_ROOT, 'src');

const sourceFiles = (dir: string): string[] =>
  readdirSync(dir).flatMap(name => {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) return sourceFiles(path);
    return /\.(ts|tsx)$/.test(name) && !/\.test\.(ts|tsx)$/.test(name) ? [path] : [];
  });

// Blanks out comments, keeping line numbers. A `//` right after a colon or a
// quote (a URL, or a string that starts with one) is left alone.
const stripComments = (text: string) =>
  text
    .replace(/\/\*[\s\S]*?\*\//g, m => m.replace(/[^\n]/g, ' '))
    .replace(/(^|[^:'"`])\/\/[^\n]*/g, (m, lead: string) => lead + ' '.repeat(m.length - lead.length));

const SOURCES = [
  ...sourceFiles(SRC).map(path => ({ path, text: stripComments(readFileSync(path, 'utf8')) })),
  { path: join(WEB_ROOT, 'index.html'), text: readFileSync(join(WEB_ROOT, 'index.html'), 'utf8') }
];

// space-x-*, space-y-*, divide-* (widths, colors, styles, reverse), with any
// variants in front and an optional negative sign.
const SPACE_DIVIDE =
  /(?<![\w\-:[\].\\])((?:[\w\-[\]@()&.:]+:)?-?(?:space-[xy]|divide)-[\w.[\]/-]*[\w\]])/g;

const usedSpaceDivideClasses = () => {
  const found = new Map<string, string>();
  for (const { path, text } of SOURCES) {
    for (const m of text.matchAll(SPACE_DIVIDE)) {
      if (!found.has(m[1])) found.set(m[1], relative(WEB_ROOT, path));
    }
  }
  return found;
};

const cssEscape = (cls: string) => cls.replace(/[^\w-]/g, c => `\\${c}`);
const escapeRegExp = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

const INDEX_CSS = readFileSync(join(SRC, 'index.css'), 'utf8');

// The selector lists of the `:where(...) > *` resets (allowing one level of
// nested parentheses, for `:is(.dark *)`).
const RESET_LISTS = [...INDEX_CSS.matchAll(/:where\(((?:[^()]|\([^()]*\))*)\)\s*>\s*\*/g)].map(m => m[1]);

// `.<class>` not followed by more of a class name (so space-y-1 does not
// match .space-y-1\.5).
const classSelector = (cls: string) => new RegExp(`\\.${escapeRegExp(cssEscape(cls))}(?![\\w\\\\-])`);

describe('Tailwind v3 compatibility in index.css', () => {
  const used = usedSpaceDivideClasses();

  it('finds the space-y / divide classes the app uses (the scan is not vacuous)', () => {
    for (const cls of ['space-y-2', 'space-y-0.5', 'divide-y', 'sm:divide-x', 'dark:divide-slate-800']) {
      expect([...used.keys()]).toContain(cls);
    }
  });

  it('has a reset and a v3 rule for every space-* / divide-* class used in src/ and index.html', () => {
    const missing: string[] = [];
    for (const [cls, file] of used) {
      const selector = classSelector(cls);
      const hasReset = RESET_LISTS.some(list => selector.test(list));
      const hasV3Rule = INDEX_CSS.includes(`.${cssEscape(cls)} > :not([hidden]) ~ :not([hidden])`);
      if (!hasReset || !hasV3Rule) {
        missing.push(`${cls} (${file}): ${hasReset ? '' : 'no :where(...) > * reset; '}${hasV3Rule ? '' : 'no v3 rule'}`);
      }
    }
    expect(missing).toEqual([]);
  });
});

describe('overflow-wrap classes', () => {
  it('never puts plain wrap-break-word and wrap-anywhere on the same element', () => {
    const offenders: string[] = [];
    for (const { path, text } of SOURCES) {
      // Each string / template literal is one class list (or part of one).
      for (const m of text.matchAll(/(['"`])((?:(?!\1)[^\\]|\\.)*)\1/g)) {
        const classes = new Set(m[2].split(/\s+/));
        if (classes.has('wrap-break-word') && classes.has('wrap-anywhere')) {
          const line = text.slice(0, m.index).split('\n').length;
          offenders.push(`${relative(WEB_ROOT, path)}:${line}`);
        }
      }
    }
    expect(offenders).toEqual([]);
  });
});
