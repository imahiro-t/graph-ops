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
// The width estimate is a deliberately conservative approximation, not a
// measurement (jsdom cannot measure SVG text, and the render must not wait
// for layout): any code point at or above U+1100 (Hangul Jamo onwards, which
// takes in kana, kanji and full-width forms) counts as 1em, everything below
// as 0.65em (a little over the ~0.6em average of a semibold Latin letter).
// Treating every code point from U+1100 up as full width makes some symbols
// shorter than they need to be, and "…" (U+2026) is counted as 1em too; both
// errors lean towards leaving a gap, never towards an overlap.

export const GRAPH_LABEL_MAX_CHARS = 12;
export const GRAPH_LABEL_BASE_FONT_UNITS = 9;
export const GRAPH_LABEL_WIDTH_BUDGET_UNITS = 72;
const ELLIPSIS = '…';

// Estimated advance width of one code point, in em.
export function graphLabelCharEm(ch: string): number {
  return (ch.codePointAt(0) ?? 0) >= 0x1100 ? 1 : 0.65;
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
