// Node name labels of the execution graph in TicketItem (DFLT-00320).
//
// The label's font size is 0.5625rem: 9 SVG user units at the default 16px
// root font size, 18 at a 200% default. Labels of nodes on the same row
// (a parallel level) are centred 78 user units apart, so a label wider than
// that runs into its neighbour. Widening the columns would only shrink the
// whole SVG (it scales to its box through the viewBox) and bring the text back
// down, so a larger font is handled by shortening the label instead.
//
// - At the default size (fontScale 1), and on a row with a single node, the
//   label keeps the long-standing rule: more than 12 characters become 12
//   characters and "…". So nothing changes at the default 16px.
// - On a parallel row at a larger font, the label is shortened by its
//   estimated width, not by a character count, so full-width (Japanese) names
//   fit as well as ASCII ones: the width budget is 72 user units (the 78-unit
//   column pitch less 6 units of gap), i.e. 72 / (9 * fontScale) em. The
//   12-character cap still applies on top of it.
//
// The width estimate is an approximation, not a measurement (jsdom cannot
// measure SVG text, and the render must not wait for layout). It is sized so
// that no printable ASCII character is estimated narrower than in Arial /
// Helvetica Bold, a bold (700) face and so wider than the labels' semibold
// (600) text; TicketItem.graphLabel.test.tsx checks it against those widths:
// - any code point at or above U+1100 (Hangul Jamo onwards, which takes in
//   kana, kanji and full-width forms): 1em;
// - the widest ASCII characters, M W m @ % (0.83-0.98em in Arial Bold): 1em;
// - other capital letters, w and & (up to 0.78em): 0.8em;
// - narrow ASCII characters -- i j l f t r, the space and narrow punctuation
//   such as . , : ; ' " ( ) [ ] { } - / | * (up to 0.47em): 0.5em;
// - everything else below U+1100 (the other lower-case letters, digits and
//   punctuation; up to 0.61em): 0.65em.
// Treating every code point from U+1100 up as full width makes some symbols
// shorter than they need to be, and "…" (U+2026) is counted as 1em too; those
// errors leave a gap. The estimate is not an upper bound everywhere, though:
// a system font whose glyphs are wider than Arial Bold's, or a name made of
// wide non-ASCII Latin characters such as Æ or Œ (about 1em, counted as 0.8em
// or 0.65em), can still come out wider than estimated. The 6-unit gap left
// out of the budget absorbs small differences of that kind.

export const GRAPH_LABEL_MAX_CHARS = 12;
export const GRAPH_LABEL_BASE_FONT_UNITS = 9;
export const GRAPH_LABEL_WIDTH_BUDGET_UNITS = 72;
const ELLIPSIS = '…';

// Width classes of the estimate (see above).
const WIDE_ASCII = /^[MWm@%]$/;
const CAPITAL_WIDTH = /^[\p{Lu}w&]$/u;
const NARROW_ASCII = /^[ijlftr !'",.:;()[\]{}\-/\\|`*]$/;

// Estimated advance width of one code point, in em.
export function graphLabelCharEm(ch: string): number {
  if ((ch.codePointAt(0) ?? 0) >= 0x1100 || WIDE_ASCII.test(ch)) return 1;
  if (CAPITAL_WIDTH.test(ch)) return 0.8;
  return NARROW_ASCII.test(ch) ? 0.5 : 0.65;
}

// Estimated width of a whole label, in em.
export function graphLabelWidthEm(text: string): number {
  return Array.from(text).reduce((sum, ch) => sum + graphLabelCharEm(ch), 0);
}

export interface GraphNodeLabel {
  text: string;
  truncated: boolean;
}

export function graphNodeLabel(
  name: string,
  opts: { parallel: boolean; fontScale: number }
): GraphNodeLabel {
  if (!opts.parallel || !(opts.fontScale > 1)) {
    // The long-standing rule, unchanged (including counting UTF-16 units),
    // so the graph looks exactly as before at the default font size.
    return name.length > GRAPH_LABEL_MAX_CHARS
      ? { text: name.slice(0, GRAPH_LABEL_MAX_CHARS) + ELLIPSIS, truncated: true }
      : { text: name, truncated: false };
  }

  const chars = Array.from(name);
  const budgetEm = GRAPH_LABEL_WIDTH_BUDGET_UNITS / (GRAPH_LABEL_BASE_FONT_UNITS * opts.fontScale);
  if (chars.length <= GRAPH_LABEL_MAX_CHARS && graphLabelWidthEm(name) <= budgetEm) {
    return { text: name, truncated: false };
  }
  const ellipsisEm = graphLabelCharEm(ELLIPSIS);
  let keep = 0;
  let width = 0;
  while (keep < chars.length && keep < GRAPH_LABEL_MAX_CHARS) {
    const next = width + graphLabelCharEm(chars[keep]);
    if (next + ellipsisEm > budgetEm) break;
    width = next;
    keep++;
  }
  // Always keep at least one character so the label is never just "…".
  keep = Math.max(1, keep);
  return { text: chars.slice(0, keep).join('') + ELLIPSIS, truncated: true };
}
