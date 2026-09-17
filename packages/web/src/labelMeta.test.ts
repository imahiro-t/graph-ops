// DFLT-00084: the label palette's class table and the label filter predicate.
import { describe, expect, it } from 'vitest';
import i18n from './i18n';
import { getLabelColorMeta, matchesLabelFilter, normalizeLabelColor } from './labelMeta';
import { LABEL_COLORS } from './types';

describe('labelMeta colors', () => {
  it('defines light and dark background, text and border classes for all 10 palette colors', () => {
    expect(LABEL_COLORS).toHaveLength(10);
    for (const color of LABEL_COLORS) {
      const meta = getLabelColorMeta(color);
      for (const part of [meta.chip.bg, meta.chip.text, meta.chip.border]) {
        const classes = part.split(' ');
        expect(classes.some(c => !c.startsWith('dark:')), `${color}: ${part}`).toBe(true);
        expect(classes.some(c => c.startsWith('dark:')), `${color}: ${part}`).toBe(true);
      }
      expect(meta.chip.bg).toMatch(/(^| )bg-/);
      expect(meta.chip.text).toMatch(/(^| )text-/);
      expect(meta.chip.border).toMatch(/(^| )border-/);
      expect(meta.chip.bg).toContain(color);
      expect(meta.swatch).toContain(color);
      expect(i18n.t(meta.nameKey)).not.toBe(meta.nameKey);
    }
  });

  it('falls back to gray for an unknown color key', () => {
    expect(normalizeLabelColor('magenta')).toBe('gray');
    expect(normalizeLabelColor(undefined)).toBe('gray');
    expect(getLabelColorMeta('magenta')).toBe(getLabelColorMeta('gray'));
  });
});

describe('matchesLabelFilter', () => {
  const bug = { id: 'label-bug' };
  const feature = { id: 'label-feature' };
  const ui = { id: 'label-ui' };

  it.each([
    { ticketLabels: [], selected: [], result: true },
    { ticketLabels: [bug], selected: [], result: true },
    { ticketLabels: [], selected: [bug.id], result: false },
    { ticketLabels: [bug], selected: [bug.id], result: true },
    { ticketLabels: [ui], selected: [bug.id], result: false },
    { ticketLabels: [bug, ui], selected: [feature.id, ui.id], result: true }
  ])('labels $ticketLabels with selection $selected -> $result', ({ ticketLabels, selected, result }) => {
    expect(matchesLabelFilter(ticketLabels, selected)).toBe(result);
  });

  it('treats a ticket without a labels key as unlabeled', () => {
    expect(matchesLabelFilter(undefined, [])).toBe(true);
    expect(matchesLabelFilter(undefined, [bug.id])).toBe(false);
  });
});
