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
// arbitrary variants that stayed then ([@container(...)]:,
// [@media(max-width:200px)]:) was not: they came out before those. That
// intended change is pinned down separately.
// DFLT-00294 named those last arbitrary at-rule prefixes too: upto-200px,
// cq-upto-12rem, cq-from-16rem (as sm:cq-from-16rem:) and cq-below-8rem.
// They are declared after the other named variants, in the order Tailwind
// emitted the arbitrary prefixes, so their order against the core screen
// variants, the other named variants and one another is the same as before;
// that is checked by building the same classes both ways, as for DFLT-00281.
// Any arbitrary variant still comes out after every named one.
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

// The arbitrary variants DFLT-00281 replaced, and the ones that stay, also
// assembled at run time.
const arb = (...parts: string[]) => ['[', ...parts, ']'].join('');
const ARB_BELOW64 = arb('@media_not_all_and_(min-width:', '64rem)');
const ARB_FROM64 = arb('@media(min-width:', '64rem)');
const ARB_FROM80 = arb('@media(min-width:', '80rem)');
const ARB_CONTAINER = arb('@container(min-width:', '20rem)');

// DFLT-00294: the named variants and the arbitrary prefixes they replaced.
const UPTO200 = 'upto-200px';
const CQ_UPTO12 = 'cq-upto-12rem';
const CQ_FROM16 = 'cq-from-16rem';
const CQ_BELOW8 = 'cq-below-8rem';
const ARB_MAX200 = arb('@media(max-width:', '200px)');
const ARB_CQ_UPTO12 = arb('@container(max-width:', '12rem)');
const ARB_CQ_FROM16 = arb('@container(min-width:', '16rem)');
const ARB_CQ_BELOW8 = arb('@container(width<', '8rem)');

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

  it('emits below-64rem / from-64rem / from-80rem before the container-query and max-width:200px variants (DFLT-00281, kept by DFLT-00294)', async () => {
    // As arbitrary prefixes below-64rem / from-64rem / from-80rem came out
    // after [@container(...)]:, and the 64rem / 80rem min-width ones also
    // after [@media(max-width:200px)]:. Named (DFLT-00281) they came out
    // before those; DFLT-00294 named the latter too and declared them after
    // the others, so that order stays.
    const classes = [v(CQ_UPTO12, 'p-1'), v(CQ_BELOW8, 'p-1'), v(UPTO200, 'p-1'), v(BELOW64, 'p-1'), v(FROM64, 'p-1'), v(FROM80, 'p-1')];
    const conditions = atRuleConditions(await buildCss(classes));
    const idx = (c: string) => conditions.indexOf(c);
    for (const later of ['@container(max-width:12rem)', '@container(width<8rem)', '@media(max-width:200px)']) {
      expect(idx(later)).toBeGreaterThanOrEqual(0);
      for (const c of ['@medianotalland(min-width:64rem)', '@media(min-width:64rem)', '@media(min-width:80rem)']) {
        expect(idx(c)).toBeGreaterThanOrEqual(0);
        expect(idx(c)).toBeLessThan(idx(later));
      }
    }
  });

  it('still emits an arbitrary at-rule variant after every named one', async () => {
    // No arbitrary @media / @container prefix is left in src/ (DFLT-00294);
    // one added later comes out after all the named variants and so wins
    // over them on the same property. The Ordering caveat in index.css
    // describes this.
    const named = [BELOW, UPTO, BELOW64, FROM64, FROM80, CQ_UPTO12, CQ_BELOW8, UPTO200];
    const conditions = atRuleConditions(await buildCss([v(ARB_CONTAINER, 'p-1'), ...named.map(n => v(n, 'p-1'))]));
    const container = conditions.indexOf('@container(min-width:20rem)');
    expect(container).toBeGreaterThanOrEqual(0);
    const namedConditions = [
      '@medianotalland(min-width:80rem)', '@media(max-width:15rem)', '@medianotalland(min-width:64rem)',
      '@media(min-width:64rem)', '@media(min-width:80rem)', '@container(max-width:12rem)',
      '@container(width<8rem)', '@media(max-width:200px)'
    ];
    for (const c of namedConditions) {
      expect(conditions.indexOf(c)).toBeGreaterThanOrEqual(0);
      expect(conditions.indexOf(c)).toBeLessThan(container);
    }
  });
});

describe('named variants for the last arbitrary at-rule prefixes (DFLT-00294)', () => {
  // The classes the app uses them with (App.tsx, AutopilotControls.tsx,
  // TicketItem.tsx) and the other variants set on the same elements.
  const pairs: Array<[string, string]> = [
    [UPTO200, ARB_MAX200],
    [CQ_UPTO12, ARB_CQ_UPTO12],
    [CQ_BELOW8, ARB_CQ_BELOW8]
  ];

  it('keeps the conditions of the arbitrary prefixes they replaced', async () => {
    for (const [name, arbitrary] of pairs) {
      const named = atRuleConditions(await buildCss([v(name, 'p-1')]));
      expect(named).toEqual(atRuleConditions(await buildCss([v(arbitrary, 'p-1')])));
    }
    // The first condition is the variant's own (index.css's other rules add
    // an @media of their own after the utilities).
    expect(atRuleConditions(await buildCss([v(UPTO200, 'p-1')]))[0]).toBe('@media(max-width:200px)');
    expect(atRuleConditions(await buildCss([v(CQ_UPTO12, 'p-1')]))[0]).toBe('@container(max-width:12rem)');
    expect(atRuleConditions(await buildCss([v(CQ_BELOW8, 'p-1')]))[0]).toBe('@container(width<8rem)');
  });

  it('nests sm:cq-from-16rem: the same way as sm:[@container(min-width:16rem)]: (@container inside the sm @media)', async () => {
    const flat = (css: string) => css.replace(/\s+/g, '');
    const nested = '@media(width>=640px){@container(min-width:16rem){';
    const named = flat(await buildCss([v('sm', v(CQ_FROM16, 'pl-3'))]));
    const arbitrary = flat(await buildCss([v('sm', v(ARB_CQ_FROM16, 'pl-3'))]));
    expect(named).toContain(nested);
    expect(arbitrary).toContain(nested);
    expect(named.match(/@container/g)).toHaveLength(1);
  });

  it('emits them after the other named variants, in declaration order: cq-upto-12rem, sm:cq-from-16rem, cq-below-8rem, upto-200px', async () => {
    const css = await buildCss([
      v(UPTO200, 'p-1'), v(CQ_BELOW8, 'p-1'), v('sm', v(CQ_FROM16, 'p-1')), v(CQ_UPTO12, 'p-1'),
      v(FROM80, 'p-1'), v(FROM64, 'p-1'), v(BELOW64, 'p-1'), v(UPTO, 'p-1'), v(BELOW, 'p-1')
    ]);
    const conditions = atRuleConditions(css);
    expect(conditions).toEqual([
      '@medianotalland(min-width:80rem)',
      '@media(max-width:15rem)',
      '@medianotalland(min-width:64rem)',
      '@media(min-width:64rem)',
      '@media(min-width:80rem)',
      '@container(max-width:12rem)',
      '@media(width>=640px)',
      '@container(min-width:16rem)',
      '@container(width<8rem)',
      '@media(max-width:200px)'
    ]);
  });

  it('emits upto-200px after max-sm: and upto-15rem:, so TicketItem\'s upto-200px:p-2 wins over their p-3', async () => {
    const conditions = atRuleConditions(await buildCss([v(UPTO200, 'p-2'), v(UPTO, 'p-3'), v('max-sm', 'p-3')]));
    const idx = (c: string) => conditions.indexOf(c);
    expect(idx('@media(width<640px)')).toBeGreaterThanOrEqual(0);
    expect(idx('@media(max-width:15rem)')).toBeGreaterThan(idx('@media(width<640px)'));
    expect(idx('@media(max-width:200px)')).toBeGreaterThan(idx('@media(max-width:15rem)'));
  });

  it('orders them against the core screen variants, narrow:, the other named variants and one another exactly as the arbitrary prefixes did', async () => {
    const common = ['sm', 'max-sm', 'lg', 'max-lg', 'narrow', BELOW, UPTO, BELOW64, FROM64, FROM80].map(n => v(n, 'p-1'));
    const before = await buildCss([...common, v(ARB_MAX200, 'p-1'), v(ARB_CQ_BELOW8, 'p-1'), v('sm', v(ARB_CQ_FROM16, 'p-1')), v(ARB_CQ_UPTO12, 'p-1')]);
    const after = await buildCss([...common, v(UPTO200, 'p-1'), v(CQ_BELOW8, 'p-1'), v('sm', v(CQ_FROM16, 'p-1')), v(CQ_UPTO12, 'p-1')]);
    const conditions = atRuleConditions(after);
    expect(conditions).toEqual(atRuleConditions(before));
    // The four come after the core, narrow: and the other named ones.
    for (const c of ['@media(width>=640px)', '@media(width<640px)', '@media(width>=1024px)', '@media(width<1024px)', '@media(width<48rem)', '@media(min-width:80rem)']) {
      expect(conditions.indexOf(c)).toBeGreaterThanOrEqual(0);
      expect(conditions.indexOf(c)).toBeLessThan(conditions.indexOf('@container(max-width:12rem)'));
    }
    const own = conditions.filter(c => /max-width:12rem|min-width:16rem|width<8rem|max-width:200px/.test(c));
    expect(own).toEqual([
      '@container(max-width:12rem)',
      '@container(min-width:16rem)',
      '@container(width<8rem)',
      '@media(max-width:200px)'
    ]);
    // Rule by rule too, not just the first appearance of each condition:
    // with the class names and whitespace taken out (the arbitrary forms
    // print `(max-width:200px)`, the named ones `(max-width: 200px)`) the
    // two builds are the same CSS.
    const rules = (css: string) =>
      css.slice(css.indexOf('@layer utilities')).replace(/\.[^\s{]+\s*\{/g, '.x{').replace(/\s+/g, '');
    expect(rules(after)).toEqual(rules(before));
  });
});
