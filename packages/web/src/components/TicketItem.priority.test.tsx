// DFLT-00083: choosing a level in the ticket header's priority selector
// sends PATCH /api/tickets/{id} with that level -- never null -- and the
// row doesn't toggle open/closed as a side effect.
import { render } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { TicketDetail, TicketPriority } from '../types';
import { TicketItem } from './TicketItem';

const makeTicket = (priority: TicketPriority): TicketDetail => ({
  id: 'TEST-00001',
  project_id: 'proj-1',
  title: 'タイトル',
  description: '説明',
  status: 'TODO',
  auto_executable: true,
  blocked: false,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
  priority,
  nodes: [],
  edges: [],
  artifacts: []
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('TicketItem priority selector', () => {
  it('PATCHes the chosen level as {"priority": "LOW"} and refreshes', async () => {
    const fetchMock = vi.fn(async () => new Response('{}', { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    const onRefresh = vi.fn();
    const onToggleExpand = vi.fn();
    const user = userEvent.setup();

    const { container } = render(
      <TicketItem
        ticket={makeTicket('HIGH')}
        isExpanded={false}
        onToggleExpand={onToggleExpand}
        onRefresh={onRefresh}
        myName=""
      />
    );
    const select = container.querySelector('#priority-select-TEST-00001') as HTMLSelectElement;
    expect(select.value).toBe('HIGH');

    await user.selectOptions(select, 'LOW');

    const patchCalls = fetchMock.mock.calls.filter(
      call => (call as unknown[])[1] && ((call as unknown[])[1] as RequestInit).method === 'PATCH'
    ) as unknown as [string, RequestInit][];
    expect(patchCalls).toHaveLength(1);
    const [url, init] = patchCalls[0];
    expect(url).toBe('/api/tickets/TEST-00001');
    const body = JSON.parse(init.body as string);
    expect(body).toEqual({ priority: 'LOW' });
    expect(body.priority).not.toBeNull();
    expect(onRefresh).toHaveBeenCalled();
    expect(onToggleExpand).not.toHaveBeenCalled();
  });

  it('does not PATCH when the current level is re-selected', async () => {
    const fetchMock = vi.fn(async () => new Response('{}', { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    const user = userEvent.setup();

    const { container } = render(
      <TicketItem ticket={makeTicket('MEDIUM')} isExpanded={false} onToggleExpand={vi.fn()} onRefresh={vi.fn()} myName="" />
    );
    await user.selectOptions(container.querySelector('select') as HTMLSelectElement, 'MEDIUM');

    expect(fetchMock).not.toHaveBeenCalled();
  });
});
