// DFLT-00191: once a confirmed ticket delete has removed the card (its
// focused delete button included), focus moves to the ticket that took its
// place in the list, else the one before it, else the header's "new ticket"
// button -- never <body>. App owns this (TicketItem's onDeleted), since only
// the list knows the neighbors and the card itself is gone.
//
// fetch is served by test/fakeBackend.ts, so the DELETE really removes the
// ticket and the re-fetch that follows no longer returns it.
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from './i18n';
import App from './App';
import { Project } from './types';
import { FakeBackend, FakeTicket, createFakeBackend, installFakeBackend } from './test/fakeBackend';

const alpha: Project = { id: 'p-alpha', name: 'Alpha', prefix: 'ALP', local_path: '/work/alpha', created_at: '', updated_at: '' };

const ticket = (n: number): FakeTicket => ({
  id: `ALP-0000${n}`,
  project_id: alpha.id,
  title: `チケット ${n}`,
  status: 'TODO',
  priority: 'MEDIUM',
  labelIds: []
});

let backend: FakeBackend;

function seed(count: number, paginationPageSize?: number) {
  backend = createFakeBackend({
    projects: [alpha],
    currentProjectId: alpha.id,
    labels: [],
    tickets: Array.from({ length: count }, (_, i) => ticket(i + 1)),
    paginationPageSize
  });
  return installFakeBackend(backend);
}

const deleteButtonOf = (id: string) => document.querySelector<HTMLElement>(`[data-focus-key="ticket-delete-${id}"]`);
const newTicketButton = () => screen.getByRole('button', { name: i18n.t('header.newTicket') });

async function renderApp(firstId = 'ALP-00001') {
  const user = userEvent.setup();
  render(<App />);
  await screen.findByText(firstId);
  return user;
}

async function deleteTicket(user: ReturnType<typeof userEvent.setup>, id: string) {
  await user.click(deleteButtonOf(id)!);
  await user.click(screen.getByTestId('ticket-delete-confirm-confirm'));
  await waitFor(() => expect(screen.queryByText(id)).not.toBeInTheDocument());
}

describe('App focus after deleting a ticket', () => {
  beforeEach(() => {
    vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it('moves focus to the next ticket\'s delete button', async () => {
    seed(3);
    const user = await renderApp();

    await deleteTicket(user, 'ALP-00002');

    await waitFor(() => expect(deleteButtonOf('ALP-00003')).toHaveFocus());
    expect(document.activeElement).not.toBe(document.body);
  });

  it('moves focus to the previous ticket\'s delete button when the last one is deleted', async () => {
    seed(3);
    const user = await renderApp();

    await deleteTicket(user, 'ALP-00003');

    await waitFor(() => expect(deleteButtonOf('ALP-00002')).toHaveFocus());
    expect(document.activeElement).not.toBe(document.body);
  });

  it('moves focus to the "new ticket" button when no ticket is left', async () => {
    seed(1);
    const user = await renderApp();

    await deleteTicket(user, 'ALP-00001');

    await waitFor(() => expect(newTicketButton()).toHaveFocus());
    expect(document.activeElement).not.toBe(document.body);
  });

  it('moves focus to the previous page\'s last ticket when the last page empties', async () => {
    seed(3, 2);
    const user = await renderApp();
    await user.click(screen.getByRole('button', { name: i18n.t('pagination.next') }));
    await screen.findByText('ALP-00003');

    await deleteTicket(user, 'ALP-00003');

    // The page clamps back to page 1, where the ticket before it is shown.
    await waitFor(() => expect(deleteButtonOf('ALP-00002')).toHaveFocus());
    expect(document.activeElement).not.toBe(document.body);
  });

  // DFLT-00191 (QA round 1): the re-fetch that follows a successful delete
  // does not always drop the card. It can fail, or settle with the card
  // still on screen (its result discarded as superseded by a newer fetch
  // leaves the list as it was); the card then goes with a later fetch, such
  // as the next poll. Focus must not end up on <body> at either point: the
  // card holds it on its own delete button meanwhile, and the move to a
  // neighbor stays pending until the card is actually gone.
  describe('when the re-fetch after the delete leaves the card on screen', () => {
    let visibility: DocumentVisibilityState;

    beforeEach(() => {
      visibility = 'visible';
      // Read-only in jsdom; shadowed so the poll that follows can be run by a
      // visibilitychange event (App's pollOnce), removed again in afterEach.
      Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => visibility });
    });

    afterEach(() => {
      Reflect.deleteProperty(document, 'visibilityState');
    });

    const isListFetch = (input: RequestInfo | URL, init?: RequestInit) =>
      String(input).startsWith('/api/tickets?') && (init?.method ?? 'GET') === 'GET';

    // Answers requests from the fake backend, except that the DELETE drops
    // focus (what a browser may do while the delete button is disabled;
    // jsdom keeps it) and the list fetch right after it is answered by
    // afterDelete instead.
    function interceptDelete(
      fetchMock: ReturnType<typeof installFakeBackend>,
      afterDelete: (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>
    ) {
      let interceptNextList = false;
      fetchMock.mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
        if (init?.method === 'DELETE') {
          (document.activeElement as HTMLElement | null)?.blur();
          interceptNextList = true;
          return backend.fetch(input, init);
        }
        if (interceptNextList && isListFetch(input, init)) {
          interceptNextList = false;
          return afterDelete(input, init);
        }
        return backend.fetch(input, init);
      });
    }

    async function confirmDeleteOf(user: ReturnType<typeof userEvent.setup>, id: string) {
      await user.click(deleteButtonOf(id)!);
      await user.click(screen.getByTestId('ticket-delete-confirm-confirm'));
    }

    // The next poll: App polls on becoming visible again.
    async function poll() {
      await act(async () => {
        document.dispatchEvent(new Event('visibilitychange'));
      });
    }

    it('keeps focus on the card after a failed re-fetch, then moves it to the next ticket when a later poll drops the card', async () => {
      const fetchMock = seed(3);
      vi.spyOn(console, 'error').mockImplementation(() => {});
      const user = await renderApp();
      interceptDelete(fetchMock, async () => new Response('{}', { status: 500 }));

      await confirmDeleteOf(user, 'ALP-00002');

      // The re-fetch failed: the card is still listed, focus is on its delete
      // button (enabled again), not on <body>.
      await waitFor(() => expect(deleteButtonOf('ALP-00002')).toHaveFocus());
      expect(deleteButtonOf('ALP-00002')).toBeEnabled();

      await poll();

      await waitFor(() => expect(screen.queryByText('ALP-00002')).not.toBeInTheDocument());
      await waitFor(() => expect(deleteButtonOf('ALP-00003')).toHaveFocus());
      expect(document.activeElement).not.toBe(document.body);
    });

    it('keeps focus on the card while the re-fetch still lists it, then moves it on when a later poll drops the card', async () => {
      const fetchMock = seed(3);
      const user = await renderApp();
      // The list as it was before the delete: what is left on screen when
      // the re-fetch's result is discarded, or the server's list lags.
      const stale = backend.tickets.slice();
      interceptDelete(fetchMock, async (input, init) => {
        const current = backend.tickets;
        backend.tickets = stale;
        try {
          return await backend.fetch(input, init);
        } finally {
          backend.tickets = current;
        }
      });

      await confirmDeleteOf(user, 'ALP-00003');

      await waitFor(() => expect(deleteButtonOf('ALP-00003')).toHaveFocus());
      expect(deleteButtonOf('ALP-00003')).toBeEnabled();

      await poll();

      await waitFor(() => expect(screen.queryByText('ALP-00003')).not.toBeInTheDocument());
      await waitFor(() => expect(deleteButtonOf('ALP-00002')).toHaveFocus());
      expect(document.activeElement).not.toBe(document.body);
    });

    it('does not take focus from where the user moved it before the card went', async () => {
      const fetchMock = seed(3);
      vi.spyOn(console, 'error').mockImplementation(() => {});
      const user = await renderApp();
      interceptDelete(fetchMock, async () => new Response('{}', { status: 500 }));

      await confirmDeleteOf(user, 'ALP-00002');
      await waitFor(() => expect(deleteButtonOf('ALP-00002')).toHaveFocus());
      deleteButtonOf('ALP-00001')!.focus();

      await poll();

      await waitFor(() => expect(screen.queryByText('ALP-00002')).not.toBeInTheDocument());
      expect(deleteButtonOf('ALP-00001')).toHaveFocus();
    });
  });

  it('moves focus to the ticket pulled in from the next page when the last card of a page is deleted', async () => {
    seed(3, 2);
    const user = await renderApp();

    await deleteTicket(user, 'ALP-00002');

    await waitFor(() => expect(deleteButtonOf('ALP-00003')).toHaveFocus());
    expect(document.activeElement).not.toBe(document.body);
  });
});
