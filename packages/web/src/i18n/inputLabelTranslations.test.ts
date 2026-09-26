// DFLT-00205: the ticket list's close-reason input and custom Claude prompt
// textarea have accessible names of their own (apart from their placeholders)
// in both languages.
import { describe, expect, it } from 'vitest';
import ja from './locales/ja/translation.json';
import en from './locales/en/translation.json';

describe('ticket list input label translations', () => {
  it('define ticketItem.close.reasonInputLabel in ja and en, distinct from the placeholder', () => {
    for (const tree of [ja, en]) {
      const value = tree.ticketItem.close.reasonInputLabel;
      expect(typeof value).toBe('string');
      expect(value.trim()).not.toBe('');
      expect(value).not.toBe(tree.ticketItem.close.reasonPlaceholder);
    }
    expect(ja.ticketItem.close.reasonInputLabel).not.toBe(en.ticketItem.close.reasonInputLabel);
  });

  it('define ticketItem.promptLabel in ja and en, distinct from the placeholder', () => {
    for (const tree of [ja, en]) {
      const value = tree.ticketItem.promptLabel;
      expect(typeof value).toBe('string');
      expect(value.trim()).not.toBe('');
      expect(value).not.toBe(tree.ticketItem.promptPlaceholder);
    }
    expect(ja.ticketItem.promptLabel).not.toBe(en.ticketItem.promptLabel);
  });
});
