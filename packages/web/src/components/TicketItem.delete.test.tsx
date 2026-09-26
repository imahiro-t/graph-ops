// DFLT-00148: ticket deletion is confirmed through the in-app ConfirmDialog,
// not window.confirm. Confirming sends DELETE and refreshes the list;
// cancelling (button, Escape, overlay) sends nothing and does not toggle the
// card.
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { TicketDetail } from '../types';
import { TicketItem } from './TicketItem';

const TICKET_ID = 'TEST-00148';

const makeTicket = (): TicketDetail => ({
  id: TICKET_ID,
  project_id: 'proj-1',
  title: '削除のテスト',
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

let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
  fetchMock = vi.fn(async () => new Response('{}', { status: 200 }));
  vi.stubGlobal('fetch', fetchMock);
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

const deleteCalls = () =>
  fetchMock.mock.calls.filter(
    ([url, init]) => String(url) === `/api/tickets/${TICKET_ID}` && (init as RequestInit | undefined)?.method === 'DELETE'
  );

const renderItem = () => {
  const onToggleExpand = vi.fn();
  const onRefresh = vi.fn();
  const confirmSpy = vi.spyOn(window, 'confirm');
  render(
    <TicketItem
      ticket={makeTicket()}
      isExpanded={false}
      onToggleExpand={onToggleExpand}
      onRefresh={onRefresh}
      myName=""
      projectLabels={[]}
    />
  );
  return { onToggleExpand, onRefresh, confirmSpy };
};

const deleteButton = () => screen.getByRole('button', { name: i18n.t('ticketItem.delete.button') });

async function openConfirm() {
  const user = userEvent.setup();
  const utils = renderItem();
  await user.click(deleteButton());
  return { user, ...utils };
}

describe('TicketItem ticket deletion', () => {
  it('opens a danger ConfirmDialog with the ticket in its message, focused on cancel', async () => {
    const { confirmSpy, onToggleExpand } = await openConfirm();

    const dialog = screen.getByRole('alertdialog', { name: i18n.t('ticketItem.delete.confirmTitle') });
    expect(dialog).toHaveAttribute('data-testid', 'ticket-delete-confirm');
    expect(dialog).toHaveAccessibleDescription(
      i18n.t('ticketItem.delete.confirm', { id: TICKET_ID, title: '削除のテスト' })
    );
    expect(screen.getByTestId('ticket-delete-confirm-confirm')).toHaveTextContent(i18n.t('ticketItem.delete.confirmButton'));
    expect(screen.getByTestId('ticket-delete-confirm-cancel')).toHaveFocus();
    expect(confirmSpy).not.toHaveBeenCalled();
    expect(deleteCalls()).toHaveLength(0);
    expect(onToggleExpand).not.toHaveBeenCalled();
  });

  it('deletes the ticket and refreshes on confirm', async () => {
    const { user, onRefresh, onToggleExpand } = await openConfirm();

    await user.click(screen.getByTestId('ticket-delete-confirm-confirm'));
    await waitFor(() => expect(onRefresh).toHaveBeenCalledTimes(1));
    expect(deleteCalls()).toHaveLength(1);
    expect(screen.queryByTestId('ticket-delete-confirm')).not.toBeInTheDocument();
    expect(onToggleExpand).not.toHaveBeenCalled();
  });

  it.each([
    ['the cancel button', async (user: ReturnType<typeof userEvent.setup>) => user.click(screen.getByTestId('ticket-delete-confirm-cancel'))],
    ['Escape', async (user: ReturnType<typeof userEvent.setup>) => user.keyboard('{Escape}')],
    ['a click on the overlay', async (user: ReturnType<typeof userEvent.setup>) => user.click(screen.getByTestId('ticket-delete-confirm-overlay'))]
  ])('does nothing on %s and returns focus to the delete button', async (_label, dismiss) => {
    const { user, onRefresh, onToggleExpand } = await openConfirm();

    await dismiss(user);
    expect(screen.queryByTestId('ticket-delete-confirm')).not.toBeInTheDocument();
    expect(deleteCalls()).toHaveLength(0);
    expect(onRefresh).not.toHaveBeenCalled();
    expect(onToggleExpand).not.toHaveBeenCalled();
    expect(deleteButton()).toHaveFocus();
  });
});
