// DFLT-00241: the execution graph on an expanded ticket reflows on a narrow
// screen. Below lg (one column) the graph's height follows its width
// (max-lg:h-auto overrides the height attribute) instead of staying at the
// fixed svgHeight, so a narrow panel shows the whole graph scaled down in
// proportion instead of a thin strip in a tall empty box. It never gets
// narrower than 180px; when the panel is narrower than that (160px wide, or
// 320px with a 200% default font) the graph's box scrolls sideways inside
// the panel -- and only then becomes a named, focusable region -- rather
// than the page. From lg up the svg keeps its height attribute, its
// w-full/max-w-[340px]/shrink-0 and its viewBox, so it renders exactly as
// before. jsdom does no layout (nor media queries), so this checks the
// classes and attributes; the geometry at 160-1440px, with a 200% default
// font, was measured in a real browser (see the ticket's implementation
// notes).
import { render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { GraphEdge, GraphNode, TicketDetail } from '../types';
import { TicketItem } from './TicketItem';

const node = (id: string, type: GraphNode['type']): GraphNode => ({
  id,
  ticket_id: 'TEST-00241',
  name: `${type} ${id}`,
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
  ticket_id: 'TEST-00241',
  from_node_id: from,
  to_node_id: to,
  condition,
  created_at: '2026-01-01T00:00:00Z'
});

// plan -> impl -> `reviews` parallel review gates (each looping back to impl) -> report
const makeGraph = (reviews: number) => {
  const gates = Array.from({ length: reviews }, (_, i) => node(`g${i}`, 'review_gate'));
  const nodes = [node('plan', 'plan'), node('impl', 'implementation'), ...gates, node('report', 'report')];
  const edges = [
    edge('plan', 'impl'),
    ...gates.flatMap(g => [edge('impl', g.id), edge(g.id, 'report'), edge(g.id, 'impl', 'iteration_loop')])
  ];
  // Same formulas as TicketItem (colSpacing 78, rowSpacing 52, four levels).
  const svgWidth = Math.max(300, reviews * 78 + 60);
  const svgHeight = Math.max(180, 4 * 52 + 46);
  return { nodes, edges, svgWidth, svgHeight };
};

const makeTicket = (nodes: GraphNode[], edges: GraphEdge[]): TicketDetail => ({
  id: 'TEST-00241',
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
});

const renderGraph = (nodes: GraphNode[], edges: GraphEdge[]) => {
  render(
    <TicketItem
      ticket={makeTicket(nodes, edges)}
      isExpanded
      onToggleExpand={vi.fn()}
      onRefresh={vi.fn(async () => {})}
      myName=""
      projectLabels={[]}
    />
  );
  const svg = screen.getByTestId('ticket-graph');
  const box = svg.parentElement!;
  const panel = box.parentElement!;
  return { svg, box, panel };
};

// Each class is checked on its own: `not.toHaveClass(a, b)` passes as soon as
// one of them is missing, so it would not catch the other slipping in.
const expectNoneOf = (el: Element, classes: string[]) => {
  for (const cls of classes) expect(el).not.toHaveClass(cls);
};

const classList = (el: Element) => (el.getAttribute('class') ?? '').split(/\s+/).filter(Boolean);

beforeEach(() => {
  vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('[]', { status: 200 }))));
  // TicketItem measures its graph panel with a ResizeObserver, which jsdom lacks.
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
});

afterEach(async () => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  await i18n.changeLanguage('ja');
});

describe.each([
  ['three parallel gates (svgWidth 300)', 3],
  ['five parallel gates (svgWidth over 340)', 5]
] as const)('TicketItem execution graph on a narrow screen: %s', (_label, reviews) => {
  const graph = makeGraph(reviews);

  it('keeps the viewBox and the fixed height attribute used from lg up', () => {
    const { svg } = renderGraph(graph.nodes, graph.edges);
    expect(svg).toHaveAttribute('viewBox', `0 0 ${graph.svgWidth} ${graph.svgHeight}`);
    expect(svg).toHaveAttribute('height', String(graph.svgHeight));
    expect(svg).not.toHaveAttribute('width');
    const style = svg.getAttribute('style') ?? '';
    expect(style).not.toMatch(/height|aspect-ratio|min-width/);
  });

  it('follows its width below lg only and keeps a 180px floor there', () => {
    const { svg } = renderGraph(graph.nodes, graph.edges);
    expect(svg).toHaveClass('w-full', 'max-w-[340px]', 'shrink-0', 'max-lg:h-auto', 'max-lg:min-w-[180px]', 'max-lg:mx-auto');
    // Every class added for the narrow layout is scoped below lg; an
    // unprefixed height, aspect ratio or floor would change the lg+ graph.
    for (const cls of classList(svg)) {
      if (['w-full', 'max-w-[340px]', 'shrink-0'].includes(cls)) continue;
      expect(cls.startsWith('max-lg:')).toBe(true);
    }
    expectNoneOf(svg, ['h-auto', 'h-full', 'aspect-auto', 'min-w-[180px]', 'mx-auto', 'lg:h-auto']);
  });

  it('scrolls the graph inside the panel below lg, never the panel or the page', () => {
    const { box, panel } = renderGraph(graph.nodes, graph.edges);
    expect(box).toHaveClass('flex-1', 'flex', 'flex-col', 'items-center', 'justify-center', 'max-lg:overflow-x-auto');
    expectNoneOf(box, ['overflow-x-auto', 'overflow-auto', 'overflow-hidden', 'overflow-x-hidden', 'lg:overflow-x-auto']);
    // The panel keeps its lg+ layout and floor, may shrink below its content's
    // width inside the grid, and never clips or scrolls itself.
    expect(panel).toHaveClass('lg:col-span-4', 'p-4', 'flex', 'flex-col', 'min-h-[32rem]', 'min-w-0');
    expectNoneOf(panel, ['overflow-hidden', 'overflow-x-auto', 'overflow-auto', 'overflow-x-hidden']);
  });

  it('lets the graph heading and the legend wrap below lg', () => {
    const { panel } = renderGraph(graph.nodes, graph.edges);
    const heading = panel.firstElementChild!;
    expect(heading).toHaveTextContent(i18n.t('ticketItem.graphTitle'));
    expect(heading).toHaveClass('flex', 'justify-between', 'max-lg:flex-wrap', 'max-lg:[overflow-wrap:anywhere]');
    expectNoneOf(heading, ['flex-wrap', 'whitespace-nowrap']);
    const legend = panel.lastElementChild!;
    expect(legend).toHaveTextContent(i18n.t('ticketItem.legend.done'));
    expect(legend).toHaveClass('flex-wrap', 'max-lg:[overflow-wrap:anywhere]');
  });

  it('draws every node and edge inside the viewBox', () => {
    const { svg } = renderGraph(graph.nodes, graph.edges);
    const { svgWidth, svgHeight } = graph;
    const inside = (x: number, y: number) => {
      expect(x).toBeGreaterThanOrEqual(0);
      expect(x).toBeLessThanOrEqual(svgWidth);
      expect(y).toBeGreaterThanOrEqual(0);
      expect(y).toBeLessThanOrEqual(svgHeight);
    };
    const labels = Array.from(svg.querySelectorAll('text'));
    expect(labels).toHaveLength(graph.nodes.length);
    for (const label of labels) inside(Number(label.getAttribute('x')), Number(label.getAttribute('y')));
    for (const c of Array.from(svg.querySelectorAll('circle'))) {
      const r = Number(c.getAttribute('r'));
      inside(Number(c.getAttribute('cx')) - r, Number(c.getAttribute('cy')) - r);
      inside(Number(c.getAttribute('cx')) + r, Number(c.getAttribute('cy')) + r);
    }
    const lines = Array.from(svg.querySelectorAll('line'));
    expect(lines).toHaveLength(graph.edges.filter(e => e.condition !== 'iteration_loop').length);
    for (const l of lines) {
      inside(Number(l.getAttribute('x1')), Number(l.getAttribute('y1')));
      inside(Number(l.getAttribute('x2')), Number(l.getAttribute('y2')));
    }
    // Loop edges curve out to the left; every point of the path, control
    // points included, stays inside the viewBox so none is cut off.
    const paths = Array.from(svg.querySelectorAll('path'));
    expect(paths).toHaveLength(graph.edges.filter(e => e.condition === 'iteration_loop').length);
    for (const p of paths) {
      const nums = (p.getAttribute('d') ?? '').match(/-?\d+(\.\d+)?/g)!.map(Number);
      expect(nums.length % 2).toBe(0);
      for (let i = 0; i < nums.length; i += 2) inside(nums[i], nums[i + 1]);
    }
  });
});

describe('TicketItem execution graph without nodes', () => {
  it('has no 180px floor, so an empty graph never scrolls', () => {
    const { svg } = renderGraph([], []);
    expect(svg).toHaveAttribute('viewBox', '0 0 300 180');
    expect(svg).toHaveAttribute('height', '180');
    expect(svg).toHaveClass('w-full', 'max-w-[340px]', 'shrink-0', 'max-lg:h-auto', 'max-lg:mx-auto');
    expect(svg).not.toHaveClass('max-lg:min-w-[180px]');
  });
});

describe('TicketItem execution graph scroll region', () => {
  const graph = makeGraph(3);

  it('is not a focusable region while the graph fits (lg and up, ordinary phones)', () => {
    const { box } = renderGraph(graph.nodes, graph.edges);
    expect(box).not.toHaveAttribute('tabindex');
    expect(box).not.toHaveAttribute('role');
    expect(screen.queryByRole('region', { name: i18n.t('ticketItem.graphTitle') })).not.toBeInTheDocument();
  });

  it('becomes a named, focusable region when the graph scrolls inside the panel', () => {
    vi.spyOn(HTMLElement.prototype, 'scrollWidth', 'get').mockReturnValue(180);
    vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(60);
    const { box, svg } = renderGraph(graph.nodes, graph.edges);
    const region = screen.getByRole('region', { name: i18n.t('ticketItem.graphTitle') });
    expect(region).toBe(box);
    expect(region).toHaveAttribute('tabindex', '0');
    expect(region).toContainElement(svg);
    expect(region).toHaveClass('focus-visible:ring-2');
    // It opens on the middle of the graph, where the main column of nodes is.
    expect(region.scrollLeft).toBe(60);
    // The parallel-rows hint stays in view while the box is scrolled.
    const hint = screen.getByText(i18n.t('ticketItem.parallelHint'));
    expect(region).toContainElement(hint);
    expect(hint).toHaveClass('w-full', 'max-lg:sticky', 'max-lg:left-0');
    expectNoneOf(hint, ['sticky', 'left-0']);
  });
});
