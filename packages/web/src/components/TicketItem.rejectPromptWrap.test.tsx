// DFLT-00253: an approval gate's reject-with-reason prompt wraps inside its
// node card on a narrow screen. It used to be one unwrapped flex line whose
// <input> kept its intrinsic width (size=20; a flex item's min-width:auto
// would not let it shrink), so at 320-375px (Japanese and English) and 480px
// (English) the input and the confirm/cancel buttons ran 16-120px past the
// card, whose overflow-hidden cut the buttons off. Now the prompt wraps
// (flex-wrap), the input shrinks from an 8rem basis and grows into the rest
// of the line (grow basis-32 min-w-0, in place of flex-1, whose 0% basis
// would fight basis-32 depending on CSS order), so the buttons move to the
// next line when the three do not fit, and on a wide row the input fills
// the line as before. The buttons stay shrink-0 but are capped at the
// prompt's width and may break their label (max-w-full,
// overflow-wrap:anywhere), for a 200% default font on a 320px screen.
//
// jsdom does no layout, so this pins the classes and checks that the
// prompt's behaviour (focus, the disabled confirm button while empty,
// clicks not toggling the row, Escape) is unchanged; the geometry at
// 320/375/480px (ja/en), the 1px focus ring inside the card and the one-line
// layout at 768/1280px were measured in a real browser (see the ticket's
// implementation notes).
import { fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { GraphEdge, GraphNode, TicketDetail } from '../types';
import { TicketItem } from './TicketItem';

const T = 'TEST-00253';

const node = (id: string, type: GraphNode['type'], status: GraphNode['status']): GraphNode => ({
  id,
  ticket_id: T,
  name: `${type} ${id}`,
  type,
  status,
  iteration_count: 0,
  max_iterations: 3,
  is_manual: type === 'approval_gate',
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z'
});

const edge = (from: string, to: string): GraphEdge => ({
  id: `${from}-${to}`,
  ticket_id: T,
  from_node_id: from,
  to_node_id: to,
  condition: 'success',
  created_at: '2026-01-01T00:00:00Z'
});

// An approval gate whose only predecessor is DONE, so it is pending and its
// approve/reject buttons show in the row.
const GATE = node(`${T}-02`, 'approval_gate', 'TODO');
const NODES = [node(`${T}-01`, 'implementation', 'DONE'), GATE];
const EDGES = [edge(`${T}-01`, GATE.id)];

const makeTicket = (): TicketDetail => ({
  id: T,
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
  nodes: NODES,
  edges: EDGES,
  artifacts: []
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

// Each class is checked on its own: `not.toHaveClass(a, b)` passes as soon as
// one of them is missing, so it would not catch the other slipping in.
const expectNoneOf = (el: Element, classes: string[]) => {
  for (const cls of classes) expect(el).not.toHaveClass(cls);
};

// Opens the prompt the way a user does and returns its parts.
const openPrompt = () => {
  fireEvent.click(screen.getByTestId(`node-reject-${GATE.id}`));
  const input = screen.getByRole('textbox', { name: i18n.t('ticketItem.approvalGate.reasonLabel') });
  const prompt = input.parentElement as HTMLElement;
  const confirm = within(prompt).getByRole('button', { name: i18n.t('ticketItem.approvalGate.confirmReject') });
  const cancel = within(prompt).getByRole('button', { name: i18n.t('ticketItem.approvalGate.cancelReject') });
  return { input, prompt, confirm, cancel };
};

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

describe.each(['ja', 'en'] as const)('TicketItem reject reason prompt on a narrow screen (%s)', lang => {
  beforeEach(async () => {
    await i18n.changeLanguage(lang);
  });

  it('wraps, with the input and both buttons as its direct children', () => {
    renderTicket();
    const { input, prompt, confirm, cancel } = openPrompt();
    expect(prompt).toHaveClass('flex', 'flex-wrap', 'items-center', 'gap-2', 'gap-y-1.5', 'px-3', 'pb-3');
    // Neither a clip nor a nowrap may come back.
    expectNoneOf(prompt, ['flex-nowrap', 'overflow-hidden', 'overflow-x-hidden', 'whitespace-nowrap']);
    expect(Array.from(prompt.children)).toEqual([input, confirm, cancel]);
  });

  it('lets the input shrink from an 8rem basis and grow into the rest of the line', () => {
    renderTicket();
    const { input } = openPrompt();
    expect(input).toHaveClass('grow', 'basis-32', 'min-w-0');
    // flex-1's 0% basis would fight basis-32 depending on CSS order.
    expectNoneOf(input, ['flex-1', 'shrink-0', 'w-full']);
    // The 1px focus ring stays (drawn inside the card thanks to px-3).
    expect(input).toHaveClass('focus:ring-1');
  });

  it('keeps each button whole but no wider than the prompt, breaking its label if it must', () => {
    renderTicket();
    const { prompt, confirm, cancel } = openPrompt();
    for (const button of [confirm, cancel]) {
      expect(prompt).toContainElement(button);
      expect(button).toHaveClass('shrink-0', 'max-w-full', 'wrap-anywhere');
      expectNoneOf(button, ['whitespace-nowrap', 'truncate']);
    }
  });

  it('behaves as before: focus in the field, confirm disabled while empty, no row toggle, Escape closes', () => {
    renderTicket();
    const toggle = screen.getByTestId(`node-toggle-expand-${GATE.id}`);
    const { input, prompt, confirm } = openPrompt();
    expect(input).toHaveFocus();
    expect(confirm).toBeDisabled();

    fireEvent.click(prompt);
    fireEvent.click(input);
    expect(toggle).toHaveAttribute('aria-expanded', 'false');

    fireEvent.change(input, { target: { value: '理由' } });
    expect(confirm).toBeEnabled();

    fireEvent.keyDown(input, { key: 'Escape' });
    expect(screen.queryByRole('textbox', { name: i18n.t('ticketItem.approvalGate.reasonLabel') })).toBeNull();
    expect(screen.getByTestId(`node-reject-${GATE.id}`)).toHaveFocus();
  });
});
