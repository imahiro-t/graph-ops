// DFLT-00251: a ticket's header row at 320px with a 200% text size and in a
// 160px window (320px at 200% zoom). The ID, the status (e.g. "AWAITING
// FIX", "リファイン済み") and a long assignee name ran past the card, whose
// overflow-clip cut them off. Now:
// - the ID chip stays on one line wherever it fits and, under 15rem (the rem
//   query of DFLT-00227), may break inside the ID; it is never wider than
//   the row;
// - the status chip (e.g. "リファイン済み") is never wider than the row and wraps inside itself
//   there (at every width: a larger root font size set on the page does not
//   move the rem query, and the chip overflowed there too; a chip that fits
//   is laid out at its one-line width as before);
// - the assignee chips, the "assign to me" button and the assign error wrap
//   and are never wider than the row.
// The header row's small text (the "+N" label chip, the rejected approval
// badge, the assignee chips and the button) and the expanded description's
// expand button use 0.6875rem instead of 11px, so they follow the browser's
// text size (11px at the default 16px, so the default size looks the same).
// The rejected badge's truncated gate name can be read in full: its title
// starts with the whole text (every gate's name when several were
// rejected), and with several the names are sr-only text inside the
// (relative) badge.
//
// jsdom does no layout, so these tests pin the classes, the title and the
// text. The page's scrollWidth, what the card clips, elementFromPoint on the
// row's buttons, the computed font sizes at 16/24/32px and the unchanged
// geometry at 100% were measured in a real browser (see the ticket's
// implementation notes).
import { render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { getStatusMeta } from '../statusMeta';
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

const gate = (id: string, name: string): GraphNode => ({
  id,
  ticket_id: 'TEST-00251',
  name,
  type: 'approval_gate',
  status: 'REJECTED',
  iteration_count: 0,
  max_iterations: 3,
  is_manual: true,
  created_at: '',
  updated_at: ''
});

const makeTicket = (overrides: Partial<TicketDetail> = {}): TicketDetail => ({
  id: 'TEST-00251',
  project_id: 'proj-1',
  title: 'タイトル',
  description: '説明の本文',
  status: 'REFINED',
  auto_executable: true,
  blocked: false,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
  refined_at: '2026-01-02T03:04:05Z',
  priority: 'MEDIUM',
  labels: LABELS,
  nodes: [],
  edges: [],
  artifacts: [],
  ...overrides
});

const renderTicket = (ticket: TicketDetail, { myName = '', isExpanded = false } = {}) =>
  render(
    <TicketItem
      ticket={ticket}
      isExpanded={isExpanded}
      onToggleExpand={vi.fn()}
      onRefresh={vi.fn(async () => {})}
      myName={myName}
      projectLabels={LABELS}
    />
  );

const NARROW = 'upto-15rem:';

beforeEach(() => {
  vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('[]', { status: 200 }))));
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
});

afterEach(async () => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  await i18n.changeLanguage('ja');
});

describe.each(['ja', 'en'] as const)('TicketItem header row in a narrow window (%s)', lng => {
  beforeEach(async () => {
    await i18n.changeLanguage(lng);
  });

  it('keeps the ID on one line where it fits and lets it break only under 15rem', () => {
    renderTicket(makeTicket());
    const id = screen.getByTestId('ticket-header-id');
    expect(id).toHaveTextContent('TEST-00251');
    expect(id).toHaveClass('shrink-0', 'whitespace-nowrap', 'select-text', 'min-w-0', 'max-w-full');
    expect(id).toHaveClass(`${NARROW}whitespace-normal`, `${NARROW}wrap-anywhere`);
    // Unconditional forms would change the one-line ID at every width.
    expect(id).not.toHaveClass('whitespace-normal');
    expect(id).not.toHaveClass('wrap-anywhere');
    expect(id).not.toHaveClass('[overflow-wrap:anywhere]');
    // The copy button still follows the ID directly.
    expect(id.nextElementSibling).toContainElement(screen.getByTestId('ticket-copy-id'));
  });

  it('lets the status chip wrap inside itself instead of running past the card', () => {
    renderTicket(makeTicket());
    const status = screen.getByTestId('ticket-header-status');
    expect(status).toHaveTextContent(i18n.t(getStatusMeta('REFINED').labelKey));
    expect(status).toHaveClass('shrink-0', 'max-w-full', 'wrap-anywhere', 'rounded-full');
    expect(status).not.toHaveClass('whitespace-nowrap');
  });

  it('lets another person\'s long name wrap inside its chip and the chip group', () => {
    const name = 'Alexandria Montgomery-Featherstonehaugh';
    renderTicket(makeTicket({ assignee: name }));
    const group = screen.getByTestId('ticket-header-assignee');
    expect(group).toHaveClass('flex', 'flex-wrap', 'min-w-0', 'max-w-full');
    const chip = within(group).getByText(name);
    expect(chip).toHaveClass('min-w-0', 'max-w-full', 'wrap-anywhere', 'text-[0.6875rem]');
    expect(chip).not.toHaveClass('text-[11px]');
  });

  it('sizes the viewer\'s own chip in rem and lets it wrap', () => {
    renderTicket(makeTicket({ assignee: 'Me' }), { myName: 'Me' });
    const chip = within(screen.getByTestId('ticket-header-assignee')).getByText('Me');
    expect(chip).toHaveClass('min-w-0', 'max-w-full', 'wrap-anywhere', 'text-[0.6875rem]');
    expect(chip).not.toHaveClass('text-[11px]');
    expect(within(chip).getByRole('button', { name: i18n.t('ticketItem.selfAssign.unassign') })).toBeInTheDocument();
  });

  it('sizes the "assign to me" button in rem and lets it wrap', () => {
    renderTicket(makeTicket({ assignee: undefined }), { myName: 'Me' });
    const button = screen.getByRole('button', { name: i18n.t('ticketItem.selfAssign.assign') });
    expect(button).toHaveClass('min-w-0', 'max-w-full', 'wrap-anywhere', 'text-[0.6875rem]');
    expect(button).not.toHaveClass('text-[11px]');
  });

  it('sizes the "+N" label chip in rem', () => {
    renderTicket(makeTicket());
    const more = screen.getByTestId('ticket-header-labels-more');
    expect(more).toHaveClass('text-[0.6875rem]', 'relative');
    expect(more).not.toHaveClass('text-[11px]');
  });

  it('sizes the expanded description\'s expand button in rem', () => {
    renderTicket(makeTicket(), { isExpanded: true });
    const expand = screen.getByRole('button', { name: i18n.t('ticketItem.description.expand') });
    expect(expand).toHaveClass('text-[0.6875rem]');
    expect(expand).not.toHaveClass('text-[11px]');
  });
});

describe.each([
  {
    lng: 'ja',
    one: 'とても長い名前のリリース前アクセシビリティ承認ゲート（最終確認）',
    shownOne: '却下済み: とても長い名前のリリース前アクセシビリティ承認ゲート（最終確認）',
    many: ['セキュリティ承認ゲート', 'リリース前の最終承認ゲート'],
    shownMany: '却下済み 2件'
  },
  {
    lng: 'en',
    one: 'Very long accessibility sign-off gate before the final release',
    shownOne: 'Rejected: Very long accessibility sign-off gate before the final release',
    many: ['Security sign-off gate', 'Final pre-release approval'],
    shownMany: '2 rejected'
  }
] as const)('TicketItem rejected approval badge shows the full gate names ($lng)', ({ lng, one, shownOne, many, shownMany }) => {
  beforeEach(async () => {
    await i18n.changeLanguage(lng);
  });

  const hint = () => i18n.t('ticketItem.approvalGate.rejectedHint');

  it('puts the one gate\'s full name in the title, the text and the rem size', () => {
    renderTicket(makeTicket({ nodes: [gate('g1', one)] }));
    const badge = screen.getByTestId('ticket-header-rejected-badge');
    // The shown text is unchanged, and whole in the DOM: truncate only clips
    // what is painted.
    const name = screen.getByText(shownOne);
    expect(name).toHaveClass('truncate', 'max-w-48');
    expect(badge).toContainElement(name);
    expect(badge).toHaveTextContent(one);
    expect(badge.title).toBe(`${shownOne}\n${hint()}`);
    expect(badge).toHaveClass('relative', 'min-w-0', 'max-w-full', 'text-[0.6875rem]');
    expect(badge).not.toHaveClass('text-[11px]');
    expect(badge.querySelector('.sr-only')).toBeNull();
  });

  it('puts every gate\'s full name in the title and in sr-only text inside the relative badge', () => {
    renderTicket(makeTicket({ nodes: [gate('g1', many[0]), gate('g2', many[1])] }));
    const badge = screen.getByTestId('ticket-header-rejected-badge');
    expect(within(badge).getByText(shownMany)).toHaveClass('truncate', 'max-w-48');
    for (const n of many) expect(badge.title).toContain(n);
    expect(badge.title).toContain(hint());
    expect(badge.title.split('\n')[0]).toBe(
      i18n.t('ticketItem.approvalGate.rejectedBadgeNames', {
        names: many.join(i18n.t('ticketItem.approvalGate.rejectedBadgeNameSeparator'))
      })
    );
    const sr = badge.querySelector('.sr-only') as HTMLElement;
    expect(sr).not.toBeNull();
    for (const n of many) expect(sr).toHaveTextContent(n);
    // Without relative the sr-only text's containing block would lie
    // outside the card and could widen the page (DFLT-00232).
    expect(sr.parentElement).toBe(badge);
    expect(badge).toHaveClass('relative');
  });
});
