// @vitest-environment node
import { describe, it, expect } from 'vitest';
import fs from 'node:fs';
import path from 'node:path';

// DFLT-00281: TicketItem.tsx sets its small text sizes in rem
// (text-[0.625rem] / text-[0.6875rem], 10px / 11px at the default 16px), not
// in px, so they follow the browser's default font size (WCAG 1.4.4). This
// reads the component's source and fails if a px text size comes back.
//
// Class names are matched by regular expressions or assembled at run time on
// purpose: Tailwind scans src/ for candidates, so writing a px text class
// literally here would add an otherwise unused rule to the app's CSS.
const SOURCE = path.resolve(__dirname, 'TicketItem.tsx');

// Removes /* ... */ (including JSX {/* ... */}) and // line comments. A line
// comment is only taken to start at the beginning of a line or after
// whitespace, so a `//` inside a string such as a URL is left alone. Being
// conservative is fine: stripping too much is caught by the rem counts below.
function stripComments(src: string): string {
  return src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|\s)\/\/[^\n]*/g, '$1');
}

const code = stripComments(fs.readFileSync(SOURCE, 'utf8'));
const count = (cls: string) => code.split(cls).length - 1;
const remText = (size: string) => ['text-[', size, 'rem]'].join('');

describe('TicketItem text sizes (DFLT-00281)', () => {
  it('uses no px text size (a text-[Npx] class) outside comments', () => {
    expect(code.match(/text-\[\d+(?:\.\d+)?px\]/g) ?? []).toEqual([]);
  });

  it('uses the rem equivalents instead (guards against the check above passing on stripped-out code)', () => {
    // Outside comments, at the time of DFLT-00281: 3 existing + 5 replaced
    // 10px sizes, and 8 existing + 13 replaced 11px sizes. Lower bounds, so
    // adding more rem sizes later is fine; stripping code with the comments
    // or missing a replacement drops below them.
    expect(count(remText('0.625'))).toBeGreaterThanOrEqual(8);
    expect(count(remText('0.6875'))).toBeGreaterThanOrEqual(21);
  });
});
