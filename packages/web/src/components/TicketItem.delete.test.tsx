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

const renderItem = (onDeleted?: (ticketId: string) => void | Promise<void>) => {
  const onToggleExpand = vi.fn();
  const onRefresh = vi.fn();
  const confirmSpy = vi.spyOn(window, 'confirm');
  render(
    <TicketItem
      ticket={makeTicket()}
      isExpanded={false}
      onToggleExpand={onToggleExpand}
      onRefresh={onRefresh}
      onDeleted={onDeleted}
      myName=""
      projectLabels={[]}
    />
  );
  return { onToggleExpand, onRefresh, confirmSpy };
};

const deleteButton = () => screen.getByRole('button', { name: i18n.t('ticketItem.delete.button') });

async function openConfirm(onDeleted?: (ticketId: string) => void | Promise<void>) {
  const user = userEvent.setup();
  const utils = renderItem(onDeleted);
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

  // DFLT-00191: the list (App) decides where focus goes once the card is
  // gone, so a successful delete hands over to onDeleted when it is given.
  it('calls onDeleted with the ticket id instead of onRefresh when it is given', async () => {
    const onDeleted = vi.fn();
    const { user, onRefresh } = await openConfirm(onDeleted);

    await user.click(screen.getByTestId('ticket-delete-confirm-confirm'));
    await waitFor(() => expect(onDeleted).toHaveBeenCalledWith(TICKET_ID));
    expect(onRefresh).not.toHaveBeenCalled();
    expect(deleteCalls()).toHaveLength(1);
  });

  // DFLT-00191: when the delete succeeded but the re-fetch still shows the
  // card (the re-fetch failed), there is no neighbor for App to move to, so
  // the card puts focus back on its own (re-enabled) delete button.
  it('puts focus back on the delete button when the card is still there after a successful delete', async () => {
    const onDeleted = vi.fn(async () => {});
    const { user } = await openConfirm(onDeleted);
    fetchMock.mockImplementation(async () => {
      // What a browser may do while the button is disabled (jsdom keeps it).
      (document.activeElement as HTMLElement | null)?.blur();
      return new Response('{}', { status: 200 });
    });

    await user.click(screen.getByTestId('ticket-delete-confirm-confirm'));

    await waitFor(() => expect(onDeleted).toHaveBeenCalledWith(TICKET_ID));
    await waitFor(() => expect(deleteButton()).toHaveFocus());
    expect(deleteButton()).toBeEnabled();
    expect(document.activeElement).not.toBe(document.body);
  });

  // A neighbor App has already focused is not pulled back.
  it('leaves focus on another element the list moved it to after a successful delete', async () => {
    const other = document.createElement('button');
    document.body.appendChild(other);
    try {
      const onDeleted = vi.fn(async () => {
        other.focus();
      });
      const { user } = await openConfirm(onDeleted);

      await user.click(screen.getByTestId('ticket-delete-confirm-confirm'));

      await waitFor(() => expect(onDeleted).toHaveBeenCalledWith(TICKET_ID));
      await waitFor(() => expect(deleteButton()).toBeEnabled());
      expect(other).toHaveFocus();
    } finally {
      other.remove();
    }
  });

  // DFLT-00191: the button is disabled while the request runs, and a browser
  // may drop focus from it then; a failed delete puts focus back on it.
  it('puts focus back on the delete button when the delete request fails', async () => {
    const onDeleted = vi.fn();
    const { user, onRefresh } = await openConfirm(onDeleted);
    fetchMock.mockImplementation(async () => {
      // What such a browser does (jsdom keeps focus on a disabled button).
      (document.activeElement as HTMLElement | null)?.blur();
      return new Response(JSON.stringify({ error: { code: 'INTERNAL', message: 'boom' } }), { status: 500 });
    });

    await user.click(screen.getByTestId('ticket-delete-confirm-confirm'));

    expect(await screen.findByText(i18n.t('errors.UNKNOWN'))).toBeInTheDocument();
    await waitFor(() => expect(deleteButton()).toHaveFocus());
    expect(deleteButton()).toBeEnabled();
    expect(document.activeElement).not.toBe(document.body);
    expect(onDeleted).not.toHaveBeenCalled();
    expect(onRefresh).not.toHaveBeenCalled();
  });
});
