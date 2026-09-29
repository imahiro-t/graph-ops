// Break-opportunity marks in translations (DFLT-00292, DFLT-00295).
//
// A few display-only translation keys (the `*Visible` ones, such as
// "ticketItem.close.reasonLabelVisible": "クローズ<wbr/>理由") mark with
// "<wbr/>" where a Japanese label may break between its words: the text is
// drawn with break-keep (word-break: keep-all), which otherwise allows no
// break between CJK characters. The mark is this app's own convention, not
// markup: withBreaks replaces it with U+200B (ZERO WIDTH SPACE), which gives
// the same break opportunity as a <wbr> element, and never parses the text.
// A `*Visible` string must never be rendered as is (i18n's escapeValue is
// off): everything that draws such a key goes through withBreaks.
//
// DFLT-00295: withBreaks returns one string, not <wbr> elements between text
// nodes. With elements, Chromium's accessibility tree exposed a label as
// several texts ("作成" "日" "時:", "Create" "d:"); one string is one text
// node, and so one text in the tree.
//
// The invisible characters are defined here once, as escapes: never type them
// raw into a source file (code or tests) or a translation, where a review or
// a diff cannot show them. Use these constants instead.

export const WBR_MARK = '<wbr/>';

// U+200B ZERO WIDTH SPACE: a break opportunity, drawn as nothing.
export const ZWSP = '\u200B';

// U+2060 WORD JOINER: forbids a break between the characters on either side
// (a label's last character and its colon), drawn as nothing.
export const WORD_JOINER = '\u2060';

// `text` with each "<wbr/>" mark replaced by ZWSP (unchanged without marks).
export const withBreaks = (text: string): string => text.split(WBR_MARK).join(ZWSP);

// `text` without ZWSP and WORD_JOINER, but with any "<wbr/>" string left as
// it is (DFLT-00310). This is what a copy puts on the clipboard (see
// lib/plainCopy): drawn text never holds the mark, so a "<wbr/>" in a copied
// selection is a person's own text (a ticket description, say) and is kept.
export const stripInvisible = (text: string): string => text.split(ZWSP).join('').split(WORD_JOINER).join('');

// `text` without the "<wbr/>" marks, ZWSP and WORD_JOINER: the words as a
// person reads them. For comparing drawn text with its translation in tests.
export const plainText = (text: string): string => stripInvisible(text.split(WBR_MARK).join(''));
