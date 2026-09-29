// DFLT-00293: in a 160px window at a 200% text size the detail panel's
// sections other than the metadata bar -- the parent/children card, the
// autopilot decisions, the description card, the graph panel, the node and
// artifact panel and the Action Footer -- showed their text one or two
// characters a line. Their rem paddings, nested four deep (the page, the
// detail panel, the card, the Markdown box / tab panel / node row, then a
// button's own padding), left the text a column of 14-46px at a 32px root.
//
// Only in a window of 200 CSS px or less (upto-200px:, index.css, a px
// query: the 15rem one also matches 320-336px with a 32px default font):
// - the nested boxes pad less (the detail panel and the cards px-1, the
//   Markdown box px-0.5, the tab panel and the node rows px-0.5, the tabs,
//   the download link, the node badges px-1 and the footer's buttons px-1.5),
//   and Markdown lists put their markers inside (list-inside pl-0);
// - rows that put an icon or a label next to a value may wrap (flex-wrap) or
//   stack (the autopilot decision items: flex-col), so each part gets the
//   whole line;
// - headings and labels break inside a word only when that word is wider
//   than the line (wrap-break-word over wrap-anywhere, which lowered their
//   min-content width and let a flex row squeeze them letter by letter).
// The graph itself keeps scrolling sideways in its own box.
//
// Measured in a real browser (see the ticket's implementation notes): at
// 160px and 200px, with a 32px root and with a 32px default font, in
// Japanese and English, no word is split into lines of one or two
// characters, no text runs past the card, and the page is as wide as the
// window. At 320/1024/1280px (100%) and 320/336px (200%) every section
// measured the same as before. jsdom does no layout, so this pins the classes.
import { render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { Artifact, GraphNode, TicketDetail } from '../types';
import { TicketItem } from './TicketItem';

const T = 'upto-200px:';

const node = (id: string, name: string, type: GraphNode['type'], status: GraphNode['status']): GraphNode => ({
  id,
  ticket_id: 'TEST-00293',
  name,
  type,
  status,
  iteration_count: 1,
  max_iterations: 3,
  is_manual: false,
  created_at: '2026-09-29T00:00:00Z',
  updated_at: '2026-09-29T00:00:00Z'
});

const artifact = (id: string, nodeId: string, name: string): Artifact => ({
  id,
  ticket_id: 'TEST-00293',
  node_id: nodeId,
  name,
  type: 'text',
  content: '本文',
  created_at: '2026-09-29T00:00:00Z'
});

const makeTicket = (): TicketDetail => ({
  id: 'TEST-00293',
  project_id: 'proj-1',
  title: 'タイトル',
  description: '## Why（背景・目的）\n\n説明の本文。\n\n1. 一つ目\n2. 二つ目\n\n- 箇条書き',
  status: 'IN PROGRESS',
  auto_executable: true,
  blocked: false,
  refined_at: '2026-09-29T00:00:00Z',
  created_at: '2026-09-29T00:00:00Z',
  updated_at: '2026-09-29T00:00:00Z',
  priority: 'MEDIUM',
  labels: [],
  nodes: [node('N-1', '計画作成', 'plan', 'DONE'), node('N-2', '実装', 'implementation', 'IN PROGRESS')],
  edges: [
    { id: 'e1', ticket_id: 'TEST-00293', from_node_id: 'N-1', to_node_id: 'N-2', condition: 'success', created_at: '' }
  ],
  artifacts: [artifact('a1', 'N-1', '実行計画'), artifact('a2', 'N-1', 'autopilot-decision-refine')],
  parent: { id: 'TEST-00276', title: '親', status: 'DONE' },
  children: [{ id: 'TEST-00300', title: '子', status: 'TODO' }]
});

const renderTicket = () =>
  render(
    <TicketItem
      ticket={makeTicket()}
      isExpanded
      onToggleExpand={vi.fn()}
      onRefresh={vi.fn(async () => {})}
      myName=""
      projectLabels={[]}
    />
  );

beforeEach(() => {
  vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('[]', { status: 200 }))));
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
});

afterEach(async () => {
  vi.unstubAllGlobals();
  await i18n.changeLanguage('ja');
});

const tiny = (...classes: string[]) => classes.map(c => `${T}${c}`);

describe.each(['ja', 'en'] as const)('TicketItem detail sections in a window of 200px or less (%s)', lng => {
  beforeEach(async () => {
    await i18n.changeLanguage(lng);
  });

  it('pads the detail panel and every card less, only in a window of 200px or less', () => {
    renderTicket();
    const details = screen.getByTestId('ticket-details');
    expect(details).toHaveClass('p-6', ...tiny('p-2', 'px-1'));
    expect(screen.getByTestId('ticket-family')).toHaveClass('p-4', ...tiny('px-1', 'py-2'));
    expect(screen.getByTestId('autopilot-decisions')).toHaveClass('p-4', ...tiny('px-1', 'py-2'));
    const descriptionCard = within(details).getByText(i18n.t('ticketItem.description.title')).closest('.rounded-xl') as HTMLElement;
    expect(descriptionCard).toHaveClass('p-4', 'upto-15rem:p-3', ...tiny('px-1', 'py-2'));
    const graphPanel = screen.getByTestId('ticket-graph').closest('.rounded-xl') as HTMLElement;
    expect(graphPanel).toHaveClass('p-4', ...tiny('px-1', 'py-2'));
    expect(screen.getByTestId('ticket-action-footer')).toHaveClass('p-4', ...tiny('p-2', 'px-1'));
  });

  it('pads the Markdown box less and puts list markers inside', () => {
    renderTicket();
    const viewer = within(screen.getByTestId('ticket-details')).getByTestId('markdown-viewer');
    expect(viewer).toHaveClass('p-3', ...tiny('px-0.5', 'py-1'));
    for (const list of viewer.querySelectorAll('ul, ol')) {
      expect(list).toHaveClass('list-outside', 'pl-5', ...tiny('list-inside', 'pl-0'));
    }
    expect(viewer.querySelectorAll('ul, ol')).toHaveLength(2);
  });

  it('lets the parent/children headings and links wrap, the id breaking only inside a word too long for the line', () => {
    renderTicket();
    const family = screen.getByTestId('ticket-family');
    const parentLink = within(family).getByTestId('ticket-family-parent');
    const childLink = within(family).getByTestId('ticket-family-child');
    for (const link of [parentLink, childLink]) {
      expect(link).toHaveClass('inline-flex', ...tiny('flex-wrap'));
      expect(link).not.toHaveClass('flex-wrap');
      const id = link.firstElementChild as HTMLElement;
      expect(id).toHaveClass(...tiny('min-w-0', 'wrap-break-word'));
    }
    const parentHeading = within(family).getByText(i18n.t('ticketItem.family.parent'));
    expect(parentHeading).toHaveClass('flex', ...tiny('flex-wrap'));
    const childrenHeading = within(family).getByText(i18n.t('ticketItem.family.children', { count: 1 }));
    expect(childrenHeading).toHaveClass('flex', ...tiny('flex-wrap'));
  });

  it('stacks each autopilot decision item and lets its parts break between words', () => {
    renderTicket();
    const section = screen.getByTestId('autopilot-decisions');
    const item = within(section).getByTestId('autopilot-decision');
    expect(item).toHaveClass('flex', 'flex-wrap', ...tiny('flex-col', 'items-start', 'wrap-break-word', '*:max-w-full'));
    const heading = within(section).getByRole('heading');
    expect(heading).toHaveClass('flex', ...tiny('flex-wrap'));
    const title = within(heading).getByText(i18n.t('autopilot.decisions.title', { count: 1 }));
    expect(title.tagName).toBe('SPAN');
    expect(title).toHaveClass(...tiny('min-w-0', 'wrap-break-word'));
    expect(title).not.toHaveClass('min-w-0');
  });

  it('breaks the description heading and the refined time between words (wrap-break-word over wrap-anywhere)', () => {
    renderTicket();
    const details = screen.getByTestId('ticket-details');
    const title = within(details).getByText(i18n.t('ticketItem.description.title'));
    expect(title).toHaveClass('wrap-anywhere', ...tiny('wrap-break-word'));
    const titleRow = title.parentElement as HTMLElement;
    expect(titleRow).toHaveClass('wrap-anywhere', ...tiny('flex-wrap', 'wrap-break-word'));
    const refined = within(details).getByText(/2026/, { selector: 'span.min-w-0' });
    expect(refined).toHaveClass('wrap-anywhere', ...tiny('wrap-break-word'));
    expect(refined.parentElement).toHaveClass('flex', ...tiny('flex-wrap'));
  });

  it('breaks the graph panel heading and legend between words, and keeps the graph scrolling in its own box', () => {
    renderTicket();
    const graph = screen.getByTestId('ticket-graph');
    const panel = graph.closest('.rounded-xl') as HTMLElement;
    const heading = panel.firstElementChild as HTMLElement;
    expect(heading).toHaveClass('max-lg:wrap-anywhere', ...tiny('wrap-break-word'));
    const legend = panel.lastElementChild as HTMLElement;
    expect(legend).toHaveTextContent(i18n.t('ticketItem.legend.done'));
    expect(legend).toHaveClass('max-lg:wrap-anywhere', ...tiny('wrap-break-word'));
    // Unchanged: the graph keeps its minimum width and its box scrolls.
    expect(graph).toHaveClass('max-lg:min-w-[180px]');
    expect(graph.parentElement).toHaveClass('max-lg:overflow-x-auto');
  });

  it('pads the tab row, the tabs, the download link, the tab panel and the node rows less', () => {
    renderTicket();
    const tabRow = screen.getByTestId('ticket-artifact-tabs');
    expect(tabRow).toHaveClass('px-4', 'upto-15rem:px-2', ...tiny('px-1'));
    for (const tab of within(tabRow).getAllByRole('tab')) {
      expect(tab).toHaveClass('px-3', 'upto-15rem:px-2', ...tiny('px-1'));
    }
    const download = within(tabRow).getByRole('link');
    expect(download).toHaveClass('px-3', 'upto-15rem:px-2', ...tiny('px-1'));
    const panel = screen.getByRole('tabpanel');
    expect(panel).toHaveClass('p-4', 'upto-15rem:p-3', ...tiny('px-0.5', 'py-2'));
    const toggle = screen.getByTestId('node-toggle-expand-N-1');
    const row = toggle.parentElement!.parentElement as HTMLElement;
    expect(row).toHaveClass('p-3', 'upto-15rem:px-1', ...tiny('px-0.5'));
  });

  it('lets the node type and artifact badges put their label under the icon, padding less', () => {
    renderTicket();
    const toggle = screen.getByTestId('node-toggle-expand-N-1');
    const left = toggle.parentElement as HTMLElement;
    // NodeTypeBadge: the only uppercase badge in the row.
    const typeBadge = left.querySelector('.uppercase') as HTMLElement;
    expect(typeBadge.querySelector('svg')).not.toBeNull();
    expect(typeBadge).toHaveClass('inline-flex', 'px-1.5', ...tiny('flex-wrap', 'px-1'));
    const artifactBadge = within(left).getByText(i18n.t('ticketItem.artifactsCount', { count: 2 })) as HTMLElement;
    expect(artifactBadge).toHaveClass('flex', 'px-1.5', ...tiny('flex-wrap', 'px-1'));
  });

  it('pads the footer buttons less, so their label keeps three characters a line', () => {
    renderTicket();
    const footer = screen.getByTestId('ticket-action-footer');
    const refine = within(footer).getByRole('button', { name: i18n.t('ticketItem.actions.refine') });
    const run = within(footer).getByRole('button', { name: i18n.t('ticketItem.actions.run') });
    const send = within(footer).getByRole('button', { name: new RegExp(i18n.t('ticketItem.send')) });
    expect(refine).toHaveClass('px-3', ...tiny('px-1.5'));
    expect(run).toHaveClass('px-3', ...tiny('px-1.5'));
    expect(send).toHaveClass('px-4', 'max-sm:px-3', ...tiny('px-1.5'));
    expect(within(footer).getByTestId('autopilot-start')).toHaveClass('px-3', ...tiny('px-1.5'));
  });

  it('uses only the px query for all of this (no 15rem or rem variant added for it)', () => {
    renderTicket();
    const details = screen.getByTestId('ticket-details');
    const tinyClasses = [details, ...details.querySelectorAll('*')].flatMap(el =>
      (el.getAttribute('class') ?? '').split(/\s+/).filter(c => c.startsWith(T))
    );
    expect(tinyClasses.length).toBeGreaterThan(20);
    // Nothing inside the details uses a 200px query other than this exact one.
    const other = [details, ...details.querySelectorAll('*')].flatMap(el =>
      (el.getAttribute('class') ?? '').split(/\s+/).filter(c => c.includes('max-width:') && !c.startsWith(T))
    );
    expect(other).toEqual([]);
  });
});
