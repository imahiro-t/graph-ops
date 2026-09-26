// DFLT-00191: once a confirmed ticket delete has removed the card (its
// focused delete button included), focus moves to the ticket that took its
// place in the list, else the one before it, else the header's "new ticket"
// button -- never <body>. App owns this (TicketItem's onDeleted), since only
// the list knows the neighbors and the card itself is gone.
//
// fetch is served by test/fakeBackend.ts, so the DELETE really removes the
// ticket and the re-fetch that follows no longer returns it.
import { render, screen, waitFor } from '@testing-library/react';
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
  installFakeBackend(backend);
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

  it('moves focus to the ticket pulled in from the next page when the last card of a page is deleted', async () => {
    seed(3, 2);
    const user = await renderApp();

    await deleteTicket(user, 'ALP-00002');

    await waitFor(() => expect(deleteButtonOf('ALP-00003')).toHaveFocus());
    expect(document.activeElement).not.toBe(document.body);
  });
});
