// DFLT-00157: a reached TODO approval_gate blinks and offers approve/reject
// in the ticket list -- except on a CLOSED ticket, whose nodes the engine
// refuses to complete (INVALID_NODE_STATE). There the gate is not "awaiting
// approval" at all, matching GET /api/projects/pending-approvals, which
// leaves CLOSED tickets out of its counts (DFLT-00144 D-1).
import React from 'react';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { GraphNode, TicketDetail, TicketStatus } from '../types';
import { TicketItem } from './TicketItem';

const node = (id: string, name: string, type: GraphNode['type'], status: GraphNode['status'], is_manual: boolean): GraphNode => ({
  id,
  ticket_id: 'TEST-00157',
  name,
  type,
  status,
  iteration_count: 0,
  max_iterations: 3,
  is_manual,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z'
});

const GATE_NAME = '計画承認';

// plan (DONE) --success--> approval_gate (TODO): the gate is reached.
const makeTicket = (status: TicketStatus, gateStatus: GraphNode['status'] = 'TODO'): TicketDetail => ({
  id: 'TEST-00157',
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
  nodes: [
    node('TEST-00157-01', '計画作成', 'plan', 'DONE', false),
    node('TEST-00157-02', GATE_NAME, 'approval_gate', gateStatus, true)
  ],
  edges: [
    {
      id: 'edge-1',
      ticket_id: 'TEST-00157',
      from_node_id: 'TEST-00157-01',
      to_node_id: 'TEST-00157-02',
      condition: 'success',
      created_at: '2026-01-01T00:00:00Z'
    }
  ],
  artifacts: []
});

const ticketElement = (ticket: TicketDetail, onRefresh: () => Promise<void> | void = vi.fn()) => (
  <TicketItem
    ticket={ticket}
    isExpanded
    onToggleExpand={vi.fn()}
    onRefresh={onRefresh as () => Promise<void>}
    myName=""
    projectLabels={[]}
  />
);

interface RenderOptions {
  ticket?: TicketDetail;
  onRefresh?: () => Promise<void> | void;
  // Wrap in React.StrictMode (kept across rerenders), as main.tsx does.
  strict?: boolean;
}

const renderTicket = (status: TicketStatus, options: RenderOptions = {}) => {
  const onRefresh = options.onRefresh ?? vi.fn();
  const result = render(ticketElement(options.ticket ?? makeTicket(status), onRefresh), {
    wrapper: options.strict ? React.StrictMode : undefined
  });
  return {
    ...result,
    rerenderTicket: (ticket: TicketDetail) => result.rerender(ticketElement(ticket, onRefresh))
  };
};

// The gate's status tick in the header row: the element whose title starts
// with the gate's name. Narrowed to it because animate-pulse is also used by
// IN PROGRESS ticks.
const gateTick = (container: HTMLElement) => {
  const ticks = Array.from(container.querySelectorAll<HTMLElement>('[title]')).filter(el =>
    el.getAttribute('title')?.startsWith(`${GATE_NAME} (`)
  );
  expect(ticks).toHaveLength(1);
  return ticks[0];
};

const pendingLabel = () => i18n.t('ticketItem.approvalGate.pendingStatus');
const approveButton = () => screen.queryByRole('button', { name: i18n.t('ticketItem.approvalGate.approve') });
const rejectButton = () => screen.queryByRole('button', { name: i18n.t('ticketItem.approvalGate.reject') });
const confirmRejectButton = () =>
  screen.queryByRole('button', { name: i18n.t('ticketItem.approvalGate.confirmReject') });

beforeEach(() => {
  // Expanded rows may fetch (autopilot decisions etc.); keep them quiet.
  vi.stubGlobal('fetch', vi.fn(async () => new Response('[]', { status: 200 })));
  // TicketItem measures its graph panel with a ResizeObserver, which jsdom lacks.
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('TicketItem approval gate on a CLOSED ticket', () => {
  it('does not blink, say "awaiting approval", or offer approve/reject', () => {
    const { container } = renderTicket('CLOSED');

    const tick = gateTick(container);
    expect(tick.className).not.toContain('animate-pulse');
    expect(tick.className).not.toContain('bg-amber-400');
    expect(tick.getAttribute('title')).not.toContain(pendingLabel());
    for (const el of Array.from(container.querySelectorAll('[title]'))) {
      expect(el.getAttribute('title')).not.toContain(pendingLabel());
    }

    expect(approveButton()).toBeNull();
    expect(rejectButton()).toBeNull();
  });

  it('drops an open reject prompt once the ticket turns CLOSED', () => {
    const { rerender } = renderTicket('IN REVIEW');
    fireEvent.click(rejectButton() as HTMLElement);
    expect(confirmRejectButton()).not.toBeNull();

    rerender(
      <TicketItem
        ticket={makeTicket('CLOSED')}
        isExpanded
        onToggleExpand={vi.fn()}
        onRefresh={vi.fn()}
        myName=""
        projectLabels={[]}
      />
    );

    expect(confirmRejectButton()).toBeNull();
    expect(approveButton()).toBeNull();
    expect(rejectButton()).toBeNull();
  });

  it('does not bring the reject prompt (or its draft) back when the ticket is reopened', () => {
    const { rerender } = renderTicket('IN REVIEW');
    fireEvent.click(rejectButton() as HTMLElement);
    const reasonInput = screen.getByPlaceholderText(i18n.t('ticketItem.approvalGate.reasonPlaceholder'));
    fireEvent.change(reasonInput, { target: { value: '途中まで書いた理由' } });

    const renderWith = (status: TicketStatus) =>
      rerender(
        <TicketItem
          ticket={makeTicket(status)}
          isExpanded
          onToggleExpand={vi.fn()}
          onRefresh={vi.fn()}
          myName=""
          projectLabels={[]}
        />
      );
    renderWith('CLOSED');
    renderWith('IN REVIEW');

    // The gate is pending again, so it offers approve/reject afresh -- the
    // autoFocus reason input must not remount on its own and steal focus.
    expect(screen.queryByPlaceholderText(i18n.t('ticketItem.approvalGate.reasonPlaceholder'))).toBeNull();
    expect(screen.queryByDisplayValue('途中まで書いた理由')).toBeNull();
    expect(confirmRejectButton()).toBeNull();
    expect(approveButton()).not.toBeNull();
    expect(rejectButton()).not.toBeNull();

    // Opening the prompt again starts from an empty draft.
    fireEvent.click(rejectButton() as HTMLElement);
    expect(screen.getByPlaceholderText(i18n.t('ticketItem.approvalGate.reasonPlaceholder'))).toHaveProperty('value', '');
  });
});

describe.each<TicketStatus>(['IN REVIEW', 'DONE'])('TicketItem approval gate on a %s ticket', status => {
  it('blinks, says "awaiting approval", and offers approve/reject as before', () => {
    const { container } = renderTicket(status);

    const tick = gateTick(container);
    expect(tick.className).toContain('bg-amber-400');
    expect(tick.className).toContain('animate-pulse');
    expect(tick.getAttribute('title')).toBe(`${GATE_NAME} (${pendingLabel()})`);

    expect(approveButton()).not.toBeNull();
    expect(rejectButton()).not.toBeNull();
  });
});

// DFLT-00172: when the reject prompt goes away, focus must not fall to
// <body>. Our own rejection and a gate that stopped being pending move focus
// to the gate's node-row toggle (always rendered) and are announced through a
// live region; cancelling returns to the Reject button.
describe('TicketItem reject prompt focus and announcements', () => {
  const GATE_ID = 'TEST-00157-02';
  const toggleOf = (nodeId: string) => screen.getByTestId(`node-toggle-expand-${nodeId}`);
  const reasonInput = () => screen.queryByPlaceholderText(i18n.t('ticketItem.approvalGate.reasonPlaceholder'));
  const cancelButton = () => screen.getByRole('button', { name: i18n.t('ticketItem.approvalGate.cancelReject') });
  const rejectedText = (name = GATE_NAME) => i18n.t('ticketItem.approvalGate.rejectedAnnouncement', { name });
  const noLongerPendingText = (name = GATE_NAME) =>
    i18n.t('ticketItem.approvalGate.noLongerPendingAnnouncement', { name });
  // The announcement lives in a role="status" live region (there are several
  // such regions in TicketItem, so it is looked up by its text).
  const announcement = (text: string) =>
    screen.queryAllByRole('status').filter(el => el.textContent === text);
  const expectNoAnnouncement = () => {
    expect(screen.queryByText(rejectedText())).toBeNull();
    expect(screen.queryByText(noLongerPendingText())).toBeNull();
  };

  const openPrompt = () => {
    fireEvent.click(rejectButton() as HTMLElement);
    const input = reasonInput() as HTMLInputElement;
    expect(document.activeElement).toBe(input);
    return input;
  };

  // Routes POST /api/nodes/<id>/complete to `complete`; everything else keeps
  // the quiet default.
  const stubComplete = (complete: (url: string) => Promise<Response>) =>
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) =>
        String(input).endsWith('/complete') ? complete(String(input)) : new Response('[]', { status: 200 })
      )
    );
  // Holds each gate's reject POST open until the test answers it.
  const stubHeldCompletes = () => {
    const pending = new Map<string, (res: Response) => void>();
    stubComplete(url => new Promise<Response>(resolve => pending.set(url.split('/').at(-2) as string, resolve)));
    return async (nodeId: string, res: Response) => {
      const respond = pending.get(nodeId);
      expect(respond).toBeDefined();
      await act(async () => {
        respond?.(res);
      });
    };
  };

  const GATE_B_ID = 'TEST-00157-03';
  const GATE_B_NAME = '設計承認';
  // plan --success--> gate A (GATE_ID) and gate B (GATE_B_ID), both reached.
  const twoGateTicket = (
    status: TicketStatus,
    gateA: GraphNode['status'] = 'TODO',
    gateB: GraphNode['status'] = 'TODO'
  ): TicketDetail => {
    const base = makeTicket(status, gateA);
    return {
      ...base,
      nodes: [...base.nodes, node(GATE_B_ID, GATE_B_NAME, 'approval_gate', gateB, true)],
      edges: [
        ...base.edges,
        {
          id: 'edge-2',
          ticket_id: 'TEST-00157',
          from_node_id: 'TEST-00157-01',
          to_node_id: GATE_B_ID,
          condition: 'success',
          created_at: '2026-01-01T00:00:00Z'
        }
      ]
    };
  };
  const rejectWithReason = (nodeId: string) => {
    fireEvent.click(screen.getByTestId(`node-reject-${nodeId}`));
    const input = reasonInput() as HTMLInputElement;
    expect(document.activeElement).toBe(input);
    fireEvent.change(input, { target: { value: '理由' } });
    fireEvent.click(confirmRejectButton() as HTMLElement);
  };

  it('moves focus to the gate toggle and announces the rejection once the reject succeeds', async () => {
    stubComplete(async () => new Response('{}', { status: 200 }));
    const { rerenderTicket } = renderTicket('IN REVIEW');
    const input = openPrompt();
    fireEvent.change(input, { target: { value: '理由' } });
    fireEvent.click(confirmRejectButton() as HTMLElement);

    await waitFor(() => expect(document.activeElement).toBe(toggleOf(GATE_ID)));
    expect(reasonInput()).toBeNull();
    expect(announcement(rejectedText())).toHaveLength(1);

    // The refresh that follows shows the gate REJECTED; focus stays put.
    rerenderTicket(makeTicket('IN REVIEW', 'REJECTED'));
    expect(document.activeElement).toBe(toggleOf(GATE_ID));
    expect(announcement(rejectedText())).toHaveLength(1);
  });

  it('moves focus to the gate toggle and announces it when the ticket turns CLOSED under the input', () => {
    const { rerenderTicket } = renderTicket('IN REVIEW');
    openPrompt();

    rerenderTicket(makeTicket('CLOSED'));

    expect(reasonInput()).toBeNull();
    expect(document.activeElement).toBe(toggleOf(GATE_ID));
    expect(announcement(noLongerPendingText())).toHaveLength(1);
  });

  it('does the same from the Cancel button when the gate is judged elsewhere', () => {
    const { rerenderTicket } = renderTicket('IN REVIEW');
    openPrompt();
    cancelButton().focus();
    expect(document.activeElement).toBe(cancelButton());

    rerenderTicket(makeTicket('IN REVIEW', 'DONE'));

    expect(reasonInput()).toBeNull();
    expect(document.activeElement).toBe(toggleOf(GATE_ID));
    expect(announcement(noLongerPendingText())).toHaveLength(1);
  });

  it('leaves focus alone and stays silent when focus was outside the prompt', () => {
    const { rerenderTicket } = renderTicket('IN REVIEW');
    openPrompt();
    const elsewhere = screen.getByTestId('ticket-copy-id');
    elsewhere.focus();

    rerenderTicket(makeTicket('CLOSED'));

    expect(reasonInput()).toBeNull();
    expect(document.activeElement).toBe(elsewhere);
    expectNoAnnouncement();
  });

  it('returns focus to the Reject button on Cancel', () => {
    renderTicket('IN REVIEW');
    openPrompt();

    fireEvent.click(cancelButton());

    expect(reasonInput()).toBeNull();
    expect(document.activeElement).toBe(rejectButton());
    expectNoAnnouncement();
  });

  it('announces the rejection once when a poll removes the prompt before the POST responds, and leaves nothing behind', async () => {
    let respond: (res: Response) => void = () => {};
    stubComplete(() => new Promise<Response>(resolve => (respond = resolve)));
    const { rerenderTicket } = renderTicket('IN REVIEW');
    const input = openPrompt();
    fireEvent.change(input, { target: { value: '理由' } });
    fireEvent.click(confirmRejectButton() as HTMLElement);

    // App's polling lands the REJECTED gate while the POST is still pending.
    rerenderTicket(makeTicket('IN REVIEW', 'REJECTED'));
    expect(reasonInput()).toBeNull();
    expectNoAnnouncement();

    await act(async () => {
      respond(new Response('{}', { status: 200 }));
    });

    await waitFor(() => expect(announcement(rejectedText())).toHaveLength(1));
    expect(screen.getAllByText(rejectedText())).toHaveLength(1);
    expect(screen.queryByText(noLongerPendingText())).toBeNull();
    expect(document.activeElement).toBe(toggleOf(GATE_ID));

    // Nothing lingers: a later prompt that closes with focus outside it is
    // neither announced as a rejection nor allowed to take focus.
    rerenderTicket(makeTicket('IN REVIEW', 'TODO'));
    openPrompt();
    const elsewhere = screen.getByTestId('ticket-copy-id');
    elsewhere.focus();
    rerenderTicket(makeTicket('CLOSED'));

    expect(reasonInput()).toBeNull();
    expect(document.activeElement).toBe(elsewhere);
    expectNoAnnouncement();
  });

  it("leaves focus in the other gate's prompt when switching the reject prompt between gates", () => {
    renderTicket('IN REVIEW', { ticket: twoGateTicket('IN REVIEW') });

    fireEvent.click(screen.getByTestId(`node-reject-${GATE_ID}`));
    expect(document.activeElement).toBe(reasonInput());

    fireEvent.click(screen.getByTestId(`node-reject-${GATE_B_ID}`));

    const inputB = reasonInput() as HTMLInputElement;
    expect(inputB).not.toBeNull();
    expect(document.activeElement).toBe(inputB);
    expect(document.activeElement).not.toBe(toggleOf(GATE_ID));
    expectNoAnnouncement();
    expect(screen.queryByText(noLongerPendingText(GATE_B_NAME))).toBeNull();
  });

  it("does not steal focus from the input under React.StrictMode's double-invoked effects", () => {
    const { rerenderTicket } = renderTicket('IN REVIEW', { strict: true });
    const input = openPrompt();

    fireEvent.change(input, { target: { value: 'a' } });
    expect(document.activeElement).toBe(input);
    expectNoAnnouncement();
    fireEvent.change(input, { target: { value: 'ab' } });
    expect(document.activeElement).toBe(input);
    expectNoAnnouncement();

    // The real paths still work under StrictMode.
    fireEvent.click(cancelButton());
    expect(document.activeElement).toBe(rejectButton());
    expectNoAnnouncement();

    openPrompt();
    rerenderTicket(makeTicket('CLOSED'));
    expect(document.activeElement).toBe(toggleOf(GATE_ID));
    expect(announcement(noLongerPendingText())).toHaveLength(1);
  });

  // Review findings on DFLT-00172 round 1: a late response for a prompt that
  // a poll already removed must not pull focus back once the user has moved
  // on, and rejects on two gates in flight at once must not clear each
  // other's state.
  it("does not pull focus out of another gate's prompt when a rejection whose prompt a poll removed responds late", async () => {
    const respond = stubHeldCompletes();
    const { rerenderTicket } = renderTicket('IN REVIEW', { ticket: twoGateTicket('IN REVIEW') });
    rejectWithReason(GATE_ID);

    // A poll lands A's REJECTED gate first; the user then opens B's prompt.
    rerenderTicket(twoGateTicket('IN REVIEW', 'REJECTED'));
    expect(reasonInput()).toBeNull();
    fireEvent.click(screen.getByTestId(`node-reject-${GATE_B_ID}`));
    const inputB = reasonInput() as HTMLInputElement;
    expect(document.activeElement).toBe(inputB);

    await respond(GATE_ID, new Response('{}', { status: 200 }));

    await waitFor(() => expect(announcement(rejectedText())).toHaveLength(1));
    expect(document.activeElement).toBe(inputB);
  });

  it('does not pull focus off a control the user moved to when a rejection whose prompt a poll removed responds late', async () => {
    const respond = stubHeldCompletes();
    const { rerenderTicket } = renderTicket('IN REVIEW');
    rejectWithReason(GATE_ID);

    rerenderTicket(makeTicket('IN REVIEW', 'REJECTED'));
    const elsewhere = screen.getByTestId('ticket-copy-id');
    elsewhere.focus();

    await respond(GATE_ID, new Response('{}', { status: 200 }));

    await waitFor(() => expect(announcement(rejectedText())).toHaveLength(1));
    expect(document.activeElement).toBe(elsewhere);
  });

  it('settles as "no longer awaiting approval" when a poll removes the prompt and the reject then fails', async () => {
    const respond = stubHeldCompletes();
    const { rerenderTicket } = renderTicket('IN REVIEW');
    rejectWithReason(GATE_ID);

    // The ticket turns CLOSED under the in-flight reject, which then fails.
    rerenderTicket(makeTicket('CLOSED'));
    expect(reasonInput()).toBeNull();
    expectNoAnnouncement();

    await respond(
      GATE_ID,
      new Response(JSON.stringify({ error: { code: 'INVALID_NODE_STATE', message: 'closed' } }), { status: 409 })
    );

    await waitFor(() => expect(announcement(noLongerPendingText())).toHaveLength(1));
    expect(document.activeElement).toBe(toggleOf(GATE_ID));
    expect(screen.getByText(i18n.t('errors.INVALID_NODE_STATE'))).not.toBeNull();
    expect(screen.queryByText(rejectedText())).toBeNull();
  });

  it("keeps each gate's in-flight reject apart when rejects on two gates overlap", async () => {
    const respond = stubHeldCompletes();
    const { rerenderTicket } = renderTicket('IN REVIEW', { ticket: twoGateTicket('IN REVIEW') });
    rejectWithReason(GATE_ID);
    // While A's POST is in flight, reject B too (A's prompt is switched out).
    rejectWithReason(GATE_B_ID);

    // A answers first: announced, and focus stays in B's (still open) prompt.
    await respond(GATE_ID, new Response('{}', { status: 200 }));
    await waitFor(() => expect(announcement(rejectedText())).toHaveLength(1));
    expect(document.activeElement).toBe(reasonInput());

    // A poll then removes B's prompt before B's own response. B's reject is
    // still in flight, so this is not "no longer awaiting approval".
    rerenderTicket(twoGateTicket('IN REVIEW', 'REJECTED', 'REJECTED'));
    expect(reasonInput()).toBeNull();
    expect(screen.queryByText(noLongerPendingText(GATE_B_NAME))).toBeNull();

    await respond(GATE_B_ID, new Response('{}', { status: 200 }));

    await waitFor(() => expect(announcement(rejectedText(GATE_B_NAME))).toHaveLength(1));
    expect(screen.queryByText(noLongerPendingText(GATE_B_NAME))).toBeNull();
    expect(document.activeElement).toBe(toggleOf(GATE_B_ID));
  });
});
