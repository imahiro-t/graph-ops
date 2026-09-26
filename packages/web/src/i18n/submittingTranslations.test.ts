// DFLT-00176 / DFLT-00206: the "submitting" suffix a button carries in its
// accessible name while the user's own action is in flight is one shared key
// (common.submitting) in both languages, bracketed so it stays readable when
// joined to the label with no space.
import { describe, expect, it } from 'vitest';
import ja from './locales/ja/translation.json';
import en from './locales/en/translation.json';

describe('submitting translations', () => {
  it('define common.submitting in ja and en', () => {
    for (const tree of [ja, en]) {
      const value = tree.common.submitting;
      expect(typeof value).toBe('string');
      expect(value.trim()).not.toBe('');
    }
    expect(ja.common.submitting).not.toBe(en.common.submitting);
  });

  it('bracket the suffix', () => {
    expect(ja.common.submitting).toBe('（送信中）');
    expect(en.common.submitting).toBe('(submitting)');
  });

  it('no longer keep the approval-gate-only key', () => {
    expect('submitting' in ja.ticketItem.approvalGate).toBe(false);
    expect('submitting' in en.ticketItem.approvalGate).toBe(false);
  });
});
