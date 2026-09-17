// DFLT-00083: a ticket's priority is always HIGH/MEDIUM/LOW, drawn as a
// one-character symbol. The only fallback is a UI-side guard that shows and
// filters an unexpected value as MEDIUM.
import { describe, expect, it } from 'vitest';
import ja from './i18n/locales/ja/translation.json';
import en from './i18n/locales/en/translation.json';
import { TICKET_PRIORITIES } from './types';
import * as priorityMeta from './priorityMeta';
import { getPriorityMeta, matchesPriorityFilter, normalizeTicketPriority } from './priorityMeta';

describe('getPriorityMeta', () => {
  it.each([
    ['HIGH', '↑', 'priority.high', 'rose'],
    ['MEDIUM', '−', 'priority.medium', 'amber'],
    ['LOW', '↓', 'priority.low', 'sky']
  ])('%s: symbol %s, label %s, %s chip', (priority, symbol, labelKey, color) => {
    const meta = getPriorityMeta(priority);
    expect(meta.symbol).toBe(symbol);
    expect([...meta.symbol]).toHaveLength(1);
    expect(meta.labelKey).toBe(labelKey);
    expect(meta.chip.bg).toContain(`bg-${color}-`);
    expect(meta.chip.text).toContain(`text-${color}-`);
  });

  it('uses U+2212 MINUS SIGN for MEDIUM, not the ASCII hyphen-minus', () => {
    const { symbol } = getPriorityMeta('MEDIUM');
    expect(symbol.codePointAt(0)).toBe(0x2212);
    expect(symbol).not.toBe('-');
  });

  it('marks only MEDIUM as understated with font-normal', () => {
    expect(getPriorityMeta('MEDIUM').symbolClass.split(' ')).toContain('font-normal');
    for (const p of ['HIGH', 'LOW']) {
      expect(getPriorityMeta(p).symbolClass.split(' ')).not.toContain('font-normal');
      expect(getPriorityMeta(p).symbolClass.split(' ')).toContain('font-bold');
    }
  });

  it.each(['', 'URGENT', 'high', null, undefined])('shows the unexpected value %j as MEDIUM', value => {
    expect(getPriorityMeta(value)).toBe(getPriorityMeta('MEDIUM'));
  });

  it('has a translation for every level in both languages, and no unset entry', () => {
    for (const p of TICKET_PRIORITIES) {
      const key = getPriorityMeta(p).labelKey.replace(/^priority\./, '') as keyof typeof ja.priority;
      expect(ja.priority[key]).toBeTruthy();
      expect(en.priority[key]).toBeTruthy();
    }
    expect(Object.keys(ja.priority).sort()).toEqual(['high', 'low', 'medium']);
    expect(Object.keys(en.priority).sort()).toEqual(['high', 'low', 'medium']);
  });

  it('exports only the three priority helpers, with no meta for a fourth level', () => {
    expect(Object.keys(priorityMeta).sort()).toEqual([
      'getPriorityMeta',
      'matchesPriorityFilter',
      'normalizeTicketPriority'
    ]);
  });
});

describe('normalizeTicketPriority', () => {
  it.each(TICKET_PRIORITIES)('keeps %s as-is', p => {
    expect(normalizeTicketPriority(p)).toBe(p);
  });

  it.each(['', 'NONE', null, undefined])('maps %j to MEDIUM, never null', value => {
    expect(normalizeTicketPriority(value)).toBe('MEDIUM');
  });
});

describe('matchesPriorityFilter', () => {
  const tickets = [
    { id: 'A', priority: 'HIGH' },
    { id: 'B', priority: 'MEDIUM' },
    { id: 'C', priority: 'LOW' }
  ];

  it('keeps only MEDIUM tickets when only MEDIUM is selected', () => {
    expect(tickets.filter(t => matchesPriorityFilter(t.priority, ['MEDIUM'])).map(t => t.id)).toEqual(['B']);
  });

  it('keeps every ticket both when all three levels are selected and when none is', () => {
    expect(tickets.filter(t => matchesPriorityFilter(t.priority, TICKET_PRIORITIES))).toHaveLength(3);
    // DFLT-00086: an empty selection means "don't filter by priority", not
    // "match nothing" -- unchecking everything widens the list rather than
    // emptying it, as it does for the other three toolbar filters.
    expect(tickets.filter(t => matchesPriorityFilter(t.priority, []))).toHaveLength(3);
  });

  it('filters an unexpected value under MEDIUM, the level it is displayed as', () => {
    expect(matchesPriorityFilter('', ['MEDIUM'])).toBe(true);
    expect(matchesPriorityFilter('', ['HIGH', 'LOW'])).toBe(false);
  });
});
