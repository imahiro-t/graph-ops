// DFLT-00232: a ticket's header row wraps instead of running past the card.
// With the autopilot badges (and a rejected approval badge, labels and
// "+N") the row's items no longer fit at 320px with the default font size,
// or at 375-700px with a 150-200% default: the card's overflow-clip hid what
// ran past it -- the badges first -- and the absolutely positioned sr-only
// text of "waiting for a person" (and of "+N"), whose containing block lay
// outside the card, escaped the clip and made the page scroll sideways.
// Now the row, its left group and its right-hand group wrap, the title keeps
// giving up space first (down to 3rem) so a row that fits on one line looks
// as before, the sr-only texts sit in relative boxes, and a large default
// font on a narrow screen (under 15rem) pads the row with p-3. jsdom does no
// layout (nor media queries), so this checks the classes; the page's
// scrollWidth, what is clipped and the row's geometry at 100-200% and
// 320-1280px were measured in a real browser, and the build output was
// checked for the generated rules (see the ticket's implementation notes).
import { render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { NO_AUTOPILOT, TicketAutopilotView } from '../lib/autopilotApi';
import { GraphNode, Label, TicketDetail } from '../types';
import { TicketItem } from './TicketItem';

const label = (id: string, name: string): Label => ({
  id,
  project_id: 'proj-1',
  name,
  color: 'blue',
  created_at: '',
  updated_at: ''
});
const LABELS = [label('l1', 'UI'), label('l2', 'Bug'), label('l3', 'Backend'), label('l4', 'Performance')];

const node = (id: string, type: GraphNode['type'], status: GraphNode['status'], name: string): GraphNode => ({
  id,
  ticket_id: 'TEST-00232',
  name,
  type,
  status,
  iteration_count: 0,
  max_iterations: 3,
  is_manual: type === 'approval_gate',
  created_at: '',
  updated_at: ''
});

const LONG_TITLE = 'A long title that has to truncate before anything in the row wraps';

const makeTicket = (): TicketDetail => ({
  id: 'TEST-00232',
  project_id: 'proj-1',
  title: LONG_TITLE,
  description: '説明',
  status: 'IN PROGRESS',
  auto_executable: true,
  blocked: false,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
  priority: 'HIGH',
  assignee: 'Someone Else',
  labels: LABELS,
  nodes: [
    node('n1', 'plan', 'DONE', 'Plan'),
    node('n2', 'implementation', 'IN PROGRESS', 'Implementation'),
    node('n3', 'approval_gate', 'REJECTED', 'Release approval')
  ],
  edges: [],
  artifacts: []
});

const AWAITING: TicketAutopilotView = {
  ...NO_AUTOPILOT,
  badges: ['running', 'awaitingHuman'],
  awaiting: 'release approval'
};

const renderRow = () =>
  render(
    <TicketItem
      ticket={makeTicket()}
      isExpanded={false}
      onToggleExpand={vi.fn()}
      onRefresh={vi.fn(async () => {})}
      myName=""
      projectLabels={LABELS}
      autopilot={AWAITING}
    />
  );

// Each class is checked on its own: `not.toHaveClass(a, b)` passes as soon as
// one of them is missing, so it would not catch the other slipping in.
const expectNoneOf = (el: Element, classes: string[]) => {
  for (const cls of classes) expect(el).not.toHaveClass(cls);
};

beforeEach(() => {
  vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('[]', { status: 200 }))));
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
});

afterEach(async () => {
  vi.unstubAllGlobals();
  await i18n.changeLanguage('ja');
});

describe.each(['ja', 'en'] as const)('TicketItem header row wraps instead of overflowing (%s)', lng => {
  beforeEach(async () => {
    await i18n.changeLanguage(lng);
  });

  it('wraps the row and pads it less only with large text on a narrow screen', () => {
    renderRow();
    const row = screen.getByTestId('ticket-header-row');
    expect(row).toHaveClass('flex', 'flex-wrap', 'justify-between', 'gap-4', 'gap-y-2');
    expect(row).toHaveClass('p-4', '[@media(max-width:15rem)]:p-3');
    // Unconditional or px-based forms would change the default size or sm+.
    expectNoneOf(row, ['p-3', 'max-sm:p-3', 'sm:p-4', 'flex-nowrap']);
  });

  it('wraps the left group, which claims 16rem before the right-hand group moves down', () => {
    renderRow();
    const row = screen.getByTestId('ticket-header-row');
    const left = row.firstElementChild as HTMLElement;
    expect(left).toContainElement(screen.getByTestId('ticket-header-title'));
    expect(left).toContainElement(screen.getByTestId('autopilot-badges'));
    expect(left).toHaveClass('flex', 'flex-wrap', 'flex-1', 'basis-64', 'min-w-0');
  });

  it('lets the title truncate down to 3rem before anything wraps, and no wider than its text', () => {
    renderRow();
    const title = screen.getByTestId('ticket-header-title');
    expect(title).toHaveTextContent(LONG_TITLE);
    expect(title).toHaveClass('grow', 'basis-12', 'max-w-max', 'truncate', 'min-w-0');
    expectNoneOf(title, ['basis-full', 'basis-0', 'shrink-0', 'whitespace-normal']);
  });

  it('wraps the labels and keeps the "+N" sr-only text inside its chip', () => {
    renderRow();
    const labels = screen.getByTestId('ticket-header-labels');
    expect(labels).toHaveClass('flex-wrap', 'min-w-0', 'max-w-full');
    expect(labels).not.toHaveClass('shrink-0');
    const more = within(labels).getByTestId('ticket-header-labels-more');
    expect(more).toHaveClass('relative');
    expect(more.querySelector('.sr-only')).not.toBeNull();
  });

  it('lets the rejected approval badge shrink and truncate its name instead of running past the card', () => {
    renderRow();
    const name = screen.getByText(i18n.t('ticketItem.approvalGate.rejectedBadgeOne', { name: 'Release approval' }));
    expect(name).toHaveClass('truncate', 'max-w-48');
    const badge = name.parentElement as HTMLElement;
    expect(badge).toHaveClass('min-w-0', 'max-w-full');
    expect(badge).not.toHaveClass('shrink-0');
  });

  it('wraps the right-hand group and its node squares only on a line of their own', () => {
    renderRow();
    const row = screen.getByTestId('ticket-header-row');
    const right = row.lastElementChild as HTMLElement;
    expect(right).toHaveTextContent('Someone Else');
    expect(right).toHaveClass('flex', 'flex-wrap', 'min-w-0');
    expect(right).not.toHaveClass('shrink-0');
    const square = within(right).getByTitle(/^Plan \(/);
    expect(square.parentElement).toHaveClass('flex', 'flex-wrap', 'min-w-0');
    expect(square.parentElement?.parentElement).toHaveClass('min-w-0');
  });

  it('shows the autopilot badges in the header row', () => {
    renderRow();
    const row = screen.getByTestId('ticket-header-row');
    expect(within(row).getByTestId('autopilot-badge-running')).toBeInTheDocument();
    expect(within(row).getByTestId('autopilot-badge-awaitingHuman')).toHaveClass('relative');
  });
});
