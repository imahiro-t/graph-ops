// DFLT-00151: the ticket list's interactive icon buttons (the header row's
// chevron, reopen/close/delete buttons, and the expanded panel's node-row
// chevron) rest at text-slate-500 / dark:text-slate-400, which clears the
// WCAG 1.4.11 3:1 non-text contrast on every background they sit on
// (white / slate-50 / slate-100 in light, slate-900 / slate-800 / slate-700
// in dark). The old text-slate-400 / dark:text-slate-500 did not.
//
// DFLT-00162: the panel's secondary text (the "no description" and
// "no artifacts yet" placeholders, among others) meets WCAG 1.4.3's 4.5:1
// text contrast at text-slate-500 / dark:text-slate-400 against the white /
// slate-900 panels. The node row's sequence number and update time sit on a
// row whose hover background is slate-100 / slate-700, where that pair falls
// short (4.34:1 / 4.04:1), so they use text-slate-600 / dark:text-slate-300
// instead. The ratios per background are in the ticket's implementation
// notes.
//
// jsdom computes no colors, so these tests pin the Tailwind classes that
// produce them, and check that the existing hover/disabled classes are kept.
import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { formatTime } from '../i18n/formatDate';
import { GraphNode, TicketDetail, TicketStatus } from '../types';
import { TicketItem } from './TicketItem';

const TICKET_ID = 'TEST-00151';

const NODE: GraphNode = {
  id: 'TEST-00151-01',
  ticket_id: TICKET_ID,
  name: '計画作成',
  type: 'plan',
  status: 'TODO',
  iteration_count: 0,
  max_iterations: 3,
  is_manual: false,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z'
};

const makeTicket = (status: TicketStatus = 'TODO', nodes: GraphNode[] = []): TicketDetail => ({
  id: TICKET_ID,
  project_id: 'proj-1',
  title: 'タイトル',
  description: '説明',
  status,
  auto_executable: true,
  blocked: false,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
  priority: 'MEDIUM',
  labels: [],
  nodes,
  edges: [],
  artifacts: []
});

const renderItem = (ticket: TicketDetail, isExpanded = false) =>
  render(
    <TicketItem
      ticket={ticket}
      isExpanded={isExpanded}
      onToggleExpand={vi.fn()}
      onRefresh={vi.fn()}
      myName=""
      projectLabels={[]}
    />
  );

const expectContrastColors = (el: HTMLElement) => {
  expect(el).toHaveClass('text-slate-500', 'dark:text-slate-400');
  expect(el).not.toHaveClass('text-slate-400');
  expect(el).not.toHaveClass('dark:text-slate-500');
};

// The chevron is found by its accessible name (DFLT-00152).
const headerChevron = () =>
  screen.getByRole('button', { name: i18n.t('ticketItem.toggleTicket', { id: TICKET_ID }) });

// TicketItem measures its graph panel with a ResizeObserver when expanded,
// which jsdom does not implement (same stub as TicketItem.labels.test.tsx).
beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('TicketItem icon button contrast (WCAG 1.4.11)', () => {
  it('uses the higher-contrast resting color on the header chevron', () => {
    renderItem(makeTicket());
    const chevron = headerChevron();
    expectContrastColors(chevron);
    expect(chevron).toHaveClass('hover:text-slate-600', 'dark:hover:text-slate-300');
  });

  it('uses the higher-contrast resting color on the close and delete buttons', () => {
    renderItem(makeTicket());
    const close = screen.getByTitle(i18n.t('ticketItem.close.button'));
    const del = screen.getByTitle(i18n.t('ticketItem.delete.button'));
    expectContrastColors(close);
    expectContrastColors(del);
    expect(close).toHaveClass('hover:text-slate-700', 'dark:hover:text-slate-200');
    expect(del).toHaveClass(
      'hover:text-red-600',
      'dark:hover:text-red-400',
      'disabled:opacity-50',
      'disabled:cursor-not-allowed'
    );
  });

  it('uses the higher-contrast resting color on the reopen button of a CLOSED ticket', () => {
    renderItem(makeTicket('CLOSED'));
    const reopen = screen.getByTitle(i18n.t('ticketItem.reopen.button'));
    expectContrastColors(reopen);
    expect(reopen).toHaveClass(
      'hover:text-indigo-600',
      'dark:hover:text-indigo-400',
      'disabled:opacity-50',
      'disabled:cursor-not-allowed'
    );
  });

  it('uses the higher-contrast resting color on the node-row chevron in the expanded panel', () => {
    renderItem(makeTicket('IN PROGRESS', [NODE]), true);
    const chevron = screen.getByRole('button', { name: i18n.t('ticketItem.toggleNode', { id: NODE.id }) });
    expectContrastColors(chevron);
  });
});

describe('TicketItem secondary text contrast (WCAG 1.4.3, DFLT-00162)', () => {
  const expectNoOldColors = (el: HTMLElement) => {
    expect(el).not.toHaveClass('text-slate-400');
    expect(el).not.toHaveClass('dark:text-slate-500');
  };

  it('draws the "no description" placeholder in slate-500 / dark:slate-400', () => {
    renderItem({ ...makeTicket(), description: '' }, true);
    const empty = screen.getByText(i18n.t('ticketItem.description.empty'));
    expectContrastColors(empty);
    // Size and style are unchanged.
    expect(empty).toHaveClass('italic', 'text-xs');
  });

  it('draws the "no artifacts yet" placeholder in slate-500 / dark:slate-400', async () => {
    const user = userEvent.setup();
    renderItem(makeTicket(), true);
    await user.click(screen.getByRole('button', { name: i18n.t('ticketItem.tabs.artifacts', { count: 0 }) }));
    expectContrastColors(screen.getByText(i18n.t('ticketItem.noArtifactsYet')));
  });

  it('draws the node row\'s sequence number and update time in slate-600 / dark:slate-300, which also clear 4.5:1 on the hover background', () => {
    renderItem(makeTicket('IN PROGRESS', [NODE]), true);
    const chevron = screen.getByRole('button', { name: i18n.t('ticketItem.toggleNode', { id: NODE.id }) });
    const row = chevron.parentElement!;
    const seq = within(row).getByText('1', { exact: true });
    const time = within(row.parentElement!).getByText(formatTime(NODE.updated_at, i18n.language));
    for (const el of [seq, time]) {
      expect(el).toHaveClass('text-slate-600', 'dark:text-slate-300', 'font-mono');
      expectNoOldColors(el);
    }
    // The row's hover background (a state color) is unchanged.
    expect(row.parentElement).toHaveClass('hover:bg-slate-100', 'dark:hover:bg-slate-700');
  });
});
