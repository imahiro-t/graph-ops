// @vitest-environment node
import { describe, it, expect } from 'vitest';
import fs from 'node:fs';
import path from 'node:path';
import { compileIndexCss } from './test/tailwindCompile';

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
// arbitrary variants that stayed then ([@container(...)]:) was not: they
// came out before those. That intended change is pinned down separately.
// DFLT-00294 named those last arbitrary at-rule prefixes too: cq-upto-12rem
// and cq-from-16rem (as sm:cq-from-16rem:). They are declared after the
// other named variants, in the order Tailwind emitted the arbitrary
// prefixes, so their order against the core screen variants, the other
// named variants and one another is the same as before; that is checked by
// building the same classes both ways, as for DFLT-00281. Any arbitrary
// variant still comes out after every named one.
// DFLT-00319: the supported range is a 320px window with up to 200% text,
// so the variants that only applied below that were removed; the last
// describe checks that they are gone.
// Class names are assembled at run time on purpose: Tailwind used to scan the
// test files too, so writing e.g. the upto-15rem gap class literally here
// added an otherwise unused rule to the app's CSS. index.css now leaves the
// test files out (DFLT-00323); this stays as a second guard.
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
const CQ_UPTO12 = 'cq-upto-12rem';
const CQ_FROM16 = 'cq-from-16rem';
const ARB_CQ_UPTO12 = arb('@container(max-width:', '12rem)');
const ARB_CQ_FROM16 = arb('@container(min-width:', '16rem)');

// DFLT-00319: the variants removed because they only applied below a 320px
// window with 200% text, assembled at run time like the others.
const REMOVED_VARIANTS = [['upto', '200px'], ['upto', '7_5rem'], ['cq', 'below', '8rem']].map(parts => parts.join('-'));

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
  return (await compileIndexCss()).build(classes);
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

  it('emits below-64rem / from-64rem / from-80rem before the container-query variants (DFLT-00281, kept by DFLT-00294)', async () => {
    // As arbitrary prefixes below-64rem / from-64rem / from-80rem came out
    // after [@container(...)]:. Named (DFLT-00281) they came out before
    // those; DFLT-00294 named the latter too and declared them after the
    // others, so that order stays.
    const classes = [v(CQ_UPTO12, 'p-1'), v('sm', v(CQ_FROM16, 'p-1')), v(BELOW64, 'p-1'), v(FROM64, 'p-1'), v(FROM80, 'p-1')];
    const conditions = atRuleConditions(await buildCss(classes));
    const idx = (c: string) => conditions.indexOf(c);
    for (const later of ['@container(max-width:12rem)', '@container(min-width:16rem)']) {
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
    const named = [BELOW, UPTO, BELOW64, FROM64, FROM80, CQ_UPTO12];
    const conditions = atRuleConditions(await buildCss([v(ARB_CONTAINER, 'p-1'), ...named.map(n => v(n, 'p-1'))]));
    const container = conditions.indexOf('@container(min-width:20rem)');
    expect(container).toBeGreaterThanOrEqual(0);
    const namedConditions = [
      '@medianotalland(min-width:80rem)', '@media(max-width:15rem)', '@medianotalland(min-width:64rem)',
      '@media(min-width:64rem)', '@media(min-width:80rem)', '@container(max-width:12rem)'
    ];
    for (const c of namedConditions) {
      expect(conditions.indexOf(c)).toBeGreaterThanOrEqual(0);
      expect(conditions.indexOf(c)).toBeLessThan(container);
    }
  });

  // DFLT-00293: LabelSelect's narrow panel (a 320px window with 200% text,
  // say) overrides unprefixed utilities through group-data-narrow:, which
  // depends on the later rule.
  it('emits group-data-narrow: rules after the plain utilities they override (DFLT-00293)', async () => {
    const narrow = (u: string) => v(['group', 'data', 'narrow'].join('-'), u);
    const css = await buildCss(['px-3', 'whitespace-nowrap', 'truncate', narrow('px-2'), narrow('whitespace-normal'), narrow('overflow-visible')]);
    const at = (needle: string) => css.indexOf(needle);
    expect(at('[data-narrow]')).toBeGreaterThan(at('.px-3'));
    expect(at('[data-narrow]')).toBeGreaterThan(at('.whitespace-nowrap'));
    expect(at('[data-narrow]')).toBeGreaterThan(at('.truncate'));
    expect(css).toMatch(/:where\(\.group\)\[data-narrow\]/);
  });
});

describe('named variants for the last arbitrary at-rule prefixes (DFLT-00294)', () => {
  // Each named variant and the arbitrary prefix it replaced. cq-from-16rem:
  // is only used inside sm: in the app, so the nesting test below covers it.
  const pairs: Array<[string, string]> = [[CQ_UPTO12, ARB_CQ_UPTO12]];

  it('keeps the conditions of the arbitrary prefixes they replaced', async () => {
    for (const [name, arbitrary] of pairs) {
      const named = atRuleConditions(await buildCss([v(name, 'p-1')]));
      expect(named).toEqual(atRuleConditions(await buildCss([v(arbitrary, 'p-1')])));
    }
    // The first condition is the variant's own (index.css's other rules add
    // an @media of their own after the utilities).
    expect(atRuleConditions(await buildCss([v(CQ_UPTO12, 'p-1')]))[0]).toBe('@container(max-width:12rem)');
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

  it('emits them after the other named variants, in declaration order: cq-upto-12rem, sm:cq-from-16rem', async () => {
    const css = await buildCss([
      v('sm', v(CQ_FROM16, 'p-1')), v(CQ_UPTO12, 'p-1'),
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
      '@container(min-width:16rem)'
    ]);
  });

  it('orders them against the core screen variants, narrow:, the other named variants and one another exactly as the arbitrary prefixes did', async () => {
    // In the app:
    // - cq-upto-12rem: shares elements with upto-15rem: (App.tsx).
    // - cq-from-16rem: is only used as sm:cq-from-16rem: (AutopilotControls.tsx).
    // The rest of the core screen variants, narrow: and the other named ones
    // are here so the order is pinned against all of them.
    const common = ['sm', 'max-sm', 'lg', 'max-lg', 'narrow', BELOW, UPTO, BELOW64, FROM64, FROM80].map(n => v(n, 'p-1'));
    const before = await buildCss([...common, v('sm', v(ARB_CQ_FROM16, 'p-1')), v(ARB_CQ_UPTO12, 'p-1')]);
    const after = await buildCss([...common, v('sm', v(CQ_FROM16, 'p-1')), v(CQ_UPTO12, 'p-1')]);
    const conditions = atRuleConditions(after);
    expect(conditions).toEqual(atRuleConditions(before));
    // The two come after the core, narrow: and the other named ones.
    for (const c of ['@media(width>=640px)', '@media(width<640px)', '@media(width>=1024px)', '@media(width<1024px)', '@media(width<48rem)', '@media(min-width:80rem)']) {
      expect(conditions.indexOf(c)).toBeGreaterThanOrEqual(0);
      expect(conditions.indexOf(c)).toBeLessThan(conditions.indexOf('@container(max-width:12rem)'));
    }
    const own = conditions.filter(c => /max-width:12rem|min-width:16rem/.test(c));
    expect(own).toEqual(['@container(max-width:12rem)', '@container(min-width:16rem)']);
    // Rule by rule too, not just the first appearance of each condition:
    // with the class names and whitespace taken out (the arbitrary forms
    // print `(max-width:12rem)`, the named ones `(max-width: 12rem)`) the
    // two builds are the same CSS.
    const rules = (css: string) =>
      css.slice(css.indexOf('@layer utilities')).replace(/\.[^\s{]+\s*\{/g, '.x{').replace(/\s+/g, '');
    expect(rules(after)).toEqual(rules(before));
  });
});

describe('src/ at-rule prefixes (DFLT-00293)', () => {
  // DFLT-00294 named the last arbitrary @media / @container prefixes. This
  // keeps it that way: an arbitrary at-rule prefix
  // would be emitted after every named variant (see the Ordering caveat in
  // index.css) and so change which rule wins. Comments are stripped as in
  // remText.test.ts; the prefix is matched by a regular expression so that
  // no class could be picked up from this file (index.css leaves the test
  // files out of Tailwind's scan since DFLT-00323; this stays as a second
  // guard).
  const SRC = __dirname;
  const listSources = (dir: string): string[] =>
    fs.readdirSync(dir, { withFileTypes: true }).flatMap(entry => {
      const full = path.join(dir, entry.name);
      if (entry.isDirectory()) return full === path.join(SRC, 'test') ? [] : listSources(full);
      return /\.(tsx?|jsx?)$/.test(entry.name) && !/\.test\.(tsx?|jsx?)$/.test(entry.name) ? [path.relative(SRC, full)] : [];
    });
  const stripComments = (src: string) => src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|\s)\/\/[^\n]*/g, '$1');
  const ARBITRARY_AT_RULE = /\[@(?:media|container)[^\]\s]*\]:/g;
  const sources = listSources(SRC);

  it('finds the source files to check', () => {
    expect(sources).toEqual(expect.arrayContaining(['App.tsx', path.join('components', 'TicketItem.tsx')]));
  });

  it.each(sources)('%s uses no arbitrary @media / @container prefix outside comments', name => {
    expect(stripComments(fs.readFileSync(path.join(SRC, name), 'utf8')).match(ARBITRARY_AT_RULE) ?? []).toEqual([]);
  });

  // DFLT-00319: nothing below a 320px window with 200% text is supported,
  // so the variants for it are neither defined nor used any more.
  it.each(REMOVED_VARIANTS)('no longer defines the %s variant', async name => {
    const css = await buildCss([v(name, 'p-1')]);
    expect(css).not.toContain(`${name}\\:p-1`);
  });

  it.each(sources)('%s uses none of the removed variants', name => {
    const src = fs.readFileSync(path.join(SRC, name), 'utf8');
    for (const variant of REMOVED_VARIANTS) expect(src).not.toContain(`${variant}:`);
  });
});
