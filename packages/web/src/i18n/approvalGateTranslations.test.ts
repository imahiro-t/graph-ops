// DFLT-00176: the "submitting" suffix the approval gate's buttons carry in
// their accessible name while a decision is in flight exists in both
// languages, bracketed so it stays readable when joined to the label with no
// space.
import { describe, expect, it } from 'vitest';
import ja from './locales/ja/translation.json';
import en from './locales/en/translation.json';

describe('approval gate translations', () => {
  it('define ticketItem.approvalGate.submitting in ja and en', () => {
    for (const tree of [ja, en]) {
      const value = tree.ticketItem.approvalGate.submitting;
      expect(typeof value).toBe('string');
      expect(value.trim()).not.toBe('');
    }
    expect(ja.ticketItem.approvalGate.submitting).not.toBe(en.ticketItem.approvalGate.submitting);
  });

  it('bracket the suffix', () => {
    expect(ja.ticketItem.approvalGate.submitting).toBe('（送信中）');
    expect(en.ticketItem.approvalGate.submitting).toBe('(submitting)');
  });
});
