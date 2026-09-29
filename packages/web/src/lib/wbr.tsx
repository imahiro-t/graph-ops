import React from 'react';

// Break-opportunity marks in translations (DFLT-00292).
//
// A few display-only translation keys (the `*Visible` ones, such as
// "ticketItem.close.reasonLabelVisible": "クローズ<wbr/>理由") mark with
// "<wbr/>" where a Japanese label may break between its words: the text is
// drawn with break-keep (word-break: keep-all), which otherwise allows no
// break between CJK characters. The mark is this app's own convention, not
// markup: it is split on here and turned into <wbr> elements, never parsed,
// and a `*Visible` string must never be rendered as is (i18n's escapeValue is
// off). Everything that draws such a key goes through these two functions.

export const WBR_MARK = '<wbr/>';

// The words of `text` between its "<wbr/>" marks (the whole text when there
// are none).
export const splitWbr = (text: string): string[] => text.split(WBR_MARK);

// `words` joined by <wbr> elements; a word is plain text.
export const renderWbr = (words: readonly string[]): React.ReactNode =>
  words.map((word, i) => (
    <React.Fragment key={i}>
      {i > 0 && <wbr />}
      {word}
    </React.Fragment>
  ));
