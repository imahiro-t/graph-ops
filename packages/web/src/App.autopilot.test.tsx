// DFLT-00142 phase 5: starting the autopilot from a ticket, the run state
// badges, the duplicate-start guard on the buttons, the translated server
// errors and the "automatic decisions" section. fetch is served by
// test/fakeBackend.ts.
import { render, screen, waitFor, within } from '@testing-library/react';
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
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true);
    const user = await renderApp();
    const controls = await expand(user, X);

    await user.click(within(controls).getByRole('button', { name: label }));

    expect(confirm).toHaveBeenCalledWith(i18n.t(`autopilot.confirm.${mode}`, { id: X }));
    await waitFor(() => expect(startRequests()).toEqual([{ url: `/api/tickets/${X}/autopilot`, body: { mode } }]));
    expect(await within(controls).findByTestId('autopilot-message')).toHaveTextContent(
      i18n.t('autopilot.started', { runId: 'run-1' })
    );
  });

  it('does not start when the confirmation is cancelled', async () => {
    seed();
    vi.spyOn(window, 'confirm').mockReturnValue(false);
    const user = await renderApp();
    const controls = await expand(user, X);
    await user.click(within(controls).getByRole('button', { name: 'オートパイロット（ツリー）' }));
    expect(startRequests()).toEqual([]);
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
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    const user = await renderApp();
    const controls = await expand(user, X);
    await user.click(within(controls).getByTestId('autopilot-start-tree'));
    const message = await within(controls).findByTestId('autopilot-message');
    expect(message).toHaveTextContent(i18n.t('autopilot.failed', { message: i18n.t(`errors.${code}`) }));
    expect(message).not.toHaveTextContent('backend text');
  });

  it('refreshes the runs right after a start', async () => {
    seed();
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    const user = await renderApp();
    const controls = await expand(user, X);
    backend.autopilotRuns = [run({ root: X, members: [X], current: undefined, tickets: {}, state: 'starting' })];
    await user.click(within(controls).getByTestId('autopilot-start-tree'));
    await waitFor(() => expect(within(card(X)).getByTestId('autopilot-badge-running')).toBeInTheDocument());
    expect(within(controls).getByTestId('autopilot-start-tree')).toBeDisabled();
  });

  it('disables the buttons of a DONE ticket unless its stopped run can be resumed', async () => {
    const list = tickets();
    list[4].status = 'DONE';
    seed({ tickets: list, runs: [run({ root: X, mode: 'tree', active: false, state: 'stopped', members: [], current: undefined })] });
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true);
    const user = await renderApp();
    const controls = await expand(user, X);
    const single = within(controls).getByTestId('autopilot-start-ticket');
    expect(single).toBeDisabled();
    expect(single).toHaveAccessibleDescription(i18n.t('autopilot.finished'));
    const tree = within(controls).getByTestId('autopilot-start-tree');
    expect(tree).toBeEnabled();
    await user.click(tree);
    expect(confirm).toHaveBeenCalledWith(i18n.t('autopilot.confirm.resume', { id: X, mode: i18n.t('autopilot.modes.tree') }));
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
