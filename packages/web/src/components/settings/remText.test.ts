// @vitest-environment node
import { describe, it, expect } from 'vitest';
import fs from 'node:fs';
import path from 'node:path';

// DFLT-00287: the settings editors set their small text sizes in rem
// (text-[0.625rem] / text-[0.6875rem], 10px / 11px at the default 16px), not
// in px, so they follow the browser's default font size (WCAG 1.4.4). This
// reads every non-test source file in this directory and fails if a px text
// size comes back. (DFLT-00294 also moved the node types list's 9px "default"
// badge to 0.5625rem and widened the check from 10px / 11px to every px size;
// DFLT-00320 made that badge 0.6875rem.)
//
// Class names are matched by regular expressions or assembled at run time on
// purpose: Tailwind used to scan the test files too, so writing a px text
// class literally here added an otherwise unused rule to the app's CSS.
// index.css now leaves the test files out (DFLT-00323); this stays as a
// second guard.
const DIR = __dirname;
const SOURCES = fs
  .readdirSync(DIR)
  .filter(name => /\.(tsx?|jsx?)$/.test(name) && !/\.test\.(tsx?|jsx?)$/.test(name))
  .sort();

const read = (name: string) => fs.readFileSync(path.join(DIR, name), 'utf8');
const PX_TEXT = /text-\[\d+(?:\.\d+)?px\]/g;
const remText = (size: string) => ['text-[', size, 'rem]'].join('');
const count = (src: string, cls: string) => src.split(cls).length - 1;

describe('settings/ text sizes (DFLT-00287)', () => {
  it('finds the settings editors to check', () => {
    // Guards against the checks below passing on an empty directory listing.
    for (const name of ['AppSettingsEditor.tsx', 'AutopilotSettingsEditor.tsx', 'NodeTypesEditor.tsx', 'SkillsEditor.tsx', 'TemplatesEditor.tsx', 'ReviewGatesEditor.tsx', 'TemplateTextEditor.tsx', 'ErrorBox.tsx', 'listPane.ts']) {
      expect(SOURCES).toContain(name);
    }
  });

  it.each(SOURCES)('%s uses no px text size, even in comments', name => {
    expect(read(name).match(PX_TEXT) ?? []).toEqual([]);
  });

  it('uses the rem equivalents instead', () => {
    // At the time of DFLT-00287: 48 replaced 10px sizes and 34 replaced 11px
    // sizes across the editors (the list heading's now lives in listPane.ts).
    // Lower bounds, so adding more rem sizes later is fine.
    const all = SOURCES.map(read).join('\n');
    expect(count(all, remText('0.625'))).toBeGreaterThanOrEqual(48);
    expect(count(all, remText('0.6875'))).toBeGreaterThanOrEqual(32);
    // DFLT-00294 made the node types list's "default" badge 0.5625rem;
    // DFLT-00320 made it 0.6875rem (11px), one more on top of the 32 above.
    expect(count(all, remText('0.6875'))).toBeGreaterThanOrEqual(33);
  });
});
