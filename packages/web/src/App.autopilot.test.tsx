// DFLT-00142 phase 5: starting the autopilot from a ticket, the run state
// badges, the duplicate-start guard on the buttons, the translated server
// errors and the "automatic decisions" section. fetch is served by
// test/fakeBackend.ts. DFLT-00147: the start is confirmed in the in-app
// dialog, never with window.confirm.
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from './i18n';
import App from './App';
import { AutopilotRun, Project } from './types';
import { FakeAutopilotStart, FakeBackend, FakeTicket, createFakeBackend, installFakeBackend } from './test/fakeBackend';

const alpha: Project = { id: 'p-alpha', name: 'Alpha', prefix: 'ALP', local_path: '/work/alpha', created_at: '', updated_at: '' };

// R -> (C, D), and P -> R: P is R's parent, so a tree start from P would
// cover a run rooted at R. X is unrelated.
const P = 'ALP-00001';
const R = 'ALP-00002';
const C = 'ALP-00003';
const D = 'ALP-00004';
const X = 'ALP-00005';

function tickets(): FakeTicket[] {
  return [
    { id: P, project_id: alpha.id, title: '親 P', status: 'TODO', priority: 'MEDIUM', labelIds: [] },
    { id: R, project_id: alpha.id, title: '起点 R', status: 'IN PROGRESS', priority: 'MEDIUM', labelIds: [], parentId: P },
    { id: C, project_id: alpha.id, title: '子 C', status: 'IN PROGRESS', priority: 'MEDIUM', labelIds: [], parentId: R },
    { id: D, project_id: alpha.id, title: '子 D', status: 'TODO', priority: 'MEDIUM', labelIds: [], parentId: R },
    { id: X, project_id: alpha.id, title: '無関係 X', status: 'TODO', priority: 'MEDIUM', labelIds: [] }
  ];
}

function run(overrides: Partial<AutopilotRun> = {}): AutopilotRun {
  return {
    run_id: 'run-20260925-100000-aaaa0001',
    project_id: alpha.id,
    mode: 'tree',
    root: R,
    state: 'running',
    active: true,
    heartbeat: '2026-09-25T10:00:00Z',
    current: C,
    current_role: 'work',
    tickets: { [R]: 'done', [C]: 'launched' },
    members: [R, C, D],
    pending: [D],
    ...overrides
  };
}

let backend: FakeBackend;
let fetchMock: ReturnType<typeof vi.fn>;

function seed(opts: { runs?: AutopilotRun[]; start?: FakeAutopilotStart; tickets?: FakeTicket[] } = {}) {
  backend = createFakeBackend({
    projects: [alpha],
    currentProjectId: alpha.id,
    labels: [],
    tickets: opts.tickets ?? tickets(),
    autopilotRuns: opts.runs ?? [],
    autopilotStart: opts.start
  });
  fetchMock = installFakeBackend(backend);
}

const card = (id: string) => document.getElementById(`ticket-${id}`) as HTMLElement;

async function renderApp() {
  const user = userEvent.setup();
  render(<App />);
  await screen.findByText(P);
  return user;
}

async function expand(user: ReturnType<typeof userEvent.setup>, id: string) {
  await user.click(within(card(id)).getByTestId('ticket-header-row'));
  return within(card(id)).findByTestId('autopilot-controls');
}

const dialog = () => screen.queryByTestId('autopilot-confirm');
const confirmStart = (user: ReturnType<typeof userEvent.setup>) => user.click(screen.getByTestId('autopilot-confirm-confirm'));
const cancelStart = (user: ReturnType<typeof userEvent.setup>) => user.click(screen.getByTestId('autopilot-confirm-cancel'));

const runRequestCount = () => fetchMock.mock.calls.filter(c => String(c[0]).startsWith('/api/autopilot/runs')).length;

// What the regular poll does: re-fetch the tickets, then the runs. Clicked
// with fireEvent so focus stays inside the open dialog.
async function poll() {
  const runsBefore = runRequestCount();
  fireEvent.click(screen.getByTitle(i18n.t('toolbar.refreshTitle')));
  await waitFor(() => expect(runRequestCount()).toBeGreaterThan(runsBefore));
}

// Holds POST /api/tickets/{id}/autopilot until the returned release() is
// called, to look at the state while the start is in flight.
function holdStarts() {
  let release!: () => void;
  const gate = new Promise<void>(resolve => {
    release = resolve;
  });
  fetchMock.mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
    if (String(input).endsWith('/autopilot') && init?.method === 'POST') await gate;
    return backend.fetch(input, init);
  });
  return release;
}

const startRequests = () =>
  fetchMock.mock.calls
    .filter(c => String(c[0]).endsWith('/autopilot') && (c[1] as RequestInit | undefined)?.method === 'POST')
    .map(c => ({ url: String(c[0]), body: JSON.parse(String((c[1] as RequestInit).body)) }));

describe('autopilot in the Web UI', () => {
  beforeEach(async () => {
    vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
    await i18n.changeLanguage('ja');
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it.each([
    ['ticket', 'オートパイロット（単一）'],
    ['tree', 'オートパイロット（ツリー）']
  ] as const)('starts a %s run from the ticket detail after confirmation', async (mode, label) => {
    seed();
    const user = await renderApp();
    const controls = await expand(user, X);

    await user.click(within(controls).getByRole('button', { name: label }));

    const shown = screen.getByRole('dialog', { name: i18n.t('autopilot.confirm.title') });
    expect(shown).toHaveAccessibleDescription(i18n.t(`autopilot.confirm.${mode}`, { id: X }));
    expect(startRequests()).toEqual([]);
    await confirmStart(user);
    expect(dialog()).not.toBeInTheDocument();
    await waitFor(() => expect(startRequests()).toEqual([{ url: `/api/tickets/${X}/autopilot`, body: { mode } }]));
    expect(await within(controls).findByTestId('autopilot-message')).toHaveTextContent(
      i18n.t('autopilot.started', { runId: 'run-1' })
    );
  });

  it('shows the dialog in English too', async () => {
    await i18n.changeLanguage('en');
    seed();
    const user = await renderApp();
    const controls = await expand(user, X);
    await user.click(within(controls).getByTestId('autopilot-start-ticket'));
    expect(screen.getByRole('dialog', { name: 'Start autopilot' })).toHaveAccessibleDescription(
      i18n.t('autopilot.confirm.ticket', { id: X })
    );
    expect(screen.getByTestId('autopilot-confirm-confirm')).toHaveTextContent('Start');
    expect(screen.getByTestId('autopilot-confirm-cancel')).toHaveTextContent('Cancel');
  });

  it('never calls window.confirm', async () => {
    seed();
    const confirm = vi.spyOn(window, 'confirm');
    const user = await renderApp();
    const controls = await expand(user, X);
    await user.click(within(controls).getByTestId('autopilot-start-tree'));
    await confirmStart(user);
    await waitFor(() => expect(startRequests()).toHaveLength(1));
    expect(confirm).not.toHaveBeenCalled();
  });

  it('does not start when the confirmation is cancelled, and returns focus to the button', async () => {
    seed();
    const user = await renderApp();
    const controls = await expand(user, X);
    const tree = within(controls).getByRole('button', { name: 'オートパイロット（ツリー）' });
    await user.click(tree);
    expect(screen.getByTestId('autopilot-confirm-cancel')).toHaveFocus();
    await cancelStart(user);
    expect(dialog()).not.toBeInTheDocument();
    expect(tree).toHaveFocus();
    expect(startRequests()).toEqual([]);
    expect(within(controls).queryByTestId('autopilot-message')).not.toBeInTheDocument();
  });

  it('does not start on Escape or a click on the overlay', async () => {
    seed();
    const user = await renderApp();
    const controls = await expand(user, X);
    await user.click(within(controls).getByTestId('autopilot-start-ticket'));
    await user.keyboard('{Escape}');
    expect(dialog()).not.toBeInTheDocument();
    await user.click(within(controls).getByTestId('autopilot-start-ticket'));
    await user.click(screen.getByTestId('autopilot-confirm-overlay'));
    expect(dialog()).not.toBeInTheDocument();
    expect(startRequests()).toEqual([]);
    // The ticket stays expanded: the dialog's clicks do not reach its header.
    expect(within(card(X)).getByTestId('autopilot-controls')).toBeInTheDocument();
  });

  it('keeps focus off <body> after confirming, and returns it to the button once the start settles', async () => {
    seed();
    const release = holdStarts();
    const user = await renderApp();
    const controls = await expand(user, X);
    const tree = within(controls).getByTestId('autopilot-start-tree');
    await user.click(tree);
    await confirmStart(user);

    await waitFor(() => expect(startRequests()).toHaveLength(1));
    expect(tree).toBeDisabled();
    expect(controls).toHaveFocus();
    expect(document.body).not.toHaveFocus();

    release();
    expect(await within(controls).findByTestId('autopilot-message')).toHaveTextContent(
      i18n.t('autopilot.started', { runId: 'run-1' })
    );
    await waitFor(() => expect(tree).toHaveFocus());
  });

  it('leaves focus on the controls when the refreshed runs disable the button after a start', async () => {
    seed();
    const release = holdStarts();
    const user = await renderApp();
    const controls = await expand(user, X);
    const tree = within(controls).getByTestId('autopilot-start-tree');
    await user.click(tree);
    await confirmStart(user);
    await waitFor(() => expect(startRequests()).toHaveLength(1));
    backend.autopilotRuns = [run({ root: X, members: [X], current: undefined, tickets: {}, state: 'starting' })];

    release();
    await waitFor(() => expect(within(card(X)).getByTestId('autopilot-badge-running')).toBeInTheDocument());
    await within(controls).findByTestId('autopilot-message');
    expect(tree).toBeDisabled();
    expect(controls).toHaveFocus();
  });

  it('keeps the text the dialog opened with when a poll changes whether the run would resume', async () => {
    const list = tickets();
    list[4].status = 'DONE';
    seed({ tickets: list, runs: [run({ root: X, mode: 'tree', active: false, state: 'stopped', members: [], current: undefined })] });
    const user = await renderApp();
    const controls = await expand(user, X);
    const tree = within(controls).getByTestId('autopilot-start-tree');
    await waitFor(() => expect(tree).toBeEnabled());
    await user.click(tree);
    const resumeText = i18n.t('autopilot.confirm.resume', { id: X, mode: i18n.t('autopilot.modes.tree') });
    expect(screen.getByRole('dialog', { name: i18n.t('autopilot.confirm.resumeTitle') })).toHaveAccessibleDescription(resumeText);

    // The poll shows the ticket reopened and the stopped run gone: a start
    // would now be a fresh one (still allowed), so only the text could change.
    backend.tickets = backend.tickets.map(tk => (tk.id === X ? { ...tk, status: 'IN PROGRESS' } : tk));
    backend.autopilotRuns = [];
    await poll();
    // The single button, disabled on the finished ticket, is enabled again
    // once the new view has arrived.
    await waitFor(() => expect(within(controls).getByTestId('autopilot-start-ticket')).toBeEnabled());

    expect(screen.getByRole('dialog', { name: i18n.t('autopilot.confirm.resumeTitle') })).toHaveAccessibleDescription(resumeText);
  });

  it('closes the dialog without starting when a poll shows the start would be refused', async () => {
    seed();
    const user = await renderApp();
    const controls = await expand(user, X);
    const tree = within(controls).getByTestId('autopilot-start-tree');
    await user.click(tree);
    expect(dialog()).toBeInTheDocument();

    backend.autopilotRuns = [run({ root: X, members: [X], current: undefined, tickets: {}, state: 'starting' })];
    await poll();

    await waitFor(() => expect(dialog()).not.toBeInTheDocument());
    expect(startRequests()).toEqual([]);
    expect(tree).toBeDisabled();
    expect(within(controls).getByTestId('autopilot-disabled-reason')).toHaveTextContent(i18n.t('autopilot.blocked', { root: X }));
    expect(controls).toHaveFocus();
    expect(document.body).not.toHaveFocus();
  });

  it('shows running / processing / waiting badges in the list', async () => {
    seed({ runs: [run()] });
    await renderApp();
    await waitFor(() => expect(within(card(R)).getByTestId('autopilot-badge-running')).toHaveTextContent('オートパイロット実行中'));
    expect(within(card(C)).getByTestId('autopilot-badge-processing')).toHaveTextContent('処理中');
    expect(within(card(D)).getByTestId('autopilot-badge-waiting')).toHaveTextContent('待機中');
    expect(within(card(R)).queryByTestId('autopilot-badge-processing')).not.toBeInTheDocument();
    expect(within(card(X)).queryByTestId('autopilot-badges')).not.toBeInTheDocument();
    expect(within(card(P)).queryByTestId('autopilot-badges')).not.toBeInTheDocument();
  });

  it('shows the waiting-for-a-person badge with what is awaited', async () => {
    seed({ runs: [run({ awaiting_human: '計画承認の判断待ち' })] });
    await renderApp();
    const badge = await within(card(C)).findByTestId('autopilot-badge-awaitingHuman');
    expect(badge).toHaveTextContent('人の判断待ち');
    expect(badge).toHaveAttribute('title', i18n.t('autopilot.badges.awaitingTitle', { what: '計画承認の判断待ち' }));
    expect(within(card(C)).queryByTestId('autopilot-badge-processing')).not.toBeInTheDocument();
  });

  it('disables both buttons on a ticket of an active run, with the reason', async () => {
    seed({ runs: [run()] });
    const user = await renderApp();
    await waitFor(() => expect(within(card(C)).getByTestId('autopilot-badge-processing')).toBeInTheDocument());
    const controls = await expand(user, C);
    const reason = i18n.t('autopilot.blocked', { root: R });
    for (const name of ['オートパイロット（単一）', 'オートパイロット（ツリー）']) {
      const button = within(controls).getByRole('button', { name });
      expect(button).toBeDisabled();
      expect(button).toHaveAttribute('title', reason);
      expect(button).toHaveAccessibleDescription(reason);
    }
    expect(within(controls).getAllByTestId('autopilot-disabled-reason')).toHaveLength(1);
  });

  it('disables only the tree button on an ancestor of an active run root', async () => {
    seed({ runs: [run()] });
    const user = await renderApp();
    await waitFor(() => expect(within(card(R)).getByTestId('autopilot-badge-running')).toBeInTheDocument());
    const controls = await expand(user, P);
    expect(within(controls).getByRole('button', { name: 'オートパイロット（単一）' })).toBeEnabled();
    const tree = within(controls).getByRole('button', { name: 'オートパイロット（ツリー）' });
    expect(tree).toBeDisabled();
    expect(tree).toHaveAccessibleDescription(i18n.t('autopilot.blockedDescendant', { root: R }));
  });

  it('ignores inactive runs for badges and buttons', async () => {
    seed({ runs: [run({ active: false, state: 'finished', members: [] })] });
    const user = await renderApp();
    const controls = await expand(user, C);
    expect(within(controls).getByRole('button', { name: 'オートパイロット（ツリー）' })).toBeEnabled();
    expect(within(card(C)).queryByTestId('autopilot-badges')).not.toBeInTheDocument();
  });

  it.each([
    ['ja', 'AUTOPILOT_ALREADY_RUNNING', 409],
    ['en', 'AUTOPILOT_ALREADY_RUNNING', 409],
    ['ja', 'PROJECT_LOCAL_PATH_NOT_SET', 400],
    ['en', 'AUTOPILOT_ROOT_FINISHED', 409]
  ] as const)('shows the server error %s %s translated', async (lang, code, status) => {
    await i18n.changeLanguage(lang);
    seed({ start: () => ({ status, body: { error: { code, message: 'backend text' } } }) });
    const user = await renderApp();
    const controls = await expand(user, X);
    await user.click(within(controls).getByTestId('autopilot-start-tree'));
    await confirmStart(user);
    const message = await within(controls).findByTestId('autopilot-message');
    expect(message).toHaveTextContent(i18n.t('autopilot.failed', { message: i18n.t(`errors.${code}`) }));
    expect(message).not.toHaveTextContent('backend text');
  });

  // DFLT-00182: the server's untrusted_folder turns into a notice that stays
  // until dismissed and is announced through an always-mounted live region.
  it('shows the untrusted-folder notice only when the start response has untrusted_folder', async () => {
    seed({
      start: (ticketId, mode) => ({
        status: 200,
        body: { run_id: 'run-1', mode, root: ticketId, state: 'starting', created: true, resumed: false, untrusted_folder: '/work/alpha' }
      })
    });
    const user = await renderApp();
    const controls = await expand(user, X);
    const notice = i18n.t('autopilot.untrustedFolder', { path: '/work/alpha' });
    const regions = within(controls).getAllByRole('status');
    expect(regions.some(r => r.textContent === notice)).toBe(false);

    await user.click(within(controls).getByTestId('autopilot-start-ticket'));
    await confirmStart(user);

    expect(await within(controls).findByTestId('autopilot-untrusted')).toHaveTextContent(notice);
    expect(within(controls).getByTestId('autopilot-message')).toHaveTextContent(i18n.t('autopilot.started', { runId: 'run-1' }));
    await waitFor(() => expect(within(controls).getAllByRole('status').some(r => r.textContent === notice)).toBe(true));
    const dismiss = within(controls).getByRole('button', { name: i18n.t('autopilot.untrustedDismiss') });
    expect(dismiss).toHaveAccessibleDescription(notice);

    await user.click(dismiss);
    expect(within(controls).queryByTestId('autopilot-untrusted')).not.toBeInTheDocument();
    expect(within(controls).getAllByRole('status').some(r => r.textContent === notice)).toBe(false);
    expect(controls).toHaveFocus();
  });

  it('shows no untrusted-folder notice when the start response has none', async () => {
    seed();
    const user = await renderApp();
    const controls = await expand(user, X);
    await user.click(within(controls).getByTestId('autopilot-start-ticket'));
    await confirmStart(user);
    expect(await within(controls).findByTestId('autopilot-message')).toHaveTextContent(i18n.t('autopilot.started', { runId: 'run-1' }));
    expect(within(controls).queryByTestId('autopilot-untrusted')).not.toBeInTheDocument();
  });

  it('refreshes the runs right after a start', async () => {
    seed();
    const user = await renderApp();
    const controls = await expand(user, X);
    backend.autopilotRuns = [run({ root: X, members: [X], current: undefined, tickets: {}, state: 'starting' })];
    await user.click(within(controls).getByTestId('autopilot-start-tree'));
    await confirmStart(user);
    await waitFor(() => expect(within(card(X)).getByTestId('autopilot-badge-running')).toBeInTheDocument());
    expect(within(controls).getByTestId('autopilot-start-tree')).toBeDisabled();
  });

  it('disables the buttons of a DONE ticket unless its stopped run can be resumed', async () => {
    const list = tickets();
    list[4].status = 'DONE';
    seed({ tickets: list, runs: [run({ root: X, mode: 'tree', active: false, state: 'stopped', members: [], current: undefined })] });
    const user = await renderApp();
    const controls = await expand(user, X);
    const single = within(controls).getByTestId('autopilot-start-ticket');
    expect(single).toBeDisabled();
    expect(single).toHaveAccessibleDescription(i18n.t('autopilot.finished'));
    const tree = within(controls).getByTestId('autopilot-start-tree');
    expect(tree).toBeEnabled();
    await user.click(tree);
    expect(screen.getByRole('dialog', { name: i18n.t('autopilot.confirm.resumeTitle') })).toHaveAccessibleDescription(
      i18n.t('autopilot.confirm.resume', { id: X, mode: i18n.t('autopilot.modes.tree') })
    );
  });

  it('lists the autopilot artifacts, and only them, under automatic decisions', async () => {
    const list = tickets();
    const names = [
      ['autopilot-decision-refine', `${R}-01`],
      ['autopilot-decision-approval', `${R}-03`],
      ['autopilot-decision-iteration', `${R}-02`],
      ['autopilot-decision-release', `${R}-04`],
      ['autopilot-decision-handoff', `${R}-04`],
      ['autopilot-tree-summary', `${R}-04`]
    ];
    list[1].nodes = [
      { id: `${R}-01`, name: '計画作成', type: 'plan', status: 'DONE' },
      { id: `${R}-02`, name: 'コードレビュー', type: 'review', status: 'DONE' },
      { id: `${R}-03`, name: '計画承認', type: 'approval_gate', status: 'DONE' },
      { id: `${R}-04`, name: 'リリース', type: 'release', status: 'DONE' }
    ];
    list[1].artifacts = [
      ...names.map(([name, node], i) => ({ id: `art-${i}`, node_id: node, name, type: 'text' as const, content: `${name} の本文` })),
      { id: 'art-plan', node_id: `${R}-01`, name: '実行計画', type: 'text', content: '計画' }
    ];
    seed({ tickets: list });
    const user = await renderApp();
    await user.click(within(card(R)).getByTestId('ticket-header-row'));
    const section = await within(card(R)).findByTestId('autopilot-decisions');
    expect(within(section).getByRole('heading')).toHaveTextContent(i18n.t('autopilot.decisions.title', { count: 6 }));
    const items = within(section).getAllByTestId('autopilot-decision');
    expect(items).toHaveLength(6);
    expect(items.map(i => i.textContent)).toEqual(names.map(([name]) => expect.stringContaining(name)));
    expect(items[0]).toHaveTextContent(i18n.t('autopilot.decisions.kinds.refine'));
    expect(items[5]).toHaveTextContent(i18n.t('autopilot.decisions.kinds.treeSummary'));
    expect(items[1]).toHaveTextContent('計画承認');
    const link = within(items[5]).getByRole('link', {
      name: i18n.t('autopilot.decisions.open', { name: i18n.t('autopilot.decisions.kinds.treeSummary') })
    });
    expect(link).toHaveAttribute('href', '/artifacts/art-5/preview?type=text&name=autopilot-tree-summary');
    expect(within(section).queryByText('実行計画')).not.toBeInTheDocument();
  });

  it('shows no automatic decisions section without autopilot artifacts', async () => {
    seed();
    const user = await renderApp();
    await expand(user, X);
    expect(within(card(X)).queryByTestId('autopilot-decisions')).not.toBeInTheDocument();
  });
});
