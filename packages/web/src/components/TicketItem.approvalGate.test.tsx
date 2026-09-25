// DFLT-00157: a reached TODO approval_gate blinks and offers approve/reject
// in the ticket list -- except on a CLOSED ticket, whose nodes the engine
// refuses to complete (INVALID_NODE_STATE). There the gate is not "awaiting
// approval" at all, matching GET /api/projects/pending-approvals, which
// leaves CLOSED tickets out of its counts (DFLT-00144 D-1).
import { fireEvent, render, screen } from '@testing-library/react';
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
const makeTicket = (status: TicketStatus): TicketDetail => ({
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
    node('TEST-00157-02', GATE_NAME, 'approval_gate', 'TODO', true)
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

const renderTicket = (status: TicketStatus) =>
  render(
    <TicketItem
      ticket={makeTicket(status)}
      isExpanded
      onToggleExpand={vi.fn()}
      onRefresh={vi.fn()}
      myName=""
      projectLabels={[]}
    />
  );

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
