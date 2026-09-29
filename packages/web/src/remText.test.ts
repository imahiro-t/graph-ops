// @vitest-environment node
import { describe, it, expect } from 'vitest';
import fs from 'node:fs';
import path from 'node:path';

// DFLT-00294: every small text size in the web UI is set in rem
// (text-[0.5625rem] / text-[0.625rem] / text-[0.6875rem], 9px / 10px / 11px
// at the default 16px), not in px, so it follows the browser's default font
// size (WCAG 1.4.4). DFLT-00281 and DFLT-00287 did this for TicketItem.tsx and
// settings/; DFLT-00294 did the rest (the project switcher's prefix, the
// pending-approval badge, labels, the Gherkin / Markdown viewers, the modals,
// tooltips, the autopilot decisions, the ticket family and the node types
// list's "default" badge). This reads every non-test source file under src/
// and fails if a px text size comes back outside comments. (DFLT-00320 also
// moved the execution graph's SVG fontSize attribute to rem and added a check
// for font sizes set outside a text size class.)
//
// Class names are matched by regular expressions or assembled at run time on
// purpose: Tailwind used to scan the test files too, so writing a px text
// class literally here added an otherwise unused rule to the app's CSS.
// index.css now leaves the test files out (DFLT-00323); this stays as a
// second guard.
const SRC = __dirname;

function listSources(dir: string): string[] {
  const out: string[] = [];
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      // src/test holds test setup and helpers, not app code.
      if (full === path.join(SRC, 'test')) continue;
      out.push(...listSources(full));
    } else if (/\.(tsx?|jsx?)$/.test(entry.name) && !/\.test\.(tsx?|jsx?)$/.test(entry.name)) {
      out.push(path.relative(SRC, full).split(path.sep).join('/'));
    }
  }
  return out.sort();
}

// Removes /* ... */ (including JSX {/* ... */}) and // line comments, as
// TicketItem.remText.test.ts does. A line comment is only taken to start at
// the beginning of a line or after whitespace, so a `//` inside a string such
// as a URL is left alone. Stripping too much is caught by the rem counts below.
function stripComments(src: string): string {
  return src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|\s)\/\/[^\n]*/g, '$1');
}

const SOURCES = listSources(SRC);
const code = (name: string) => stripComments(fs.readFileSync(path.join(SRC, name), 'utf8'));
const PX_TEXT = /text-\[\d+(?:\.\d+)?px\]/g;
const remText = (size: string) => ['text-[', size, 'rem]'].join('');
const count = (src: string, cls: string) => src.split(cls).length - 1;

// The sizes DFLT-00294 replaced, per file (outside comments). Lower bounds,
// so adding more rem sizes later is fine; a replacement reverting to px, or
// the comment stripping eating code, drops a file below them.
const REPLACED: Record<string, Partial<Record<'0.5625' | '0.625' | '0.6875', number>>> = {
  'App.tsx': { '0.625': 1 },
  'components/PendingApprovalBadge.tsx': { '0.625': 1 },
  'components/TicketFamily.tsx': { '0.625': 1 },
  'components/AutopilotDecisions.tsx': { '0.625': 1, '0.6875': 1 },
  'components/LabelSelect.tsx': { '0.6875': 2 },
  'components/IconButton.tsx': { '0.6875': 1 },
  'components/LabelChip.tsx': { '0.6875': 1 },
  'components/GherkinViewer.tsx': { '0.6875': 2 },
  'components/CreateTicketModal.tsx': { '0.6875': 1 },
  'components/MarkdownViewer.tsx': { '0.6875': 3 },
  'components/ProjectSetupModal.tsx': { '0.6875': 4 },
  // DFLT-00320 moved the "default" badge from 0.5625rem to 0.6875rem (the
  // other 3 are the editor's existing 11px sizes).
  'components/settings/NodeTypesEditor.tsx': { '0.6875': 4 },
  // DFLT-00320: the execution graph's node name labels (a fontSize="9"
  // attribute before).
  'components/TicketItem.tsx': { '0.5625': 1 }
};

// DFLT-00320: a font size set outside a text size class is not seen by the
// text-[Npx] check, and in px / SVG user units it ignores the browser's
// default font size (the execution graph's fontSize="9" attribute was one).
// Sizes are set with rem text size classes, so any such font size is rejected,
// whatever its unit: an SVG fontSize attribute or a fontSize style whose value
// is a number or starts with one (fontSize="9", fontSize={9},
// { fontSize: '9px' }, but also '0.5rem'), and any CSS font-size declaration.
const FONT_SIZE_OUTSIDE_CLASSES = [
  /fontSize\s*=\s*(?:"\s*[\d.]|'\s*[\d.]|\{\s*[\d.'"])/g,
  /fontSize\s*:\s*['"]?\s*\d/g,
  /font-size\s*[:=]/g
];

describe('src/ text sizes (DFLT-00294)', () => {
  it('finds the source files to check', () => {
    // Guards against the checks below passing on an empty or partial listing.
    for (const name of [...Object.keys(REPLACED), 'components/TicketItem.tsx', 'main.tsx']) {
      expect(SOURCES).toContain(name);
    }
    expect(SOURCES.some(name => name.includes('.test.'))).toBe(false);
    expect(SOURCES.some(name => name.startsWith('test/'))).toBe(false);
  });

  it.each(SOURCES)('%s uses no px text size (a text-[Npx] class) outside comments', name => {
    expect(code(name).match(PX_TEXT) ?? []).toEqual([]);
  });

  it.each(SOURCES)('%s sets no font size outside a text size class (fontSize attribute or style, CSS font-size) outside comments', name => {
    const src = code(name);
    for (const re of FONT_SIZE_OUTSIDE_CLASSES) expect(src.match(re) ?? []).toEqual([]);
  });

  it.each(Object.entries(REPLACED))('%s uses the rem sizes instead', (name, sizes) => {
    const src = code(name);
    for (const [size, n] of Object.entries(sizes)) {
      expect(count(src, remText(size))).toBeGreaterThanOrEqual(n!);
    }
  });
});
