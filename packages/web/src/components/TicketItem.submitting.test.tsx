// DFLT-00206: the ticket row's close confirmation button is aria-busy and says
// "(submitting)" in its accessible name while the close request is in
// flight; both go away once the request settles (success or failure).
import { act, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { TicketDetail } from '../types';
import { TicketItem } from './TicketItem';
import { submittingName } from '../test/submittingName';

const TICKET_ID = 'TEST-00206';

const makeTicket = (): TicketDetail => ({
  id: TICKET_ID,
  project_id: 'proj-1',
  title: '送信中のテスト',
  description: '説明',
  status: 'TODO',
  auto_executable: true,
  blocked: false,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
  priority: 'MEDIUM',
  labels: [],
  nodes: [],
  edges: [],
  artifacts: []
});

// The close request is held until the test settles it; everything else
// answers at once.
let settleClose: (res: Response) => void = () => {};

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
  vi.stubGlobal(
    'fetch',
    vi.fn((url: RequestInfo | URL) => {
      if (String(url) === `/api/tickets/${TICKET_ID}/close`) {
        return new Promise<Response>(resolve => {
          settleClose = resolve;
        });
      }
      return Promise.resolve(new Response('{}', { status: 200 }));
    })
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

const confirmLabel = () => i18n.t('ticketItem.close.confirm');

async function startClosing(onRefresh: () => Promise<void>) {
  const user = userEvent.setup();
  const { container } = render(
    <TicketItem
      ticket={makeTicket()}
      isExpanded={false}
      onToggleExpand={vi.fn()}
      onRefresh={onRefresh}
      myName=""
      projectLabels={[]}
    />
  );
  await user.click(screen.getByRole('button', { name: i18n.t('ticketItem.close.button') }));
  const confirm = screen.getByRole('button', { name: confirmLabel() });
  expect(confirm).not.toHaveAttribute('aria-busy');
  await user.click(confirm);
  return { container, confirm };
}

describe('TicketItem close confirmation while submitting', () => {
  it('is busy with "(submitting)" in its name while the close request is pending', async () => {
    const { confirm } = await startClosing(vi.fn(async () => {}));

    expect(confirm).toBeDisabled();
    expect(confirm).toHaveAttribute('aria-busy', 'true');
    expect(confirm).toHaveAccessibleName(submittingName(confirmLabel()));
  });

  it('drops aria-busy and "(submitting)" when the request fails, leaving the prompt open', async () => {
    const { confirm } = await startClosing(vi.fn(async () => {}));

    await act(async () => {
      settleClose(new Response(JSON.stringify({ code: 'UNKNOWN', message: 'boom' }), { status: 500 }));
    });

    expect(screen.getByRole('button', { name: confirmLabel() })).toBe(confirm);
    expect(confirm).toBeEnabled();
    expect(confirm).not.toHaveAttribute('aria-busy');
    expect(confirm).toHaveAccessibleName(confirmLabel());
  });

  it('closes the prompt on success and leaves nothing busy once the refresh finishes', async () => {
    let finishRefresh: () => void = () => {};
    const onRefresh = vi.fn(
      () =>
        new Promise<void>(resolve => {
          finishRefresh = resolve;
        })
    );
    const { container, confirm } = await startClosing(onRefresh);

    await act(async () => {
      settleClose(new Response('{}', { status: 200 }));
    });
    // The prompt closes before the list refresh settles.
    expect(confirm).not.toBeInTheDocument();
    expect(onRefresh).toHaveBeenCalledTimes(1);

    await act(async () => {
      finishRefresh();
    });
    expect(container.querySelector('[aria-busy="true"]')).toBeNull();
  });
});
