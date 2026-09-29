// @vitest-environment node
// DFLT-00321: source checks that keep two accessibility fixes from being
// undone by a new component.
// - Every spinner stops under prefers-reduced-motion: reduce. Spinners get
//   that from components/Spinner.tsx (Spinner / SPIN_CLASS), the only
//   non-test file allowed to write the spin class, so a new unconditional
//   spinner fails here.
// - No control drops its keyboard focus indicator with a ring-0 focus
//   override (the checkboxes in LabelSelect, MultiSelectFilter and
//   ReviewGatesEditor had one).
// Class names that must not reach the app's CSS are assembled at run time:
// Tailwind scans src/ for candidates, test files included.
import { describe, expect, it } from 'vitest';
import fs from 'node:fs';
import path from 'node:path';

const SRC = __dirname;

function walk(dir: string): string[] {
  return fs.readdirSync(dir, { withFileTypes: true }).flatMap(entry => {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) return walk(full);
    return /\.(tsx?|jsx?|css)$/.test(entry.name) && !/\.test\.(tsx?|jsx?)$/.test(entry.name) ? [full] : [];
  });
}

const SOURCES = walk(SRC)
  .map(file => path.relative(SRC, file))
  .sort();
const read = (rel: string) => fs.readFileSync(path.join(SRC, rel), 'utf8');

const SPIN = ['animate', 'spin'].join('-');
const NO_FOCUS_RING = ['focus', 'ring-0'].join(':');

describe('source guards (DFLT-00321)', () => {
  it('finds the sources to check', () => {
    // Guards against the checks below passing on an empty listing.
    for (const rel of ['App.tsx', 'components/Spinner.tsx', 'components/TicketItem.tsx', 'components/LabelSelect.tsx', 'components/settings/ReviewGatesEditor.tsx', 'index.css']) {
      expect(SOURCES).toContain(rel);
    }
  });

  it('writes the spin class only in the Spinner module, which pairs it with motion-reduce:animate-none', () => {
    const offenders = SOURCES.filter(rel => rel !== path.join('components', 'Spinner.tsx') && read(rel).includes(SPIN));
    expect(offenders).toEqual([]);
    expect(read(path.join('components', 'Spinner.tsx'))).toContain(`'${SPIN} motion-reduce:animate-none'`);
  });

  it.each(SOURCES)('%s has no ring-0 focus override', rel => {
    expect(read(rel)).not.toContain(NO_FOCUS_RING);
  });
});
