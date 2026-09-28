// @vitest-environment node
import { describe, it, expect } from 'vitest';
import fs from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';
import { compile } from 'tailwindcss';

// DFLT-00260: pins down the CSS output order of the named rem media-query
// variants defined in index.css (below-80rem / upto-15rem), which decides
// which one wins when two of them, or one of them and a core screen variant,
// set the same property on one element. See the comment next to their
// @custom-variant definitions in index.css.
const INDEX_CSS = path.resolve(__dirname, 'index.css');
const require = createRequire(import.meta.url);

// Class names are assembled at run time on purpose: Tailwind scans src/ for
// candidates, so writing e.g. the upto-15rem gap class literally here would
// add an otherwise unused rule to the app's CSS.
const v = (variant: string, utility: string) => [variant, utility].join(':');
const BELOW = 'below-80rem';
const UPTO = 'upto-15rem';

const BELOW_MEDIA = '@media not all and (min-width: 80rem)';
const UPTO_MEDIA = '@media (max-width: 15rem)';

async function buildCss(classes: string[]): Promise<string> {
  const compiler = await compile(fs.readFileSync(INDEX_CSS, 'utf8'), {
    base: path.dirname(INDEX_CSS),
    async loadStylesheet(id: string, base: string) {
      const file = id === 'tailwindcss' ? require.resolve('tailwindcss/index.css') : path.resolve(base, id);
      return { path: file, base: path.dirname(file), content: fs.readFileSync(file, 'utf8') };
    }
  });
  return compiler.build(classes);
}

describe('named rem media-query variants (index.css)', () => {
  it('keeps the same media conditions as the arbitrary variants they replaced', async () => {
    const css = await buildCss([v(BELOW, 'gap-x-3'), v(UPTO, 'gap-x-1')]);
    expect(css).toContain(BELOW_MEDIA);
    expect(css).toContain(UPTO_MEDIA);
  });

  it('emits upto-15rem after below-80rem, so the narrower condition wins on the same property', async () => {
    const css = await buildCss([v(BELOW, 'gap-x-3'), v(UPTO, 'gap-x-1')]);
    const below = css.indexOf(BELOW_MEDIA);
    const upto = css.indexOf(UPTO_MEDIA);
    expect(below).toBeGreaterThanOrEqual(0);
    expect(upto).toBeGreaterThan(below);
  });

  it('is emitted after the core max-sm: screen variant, so it wins over max-sm: on the same property', async () => {
    const css = await buildCss([v(UPTO, 'p-2'), v('max-sm', 'p-3')]);
    const upto = css.indexOf(UPTO_MEDIA);
    const maxSm = css.indexOf('@media (width < 640px)');
    expect(maxSm).toBeGreaterThanOrEqual(0);
    expect(upto).toBeGreaterThan(maxSm);
  });
});
