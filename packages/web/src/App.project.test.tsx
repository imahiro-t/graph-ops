// DFLT-00106: the ticket list is always fetched for the project the header
// is showing, and never fetched before that project is known.
//
// The bug this guards against: GET /api/tickets used to be called with no
// project_id at all and the server filtered by a "current project" shared by
// everyone on the same data source. A teammate (or another tab) switching
// projects therefore replaced this window's list on the next 15s poll while
// the header, the label filter and everything else still showed the old
// project -- a screen describing two different projects at once.
//
// fetch is served by test/fakeBackend.ts, which since DFLT-00106 answers
// /api/tickets purely from the request's own query, so an unscoped request
// here comes back empty exactly as the real server's does.
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from './i18n';
import App from './App';
import { Project } from './types';
import { FakeBackend, FakeBackendSeed, createFakeBackend, installFakeBackend } from './test/fakeBackend';

const alpha: Project = { id: 'p-alpha', name: 'Alpha', prefix: 'ALP', local_path: '/work/alpha', created_at: '', updated_at: '' };
const beta: Project = { id: 'p-beta', name: 'Beta', prefix: 'BETA', local_path: '/work/beta', created_at: '', updated_at: '' };

let backend: FakeBackend;
let fetchMock: ReturnType<typeof vi.fn>;

function seed(overrides: Partial<FakeBackendSeed> = {}) {
  backend = createFakeBackend({
    projects: [alpha, beta],
    currentProjectId: alpha.id,
    labels: [],
    tickets: [
      { id: 'ALP-00001', project_id: alpha.id, title: 'Alpha のチケット', status: 'TODO', priority: 'HIGH', labelIds: [] },
      { id: 'BETA-00001', project_id: beta.id, title: 'Beta のチケット', status: 'TODO', priority: 'MEDIUM', labelIds: [] }
    ],
    ...overrides
  });
  fetchMock = installFakeBackend(backend);
}

// ticketListRequests are every GET that hit the ticket list endpoint, in
// order, as full URLs -- including any unscoped one, which is the thing
// several of these tests assert never happens.
function ticketListRequests(): string[] {
  return fetchMock.mock.calls
    .map(c => String(c[0]))
    .filter(url => url.split('?')[0] === '/api/tickets' && !url.startsWith('/api/tickets/'));
}

describe('App project scoping', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    vi.useRealTimers();
  });

  describe('once the current project has resolved', () => {
    beforeEach(() => seed());

    it('always fetches the list with the current project_id, never unscoped', async () => {
      render(<App />);
      await screen.findByText('ALP-00001');

      expect(ticketListRequests()).toEqual([`/api/tickets?project_id=${alpha.id}`]);
      expect(screen.queryByText('BETA-00001')).not.toBeInTheDocument();
    });

    it('keeps polling the same project every 15 seconds', async () => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      render(<App />);
      await screen.findByText('ALP-00001');

      await vi.advanceTimersByTimeAsync(15_000);
      await waitFor(() => expect(ticketListRequests().length).toBeGreaterThan(1));
      expect(new Set(ticketListRequests())).toEqual(new Set([`/api/tickets?project_id=${alpha.id}`]));
    });

    // The whole point: another environment moving the data source's shared
    // "current project" must not reach this window, not even via the poll.
    it('ignores another environment switching the shared current project', async () => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      render(<App />);
      await screen.findByText('ALP-00001');

      backend.currentProjectId = beta.id;
      await vi.advanceTimersByTimeAsync(15_000);
      await waitFor(() => expect(ticketListRequests().length).toBeGreaterThan(1));

      expect(new Set(ticketListRequests())).toEqual(new Set([`/api/tickets?project_id=${alpha.id}`]));
      expect(screen.getByText('ALP-00001')).toBeInTheDocument();
      expect(screen.queryByText('BETA-00001')).not.toBeInTheDocument();
      expect(screen.getByRole('button', { name: /Alpha/ })).toBeInTheDocument();
    });

    it('re-points the list and the poll at the project the user switches to', async () => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
      render(<App />);
      await screen.findByText('ALP-00001');

      await user.click(screen.getByRole('button', { name: /Alpha/ }));
      await user.click(screen.getByRole('button', { name: /Beta/ }));
      await screen.findByText('BETA-00001');
      expect(ticketListRequests()).toContain(`/api/tickets?project_id=${beta.id}`);

      const before = ticketListRequests().length;
      await vi.advanceTimersByTimeAsync(15_000);
      await waitFor(() => expect(ticketListRequests().length).toBeGreaterThan(before));

      // The interval for the old project was torn down, so nothing after the
      // switch asks for Alpha again.
      expect(ticketListRequests().slice(before)).toEqual([`/api/tickets?project_id=${beta.id}`]);
    });

    it('scopes the manual refresh too', async () => {
      const user = userEvent.setup();
      render(<App />);
      await screen.findByText('ALP-00001');
      const before = ticketListRequests().length;

      await user.click(screen.getByTitle(i18n.t('toolbar.refreshTitle')));
      await waitFor(() => expect(ticketListRequests().length).toBeGreaterThan(before));
      expect(ticketListRequests().slice(before)).toEqual([`/api/tickets?project_id=${alpha.id}`]);
    });
  });

  // The completion criterion names three things that a teammate's switch must
  // not move: the header, the ticket list and the LABEL FILTER'S OPTIONS.
  // The options come from their own request (/api/projects/<id>/labels) driven
  // by the same current project, so they need their own seeded labels and
  // their own assertion -- the tests above run with no labels at all and
  // would pass whether or not the option list followed the shared row.
  describe('the label filter options', () => {
    beforeEach(() =>
      seed({
        labels: [
          { id: 'label-alpha', project_id: alpha.id, name: 'Alpha のラベル', color: 'green' },
          { id: 'label-beta', project_id: beta.id, name: 'Beta のラベル', color: 'red' }
        ]
      })
    );

    it('stay on this environment when another one switches the shared current project', async () => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
      render(<App />);
      await screen.findByText('ALP-00001');
      await waitFor(() =>
        expect(fetchMock.mock.calls.some(c => String(c[0]) === `/api/projects/${alpha.id}/labels`)).toBe(true)
      );

      backend.currentProjectId = beta.id;
      await vi.advanceTimersByTimeAsync(15_000);
      await waitFor(() => expect(ticketListRequests().length).toBeGreaterThan(1));

      // Every poll re-fetches the labels too, so this is the request that
      // would have followed the shared row had the id not come from the
      // header's own project.
      expect(fetchMock.mock.calls.map(c => String(c[0]))).not.toContain(`/api/projects/${beta.id}/labels`);

      await user.click(screen.getByRole('button', { name: /^ラベル: / }));
      const panel = screen.getByRole('group', { name: i18n.t('toolbar.labelGroupLabel') });
      expect(within(panel).getAllByRole('checkbox').map(c => c.getAttribute('aria-label'))).toEqual(['Alpha のラベル']);
    });
  });

  describe('before the current project has resolved', () => {
    it('fetches no ticket list and shows loading rather than the empty state', async () => {
      seed();
      // Hold GET /api/current-project open so the app stays in the
      // unresolved state for the duration of the assertions.
      let releaseCurrentProject!: () => void;
      const held = new Promise<void>(resolve => {
        releaseCurrentProject = resolve;
      });
      const realFetch = backend.fetch.bind(backend);
      backend.fetch = async (input, init) => {
        if (String(input) === '/api/current-project' && (init?.method ?? 'GET') === 'GET') {
          await held;
        }
        return realFetch(input, init);
      };

      render(<App />);

      await screen.findByText(i18n.t('emptyState.loadingTickets'));
      expect(screen.queryByText(i18n.t('projectSwitcher.noProjectYet'))).not.toBeInTheDocument();
      expect(ticketListRequests()).toEqual([]);

      releaseCurrentProject();
      await screen.findByText('ALP-00001');
    });
  });

  describe('with no project selected at all', () => {
    beforeEach(() => seed({ currentProjectId: '' }));

    it('fetches no ticket list and offers to create a project', async () => {
      render(<App />);

      await screen.findByText(i18n.t('projectSwitcher.noProjectYet'));
      expect(screen.queryByText(i18n.t('emptyState.loadingTickets'))).not.toBeInTheDocument();
      expect(ticketListRequests()).toEqual([]);
    });
  });
});
