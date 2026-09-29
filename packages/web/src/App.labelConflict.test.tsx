// DFLT-00351: a label change that conflicts (409 TICKET_CHANGED) reloads the
// list through App's refreshTickets, and whether that reload worked reaches
// LabelSelect: a failed GET /api/tickets must not leave the message saying
// the latest version was loaded. The other refreshTickets callers ignore the
// result, so the toolbar's refresh still fails quietly and keeps the list.
//
// fetch is served by test/fakeBackend.ts, which has no PATCH
// /api/tickets/{id}: the label save is answered here.
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from './i18n';
import App from './App';
import { Project } from './types';
import { FakeBackend, createFakeBackend, installFakeBackend } from './test/fakeBackend';

const alpha: Project = { id: 'p-alpha', name: 'Alpha', prefix: 'ALP', local_path: '/work/alpha', created_at: '', updated_at: '' };
const TICKET = 'ALP-00001';

let backend: FakeBackend;
let fetchMock: ReturnType<typeof installFakeBackend>;
// Set by a test: whether GET /api/tickets fails from now on.
let failList: boolean;
// Set by a test: whether the ticket list starts failing once the label save
// has been refused.
let failListAfterConflict: boolean;
let labelPatches: Record<string, unknown>[];

const isListRequest = (url: string, method: string) => url.split('?')[0] === '/api/tickets' && method === 'GET';

beforeEach(() => {
  backend = createFakeBackend({
    projects: [alpha],
    currentProjectId: alpha.id,
    labels: [
      { id: 'label-bug', project_id: alpha.id, name: 'バグ', color: 'red' },
      { id: 'label-feat', project_id: alpha.id, name: '機能追加', color: 'blue' }
    ],
    tickets: [{ id: TICKET, project_id: alpha.id, title: 'ログイン画面の不具合', status: 'TODO', priority: 'HIGH', labelIds: ['label-bug'] }]
  });
  fetchMock = installFakeBackend(backend);
  failList = false;
  failListAfterConflict = false;
  labelPatches = [];
  fetchMock.mockImplementation((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = init?.method ?? 'GET';
    if (url === `/api/tickets/${TICKET}` && method === 'PATCH') {
      const body = JSON.parse(String(init?.body ?? '{}')) as Record<string, unknown>;
      // Only the label save conflicts; any other edit of the ticket would
      // fall through to the fake backend.
      if ('label_ids' in body) {
        labelPatches.push(body);
        if (failListAfterConflict) failList = true;
        return Promise.resolve(
          new Response(JSON.stringify({ error: { code: 'TICKET_CHANGED', message: 'changed' } }), { status: 409 })
        );
      }
    }
    if (failList && isListRequest(url, method)) {
      return Promise.resolve(new Response(JSON.stringify({ error: { code: 'INTERNAL', message: 'boom' } }), { status: 500 }));
    }
    return backend.fetch(input, init);
  });
  // TicketItem measures its graph panel with a ResizeObserver, which jsdom lacks.
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
  // The failed reload is logged by fetchAllTickets; keep the output quiet.
  vi.spyOn(console, 'error').mockImplementation(() => {});
});

afterEach(async () => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  await i18n.changeLanguage('ja');
});

async function renderApp() {
  const user = userEvent.setup();
  render(<App />);
  await screen.findByText(TICKET);
  return user;
}

// Expands the ticket, opens its label picker and checks 機能追加, which the
// server refuses with 409 TICKET_CHANGED. Resolves once the save and the
// reload that follows it are over.
async function conflictingLabelChange(user: ReturnType<typeof userEvent.setup>) {
  const card = document.getElementById(`ticket-${TICKET}`) as HTMLElement;
  await user.click(within(card).getByTestId('ticket-header-row'));
  const labelsItem = await within(card).findByTestId('ticket-detail-labels');
  await user.click(within(labelsItem).getByRole('button', { name: `${i18n.t('ticket.labels.edit')}: ${TICKET}` }));
  const box = await within(labelsItem).findByRole('checkbox', { name: '機能追加' });
  const listCallsBefore = fetchMock.mock.calls.filter(([input, init]) => isListRequest(String(input), init?.method ?? 'GET')).length;
  await user.click(box);
  const alert = await within(labelsItem).findByRole('alert');
  // The reload has been made and answered once the checkboxes are usable again.
  await waitFor(() => {
    expect(within(labelsItem).getByRole('checkbox', { name: '機能追加' })).toHaveAttribute('aria-disabled', 'false');
    const listCalls = fetchMock.mock.calls.filter(([input, init]) => isListRequest(String(input), init?.method ?? 'GET')).length;
    expect(listCalls).toBeGreaterThan(listCallsBefore);
  });
  // The label save really was the conditioned PATCH the mock refuses, so the
  // 409 branch was taken.
  expect(labelPatches).toHaveLength(1);
  expect(labelPatches[0]).toEqual({ label_ids: ['label-bug', 'label-feat'], if_updated_at: expect.any(String) });
  expect(within(labelsItem).getByRole('checkbox', { name: '機能追加' })).not.toBeChecked();
  return alert;
}

describe('App label conflict reload (DFLT-00351)', () => {
  it('says the latest could not be loaded when the reload after a 409 fails', async () => {
    failListAfterConflict = true;
    const user = await renderApp();
    const alert = await conflictingLabelChange(user);
    await waitFor(() => expect(alert).toHaveTextContent(i18n.t('ticket.labels.conflictReloadFailed')));
    expect(alert).not.toHaveTextContent(i18n.t('errors.TICKET_CHANGED'));
    // The list is kept, as for any failed refresh.
    expect(screen.getByText(TICKET)).toBeInTheDocument();
  });

  it.each(['ja', 'en'])('shows the %s text for a failed reload', async lang => {
    await i18n.changeLanguage(lang);
    failListAfterConflict = true;
    const user = await renderApp();
    const alert = await conflictingLabelChange(user);
    const expected = i18n.getResource(lang, 'translation', 'ticket.labels.conflictReloadFailed') as string;
    expect(expected).toBeTruthy();
    await waitFor(() => expect(alert).toHaveTextContent(expected));
  });

  it('keeps the TICKET_CHANGED text when the reload after a 409 succeeds', async () => {
    const user = await renderApp();
    const alert = await conflictingLabelChange(user);
    expect(alert).toHaveTextContent(i18n.t('errors.TICKET_CHANGED'));
    expect(alert).not.toHaveTextContent(i18n.t('ticket.labels.conflictReloadFailed'));
  });

  it('keeps the list and shows no error when the toolbar refresh fails', async () => {
    const user = await renderApp();
    const refreshButton = screen.getByRole('button', { name: i18n.t('toolbar.refreshTitle') });
    await waitFor(() => expect(refreshButton).toBeEnabled());
    failList = true;
    const listCalls = () => fetchMock.mock.calls.filter(([input, init]) => isListRequest(String(input), init?.method ?? 'GET')).length;
    const before = listCalls();
    await user.click(refreshButton);
    await waitFor(() => {
      expect(listCalls()).toBeGreaterThan(before);
      expect(refreshButton).toBeEnabled();
    });
    expect(screen.getByText(TICKET)).toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });
});
