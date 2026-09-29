// DFLT-00320: the execution graph's node name labels follow the browser's
// default font size. The label is sized with text-[0.5625rem] (9 user units at
// the default 16px) instead of a fontSize attribute, its baseline is
// y + 6 plus dy 1.3333em (y + 18 at the default size, as before), and on a
// parallel row it is shortened by estimated width so it does not run into its
// neighbour 78 units away, with the full name in a <title>. DFLT-00335 extended
// that shortening from larger fonts to the default size; a single-node row
// keeps the 12-character rule at every size.
// jsdom does no layout, so overlaps were checked in a real browser (see the
// ticket's implementation notes); this checks the label text, classes and
// attributes, and that every shortened label fits the width budget.
import { render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { GraphEdge, GraphNode, TicketDetail } from '../types';
import {
  GRAPH_LABEL_BASE_FONT_UNITS,
  GRAPH_LABEL_WIDTH_BUDGET_UNITS,
  graphLabelCharEm,
  graphLabelWidthEm,
  graphNodeLabel
} from '../lib/graphNodeLabel';
import { TicketItem } from './TicketItem';

let rootFontSize = 16;
vi.mock('../lib/popupPlacement', async importOriginal => ({
  ...(await importOriginal<typeof import('../lib/popupPlacement')>()),
  rootFontSizePx: () => rootFontSize
}));

const JA_LONG = 'セキュリティレビュー'; // 10 full-width characters
const EN_LONG = 'security-review'; // 15 half-width characters
const MIXED = 'API設計レビュー';
const THIRTEEN_JA = 'あいうえおかきくけこさしす';
const THIRTEEN_EN = 'abcdefghijklm';
const QA_REVIEW = 'QAレビュー';
const A11Y_REVIEW = 'アクセシビリティレビュー'; // 12 full-width characters

// Estimated width of a label in user units at the given font scale.
const widthUnits = (text: string, fontScale: number) =>
  graphLabelWidthEm(text) * GRAPH_LABEL_BASE_FONT_UNITS * fontScale;

// Advance widths of printable ASCII in Arial Bold (metric-compatible with
// Helvetica Bold), in 1/1000 em. A bold (700) face is wider than the labels'
// semibold (600) text, so the estimate must not be below these.
const ARIAL_BOLD_WIDTHS: Record<string, number> = {
  ' ': 278, '!': 333, '"': 474, '#': 556, $: 556, '%': 889, '&': 722, "'": 238,
  '(': 333, ')': 333, '*': 389, '+': 584, ',': 278, '-': 333, '.': 278, '/': 278,
  '0': 556, '1': 556, '2': 556, '3': 556, '4': 556, '5': 556, '6': 556, '7': 556,
  '8': 556, '9': 556, ':': 333, ';': 333, '<': 584, '=': 584, '>': 584, '?': 611,
  '@': 975, A: 722, B: 722, C: 722, D: 722, E: 667, F: 611, G: 778, H: 722,
  I: 278, J: 556, K: 722, L: 611, M: 833, N: 722, O: 778, P: 667, Q: 778,
  R: 722, S: 667, T: 611, U: 722, V: 667, W: 944, X: 667, Y: 667, Z: 611,
  '[': 333, '\\': 278, ']': 333, '^': 584, _: 556, '`': 333, a: 556, b: 611,
  c: 556, d: 611, e: 556, f: 333, g: 611, h: 611, i: 278, j: 278, k: 556,
  l: 278, m: 889, n: 611, o: 611, p: 611, q: 611, r: 389, s: 556, t: 333,
  u: 611, v: 556, w: 778, x: 556, y: 556, z: 500, '{': 389, '|': 280, '}': 389,
  '~': 584
};

describe('graphNodeLabel', () => {
  it('never estimates a printable ASCII character narrower than in Arial Bold', () => {
    expect(Object.keys(ARIAL_BOLD_WIDTHS)).toHaveLength(95);
    for (const [ch, width] of Object.entries(ARIAL_BOLD_WIDTHS)) {
      expect(graphLabelCharEm(ch), ch).toBeGreaterThanOrEqual(width / 1000);
    }
  });

  describe('at the default font size on a single-node row', () => {
    it('keeps names of up to 12 characters and cuts longer ones to 12 + "…", as before', () => {
      const opts = { parallel: false, fontScale: 1 };
      expect(graphNodeLabel('review', opts)).toEqual({ text: 'review', truncated: false });
      expect(graphNodeLabel(JA_LONG, opts)).toEqual({ text: JA_LONG, truncated: false });
      expect(graphNodeLabel('abcdefghijkl', opts)).toEqual({ text: 'abcdefghijkl', truncated: false });
      expect(graphNodeLabel(THIRTEEN_EN, opts)).toEqual({ text: 'abcdefghijkl…', truncated: true });
      expect(graphNodeLabel(THIRTEEN_JA, opts)).toEqual({ text: 'あいうえおかきくけこさし…', truncated: true });
      expect(graphNodeLabel(EN_LONG, opts)).toEqual({ text: 'security-rev…', truncated: true });
    });
  });

  describe('at the default font size on a parallel row (DFLT-00335)', () => {
    const opts = { parallel: true, fontScale: 1 };

    it('shortens by estimated width within the 8em budget', () => {
      // 1em * 7 + … (1em) = 8em.
      expect(graphNodeLabel(JA_LONG, opts)).toEqual({ text: 'セキュリティレ…', truncated: true });
      expect(graphNodeLabel(A11Y_REVIEW, opts)).toEqual({ text: 'アクセシビリテ…', truncated: true });
      expect(graphNodeLabel(THIRTEEN_JA, opts)).toEqual({ text: 'あいうえおかき…', truncated: true });
      // s e c u y (0.65em each) + r i t - r (0.5em each) = 5.75em, + e
      // (0.65em) = 6.4em, + … = 7.4em; the next v (0.65em) would make it 8.05em.
      expect(graphNodeLabel(EN_LONG, opts)).toEqual({ text: 'security-re…', truncated: true });
      // a..k = 6.7em, + … = 7.7em; the next l (0.5em) would make it 8.2em.
      expect(graphNodeLabel(THIRTEEN_EN, opts)).toEqual({ text: 'abcdefghijk…', truncated: true });
      // A P I (0.8em each) + 設計レビ (1em each) = 6.4em, + … = 7.4em; with ュ
      // it would be 8.4em.
      expect(graphNodeLabel(MIXED, opts)).toEqual({ text: 'API設計レビ…', truncated: true });
      // Q A (0.8em each) + レビュー (1em each) = 5.6em.
      expect(graphNodeLabel(QA_REVIEW, opts)).toEqual({ text: QA_REVIEW, truncated: false });
      // 7.2em.
      expect(graphNodeLabel('abcdefghijkl', opts)).toEqual({ text: 'abcdefghijkl', truncated: false });
      expect(graphNodeLabel('review', opts)).toEqual({ text: 'review', truncated: false });
    });

    it('still applies the 12-character cap to names that fit the width', () => {
      // 13 narrow characters are 6.5em, within 8em, but longer than 12.
      expect(graphNodeLabel('iiiiiiiiiiiii', opts)).toEqual({ text: 'iiiiiiiiiiii…', truncated: true });
    });

    it('treats a font scale below 1, or NaN, as the default size', () => {
      for (const fontScale of [0.5, Number.NaN]) {
        expect(graphNodeLabel(A11Y_REVIEW, { parallel: true, fontScale })).toEqual({
          text: 'アクセシビリテ…',
          truncated: true
        });
      }
    });

    it('leaves a gap between "QAレビュー" and "アクセシビリティレビュー" side by side', () => {
      // Labels are centred 78 units apart, so the half-widths of the two
      // neighbours must add up to no more than 78 less the 6-unit gap.
      const halfWidths =
        widthUnits(graphNodeLabel(QA_REVIEW, opts).text, 1) / 2 +
        widthUnits(graphNodeLabel(A11Y_REVIEW, opts).text, 1) / 2;
      expect(halfWidths).toBeCloseTo(25.2 + 36);
      expect(halfWidths).toBeLessThanOrEqual(78 - 6);
    });
  });

  describe.each([1, 1.25, 2])('on a parallel row at font scale %s', fontScale => {
    const opts = { parallel: true, fontScale };

    it('keeps every label within the 72-unit budget', () => {
      for (const name of [
        JA_LONG, EN_LONG, MIXED, 'review', THIRTEEN_JA, THIRTEEN_EN, QA_REVIEW, A11Y_REVIEW, 'WWWWWWWWWW', 'MMMMMMMM'
      ]) {
        const { text } = graphNodeLabel(name, opts);
        expect(widthUnits(text, fontScale)).toBeLessThanOrEqual(GRAPH_LABEL_WIDTH_BUDGET_UNITS);
      }
    });

    it('keeps labels of every ASCII character within the budget by their Arial Bold widths too', () => {
      // Unlike the check above, this measures with the reference widths, not
      // with the estimate itself ("…" at 1em, as in Arial Bold).
      for (const ch of Object.keys(ARIAL_BOLD_WIDTHS)) {
        const { text } = graphNodeLabel(ch.repeat(12), opts);
        const em = Array.from(text).reduce((sum, c) => sum + (c === '…' ? 1 : ARIAL_BOLD_WIDTHS[c] / 1000), 0);
        expect(em * GRAPH_LABEL_BASE_FONT_UNITS * fontScale).toBeLessThanOrEqual(GRAPH_LABEL_WIDTH_BUDGET_UNITS);
      }
    });
  });

  describe('at 200% on a parallel row', () => {
    const opts = { parallel: true, fontScale: 2 };

    it('shortens by estimated width so full-width and half-width names both fit', () => {
      expect(graphNodeLabel(JA_LONG, opts)).toEqual({ text: 'セキュ…', truncated: true });
      expect(graphNodeLabel(EN_LONG, opts)).toEqual({ text: 'secu…', truncated: true });
      // A, P, I (0.8em each) + … (1em) = 3.4em; with 設 (1em) it would be
      // 4.4em, over the 4em budget.
      expect(graphNodeLabel(MIXED, opts)).toEqual({ text: 'API…', truncated: true });
      // W counts as 1em (0.94em in Arial Bold), so 3 W's + "…" = 4em.
      expect(graphNodeLabel('WWWWWWWWWW', opts)).toEqual({ text: 'WWW…', truncated: true });
      // Q, A (0.8em each) + レ (1em) + … (1em) = 3.6em.
      expect(graphNodeLabel(QA_REVIEW, opts)).toEqual({ text: 'QAレ…', truncated: true });
      expect(graphNodeLabel('review', opts)).toEqual({ text: 'review', truncated: false });
    });

    it('never shortens a label to the ellipsis alone', () => {
      expect(graphNodeLabel('あいうえお', { parallel: true, fontScale: 8 }).text).toBe('あ…');
    });
  });

  it('uses a wider budget at 125% (6.4em)', () => {
    const opts = { parallel: true, fontScale: 1.25 };
    // s e c u y (0.65em each) + r i t - (0.5em each) = 5.25em, + … (1em) =
    // 6.25em; the next r (0.5em) would make it 6.75em.
    expect(graphNodeLabel(EN_LONG, opts)).toEqual({ text: 'security-…', truncated: true });
    // 1em * 5 + 1em = 6em.
    expect(graphNodeLabel(JA_LONG, opts)).toEqual({ text: 'セキュリテ…', truncated: true });
    expect(graphNodeLabel('review', opts)).toEqual({ text: 'review', truncated: false });
    for (const name of [JA_LONG, EN_LONG, MIXED]) {
      expect(widthUnits(graphNodeLabel(name, opts).text, 1.25)).toBeLessThanOrEqual(GRAPH_LABEL_WIDTH_BUDGET_UNITS);
    }
  });

  it('applies only the 12-character cap on a single-node row at 200%', () => {
    const opts = { parallel: false, fontScale: 2 };
    expect(graphNodeLabel(JA_LONG, opts)).toEqual({ text: JA_LONG, truncated: false });
    expect(graphNodeLabel(THIRTEEN_JA, opts)).toEqual({ text: 'あいうえおかきくけこさし…', truncated: true });
  });
});

const node = (id: string, type: GraphNode['type'], name: string): GraphNode => ({
  id,
  ticket_id: 'TEST-00320',
  name,
  type,
  status: 'TODO',
  iteration_count: 0,
  max_iterations: 3,
  is_manual: false,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z'
});

const edge = (from: string, to: string, condition = 'success'): GraphEdge => ({
  id: `${from}-${to}-${condition}`,
  ticket_id: 'TEST-00320',
  from_node_id: from,
  to_node_id: to,
  condition,
  created_at: '2026-01-01T00:00:00Z'
});

// implementation (a single-node row, long Japanese name) -> two parallel
// review gates with long full-width / half-width names.
const defaultNodes = [
  node('impl', 'implementation', 'とても長い実装ノードの名前です'),
  node('g1', 'review_gate', JA_LONG),
  node('g2', 'review_gate', EN_LONG)
];
const edges = [edge('impl', 'g1'), edge('impl', 'g2')];

const renderGraph = (nodes: GraphNode[] = defaultNodes) => {
  const ticket: TicketDetail = {
    id: 'TEST-00320',
    project_id: 'proj-1',
    title: 'タイトル',
    description: '説明',
    status: 'IN PROGRESS',
    auto_executable: true,
    blocked: false,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    priority: 'MEDIUM',
    labels: [],
    nodes,
    edges,
    artifacts: []
  };
  render(
    <TicketItem
      ticket={ticket}
      isExpanded
      onToggleExpand={vi.fn()}
      onRefresh={vi.fn(async () => {})}
      myName=""
      projectLabels={[]}
    />
  );
  const svg = screen.getByTestId('ticket-graph');
  const labels = Array.from(svg.querySelectorAll('text'));
  expect(labels).toHaveLength(nodes.length);
  // Labels are drawn in node order.
  const [impl, g1, g2] = labels;
  const circleY = (i: number) => Number(svg.querySelectorAll('g > circle[r="6"][stroke="#ffffff"]')[i].getAttribute('cy'));
  return { svg, impl, g1, g2, circleY };
};

// The label's own text, without its <title>.
const shownText = (el: Element) =>
  Array.from(el.childNodes)
    .filter(c => c.nodeType === Node.TEXT_NODE)
    .map(c => c.textContent)
    .join('');

beforeEach(() => {
  rootFontSize = 16;
  vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('[]', { status: 200 }))));
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
});

afterEach(async () => {
  rootFontSize = 16;
  vi.unstubAllGlobals();
  await i18n.changeLanguage('ja');
});

describe('TicketItem execution graph node labels (DFLT-00320)', () => {
  it('sizes the label in rem and places its baseline at y + 6 + 1.3333em', () => {
    const { impl, g1, g2, circleY } = renderGraph();
    [impl, g1, g2].forEach((label, i) => {
      expect(label).not.toHaveAttribute('fontSize');
      expect(label).not.toHaveAttribute('font-size');
      expect(label).toHaveClass('text-[0.5625rem]');
      expect(label).toHaveAttribute('dy', '1.3333em');
      expect(Number(label.getAttribute('y'))).toBe(circleY(i) + 6);
    });
  });

  it('shortens parallel labels by width at the default 16px too, keeping the full name in a <title>', () => {
    const { impl, g1, g2 } = renderGraph();
    expect(shownText(g1)).toBe('セキュリティレ…');
    expect(g1.querySelector('title')).toHaveTextContent(JA_LONG);
    expect(shownText(g2)).toBe('security-re…');
    expect(g2.querySelector('title')).toHaveTextContent(EN_LONG);
    // A single-node row keeps the 12-character rule.
    expect(shownText(impl)).toBe('とても長い実装ノードの名…');
    expect(impl.querySelector('title')).toHaveTextContent('とても長い実装ノードの名前です');
  });

  it('adds a <title> at the default 16px only to the parallel label that is shortened', () => {
    const { impl, g1, g2 } = renderGraph([
      node('impl', 'implementation', '実装'),
      node('g1', 'review_gate', QA_REVIEW),
      node('g2', 'review_gate', A11Y_REVIEW)
    ]);
    expect(shownText(impl)).toBe('実装');
    expect(impl.querySelector('title')).toBeNull();
    expect(shownText(g1)).toBe(QA_REVIEW);
    expect(g1.querySelector('title')).toBeNull();
    expect(shownText(g2)).toBe('アクセシビリテ…');
    expect(g2.querySelector('title')).toHaveTextContent(A11Y_REVIEW);
  });

  it('shortens parallel labels by width at 200%, keeping the full name in a <title>', () => {
    rootFontSize = 32;
    const { impl, g1, g2 } = renderGraph();
    expect(shownText(g1)).toBe('セキュ…');
    expect(g1.querySelector('title')).toHaveTextContent(JA_LONG);
    expect(shownText(g2)).toBe('secu…');
    expect(g2.querySelector('title')).toHaveTextContent(EN_LONG);
    // A single-node row keeps the 12-character rule.
    expect(shownText(impl)).toBe('とても長い実装ノードの名…');
  });
});
