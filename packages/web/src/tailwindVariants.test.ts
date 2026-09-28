// @vitest-environment node
import { describe, it, expect } from 'vitest';
import path from 'node:path';
import postcss from 'postcss';
import tailwindcss from 'tailwindcss';

// DFLT-00260: pins down the CSS output order of the named rem media-query
// variants defined in tailwind.config.js (below-80rem / upto-15rem), which
// decides which one wins when two of them set the same property on one
// element. See the "Ordering caveat" comment in tailwind.config.js.
const CONFIG = path.resolve(__dirname, '../tailwind.config.js');

// Class names are assembled at run time on purpose: tailwind.config.js scans
// src/**/*.ts as content, so writing e.g. the upto-15rem gap class literally
// here would add an otherwise unused rule to the app's CSS.
const v = (variant: string, utility: string) => [variant, utility].join(':');
const BELOW = 'below-80rem';
const UPTO = 'upto-15rem';

async function buildCss(classes: string): Promise<string> {
  const result = await postcss([
    tailwindcss({
      config: CONFIG,
      content: [{ raw: `<div class="${classes}"></div>`, extension: 'html' }],
      corePlugins: { preflight: false },
    }),
  ]).process('@tailwind utilities;', { from: undefined });
  return result.css;
}

describe('named rem media-query variants (tailwind.config.js)', () => {
  it('keeps the same media conditions as the arbitrary variants they replaced', async () => {
    const css = await buildCss(`${v(BELOW, 'gap-x-3')} ${v(UPTO, 'gap-x-1')}`);
    expect(css).toContain('@media not all and (min-width: 80rem)');
    expect(css).toContain('@media (max-width: 15rem)');
  });

  it('emits upto-15rem after below-80rem, so the narrower condition wins on the same property', async () => {
    const css = await buildCss(`${v(BELOW, 'gap-x-3')} ${v(UPTO, 'gap-x-1')}`);
    const below = css.indexOf('@media not all and (min-width: 80rem)');
    const upto = css.indexOf('@media (max-width: 15rem)');
    expect(below).toBeGreaterThanOrEqual(0);
    expect(upto).toBeGreaterThan(below);
  });

  it('is emitted before the core max-sm: screen variant (documented caveat)', async () => {
    const css = await buildCss(`${v(UPTO, 'p-2')} ${v('max-sm', 'p-3')}`);
    const upto = css.indexOf('@media (max-width: 15rem)');
    const maxSm = css.indexOf('@media not all and (min-width: 640px)');
    expect(upto).toBeGreaterThanOrEqual(0);
    expect(maxSm).toBeGreaterThan(upto);
  });
});
