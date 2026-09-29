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
// DFLT-00281 added below-64rem / from-64rem / from-80rem, replacing the
// arbitrary [@media_not_all_and_(min-width:64rem)]:, [@media(min-width:64rem)]:
// and [@media(min-width:80rem)]: prefixes. Their order against the core
// screen variants and the other named variants is the same as before, which
// is checked by building the same classes both ways. Their order against the
// arbitrary variants that stay ([@container(...)]:,
// [@media(max-width:200px)]:) is not: they now come out before those. That
// intended change is pinned down separately.
// DFLT-00290 added upto-200px, which replaces TicketItem.tsx's
// [@media(max-width:200px)]: prefix on p-2 with the same (px) condition, and
// upto-7_5rem, a 7.5rem max-width query for a large default font in a very
// narrow window ("." is not allowed in a variant name). Both are declared
// last, upto-7_5rem after upto-200px, so that on the same property
// upto-7_5rem wins over upto-15rem and upto-200px -- which the arbitrary
// [@media(max-width:200px)]: it replaced, emitted after every named variant,
// would not have let it do.
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

const BELOW64 = 'below-64rem';
const FROM64 = 'from-64rem';
const FROM80 = 'from-80rem';
const BELOW64_MEDIA = '@media not all and (min-width: 64rem)';
const FROM64_MEDIA = '@media (min-width: 64rem)';
const FROM80_MEDIA = '@media (min-width: 80rem)';

const UPTO200PX = 'upto-200px';
const UPTO7_5 = 'upto-7_5rem';
const UPTO200PX_MEDIA = '@media (max-width: 200px)';
const UPTO7_5_MEDIA = '@media (max-width: 7.5rem)';

// The arbitrary variants DFLT-00281 replaced, and the ones that stay, also
// assembled at run time.
const arb = (...parts: string[]) => ['[', ...parts, ']'].join('');
const ARB_BELOW64 = arb('@media_not_all_and_(min-width:', '64rem)');
const ARB_FROM64 = arb('@media(min-width:', '64rem)');
const ARB_FROM80 = arb('@media(min-width:', '80rem)');
const ARB_MAX200 = arb('@media(max-width:', '200px)');
const ARB_CONTAINER = arb('@container(min-width:', '20rem)');

// The @media / @container conditions of the built CSS in the order they first
// appear, with whitespace removed: the arbitrary forms print
// `(min-width:64rem)`, the named ones `(min-width: 64rem)`. Only at-rule
// headers are taken (at the start of a line or after `{`, `}` or
// whitespace), not the escaped `\[\@media...` inside a selector.
function atRuleConditions(css: string): string[] {
  const seen: string[] = [];
  for (const m of css.matchAll(/(?:^|[\s{}])(@(?:media|container)[^{]*)\{/g)) {
    const cond = m[1].replace(/\s+/g, '');
    if (!seen.includes(cond)) seen.push(cond);
  }
  return seen;
}

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

  it('keeps the media conditions of the arbitrary variants below-64rem / from-64rem / from-80rem replaced (DFLT-00281)', async () => {
    const named = await buildCss([v(BELOW64, 'min-w-0'), v(FROM64, 'px-4'), v(FROM80, 'pb-0')]);
    expect(named).toContain(BELOW64_MEDIA);
    expect(named).toContain(FROM64_MEDIA);
    expect(named).toContain(FROM80_MEDIA);
    const arbitrary = await buildCss([v(ARB_BELOW64, 'min-w-0'), v(ARB_FROM64, 'px-4'), v(ARB_FROM80, 'pb-0')]);
    expect(atRuleConditions(named)).toEqual(atRuleConditions(arbitrary));
    expect(atRuleConditions(named)).toEqual(
      expect.arrayContaining(['@medianotalland(min-width:64rem)', '@media(min-width:64rem)', '@media(min-width:80rem)'])
    );
  });

  it('emits the named variants in declaration order: below-80rem, upto-15rem, below-64rem, from-64rem, from-80rem', async () => {
    const css = await buildCss([v(FROM80, 'p-1'), v(FROM64, 'p-1'), v(BELOW64, 'p-1'), v(UPTO, 'p-1'), v(BELOW, 'p-1')]);
    const at = [BELOW_MEDIA, UPTO_MEDIA, BELOW64_MEDIA, FROM64_MEDIA, FROM80_MEDIA].map(m => css.indexOf(m));
    expect(at.every(i => i >= 0)).toBe(true);
    expect([...at].sort((a, b) => a - b)).toEqual(at);
  });

  it('orders below-64rem / from-64rem / from-80rem against the core screen and the other named variants exactly as the arbitrary prefixes did', async () => {
    const common = [v('max-lg', 'p-1'), v('lg', 'p-1'), v('max-sm', 'p-1'), v(BELOW, 'p-1'), v(UPTO, 'p-1')];
    const before = await buildCss([...common, v(ARB_BELOW64, 'p-1'), v(ARB_FROM64, 'p-1'), v(ARB_FROM80, 'p-1')]);
    const after = await buildCss([...common, v(BELOW64, 'p-1'), v(FROM64, 'p-1'), v(FROM80, 'p-1')]);
    const conditions = atRuleConditions(after);
    expect(conditions).toEqual(atRuleConditions(before));
    // The three come out after the core and the DFLT-00260 variants.
    const idx = (c: string) => conditions.indexOf(c);
    for (const c of ['@media(width<1024px)', '@media(width>=1024px)', '@media(width<640px)', '@medianotalland(min-width:80rem)', '@media(max-width:15rem)']) {
      expect(idx(c)).toBeGreaterThanOrEqual(0);
      expect(idx(c)).toBeLessThan(idx('@medianotalland(min-width:64rem)'));
    }
    expect(idx('@medianotalland(min-width:64rem)')).toBeLessThan(idx('@media(min-width:64rem)'));
    expect(idx('@media(min-width:64rem)')).toBeLessThan(idx('@media(min-width:80rem)'));
  });

  it('emits below-64rem / from-64rem / from-80rem before the arbitrary @container and max-width:200px variants (an intended change, see index.css)', async () => {
    // As arbitrary prefixes they came out after [@container(...)]:, and the
    // 64rem / 80rem min-width ones also after [@media(max-width:200px)]:.
    // Named, they precede every arbitrary variant, so an arbitrary variant
    // setting the same property on the same element now wins. The
    // Ordering caveat in index.css describes this.
    const classes = [v(ARB_CONTAINER, 'p-1'), v(ARB_MAX200, 'p-1'), v(BELOW64, 'p-1'), v(FROM64, 'p-1'), v(FROM80, 'p-1')];
    const conditions = atRuleConditions(await buildCss(classes));
    const idx = (c: string) => conditions.indexOf(c);
    const container = idx('@container(min-width:20rem)');
    const max200 = idx('@media(max-width:200px)');
    expect(container).toBeGreaterThanOrEqual(0);
    expect(max200).toBeGreaterThanOrEqual(0);
    for (const c of ['@medianotalland(min-width:64rem)', '@media(min-width:64rem)', '@media(min-width:80rem)']) {
      expect(idx(c)).toBeGreaterThanOrEqual(0);
      expect(idx(c)).toBeLessThan(container);
      expect(idx(c)).toBeLessThan(max200);
    }
  });

  it('keeps the media condition of the arbitrary [@media(max-width:200px)]: that upto-200px replaced (DFLT-00290)', async () => {
    const named = await buildCss([v(UPTO200PX, 'p-2')]);
    expect(named).toContain(UPTO200PX_MEDIA);
    const arbitrary = await buildCss([v(ARB_MAX200, 'p-2')]);
    expect(atRuleConditions(named)).toEqual(atRuleConditions(arbitrary));
    expect(atRuleConditions(named)).toContain('@media(max-width:200px)');
  });

  it('gives upto-7_5rem a 7.5rem max-width condition (DFLT-00290)', async () => {
    const css = await buildCss([v(UPTO7_5, 'p-1')]);
    expect(css).toContain(UPTO7_5_MEDIA);
    expect(atRuleConditions(css)).toContain('@media(max-width:7.5rem)');
    // The rule really is generated for the class (the variant name is valid).
    expect(css).toMatch(/\.upto-7_5rem\\:p-1\s*\{/);
  });

  it('emits the named variants in declaration order, upto-200px and upto-7_5rem last (DFLT-00290)', async () => {
    const css = await buildCss([
      v(UPTO7_5, 'p-1'),
      v(UPTO200PX, 'p-1'),
      v(FROM80, 'p-1'),
      v(FROM64, 'p-1'),
      v(BELOW64, 'p-1'),
      v(UPTO, 'p-1'),
      v(BELOW, 'p-1')
    ]);
    const at = [BELOW_MEDIA, UPTO_MEDIA, BELOW64_MEDIA, FROM64_MEDIA, FROM80_MEDIA, UPTO200PX_MEDIA, UPTO7_5_MEDIA].map(m => css.indexOf(m));
    expect(at.every(i => i >= 0)).toBe(true);
    expect([...at].sort((a, b) => a - b)).toEqual(at);
  });

  it('lets upto-7_5rem win over upto-15rem and upto-200px on the same property, and upto-200px over upto-15rem (DFLT-00290)', async () => {
    // The classes TicketItem.tsx puts on the expanded details and the Action
    // Footer card: the one emitted last is the one that applies.
    const css = await buildCss([v(UPTO, 'p-3'), v(UPTO200PX, 'p-2'), v(UPTO7_5, 'p-1'), v('max-sm', 'p-3')]);
    const conditions = atRuleConditions(css);
    const idx = (c: string) => conditions.indexOf(c);
    expect(idx('@media(width<640px)')).toBeLessThan(idx('@media(max-width:15rem)'));
    expect(idx('@media(max-width:15rem)')).toBeLessThan(idx('@media(max-width:200px)'));
    expect(idx('@media(max-width:200px)')).toBeLessThan(idx('@media(max-width:7.5rem)'));
  });

  it('would have lost to the arbitrary [@media(max-width:200px)]: it replaced, which is why that became upto-200px (DFLT-00290)', async () => {
    const conditions = atRuleConditions(await buildCss([v(ARB_MAX200, 'p-2'), v(UPTO7_5, 'p-1')]));
    expect(conditions.indexOf('@media(max-width:7.5rem)')).toBeGreaterThanOrEqual(0);
    expect(conditions.indexOf('@media(max-width:7.5rem)')).toBeLessThan(conditions.indexOf('@media(max-width:200px)'));
  });

  it('emits upto-200px / upto-7_5rem before the arbitrary @container variants (DFLT-00290, see index.css)', async () => {
    const conditions = atRuleConditions(await buildCss([v(ARB_CONTAINER, 'p-1'), v(UPTO200PX, 'p-1'), v(UPTO7_5, 'p-1')]));
    const container = conditions.indexOf('@container(min-width:20rem)');
    expect(container).toBeGreaterThanOrEqual(0);
    expect(conditions.indexOf('@media(max-width:200px)')).toBeLessThan(container);
    expect(conditions.indexOf('@media(max-width:7.5rem)')).toBeLessThan(container);
  });
});
