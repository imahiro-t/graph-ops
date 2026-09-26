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
//
// DFLT-00162: the list's empty and loading states, and the project
// switcher's "no projects" line, are drawn in text-slate-500 /
// dark:text-slate-400 (4.76:1 on white, 6.96:1 on slate-900), meeting WCAG
// 1.4.3's 4.5:1. jsdom computes no colors, so those tests pin the classes.
//
// DFLT-00164: the load-failure retry button used to inherit that
// text-slate-500, which drops to 4.34:1 on its slate-100 hover background,
// so it sets text-slate-600 / dark:text-slate-300 of its own.
//
// DFLT-00167: the same retry button draws its own focus-visible ring
// (blue-500 / dark:blue-400, as the TicketItem chevrons do) instead of the
// browser's default outline, and no ring on a plain (mouse) focus. jsdom
// draws no focus rings, so that test pins the classes too.
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
      expect(screen.getByRole('button', { name: /^Alpha/ })).toBeInTheDocument();
    });

    it('re-points the list and the poll at the project the user switches to', async () => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
      render(<App />);
      await screen.findByText('ALP-00001');

      await user.click(screen.getByRole('button', { name: /^Alpha/ }));
      await user.click(screen.getByRole('button', { name: /^Beta/ }));
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

      await user.click(screen.getByRole('button', { name: i18n.t('toolbar.refreshTitle') }));
      await waitFor(() => expect(ticketListRequests().length).toBeGreaterThan(before));
      expect(ticketListRequests().slice(before)).toEqual([`/api/tickets?project_id=${alpha.id}`]);
    });

    // The refetch that follows ticket creation goes through the same
    // refreshTickets callback, so it is scoped like every other one -- it is
    // asserted here rather than assumed because it is the one refetch that
    // is triggered from outside the component (useClaudeLaunch's onDone).
    it('scopes the refetch that follows ticket creation', async () => {
      const user = userEvent.setup();
      render(<App />);
      await screen.findByText('ALP-00001');
      const before = ticketListRequests().length;

      await user.click(screen.getByRole('button', { name: i18n.t('header.newTicket') }));
      await user.type(screen.getByLabelText(i18n.t('createModal.requestLabel')), 'ログイン画面を直したい');
      await user.click(screen.getByRole('button', { name: i18n.t('createModal.submit') }));

      await waitFor(() => expect(ticketListRequests().length).toBeGreaterThan(before));
      expect(ticketListRequests().slice(before)).toEqual([`/api/tickets?project_id=${alpha.id}`]);
    });
  });

  // Scoping every request to the header's project is not by itself enough to
  // keep the header and the list in step: a request issued for the previous
  // project is still in flight when the user switches, and it still comes
  // back. Before DFLT-00106's fix, that late response repainted the list --
  // header Beta, list Alpha -- and the screen stayed that way until the next
  // 15s poll, which is the very symptom this ticket set out to remove.
  describe('a response that arrives after the user has switched away', () => {
    beforeEach(() => seed());

    it('never replaces the list of the project the header now shows', async () => {
      const user = userEvent.setup();

      // Hold Alpha's list response open, so it can be released after Beta's
      // has already been rendered.
      let releaseAlpha!: () => void;
      const held = new Promise<void>(resolve => {
        releaseAlpha = resolve;
      });
      const realFetch = backend.fetch.bind(backend);
      let heldOnce = false;
      backend.fetch = async (input, init) => {
        if (String(input) === `/api/tickets?project_id=${alpha.id}` && !heldOnce) {
          heldOnce = true;
          await held;
        }
        return realFetch(input, init);
      };

      render(<App />);
      await screen.findByRole('button', { name: /^Alpha/ });
      expect(screen.queryByText('ALP-00001')).not.toBeInTheDocument();

      await user.click(screen.getByRole('button', { name: /^Alpha/ }));
      await user.click(screen.getByRole('button', { name: /^Beta/ }));
      await screen.findByText('BETA-00001');

      releaseAlpha();
      await new Promise(r => setTimeout(r, 50));

      expect(screen.getByRole('button', { name: /^Beta/ })).toBeInTheDocument();
      expect(screen.getByText('BETA-00001')).toBeInTheDocument();
      expect(screen.queryByText('ALP-00001')).not.toBeInTheDocument();
    });

    // The same guard covers the spinner: a stale run finishing must not
    // report the current run's fetch as done.
    it('does not clear the loading state the current project put up', async () => {
      const user = userEvent.setup();

      let releaseAlpha!: () => void;
      const heldAlpha = new Promise<void>(resolve => {
        releaseAlpha = resolve;
      });
      let releaseBeta!: () => void;
      const heldBeta = new Promise<void>(resolve => {
        releaseBeta = resolve;
      });
      const realFetch = backend.fetch.bind(backend);
      backend.fetch = async (input, init) => {
        if (String(input) === `/api/tickets?project_id=${alpha.id}`) await heldAlpha;
        if (String(input) === `/api/tickets?project_id=${beta.id}`) await heldBeta;
        return realFetch(input, init);
      };

      render(<App />);
      await screen.findByRole('button', { name: /^Alpha/ });
      await user.click(screen.getByRole('button', { name: /^Alpha/ }));
      await user.click(screen.getByRole('button', { name: /^Beta/ }));

      // Alpha answers while Beta is still loading.
      releaseAlpha();
      await new Promise(r => setTimeout(r, 50));
      expect(screen.getByRole('button', { name: i18n.t('toolbar.refreshTitle') })).toBeDisabled();

      releaseBeta();
      await screen.findByText('BETA-00001');
      await waitFor(() => expect(screen.getByRole('button', { name: i18n.t('toolbar.refreshTitle') })).toBeEnabled());
    });

    // The other way the header can move while a fetch is on its way: the
    // current project is deleted from the settings modal. refreshProjects
    // then leaves no current project, the effect calls fetchAllTickets('')
    // -- which fetches nothing -- and Alpha's run, arriving afterwards, is
    // stale and so skips setLoading(false) by design. Unless the empty-id
    // path clears the spinner itself, nothing ever does, and the refresh
    // button stays spinning and disabled for good.
    it('does not leave the spinner stuck when the current project is deleted mid-fetch', async () => {
      const user = userEvent.setup();

      let releaseAlpha!: () => void;
      const heldAlpha = new Promise<void>(resolve => {
        releaseAlpha = resolve;
      });
      const realFetch = backend.fetch.bind(backend);
      backend.fetch = async (input, init) => {
        const url = String(input);
        if (url === `/api/tickets?project_id=${alpha.id}`) await heldAlpha;
        // DeleteProject also clears the current project server-side.
        const m = url.match(/^\/api\/projects\/([^/]+)$/);
        if (m && init?.method === 'DELETE') {
          backend.projects = backend.projects.filter(p => p.id !== m[1]);
          if (backend.currentProjectId === m[1]) backend.currentProjectId = '';
          return new Response(JSON.stringify({ success: true }), { status: 200 });
        }
        return realFetch(input, init);
      };

      render(<App />);
      await screen.findByRole('button', { name: /^Alpha/ });
      const refresh = screen.getByRole('button', { name: i18n.t('toolbar.refreshTitle') });
      await waitFor(() => expect(refresh).toBeDisabled());

      await user.click(screen.getByRole('button', { name: i18n.t('header.settings') }));
      await user.click(screen.getByRole('button', { name: i18n.t('settings.tabs.appSettings') }));
      const deleteAlpha = await screen.findByRole('button', { name: i18n.t('settings.appSettings.projects.deleteAriaLabel', { name: 'Alpha' }) });
      await user.click(deleteAlpha);
      // The in-app confirmation (DFLT-00148).
      await user.click(screen.getByTestId('project-delete-confirm-confirm'));
      await screen.findByText(i18n.t('projectSwitcher.noProjectYet'));

      // Alpha's list finally answers, for a project that no longer exists.
      releaseAlpha();
      await new Promise(r => setTimeout(r, 50));

      expect(screen.getByRole('button', { name: i18n.t('toolbar.refreshTitle') })).toBeEnabled();
      expect(screen.queryByText('ALP-00001')).not.toBeInTheDocument();
    });
  });

  // The window the tests above do not look at: after the header has moved
  // to Beta but before Beta's list has arrived. The previous project's list
  // used to stay on screen for that whole time -- one list request plus one
  // detail request per ticket -- under a header naming the new project.
  describe('while the project the user switched to is still loading', () => {
    it('shows loading, not the previous project\'s list, under the new header', async () => {
      seed();
      const user = userEvent.setup();
      let releaseBeta!: () => void;
      const heldBeta = new Promise<void>(resolve => {
        releaseBeta = resolve;
      });
      const realFetch = backend.fetch.bind(backend);
      backend.fetch = async (input, init) => {
        if (String(input) === `/api/tickets?project_id=${beta.id}`) await heldBeta;
        return realFetch(input, init);
      };

      render(<App />);
      await screen.findByText('ALP-00001');
      await user.click(screen.getByRole('button', { name: /^Alpha/ }));
      await user.click(screen.getByRole('button', { name: /^Beta/ }));
      await screen.findByRole('button', { name: /^Beta/ });
      await new Promise(r => setTimeout(r, 50));

      expect(screen.queryByText('ALP-00001')).not.toBeInTheDocument();
      expect(screen.getByText(i18n.t('emptyState.loadingTickets'))).toBeInTheDocument();
      expect(screen.queryByText(i18n.t('emptyState.noTicketsMatch'))).not.toBeInTheDocument();

      releaseBeta();
      await screen.findByText('BETA-00001');
      expect(screen.queryByText(i18n.t('emptyState.loadingTickets'))).not.toBeInTheDocument();
    });

    // The list body is not the only thing drawn from the ticket list: the
    // summary counts and the assignee filter's options are rendered outside
    // the body's "loading" branch, so they depend on the tag check alone.
    // The two projects are seeded with different ticket counts and
    // different assignees so that a leak of Alpha's list would show up as
    // a wrong number and a wrong option, not as the same value by accident.
    it('does not show the previous project\'s summary counts or assignees under the new header', async () => {
      seed({
        tickets: [
          { id: 'ALP-00001', project_id: alpha.id, title: 'Alpha 1', status: 'IN PROGRESS', priority: 'HIGH', assignee: 'alice', labelIds: [] },
          { id: 'ALP-00002', project_id: alpha.id, title: 'Alpha 2', status: 'IN PROGRESS', priority: 'HIGH', assignee: 'alice', labelIds: [] },
          { id: 'ALP-00003', project_id: alpha.id, title: 'Alpha 3', status: 'TODO', priority: 'LOW', assignee: 'alice', labelIds: [] },
          { id: 'BETA-00001', project_id: beta.id, title: 'Beta 1', status: 'TODO', priority: 'MEDIUM', assignee: 'bob', labelIds: [] }
        ]
      });
      const user = userEvent.setup();
      let releaseBeta!: () => void;
      const heldBeta = new Promise<void>(resolve => {
        releaseBeta = resolve;
      });
      const realFetch = backend.fetch.bind(backend);
      backend.fetch = async (input, init) => {
        if (String(input) === `/api/tickets?project_id=${beta.id}`) await heldBeta;
        return realFetch(input, init);
      };
      // The status words ("進行中" etc.) also appear on ticket badges, so
      // look them up inside the summary card only.
      const summaryValue = (key: string) => {
        const card = screen.getByText(i18n.t('summary.title')).parentElement!.parentElement!;
        return within(card).getByText(i18n.t(key)).previousElementSibling?.textContent;
      };
      const assigneeButton = () => screen.getByRole('button', { name: /^担当者: / });
      const assigneeOptions = () =>
        within(screen.getByRole('group', { name: i18n.t('toolbar.assigneeGroupLabel') }))
          .queryAllByRole('checkbox')
          .map(c => c.closest('label')?.textContent?.trim());

      render(<App />);
      await screen.findByText('ALP-00001');
      expect(summaryValue('summary.total')).toBe('3');
      expect(summaryValue('summary.inProgress')).toBe('2');
      await user.click(assigneeButton());
      expect(assigneeOptions()).toContain('alice');
      await user.click(assigneeButton());

      await user.click(screen.getByRole('button', { name: /^Alpha/ }));
      await user.click(screen.getByRole('button', { name: /^Beta/ }));
      await screen.findByRole('button', { name: /^Beta/ });
      await new Promise(r => setTimeout(r, 50));

      // Beta's list is still on its way: nothing of Alpha's may be counted.
      expect(screen.getByText(i18n.t('emptyState.loadingTickets'))).toBeInTheDocument();
      expect(summaryValue('summary.total')).toBe('0');
      expect(summaryValue('summary.inProgress')).toBe('0');
      await user.click(assigneeButton());
      expect(assigneeOptions()).not.toContain('alice');
      await user.click(assigneeButton());

      releaseBeta();
      await screen.findByText('BETA-00001');
      expect(summaryValue('summary.total')).toBe('1');
      await user.click(assigneeButton());
      expect(assigneeOptions()).toContain('bob');
      expect(assigneeOptions()).not.toContain('alice');
    });

    // The label filter's options are the third thing the completion
    // criterion names, and they have the same window: Alpha's labels must
    // not be offered under Beta's header while Beta's are on their way.
    it('does not offer the previous project\'s labels under the new header', async () => {
      seed({
        labels: [
          { id: 'label-alpha', project_id: alpha.id, name: 'Alpha のラベル', color: 'green' },
          { id: 'label-beta', project_id: beta.id, name: 'Beta のラベル', color: 'red' }
        ]
      });
      const user = userEvent.setup();
      let releaseBetaLabels!: () => void;
      const heldBetaLabels = new Promise<void>(resolve => {
        releaseBetaLabels = resolve;
      });
      const realFetch = backend.fetch.bind(backend);
      backend.fetch = async (input, init) => {
        if (String(input) === `/api/projects/${beta.id}/labels`) await heldBetaLabels;
        return realFetch(input, init);
      };

      render(<App />);
      await screen.findByText('ALP-00001');
      const labelButton = () => screen.getByRole('button', { name: /^ラベル: / });
      await user.click(labelButton());
      await waitFor(() =>
        expect(
          within(screen.getByRole('group', { name: i18n.t('toolbar.labelGroupLabel') }))
            .getAllByRole('checkbox')
            .map(c => c.getAttribute('aria-label'))
        ).toEqual(['Alpha のラベル'])
      );
      await user.click(labelButton());

      await user.click(screen.getByRole('button', { name: /^Alpha/ }));
      await user.click(screen.getByRole('button', { name: /^Beta/ }));
      await screen.findByText('BETA-00001');

      await user.click(labelButton());
      const options = () =>
        within(screen.getByRole('group', { name: i18n.t('toolbar.labelGroupLabel') }))
          .queryAllByRole('checkbox')
          .map(c => c.getAttribute('aria-label'));
      expect(options()).not.toContain('Alpha のラベル');

      releaseBetaLabels();
      await waitFor(() => expect(options()).toEqual(['Beta のラベル']));
    });
  });

  // Only the newest run of fetchAllTickets may write, so a poll that starts
  // a new run while the previous one is still in flight throws the previous
  // one's result away. When one full fetch (the list plus one request per
  // ticket) takes longer than the 15s interval -- many tickets, or a slow
  // HTTP data source -- every run used to be overtaken before it finished:
  // the list stayed on "loading" and the spinner never stopped. The poll now
  // skips its tick while a run for the same project is still in flight.
  describe('when one fetch takes longer than the poll interval', () => {
    beforeEach(() => seed());

    it('still shows the list and stops the spinner', async () => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      const realFetch = backend.fetch.bind(backend);
      backend.fetch = async (input, init) => {
        if (String(input) === `/api/tickets?project_id=${alpha.id}`) {
          await new Promise(r => setTimeout(r, 20_000));
        }
        return realFetch(input, init);
      };

      render(<App />);
      await screen.findByRole('button', { name: /^Alpha/ });
      await waitFor(() => expect(ticketListRequests()).toHaveLength(1));
      const refresh = screen.getByRole('button', { name: i18n.t('toolbar.refreshTitle') });
      expect(refresh).toBeDisabled();

      // The 15s tick falls inside the first fetch; the first fetch answers
      // at 20s and must be shown rather than overtaken.
      await vi.advanceTimersByTimeAsync(20_500);
      await screen.findByText('ALP-00001');
      await waitFor(() => expect(refresh).toBeEnabled());
      // The tick was skipped rather than sent: the first run was still
      // fetching the same thing.
      expect(ticketListRequests()).toHaveLength(1);

      // Polling carries on once nothing is in flight.
      await vi.advanceTimersByTimeAsync(10_000);
      await waitFor(() => expect(ticketListRequests()).toHaveLength(2));
      expect(new Set(ticketListRequests())).toEqual(new Set([`/api/tickets?project_id=${alpha.id}`]));
      // A slow poll of an already-loaded list keeps showing it.
      expect(screen.getByText('ALP-00001')).toBeInTheDocument();
    });

    // DFLT-00112 added a second way to trigger a poll: the tab becoming
    // visible again. It is held to the same rule as the interval, or
    // switching back to a tab whose (slow) first fetch is still running
    // would supersede that fetch exactly like the tick used to.
    it('does not start a second run when the tab becomes visible mid-fetch', async () => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      let visibility: DocumentVisibilityState = 'visible';
      Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => visibility });
      try {
        const realFetch = backend.fetch.bind(backend);
        backend.fetch = async (input, init) => {
          if (String(input) === `/api/tickets?project_id=${alpha.id}`) {
            await new Promise(r => setTimeout(r, 20_000));
          }
          return realFetch(input, init);
        };

        render(<App />);
        await screen.findByRole('button', { name: /^Alpha/ });
        await waitFor(() => expect(ticketListRequests()).toHaveLength(1));

        visibility = 'hidden';
        document.dispatchEvent(new Event('visibilitychange'));
        visibility = 'visible';
        document.dispatchEvent(new Event('visibilitychange'));
        expect(ticketListRequests()).toHaveLength(1);

        await vi.advanceTimersByTimeAsync(20_500);
        await screen.findByText('ALP-00001');
        expect(ticketListRequests()).toHaveLength(1);
      } finally {
        Reflect.deleteProperty(document, 'visibilityState');
      }
    });
  });

  // DFLT-00112 moved artifacts out of the list: an expanded ticket's detail
  // is its own request, issued on expand and on every round. Such a request
  // can still be in flight when the user switches projects.
  describe('an expanded ticket of the project the user has left', () => {
    beforeEach(() => {
      seed();
      // An expanded panel measures its node list with a ResizeObserver,
      // which jsdom lacks. Undone by unstubAllGlobals in afterEach.
      vi.stubGlobal(
        'ResizeObserver',
        class {
          observe() {}
          unobserve() {}
          disconnect() {}
        }
      );
    });

    it('does not bring that ticket back when its detail arrives after the switch', async () => {
      const user = userEvent.setup();
      let releaseDetail!: () => void;
      const held = new Promise<void>(resolve => {
        releaseDetail = resolve;
      });
      const realFetch = backend.fetch.bind(backend);
      backend.fetch = async (input, init) => {
        if (String(input) === '/api/tickets/ALP-00001') await held;
        return realFetch(input, init);
      };

      render(<App />);
      await user.click(await screen.findByText('ALP-00001'));
      await waitFor(() => expect(fetchMock.mock.calls.map(c => String(c[0]))).toContain('/api/tickets/ALP-00001'));

      await user.click(screen.getByRole('button', { name: /^Alpha/ }));
      await user.click(screen.getByRole('button', { name: /^Beta/ }));
      await screen.findByText('BETA-00001');

      releaseDetail();
      await new Promise(r => setTimeout(r, 50));

      expect(screen.getByRole('button', { name: /^Beta/ })).toBeInTheDocument();
      expect(screen.getByText('BETA-00001')).toBeInTheDocument();
      expect(screen.queryByText('ALP-00001')).not.toBeInTheDocument();
    });
  });

  // DFLT-00112 stops polling while the tab is hidden and fetches once when it
  // becomes visible again. That fetch has to follow the header like every
  // other one: the listener belongs to the same effect as the interval, so a
  // switch re-points it too.
  describe('coming back to a hidden tab after a switch', () => {
    beforeEach(() => seed());

    it('fetches the project the header shows now, and nothing while hidden', async () => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
      let visibility: DocumentVisibilityState = 'visible';
      Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => visibility });
      try {
        render(<App />);
        await screen.findByText('ALP-00001');
        await user.click(screen.getByRole('button', { name: /^Alpha/ }));
        await user.click(screen.getByRole('button', { name: /^Beta/ }));
        await screen.findByText('BETA-00001');

        visibility = 'hidden';
        document.dispatchEvent(new Event('visibilitychange'));
        const hiddenMark = ticketListRequests().length;
        await vi.advanceTimersByTimeAsync(40_000);
        expect(ticketListRequests().slice(hiddenMark)).toEqual([]);

        visibility = 'visible';
        document.dispatchEvent(new Event('visibilitychange'));
        await waitFor(() => expect(ticketListRequests().length).toBeGreaterThan(hiddenMark));
        expect(ticketListRequests().slice(hiddenMark)).toEqual([`/api/tickets?project_id=${beta.id}`]);
        expect(screen.getByText('BETA-00001')).toBeInTheDocument();
      } finally {
        Reflect.deleteProperty(document, 'visibilityState');
      }
    });
  });

  // GET /api/current-project reads this user's home config file
  // (DFLT-00106), a local file that can be unreadable on its own while
  // everything else works. "Could not read it" is not "you have no project":
  // showing the create-a-project screen to somebody who does have one is how
  // a shared data source ends up with duplicate projects.
  describe('when the current project cannot be read', () => {
    // Flipped to false by the retry test to let the read succeed again.
    let readFails = true;

    beforeEach(() => {
      seed();
      readFails = true;
      const realFetch = backend.fetch.bind(backend);
      backend.fetch = async (input, init) => {
        if (readFails && String(input) === '/api/current-project' && (init?.method ?? 'GET') === 'GET') {
          return new Response(JSON.stringify({ error: { code: 'CONFIG_READ_FAILED', message: 'boom' } }), {
            status: 500
          });
        }
        return realFetch(input, init);
      };
      vi.spyOn(console, 'error').mockImplementation(() => {});
    });

    it('reports the failure instead of offering to create a project', async () => {
      render(<App />);

      await screen.findByText(i18n.t('projectSwitcher.loadFailed'));
      expect(screen.queryByText(i18n.t('projectSwitcher.noProjectYet'))).not.toBeInTheDocument();
      expect(screen.queryByRole('button', { name: i18n.t('projectSwitcher.createNew') })).not.toBeInTheDocument();
      expect(screen.queryByText(i18n.t('emptyState.loadingTickets'))).not.toBeInTheDocument();
      // No project is known, so nothing is fetched under one.
      expect(ticketListRequests()).toEqual([]);
    });

    it('offers a retry that recovers once the read succeeds', async () => {
      const user = userEvent.setup();
      render(<App />);
      await screen.findByText(i18n.t('projectSwitcher.loadFailed'));

      // The file becomes readable again.
      readFails = false;
      await user.click(screen.getByRole('button', { name: i18n.t('projectSwitcher.retry') }));

      await screen.findByText('ALP-00001');
      expect(screen.queryByText(i18n.t('projectSwitcher.loadFailed'))).not.toBeInTheDocument();
      expect(ticketListRequests()).toEqual([`/api/tickets?project_id=${alpha.id}`]);
    });

    it('draws the retry button in slate-600 / dark:slate-300, which also clear 4.5:1 on its hover background (DFLT-00164)', async () => {
      render(<App />);
      await screen.findByText(i18n.t('projectSwitcher.loadFailed'));

      const retry = screen.getByRole('button', { name: i18n.t('projectSwitcher.retry') });
      expect(retry).toHaveClass('text-slate-600', 'dark:text-slate-300');
      expect(retry).not.toHaveClass('text-slate-500');
      expect(retry).not.toHaveClass('dark:text-slate-400');
      // The hover backgrounds (state colors) are unchanged.
      expect(retry).toHaveClass('hover:bg-slate-100', 'dark:hover:bg-slate-800');
    });

    it('gives the retry button a focus-visible ring and no ring on a plain focus, keeping its colours (DFLT-00167)', async () => {
      render(<App />);
      await screen.findByText(i18n.t('projectSwitcher.loadFailed'));

      const tokens = screen.getByRole('button', { name: i18n.t('projectSwitcher.retry') }).className.split(/\s+/);
      for (const cls of [
        'focus:outline-none',
        'focus-visible:ring-2',
        'focus-visible:ring-blue-500',
        'dark:focus-visible:ring-blue-400',
        // DFLT-00164's text, hover background and border colours stay.
        'text-slate-600',
        'dark:text-slate-300',
        'hover:bg-slate-100',
        'dark:hover:bg-slate-800',
        'border',
        'border-slate-300',
        'dark:border-slate-700',
      ]) {
        expect(tokens).toContain(cls);
      }
      // Only focus-visible draws a ring, so a mouse click shows none.
      expect(tokens.filter(c => c.startsWith('focus:ring') || c.startsWith('dark:focus:ring'))).toEqual([]);
    });

    // The failure screen deliberately keeps the header's switcher usable.
    // Picking a project there answers the question the failed read could
    // not, so the error has to go with it: before, "failed" was a flag of
    // its own that only a successful read or the retry button cleared, and
    // the screen ended up with Beta in the header, the error in the body,
    // and Beta's tickets fetched but never shown.
    it('clears the failure once the user switches to a project from the switcher', async () => {
      const user = userEvent.setup();
      render(<App />);
      await screen.findByText(i18n.t('projectSwitcher.loadFailed'));

      await user.click(screen.getByRole('button', { name: i18n.t('projectSwitcher.noProject') }));
      await user.click(await screen.findByRole('button', { name: /^Beta/ }));

      await screen.findByText('BETA-00001');
      expect(screen.getByRole('button', { name: /^Beta/ })).toBeInTheDocument();
      expect(screen.queryByText(i18n.t('projectSwitcher.loadFailed'))).not.toBeInTheDocument();
      expect(screen.queryByRole('button', { name: i18n.t('projectSwitcher.retry') })).not.toBeInTheDocument();
    });

    // The same pick can race the retry: the retry's GET was answered with
    // the setting as it was (Alpha) before the user chose Beta, and arrives
    // after. The user's choice is newer and must stay, header and list both.
    it('does not let a retry answered before the switch undo it', async () => {
      const user = userEvent.setup();
      render(<App />);
      await screen.findByText(i18n.t('projectSwitcher.loadFailed'));

      readFails = false;
      let releaseRead!: () => void;
      const heldRead = new Promise<void>(resolve => {
        releaseRead = resolve;
      });
      const failingFetch = backend.fetch.bind(backend);
      backend.fetch = async (input, init) => {
        if (String(input) === '/api/current-project' && (init?.method ?? 'GET') === 'GET') {
          // Answered now (Alpha), delivered later.
          const res = await failingFetch(input, init);
          await heldRead;
          return res;
        }
        return failingFetch(input, init);
      };

      await user.click(screen.getByRole('button', { name: i18n.t('projectSwitcher.retry') }));
      await user.click(screen.getByRole('button', { name: i18n.t('projectSwitcher.noProject') }));
      await user.click(await screen.findByRole('button', { name: /^Beta/ }));
      await screen.findByText('BETA-00001');

      releaseRead();
      await new Promise(r => setTimeout(r, 50));

      expect(screen.getByRole('button', { name: /^Beta/ })).toBeInTheDocument();
      expect(screen.getByText('BETA-00001')).toBeInTheDocument();
      expect(screen.queryByText('ALP-00001')).not.toBeInTheDocument();
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

  describe('secondary text contrast (DFLT-00162)', () => {
    const expectContrastColors = (el: HTMLElement) => {
      expect(el).toHaveClass('text-slate-500', 'dark:text-slate-400');
      expect(el).not.toHaveClass('text-slate-400');
      expect(el).not.toHaveClass('dark:text-slate-500');
    };

    it('draws the "no project yet" state in slate-500 / dark:slate-400', async () => {
      seed({ currentProjectId: '' });
      render(<App />);
      const text = await screen.findByText(i18n.t('projectSwitcher.noProjectYet'));
      expectContrastColors(text.parentElement!);
    });

    it('draws the "no tickets match" state in slate-500 / dark:slate-400', async () => {
      seed({ tickets: [] });
      render(<App />);
      expectContrastColors(await screen.findByText(i18n.t('emptyState.noTicketsMatch')));
    });

    it('draws the project switcher\'s "no projects" line in slate-500 / dark:slate-400', async () => {
      seed({ projects: [], currentProjectId: '', tickets: [] });
      const user = userEvent.setup();
      render(<App />);
      await screen.findByText(i18n.t('projectSwitcher.noProjectYet'));
      await user.click(screen.getByRole('button', { name: i18n.t('projectSwitcher.noProject') }));
      const empty = await screen.findByText(i18n.t('projectSwitcher.empty'));
      expectContrastColors(empty);
      expect(empty).toHaveClass('text-xs');
    });
  });
});
