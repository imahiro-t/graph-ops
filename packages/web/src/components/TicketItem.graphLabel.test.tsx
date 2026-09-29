// DFLT-00320: the execution graph's node name labels follow the browser's
// default font size. The label is sized with text-[0.5625rem] (9 user units at
// the default 16px) instead of a fontSize attribute, its baseline is
// y + 6 plus dy 1.3333em (y + 18 at the default size, as before), and on a
// parallel row at a larger font it is shortened by estimated width so it does
// not run into its neighbour 78 units away, with the full name in a <title>.
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

// Estimated width of a label in user units at the given font scale.
const widthUnits = (text: string, fontScale: number) =>
  graphLabelWidthEm(text) * GRAPH_LABEL_BASE_FONT_UNITS * fontScale;

describe('graphNodeLabel', () => {
  describe.each([true, false])('at the default font size (parallel: %s)', parallel => {
    it('keeps names of up to 12 characters and cuts longer ones to 12 + "…", as before', () => {
      const opts = { parallel, fontScale: 1 };
      expect(graphNodeLabel('review', opts)).toEqual({ text: 'review', truncated: false });
      expect(graphNodeLabel(JA_LONG, opts)).toEqual({ text: JA_LONG, truncated: false });
      expect(graphNodeLabel('abcdefghijkl', opts)).toEqual({ text: 'abcdefghijkl', truncated: false });
      expect(graphNodeLabel(THIRTEEN_EN, opts)).toEqual({ text: 'abcdefghijkl…', truncated: true });
      expect(graphNodeLabel(THIRTEEN_JA, opts)).toEqual({ text: 'あいうえおかきくけこさし…', truncated: true });
      expect(graphNodeLabel(EN_LONG, opts)).toEqual({ text: 'security-rev…', truncated: true });
    });
  });

  describe('at 200% on a parallel row', () => {
    const opts = { parallel: true, fontScale: 2 };

    it('shortens by estimated width so full-width and half-width names both fit', () => {
      expect(graphNodeLabel(JA_LONG, opts)).toEqual({ text: 'セキュ…', truncated: true });
      expect(graphNodeLabel(EN_LONG, opts)).toEqual({ text: 'secu…', truncated: true });
      // A, P, I (0.65em each) + 設 (1em) + … (1em) = 3.95em, within 4em.
      expect(graphNodeLabel(MIXED, opts)).toEqual({ text: 'API設…', truncated: true });
      expect(graphNodeLabel('review', opts)).toEqual({ text: 'review', truncated: false });
    });

    it('keeps every label within the 72-unit budget', () => {
      for (const name of [JA_LONG, EN_LONG, MIXED, 'review', THIRTEEN_JA, THIRTEEN_EN, 'WWWWWWWWWW']) {
        const { text } = graphNodeLabel(name, opts);
        expect(widthUnits(text, 2)).toBeLessThanOrEqual(GRAPH_LABEL_WIDTH_BUDGET_UNITS);
      }
    });

    it('never shortens a label to the ellipsis alone', () => {
      expect(graphNodeLabel('あいうえお', { parallel: true, fontScale: 8 }).text).toBe('あ…');
    });
  });

  it('uses a wider budget at 125% (6.4em)', () => {
    const opts = { parallel: true, fontScale: 1.25 };
    // 0.65em * 8 + 1em = 6.2em; a ninth character would make it 6.85em.
    expect(graphNodeLabel(EN_LONG, opts)).toEqual({ text: 'security…', truncated: true });
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
const nodes = [
  node('impl', 'implementation', 'とても長い実装ノードの名前です'),
  node('g1', 'review_gate', JA_LONG),
  node('g2', 'review_gate', EN_LONG)
];
const edges = [edge('impl', 'g1'), edge('impl', 'g2')];

const renderGraph = () => {
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

  it('keeps the 12-character rule at the default 16px, with a <title> only on shortened labels', () => {
    const { impl, g1, g2 } = renderGraph();
    expect(shownText(impl)).toBe('とても長い実装ノードの名…');
    expect(impl.querySelector('title')).toHaveTextContent('とても長い実装ノードの名前です');
    expect(shownText(g1)).toBe(JA_LONG);
    expect(g1.querySelector('title')).toBeNull();
    expect(shownText(g2)).toBe('security-rev…');
    expect(g2.querySelector('title')).toHaveTextContent(EN_LONG);
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
