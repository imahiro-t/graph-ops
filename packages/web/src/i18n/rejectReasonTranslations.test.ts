// DFLT-00177: the approval gate's reject reason field has an accessible name
// of its own (apart from its placeholder) in both languages.
import { describe, expect, it } from 'vitest';
import ja from './locales/ja/translation.json';
import en from './locales/en/translation.json';

describe('reject reason field translations', () => {
  it('define ticketItem.approvalGate.reasonLabel in ja and en', () => {
    for (const tree of [ja, en]) {
      const value = tree.ticketItem.approvalGate.reasonLabel;
      expect(typeof value).toBe('string');
      expect(value.trim()).not.toBe('');
      expect(value).not.toBe(tree.ticketItem.approvalGate.reasonPlaceholder);
    }
    expect(ja.ticketItem.approvalGate.reasonLabel).not.toBe(en.ticketItem.approvalGate.reasonLabel);
  });
});
