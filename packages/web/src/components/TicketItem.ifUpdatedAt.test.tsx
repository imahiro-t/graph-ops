// DFLT-00330: only the label change is conditioned on the ticket's
// updated_at; the priority and "assign to me" PATCHes stay unconditional.
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { Label, TicketDetail } from '../types';
import { TicketItem } from './TicketItem';

const BUG: Label = { id: 'label-bug', project_id: 'proj-1', name: 'バグ', color: 'red', created_at: '', updated_at: '' };
const U0 = '2026-09-30T00:00:00.123456789Z';

const ticket: TicketDetail = {
  id: 'TEST-00001',
  project_id: 'proj-1',
  title: 'タイトル',
  description: '説明',
  status: 'TODO',
  auto_executable: true,
  blocked: false,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: U0,
  priority: 'MEDIUM',
  labels: [],
  nodes: [],
  edges: [],
  artifacts: []
};

function patchBodies(fetchMock: ReturnType<typeof vi.fn>): Record<string, unknown>[] {
  return (fetchMock.mock.calls as unknown as [string, RequestInit | undefined][])
    .filter(([, init]) => init?.method === 'PATCH')
    .map(([, init]) => JSON.parse(String(init?.body)));
}

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} });
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('TicketItem if_updated_at', () => {
  it('sends the ticket updated_at with a label change', async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ ...ticket, labels: [BUG] }), { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    const user = userEvent.setup();
    render(<TicketItem ticket={ticket} isExpanded onToggleExpand={vi.fn()} onRefresh={vi.fn()} myName="" projectLabels={[BUG]} />);

    await user.click(screen.getByRole('button', { name: `${i18n.t('ticket.labels.edit')}: TEST-00001` }));
    await user.click(screen.getByRole('checkbox', { name: 'バグ' }));

    expect(patchBodies(fetchMock)).toEqual([{ label_ids: ['label-bug'], if_updated_at: U0 }]);
  });

  it('sends no if_updated_at with a priority change', async () => {
    const fetchMock = vi.fn(async () => new Response('{}', { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    const user = userEvent.setup();
    const { container } = render(<TicketItem ticket={ticket} isExpanded={false} onToggleExpand={vi.fn()} onRefresh={vi.fn()} myName="" />);

    await user.selectOptions(container.querySelector('#priority-select-TEST-00001') as HTMLSelectElement, 'LOW');

    expect(patchBodies(fetchMock)).toEqual([{ priority: 'LOW' }]);
  });

  it('sends no if_updated_at with "assign to me"', async () => {
    const fetchMock = vi.fn(async () => new Response('{}', { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    const user = userEvent.setup();
    render(<TicketItem ticket={ticket} isExpanded={false} onToggleExpand={vi.fn()} onRefresh={vi.fn()} myName="me" />);

    await user.click(screen.getByRole('button', { name: new RegExp(i18n.t('ticketItem.selfAssign.assign')) }));

    expect(patchBodies(fetchMock)).toEqual([{ assignee: 'me' }]);
  });

  it('has no title or description editor', () => {
    render(<TicketItem ticket={ticket} isExpanded onToggleExpand={vi.fn()} onRefresh={vi.fn()} myName="" projectLabels={[BUG]} />);
    expect(screen.queryByRole('textbox', { name: /タイトル|title|説明|description/i })).not.toBeInTheDocument();
  });
});
