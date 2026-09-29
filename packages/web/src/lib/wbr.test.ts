import { describe, expect, it } from 'vitest';
import { plainText, stripInvisible, WBR_MARK, withBreaks, WORD_JOINER, ZWSP } from './wbr';

describe('wbr marks in translations (DFLT-00292, DFLT-00295)', () => {
  it('replaces each mark with a zero width space in one string', () => {
    expect(withBreaks(`クローズ${WBR_MARK}理由`)).toBe(`クローズ${ZWSP}理由`);
    expect(withBreaks(`a${WBR_MARK}b${WBR_MARK}c`)).toBe(`a${ZWSP}b${ZWSP}c`);
  });

  it('returns a text without marks unchanged', () => {
    expect(withBreaks('Edit labels')).toBe('Edit labels');
    expect(withBreaks('')).toBe('');
  });

  it('never parses the text as markup', () => {
    expect(withBreaks('<b>編集</b>')).toBe('<b>編集</b>');
  });

  it('turns a drawn label back into its words', () => {
    expect(plainText(`${withBreaks(`作成${WBR_MARK}日時`)}${WORD_JOINER}:`)).toBe('作成日時:');
  });

  it.each([
    [`作成${WBR_MARK}日時`, '作成日時'],
    [`クローズ${WBR_MARK}理由`, 'クローズ理由'],
    [`ラベルを${WBR_MARK}編集`, 'ラベルを編集'],
    [`作成${ZWSP}日時`, '作成日時'],
    [`Created${WORD_JOINER}:`, 'Created:'],
    ['Edit labels', 'Edit labels']
  ])('removes the marks and invisible characters from %j', (input, expected) => {
    expect(plainText(input)).toBe(expected);
  });

  it.each([
    [`作成${ZWSP}日時${WORD_JOINER}:`, '作成日時:'],
    [`ラベルを${ZWSP}編集`, 'ラベルを編集'],
    [`Created${WORD_JOINER}:`, 'Created:'],
    [`${ZWSP}${WORD_JOINER}${ZWSP}`, '']
  ])('strips only the invisible characters from %j (DFLT-00310)', (input, expected) => {
    expect(stripInvisible(input)).toBe(expected);
  });

  it('keeps a "<wbr/>" string, which drawn text never holds (DFLT-00310)', () => {
    expect(stripInvisible(`a${WBR_MARK}b`)).toBe(`a${WBR_MARK}b`);
    expect(stripInvisible(`${WBR_MARK}${ZWSP}x`)).toBe(`${WBR_MARK}x`);
  });

  it('returns a text without invisible characters unchanged (DFLT-00310)', () => {
    expect(stripInvisible('Edit labels')).toBe('Edit labels');
    expect(stripInvisible('')).toBe('');
  });

  it('defines the invisible characters by their code points', () => {
    expect(ZWSP).toHaveLength(1);
    expect(ZWSP.charCodeAt(0)).toBe(0x200b);
    expect(WORD_JOINER).toHaveLength(1);
    expect(WORD_JOINER.charCodeAt(0)).toBe(0x2060);
  });
});
