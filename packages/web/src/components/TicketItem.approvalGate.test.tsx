// DFLT-00157: a reached TODO approval_gate blinks and offers approve/reject
// in the ticket list -- except on a CLOSED ticket, whose nodes the engine
// refuses to complete (INVALID_NODE_STATE). There the gate is not "awaiting
// approval" at all, matching GET /api/projects/pending-approvals, which
// leaves CLOSED tickets out of its counts (DFLT-00144 D-1).
import React from 'react';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
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
const escapeRegExp = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

// DFLT-00176: while a decision is being submitted the gate's buttons carry a
// visually hidden "(submitting)" text in their accessible name. Match the
// visible label plus that optional suffix exactly (anchored at both ends):
// a prefix match would let 却下 (reject) also match 却下を確定 (confirm reject)
// under the ja locale the tests run in.
//   submitting omitted -> the suffix is optional
//   submitting: true   -> the suffix is required
//   submitting: false  -> no suffix
type ButtonKey = 'approve' | 'reject' | 'confirmReject';
const buttonName = (key: ButtonKey, { submitting }: { submitting?: boolean } = {}) => {
  const label = escapeRegExp(i18n.t(`ticketItem.approvalGate.${key}`));
  const sub = escapeRegExp(i18n.t('common.submitting'));
  if (submitting === true) return new RegExp(`^${label}\\s*${sub}$`);
  if (submitting === false) return new RegExp(`^${label}$`);
  return new RegExp(`^${label}\\s*(?:${sub})?$`);
};
const approveButton = () => screen.queryByRole('button', { name: buttonName('approve') });
const rejectButton = () => screen.queryByRole('button', { name: buttonName('reject') });
const confirmRejectButton = () => screen.queryByRole('button', { name: buttonName('confirmReject') });

// DFLT-00207: while a decision is being sent, the gate's buttons are
// aria-disabled rather than disabled, so they keep focus. "Inert" is that
// state; "idle" is the normal one (no aria-disabled at all, and not disabled
// either -- a confirm button with a reason in its field).
const expectInert = (button: HTMLElement | null) => {
  expect(button).toHaveAttribute('aria-disabled', 'true');
  expect(button).toHaveProperty('disabled', false);
};
const isIdle = (button: HTMLElement | null) =>
  button !== null && !button.hasAttribute('aria-disabled') && !(button as HTMLButtonElement).disabled;
const expectIdleButton = (button: HTMLElement | null) => expect(isIdle(button)).toBe(true);

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

  // DFLT-00174: Escape in the prompt does what Cancel does.
  describe('Escape', () => {
    const expectCancelled = () => {
      expect(reasonInput()).toBeNull();
      expect(document.activeElement).toBe(rejectButton());
      expectNoAnnouncement();
      // The draft is gone: reopening starts empty.
      expect(openPrompt().value).toBe('');
    };

    it.each([
      ['the reason field', () => reasonInput() as HTMLElement],
      ['the confirm button', () => confirmRejectButton() as HTMLElement],
      ['the Cancel button', () => cancelButton()]
    ])('closes the prompt like Cancel from %s', (_label, target) => {
      renderTicket('IN REVIEW');
      const input = openPrompt();
      fireEvent.change(input, { target: { value: '理由' } });
      const el = target();
      el.focus();

      const notCancelled = fireEvent.keyDown(el, { key: 'Escape' });

      expect(notCancelled).toBe(false);
      expectCancelled();
    });

    it('does not reach document-level listeners', () => {
      renderTicket('IN REVIEW');
      const input = openPrompt();
      const documentListener = vi.fn();
      document.addEventListener('keydown', documentListener);
      let notCancelled: boolean;
      try {
        notCancelled = fireEvent.keyDown(input, { key: 'Escape' });
      } finally {
        document.removeEventListener('keydown', documentListener);
      }
      expect(documentListener).not.toHaveBeenCalled();
      expect(notCancelled).toBe(false);
      expect(reasonInput()).toBeNull();
    });

    it('stops at the prompt for React handlers of its ancestors', () => {
      const onKeyDown = vi.fn();
      render(
        <div onKeyDown={onKeyDown}>
          {ticketElement(makeTicket('IN REVIEW'))}
        </div>
      );
      const input = openPrompt();

      fireEvent.keyDown(input, { key: 'Escape' });
      expect(onKeyDown).not.toHaveBeenCalled();
      expect(reasonInput()).toBeNull();

      // Other keys still bubble as before.
      const reopened = openPrompt();
      fireEvent.keyDown(reopened, { key: 'a' });
      expect(onKeyDown).toHaveBeenCalledTimes(1);
    });

    it.each([
      ['isComposing', { isComposing: true }],
      ['keyCode 229', { keyCode: 229 }]
    ])('does not close while an IME composition is in progress (%s)', (_label, init) => {
      renderTicket('IN REVIEW');
      const input = openPrompt();
      fireEvent.change(input, { target: { value: '理由' } });

      const notCancelled = fireEvent.keyDown(input, { key: 'Escape', ...init });

      expect(notCancelled).toBe(true);
      expect(reasonInput()).toBe(input);
      expect(input.value).toBe('理由');
      expect(document.activeElement).toBe(input);
    });

    it('does not close while the rejection is being submitted', async () => {
      const respond = stubHeldCompletes();
      renderTicket('IN REVIEW');
      rejectWithReason(GATE_ID);
      const input = reasonInput() as HTMLInputElement;
      input.focus();

      const notCancelled = fireEvent.keyDown(input, { key: 'Escape' });

      expect(notCancelled).toBe(true);
      expect(reasonInput()).toBe(input);
      expect(input.value).toBe('理由');

      await respond(GATE_ID, new Response('{}', { status: 200 }));
      await waitFor(() => expect(reasonInput()).toBeNull());
    });
  });

  // DFLT-00174: a failed rejection leaves the prompt up; focus goes back to
  // its reason field unless the user moved elsewhere meanwhile.
  describe('when the rejection fails', () => {
    const failure = () =>
      new Response(JSON.stringify({ error: { code: 'INVALID_NODE_STATE', message: 'refused' } }), { status: 409 });
    const errorShown = () => expect(screen.getByText(i18n.t('errors.INVALID_NODE_STATE'))).not.toBeNull();

    it('brings focus back to the reason field when it fell to <body>', async () => {
      const respond = stubHeldCompletes();
      renderTicket('IN REVIEW');
      rejectWithReason(GATE_ID);
      // The confirm button keeps focus while submitting (aria-disabled,
      // DFLT-00207); this is the safety net for focus lost some other way.
      (document.activeElement as HTMLElement).blur();
      expect(document.activeElement).toBe(document.body);

      await respond(GATE_ID, failure());

      const input = reasonInput() as HTMLInputElement;
      await waitFor(() => expect(document.activeElement).toBe(input));
      expect(input.value).toBe('理由');
      expectIdleButton(confirmRejectButton());
      errorShown();
      expectNoAnnouncement();
    });

    it('moves focus from the confirm button to the reason field', async () => {
      const respond = stubHeldCompletes();
      renderTicket('IN REVIEW');
      const input = openPrompt();
      fireEvent.change(input, { target: { value: '理由' } });
      // Clicking the button focuses it, and it keeps focus while submitting:
      // it is only aria-disabled then (DFLT-00207).
      const confirm = confirmRejectButton() as HTMLElement;
      confirm.focus();
      fireEvent.click(confirm);
      expect(document.activeElement).toBe(confirm);
      expectInert(confirm);

      await respond(GATE_ID, failure());

      await waitFor(() => expect(document.activeElement).toBe(reasonInput()));
      expect((reasonInput() as HTMLInputElement).value).toBe('理由');
      errorShown();
    });

    it('leaves focus on a control the user moved to during the submit', async () => {
      const respond = stubHeldCompletes();
      renderTicket('IN REVIEW');
      rejectWithReason(GATE_ID);
      const elsewhere = screen.getByTestId('ticket-copy-id');
      elsewhere.focus();

      await respond(GATE_ID, failure());

      await waitFor(() => expectIdleButton(confirmRejectButton()));
      expect(reasonInput()).not.toBeNull();
      expect(document.activeElement).toBe(elsewhere);
    });

    it('can then be cancelled with Escape', async () => {
      const respond = stubHeldCompletes();
      renderTicket('IN REVIEW');
      rejectWithReason(GATE_ID);
      await respond(GATE_ID, failure());
      await waitFor(() => expect(document.activeElement).toBe(reasonInput()));

      fireEvent.keyDown(reasonInput() as HTMLElement, { key: 'Escape' });

      expect(reasonInput()).toBeNull();
      expect(document.activeElement).toBe(rejectButton());
    });
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

  // DFLT-00173: decisions on two gates in flight at once. One finishing must
  // neither wipe the reason being typed in another gate's prompt nor make a
  // gate whose own decision is still in flight look idle and clickable again.
  const approveOf = (nodeId: string) => screen.getByTestId(`node-approve-${nodeId}`) as HTMLButtonElement;
  const completeCalls = (nodeId: string) =>
    vi.mocked(fetch).mock.calls.filter(([input]) => String(input).endsWith(`/nodes/${nodeId}/complete`)).length;
  const isSpinning = (button: HTMLElement) => button.querySelector('.animate-spin') !== null;

  it("keeps the draft in another gate's reject prompt when an approval on one gate succeeds", async () => {
    const respond = stubHeldCompletes();
    renderTicket('IN REVIEW', { ticket: twoGateTicket('IN REVIEW') });
    fireEvent.click(approveOf(GATE_ID));
    fireEvent.click(screen.getByTestId(`node-reject-${GATE_B_ID}`));
    const inputB = reasonInput() as HTMLInputElement;
    fireEvent.change(inputB, { target: { value: '書きかけの理由' } });

    await respond(GATE_ID, new Response('{}', { status: 200 }));

    await waitFor(() => expect(isSpinning(approveOf(GATE_ID))).toBe(false));
    expect(reasonInput()).toBe(inputB);
    expect(inputB.value).toBe('書きかけの理由');
    expect(document.activeElement).toBe(inputB);
    expectIdleButton(confirmRejectButton());
  });

  it("keeps the draft in another gate's reject prompt when a rejection on one gate succeeds", async () => {
    const respond = stubHeldCompletes();
    renderTicket('IN REVIEW', { ticket: twoGateTicket('IN REVIEW') });
    rejectWithReason(GATE_ID);
    // While A's reject is in flight, start typing a reason for B.
    fireEvent.click(screen.getByTestId(`node-reject-${GATE_B_ID}`));
    const inputB = reasonInput() as HTMLInputElement;
    fireEvent.change(inputB, { target: { value: '書きかけの理由' } });

    await respond(GATE_ID, new Response('{}', { status: 200 }));

    await waitFor(() => expect(announcement(rejectedText())).toHaveLength(1));
    expect(reasonInput()).toBe(inputB);
    expect(inputB.value).toBe('書きかけの理由');
    expect(document.activeElement).toBe(inputB);
  });

  it('keeps a gate whose approval is still in flight aria-disabled and spinning when another gate finishes first', async () => {
    const respond = stubHeldCompletes();
    renderTicket('IN REVIEW', { ticket: twoGateTicket('IN REVIEW') });
    fireEvent.click(approveOf(GATE_ID));
    fireEvent.click(approveOf(GATE_B_ID));
    expectInert(approveOf(GATE_ID));
    expectInert(approveOf(GATE_B_ID));

    await respond(GATE_ID, new Response('{}', { status: 200 }));

    // A is done; B is still in flight.
    await waitFor(() => expectIdleButton(approveOf(GATE_ID)));
    expectInert(approveOf(GATE_B_ID));
    expect(isSpinning(approveOf(GATE_B_ID))).toBe(true);
    expectInert(screen.getByTestId(`node-reject-${GATE_B_ID}`));

    await respond(GATE_B_ID, new Response('{}', { status: 200 }));
    await waitFor(() => expectIdleButton(approveOf(GATE_B_ID)));
    expect(isSpinning(approveOf(GATE_B_ID))).toBe(false);
  });

  it("keeps a gate's reject confirm button aria-disabled while its reject is in flight and another gate finishes first", async () => {
    const respond = stubHeldCompletes();
    renderTicket('IN REVIEW', { ticket: twoGateTicket('IN REVIEW') });
    fireEvent.click(approveOf(GATE_ID));
    rejectWithReason(GATE_B_ID);
    expectInert(confirmRejectButton());

    await respond(GATE_ID, new Response('{}', { status: 200 }));

    await waitFor(() => expectIdleButton(approveOf(GATE_ID)));
    // B's prompt is still open with its reason, and still submitting.
    expect((reasonInput() as HTMLInputElement).value).toBe('理由');
    const confirmB = confirmRejectButton() as HTMLButtonElement;
    expectInert(confirmB);
    expect(isSpinning(confirmB)).toBe(true);
    expect(screen.getByRole('button', { name: i18n.t('ticketItem.approvalGate.cancelReject') })).toHaveProperty(
      'disabled',
      true
    );
  });

  it('does not send a second decision for a gate whose first one is still in flight', async () => {
    const respond = stubHeldCompletes();
    renderTicket('IN REVIEW', { ticket: twoGateTicket('IN REVIEW') });
    fireEvent.click(approveOf(GATE_ID));
    fireEvent.click(approveOf(GATE_B_ID));

    await respond(GATE_B_ID, new Response('{}', { status: 200 }));
    await waitFor(() => expectIdleButton(approveOf(GATE_B_ID)));

    // A is still in flight: clicking it again must not post again.
    fireEvent.click(approveOf(GATE_ID));
    expect(completeCalls(GATE_ID)).toBe(1);

    await respond(GATE_ID, new Response('{}', { status: 200 }));
    await waitFor(() => expectIdleButton(approveOf(GATE_ID)));
    expect(completeCalls(GATE_ID)).toBe(1);
  });

  // DFLT-00176: while a decision is in flight the buttons it makes inert tell
  // assistive technology "submitting" -- aria-busy plus a visually hidden
  // suffix in the accessible name -- not just "unavailable". Derived from the
  // per-gate in-flight state only, so it clears when the response arrives and
  // never shows on another gate.
  describe('tells assistive technology a decision is being submitted', () => {
    const rejectOf = (nodeId: string) => screen.getByTestId(`node-reject-${nodeId}`) as HTMLButtonElement;
    const expectSubmitting = (button: HTMLElement, key: ButtonKey) => {
      expect(button).toHaveAttribute('aria-busy', 'true');
      expect(button).toHaveAccessibleName(buttonName(key, { submitting: true }));
    };
    const expectIdle = (button: HTMLElement, key: ButtonKey) => {
      expect(button).not.toHaveAttribute('aria-busy');
      expect(button).toHaveAccessibleName(buttonName(key, { submitting: false }));
    };

    it('marks the approve and reject buttons busy while an approval is in flight, and clears it once it responds', async () => {
      const respond = stubHeldCompletes();
      renderTicket('IN REVIEW');
      expectIdle(approveOf(GATE_ID), 'approve');
      expectIdle(rejectOf(GATE_ID), 'reject');

      fireEvent.click(approveOf(GATE_ID));

      expectSubmitting(approveOf(GATE_ID), 'approve');
      expectSubmitting(rejectOf(GATE_ID), 'reject');
      // The visual spinner is still there.
      expect(isSpinning(approveOf(GATE_ID))).toBe(true);

      await respond(GATE_ID, new Response('{}', { status: 200 }));

      // onRefresh leaves the ticket as is, so the gate is still pending.
      await waitFor(() => expectIdleButton(approveOf(GATE_ID)));
      expectIdle(approveOf(GATE_ID), 'approve');
      expectIdle(rejectOf(GATE_ID), 'reject');
      expect(isSpinning(approveOf(GATE_ID))).toBe(false);
    });

    it('marks the reject confirm button busy while a rejection is in flight, and clears it when it fails', async () => {
      const respond = stubHeldCompletes();
      renderTicket('IN REVIEW');
      expect(rejectButton()).not.toBeNull();
      fireEvent.click(rejectButton() as HTMLElement);
      const input = reasonInput() as HTMLInputElement;
      fireEvent.change(input, { target: { value: '理由' } });
      expectIdle(confirmRejectButton() as HTMLElement, 'confirmReject');

      fireEvent.click(confirmRejectButton() as HTMLElement);

      const confirm = confirmRejectButton() as HTMLButtonElement;
      expectInert(confirm);
      expectSubmitting(confirm, 'confirmReject');
      expect(isSpinning(confirm)).toBe(true);
      // The cancel button keeps its plain name.
      expect(screen.getByRole('button', { name: i18n.t('ticketItem.approvalGate.cancelReject') })).not.toHaveAttribute(
        'aria-busy'
      );

      await respond(GATE_ID, new Response('{}', { status: 500 }));

      await waitFor(() => expectIdleButton(confirmRejectButton()));
      expectIdle(confirmRejectButton() as HTMLElement, 'confirmReject');
      expect(isSpinning(confirmRejectButton() as HTMLElement)).toBe(false);
      // The prompt stays open with its reason, and focus is in the reason field.
      expect(reasonInput()).toBe(input);
      expect(input.value).toBe('理由');
      expect(document.activeElement).toBe(input);
    });

    it("shows it only on the gate whose decision is in flight", async () => {
      const respond = stubHeldCompletes();
      renderTicket('IN REVIEW', { ticket: twoGateTicket('IN REVIEW') });

      fireEvent.click(approveOf(GATE_ID));

      expectSubmitting(approveOf(GATE_ID), 'approve');
      expectSubmitting(rejectOf(GATE_ID), 'reject');
      expectIdle(approveOf(GATE_B_ID), 'approve');
      expectIdle(rejectOf(GATE_B_ID), 'reject');

      fireEvent.click(approveOf(GATE_B_ID));
      await respond(GATE_ID, new Response('{}', { status: 200 }));

      // A is done; B is still in flight.
      await waitFor(() => expectIdleButton(approveOf(GATE_ID)));
      expectIdle(approveOf(GATE_ID), 'approve');
      expectIdle(rejectOf(GATE_ID), 'reject');
      expectSubmitting(approveOf(GATE_B_ID), 'approve');
      expectSubmitting(rejectOf(GATE_B_ID), 'reject');

      await respond(GATE_B_ID, new Response('{}', { status: 200 }));
      await waitFor(() => expectIdleButton(approveOf(GATE_B_ID)));
      expectIdle(approveOf(GATE_B_ID), 'approve');
      expectIdle(rejectOf(GATE_B_ID), 'reject');
    });

    it("does not mark another gate's open reject prompt busy while one gate's approval is in flight", async () => {
      const respond = stubHeldCompletes();
      renderTicket('IN REVIEW', { ticket: twoGateTicket('IN REVIEW') });
      fireEvent.click(approveOf(GATE_ID));
      fireEvent.click(rejectOf(GATE_B_ID));
      fireEvent.change(reasonInput() as HTMLInputElement, { target: { value: '理由' } });

      expectSubmitting(approveOf(GATE_ID), 'approve');
      expectIdle(confirmRejectButton() as HTMLElement, 'confirmReject');

      await respond(GATE_ID, new Response('{}', { status: 200 }));
      await waitFor(() => expectIdleButton(approveOf(GATE_ID)));
      expectIdle(confirmRejectButton() as HTMLElement, 'confirmReject');
    });
  });

  // DFLT-00207: a disabled button drops focus to <body> in some browsers, so
  // the approve, reject and confirm-reject buttons are only aria-disabled
  // while a decision is being sent. Focus stays on the pressed button, where
  // its busy name is read out; presses meanwhile (click, Enter, Space) are
  // ignored; the look (dimmed, not-allowed cursor) is unchanged; and the
  // focus moves at the end of the submit (DFLT-00172/00174) still happen.
  describe('keeps focus on the pressed button while a decision is being sent', () => {
    const rejectOf = (nodeId: string) => screen.getByTestId(`node-reject-${nodeId}`) as HTMLButtonElement;
    const expectFocusedAndSubmitting = (button: HTMLElement, key: ButtonKey) => {
      expect(document.activeElement).toBe(button);
      expectInert(button);
      expect(button).toHaveAttribute('aria-busy', 'true');
      expect(button).toHaveAccessibleName(buttonName(key, { submitting: true }));
    };
    const expectDimmedWhileInert = (button: HTMLElement) => {
      expect(button.className).toContain('aria-disabled:opacity-50');
      expect(button.className).toContain('aria-disabled:cursor-not-allowed');
    };

    it('keeps focus on the approve button and ignores further clicks, Enter and Space', async () => {
      const user = userEvent.setup();
      const respond = stubHeldCompletes();
      renderTicket('IN REVIEW');

      await user.click(approveOf(GATE_ID));

      const approve = approveOf(GATE_ID);
      expectFocusedAndSubmitting(approve, 'approve');
      expectDimmedWhileInert(approve);
      await user.click(approve);
      await user.keyboard('{Enter}');
      await user.keyboard(' ');
      expect(completeCalls(GATE_ID)).toBe(1);
      expect(document.activeElement).toBe(approve);

      await respond(GATE_ID, new Response('{}', { status: 200 }));

      // onRefresh leaves the ticket as is, so the gate is still pending.
      await waitFor(() => expectIdleButton(approveOf(GATE_ID)));
      expect(approveOf(GATE_ID)).not.toHaveAttribute('aria-busy');
      expect(completeCalls(GATE_ID)).toBe(1);
      // DFLT-00215: the approval succeeded, so focus has already moved on to
      // the gate toggle, ahead of the refresh that removes the buttons.
      expect(document.activeElement).toBe(toggleOf(GATE_ID));
    });

    it('does not open the reject prompt from the reject button while an approval is in flight', async () => {
      const user = userEvent.setup();
      const respond = stubHeldCompletes();
      renderTicket('IN REVIEW');
      await user.click(approveOf(GATE_ID));

      const reject = rejectOf(GATE_ID);
      expectInert(reject);
      expect(reject).toHaveAccessibleName(buttonName('reject', { submitting: true }));
      expectDimmedWhileInert(reject);
      await user.click(reject);
      expect(document.activeElement).toBe(reject);
      await user.keyboard('{Enter}');
      await user.keyboard(' ');
      expect(reasonInput()).toBeNull();
      expect(completeCalls(GATE_ID)).toBe(1);

      await respond(GATE_ID, new Response('{}', { status: 200 }));
      await waitFor(() => expectIdleButton(rejectOf(GATE_ID)));
      // Idle again: it opens the prompt as usual.
      await user.click(rejectOf(GATE_ID));
      expect(document.activeElement).toBe(reasonInput());
    });

    it('keeps focus on the confirm button, ignores further presses, and moves it to the gate toggle on success', async () => {
      const user = userEvent.setup();
      const respond = stubHeldCompletes();
      renderTicket('IN REVIEW');
      await user.click(rejectOf(GATE_ID));
      await user.type(reasonInput() as HTMLInputElement, '理由');

      await user.click(confirmRejectButton() as HTMLElement);

      const confirm = confirmRejectButton() as HTMLElement;
      expectFocusedAndSubmitting(confirm, 'confirmReject');
      expectDimmedWhileInert(confirm);
      await user.click(confirm);
      await user.keyboard('{Enter}');
      await user.keyboard(' ');
      expect(completeCalls(GATE_ID)).toBe(1);
      expect(document.activeElement).toBe(confirm);

      await respond(GATE_ID, new Response('{}', { status: 200 }));

      await waitFor(() => expect(reasonInput()).toBeNull());
      expect(document.activeElement).toBe(toggleOf(GATE_ID));
      expect(announcement(rejectedText())).toHaveLength(1);
      expect(completeCalls(GATE_ID)).toBe(1);
    });

    it('moves focus from the confirm button back to the reason field when the rejection fails', async () => {
      const user = userEvent.setup();
      const respond = stubHeldCompletes();
      renderTicket('IN REVIEW');
      await user.click(rejectOf(GATE_ID));
      await user.type(reasonInput() as HTMLInputElement, '理由');
      await user.click(confirmRejectButton() as HTMLElement);
      expectFocusedAndSubmitting(confirmRejectButton() as HTMLElement, 'confirmReject');

      await respond(
        GATE_ID,
        new Response(JSON.stringify({ error: { code: 'INVALID_NODE_STATE', message: 'refused' } }), { status: 409 })
      );

      await waitFor(() => expect(document.activeElement).toBe(reasonInput()));
      expectIdleButton(confirmRejectButton());
      expect(confirmRejectButton()).not.toHaveAttribute('aria-busy');
      expect(completeCalls(GATE_ID)).toBe(1);
    });

    it('does not natively disable the confirm button when its reason is cleared mid-submit', async () => {
      const user = userEvent.setup();
      const respond = stubHeldCompletes();
      renderTicket('IN REVIEW');
      await user.click(rejectOf(GATE_ID));
      const input = reasonInput() as HTMLInputElement;
      await user.type(input, '理由');
      await user.click(confirmRejectButton() as HTMLElement);
      const confirm = confirmRejectButton() as HTMLElement;

      // The field stays editable while sending.
      fireEvent.change(input, { target: { value: '' } });

      expectInert(confirm);
      expect(document.activeElement).toBe(confirm);
      await user.click(confirm);
      expect(completeCalls(GATE_ID)).toBe(1);

      await respond(GATE_ID, new Response('{}', { status: 500 }));

      // After the failed submit an empty reason disables it natively again,
      // and focus is back in the (empty) reason field.
      await waitFor(() => expect(document.activeElement).toBe(input));
      expect(confirmRejectButton()).toHaveProperty('disabled', true);
      expect(confirmRejectButton()).not.toHaveAttribute('aria-disabled');
    });
  });

  // DFLT-00215: a successful approval removes the gate's Approve and Reject
  // buttons, which left focus on <body> with nothing announced. It now moves
  // to the gate toggle (from either button, or from <body>) and "<gate>
  // approved" is announced -- without pulling focus off a control the user
  // moved to while the approval was in flight.
  describe('moves focus to the gate toggle and announces it when an approval succeeds', () => {
    const approvedText = (name = GATE_NAME) => i18n.t('ticketItem.approvalGate.approvedAnnouncement', { name });
    const rejectOf = (nodeId: string) => screen.getByTestId(`node-reject-${nodeId}`) as HTMLButtonElement;
    const expectApprovedAndOnToggle = () => {
      expect(document.activeElement).toBe(toggleOf(GATE_ID));
      expect(document.activeElement).not.toBe(document.body);
      expect(announcement(approvedText())).toHaveLength(1);
    };

    it('from a mouse click on the approve button', async () => {
      const user = userEvent.setup();
      const respond = stubHeldCompletes();
      const { rerenderTicket } = renderTicket('IN REVIEW', { strict: true });

      await user.click(approveOf(GATE_ID));
      expect(document.activeElement).toBe(approveOf(GATE_ID));
      expect(announcement(approvedText())).toHaveLength(0);

      await respond(GATE_ID, new Response('{}', { status: 200 }));
      await waitFor(() => expect(announcement(approvedText())).toHaveLength(1));

      // The refresh that follows shows the gate DONE: both buttons are gone.
      rerenderTicket(makeTicket('IN REVIEW', 'DONE'));
      expect(screen.queryByTestId(`node-approve-${GATE_ID}`)).toBeNull();
      expect(screen.queryByTestId(`node-reject-${GATE_ID}`)).toBeNull();
      expectApprovedAndOnToggle();
      expect(completeCalls(GATE_ID)).toBe(1);
    });

    it.each([
      ['Enter', '{Enter}'],
      ['Space', ' ']
    ])('from %s on the focused approve button', async (_label, keys) => {
      const user = userEvent.setup();
      const respond = stubHeldCompletes();
      const { rerenderTicket } = renderTicket('IN REVIEW', { strict: true });
      approveOf(GATE_ID).focus();

      await user.keyboard(keys);
      expect(completeCalls(GATE_ID)).toBe(1);
      expect(document.activeElement).toBe(approveOf(GATE_ID));

      await respond(GATE_ID, new Response('{}', { status: 200 }));
      await waitFor(() => expect(announcement(approvedText())).toHaveLength(1));

      rerenderTicket(makeTicket('IN REVIEW', 'DONE'));
      expect(screen.queryByTestId(`node-approve-${GATE_ID}`)).toBeNull();
      expectApprovedAndOnToggle();
    });

    it("from the same gate's reject button the user tabbed to while the approval was in flight", async () => {
      const user = userEvent.setup();
      const respond = stubHeldCompletes();
      const { rerenderTicket } = renderTicket('IN REVIEW', { strict: true });
      await user.click(approveOf(GATE_ID));

      await user.tab();
      expect(document.activeElement).toBe(rejectOf(GATE_ID));

      await respond(GATE_ID, new Response('{}', { status: 200 }));
      await waitFor(() => expect(announcement(approvedText())).toHaveLength(1));

      rerenderTicket(makeTicket('IN REVIEW', 'DONE'));
      expect(screen.queryByTestId(`node-reject-${GATE_ID}`)).toBeNull();
      expectApprovedAndOnToggle();
    });

    it('from <body> when a poll removed the buttons before the approval responded', async () => {
      const user = userEvent.setup();
      const respond = stubHeldCompletes();
      const { rerenderTicket } = renderTicket('IN REVIEW', { strict: true });
      await user.click(approveOf(GATE_ID));

      // App's polling lands the DONE gate while the POST is still pending.
      rerenderTicket(makeTicket('IN REVIEW', 'DONE'));
      expect(screen.queryByTestId(`node-approve-${GATE_ID}`)).toBeNull();
      expect(document.activeElement).toBe(document.body);
      expect(announcement(approvedText())).toHaveLength(0);

      await respond(GATE_ID, new Response('{}', { status: 200 }));

      await waitFor(() => expect(announcement(approvedText())).toHaveLength(1));
      expectApprovedAndOnToggle();
    });

    it('leaves focus on a control the user moved to while the approval was in flight, and still announces it', async () => {
      const user = userEvent.setup();
      const respond = stubHeldCompletes();
      const { rerenderTicket } = renderTicket('IN REVIEW', { ticket: twoGateTicket('IN REVIEW'), strict: true });
      await user.click(approveOf(GATE_ID));

      // Another gate's button: a control that survives this gate's approval.
      const elsewhere = approveOf(GATE_B_ID);
      elsewhere.focus();

      await respond(GATE_ID, new Response('{}', { status: 200 }));
      await waitFor(() => expect(announcement(approvedText())).toHaveLength(1));
      expect(document.activeElement).toBe(elsewhere);

      rerenderTicket(twoGateTicket('IN REVIEW', 'DONE'));
      expect(screen.queryByTestId(`node-approve-${GATE_ID}`)).toBeNull();
      expect(document.activeElement).toBe(approveOf(GATE_B_ID));
      expect(announcement(approvedText())).toHaveLength(1);
      expect(completeCalls(GATE_B_ID)).toBe(0);
    });

    it('keeps focus on the approve button, shows the error and announces nothing when the approval fails', async () => {
      const user = userEvent.setup();
      const respond = stubHeldCompletes();
      renderTicket('IN REVIEW', { strict: true });
      await user.click(approveOf(GATE_ID));

      await respond(
        GATE_ID,
        new Response(JSON.stringify({ error: { code: 'INVALID_NODE_STATE', message: 'refused' } }), { status: 409 })
      );

      await waitFor(() => expectIdleButton(approveOf(GATE_ID)));
      expect(screen.getByText(i18n.t('errors.INVALID_NODE_STATE'))).not.toBeNull();
      expect(document.activeElement).toBe(approveOf(GATE_ID));
      expect(screen.queryByText(approvedText())).toBeNull();
    });

    it('announces a second approval of a gate with the same name again', async () => {
      const user = userEvent.setup();
      const respond = stubHeldCompletes();
      const { rerenderTicket } = renderTicket('IN REVIEW', { strict: true });
      await user.click(approveOf(GATE_ID));
      await respond(GATE_ID, new Response('{}', { status: 200 }));
      await waitFor(() => expect(announcement(approvedText())).toHaveLength(1));

      // The gate is reached again (a loop back) and approved once more: the
      // slot is emptied when the approval starts, so the same text is a
      // change the live region reads out again.
      rerenderTicket(makeTicket('IN REVIEW', 'TODO'));
      await user.click(approveOf(GATE_ID));
      expect(announcement(approvedText())).toHaveLength(0);
      await respond(GATE_ID, new Response('{}', { status: 200 }));
      await waitFor(() => expect(announcement(approvedText())).toHaveLength(1));
      expect(document.activeElement).toBe(toggleOf(GATE_ID));
    });
  });
});
