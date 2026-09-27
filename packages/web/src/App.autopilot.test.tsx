// DFLT-00142 phase 5: starting the autopilot from a ticket, the run state
// badges, the duplicate-start guard on the button, the translated server
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
const startButton = (controls: HTMLElement) => within(controls).getByTestId('autopilot-start');
// DFLT-00218: `controls` (from expand) is the whole action row; focus goes
// to this column inside it, which holds the autopilot alone.
const focusFallback = (controls: HTMLElement) => within(controls).getByTestId('autopilot-focus-fallback');
const regularActions = (controls: HTMLElement) => [
  within(controls).getByRole('button', { name: i18n.t('ticketItem.actions.refine') }),
  within(controls).getByRole('button', { name: i18n.t('ticketItem.actions.run') })
];
const modeTitle = (mode: 'ticket' | 'tree', resume = false) =>
  i18n.t(resume ? 'autopilot.confirm.resumeTitle' : 'autopilot.confirm.title', { mode: i18n.t(`autopilot.modes.${mode}`) });
const confirmStart = (user: ReturnType<typeof userEvent.setup>) => user.click(screen.getByTestId('autopilot-confirm-confirm'));
const cancelStart = (user: ReturnType<typeof userEvent.setup>) => user.click(screen.getByTestId('autopilot-confirm-cancel'));

const runRequestCount = () => fetchMock.mock.calls.filter(c => String(c[0]).startsWith('/api/autopilot/runs')).length;

// What the regular poll does: re-fetch the tickets, then the runs. Clicked
// with fireEvent so focus stays inside the open dialog.
async function poll() {
  const runsBefore = runRequestCount();
  fireEvent.click(screen.getByRole('button', { name: i18n.t('toolbar.refreshTitle') }));
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

  it('shows one autopilot button, at the right end of the action row', async () => {
    seed();
    const user = await renderApp();
    const controls = await expand(user, X);
    const button = within(controls).getByRole('button', { name: 'オートパイロット' });
    expect(button).toBe(startButton(controls));
    expect(button).toBeEnabled();
    // No per-mode buttons any more.
    expect(controls.querySelectorAll('[data-testid^="autopilot-start"]')).toHaveLength(1);
    const group = within(controls).getByTestId('autopilot-group');
    expect(group).toContainElement(button);
    // DFLT-00219: the divider shows on sm+ only while the row (a size
    // container) is at least 16rem wide, so it never stays behind at the
    // start of a wrapped line.
    expect(group).toHaveClass('sm:[@container(min-width:16rem)]:border-l');
    expect(group).not.toHaveClass('sm:border-l');
    // The autopilot column sits in a slot pushed to the right end of the row.
    const column = focusFallback(controls);
    expect(column).toContainElement(group);
    const slot = within(controls).getByTestId('autopilot-slot');
    expect(slot).toContainElement(column);
    expect(slot).toHaveClass('ml-auto');
    // The regular actions share the row, on its left.
    const row = controls;
    const [refine, runButton] = regularActions(row);
    expect(refine.compareDocumentPosition(button) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(runButton.compareDocumentPosition(button) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(row.lastElementChild).toBe(slot);
    // DFLT-00219: the row may wrap, and is the divider's size container. On
    // sm+ both sides start from a zero basis with their min-content as the
    // floor -- for the slot, the whole button, its label not wrapping -- so
    // the row wraps only when those floors do not fit, and the button is
    // never squeezed or drawn over the regular actions; the free space goes
    // to the regular actions first, up to their one-line width.
    expect(row).toHaveClass('flex-wrap', '[container-type:inline-size]');
    expect(row).not.toHaveClass('sm:flex-nowrap');
    const actions = within(row).getByTestId('autopilot-row-actions');
    expect(actions).toHaveClass('sm:basis-0', 'sm:min-w-min', 'sm:grow-[999]', 'sm:max-w-max');
    expect(actions).toContainElement(refine);
    expect(actions).toContainElement(runButton);
    expect(slot).toHaveClass('sm:basis-0', 'sm:min-w-min', 'sm:grow', 'sm:max-w-xs');
    expect(button).toHaveClass('sm:whitespace-nowrap');
  });

  // DFLT-00224: with a 200% default font size at 375px the action row is only
  // a few words wide, narrower than a button's icon, label and padding side
  // by side. Below sm each button may then put its icon on a line of its own
  // and break its label anywhere, so the regular actions and the autopilot
  // button stay inside the row instead of running past the card. Only below
  // sm: on sm+ the labels do not wrap -- their width is the floor the row's
  // layout is built on (DFLT-00219, above).
  it('lets the action row\'s buttons wrap below sm, and only there', async () => {
    seed();
    const user = await renderApp();
    const controls = await expand(user, X);
    for (const button of [...regularActions(controls), startButton(controls)]) {
      expect(button).toHaveClass('flex', 'max-sm:flex-wrap', 'max-sm:[overflow-wrap:anywhere]');
      // One class per assertion: a negated multi-class toHaveClass passes as
      // soon as any one of the classes is missing, so it would check nothing.
      for (const cls of ['flex-wrap', '[overflow-wrap:anywhere]', 'break-words', 'sm:flex-wrap']) {
        expect(button).not.toHaveClass(cls);
      }
      // The icon keeps its size when the label wraps.
      const icon = button.querySelector('svg');
      expect(icon).not.toBeNull();
      expect(icon).toHaveClass('shrink-0');
    }
    expect(startButton(controls)).toHaveClass('sm:whitespace-nowrap');
    // The layout the DFLT-00219 floors rely on is unchanged.
    expect(within(controls).getByTestId('autopilot-row-actions')).toHaveClass('min-w-0', 'sm:min-w-min', 'sm:max-w-max');
    expect(within(controls).getByTestId('autopilot-slot')).toHaveClass('min-w-0', 'max-w-full', 'sm:min-w-min');
  });

  // DFLT-00218: the focus fallback holds the autopilot button and the lines
  // under it, not the regular actions, so its ring and what a screen reader
  // reads out there cover the autopilot alone.
  it('keeps the regular actions out of the focus fallback', async () => {
    seed({ runs: [run()] });
    const user = await renderApp();
    await waitFor(() => expect(within(card(C)).getByTestId('autopilot-badge-processing')).toBeInTheDocument());
    const controls = await expand(user, C);
    const fallback = focusFallback(controls);
    expect(fallback).toHaveAttribute('tabindex', '-1');
    expect(controls).not.toHaveAttribute('tabindex');
    expect(fallback).toContainElement(startButton(controls));
    expect(fallback).toContainElement(within(controls).getByTestId('autopilot-disabled-reason'));
    const label = within(controls).getByText(i18n.t('ticketItem.actions.label'));
    for (const el of [...regularActions(controls), label]) {
      expect(controls).toContainElement(el);
      expect(fallback).not.toContainElement(el);
    }
    expect(within(fallback).queryByText(i18n.t('ticketItem.actions.label'))).not.toBeInTheDocument();
  });

  it('shows what a person is awaited for inside the focus fallback', async () => {
    seed({ runs: [run({ awaiting_human: '計画承認の判断待ち' })] });
    const user = await renderApp();
    await within(card(C)).findByTestId('autopilot-badge-awaitingHuman');
    const controls = await expand(user, C);
    const awaiting = await within(controls).findByTestId('autopilot-awaiting');
    expect(awaiting).toHaveTextContent('計画承認の判断待ち');
    const fallback = focusFallback(controls);
    expect(fallback).toContainElement(awaiting);
    for (const el of regularActions(controls)) expect(fallback).not.toContainElement(el);
  });

  it.each(['ticket', 'tree'] as const)('starts a %s run from the ticket detail after confirmation', async mode => {
    seed();
    const user = await renderApp();
    const controls = await expand(user, X);

    await user.click(within(controls).getByRole('button', { name: 'オートパイロット' }));

    // The tree is the initial choice, listed first.
    const group = screen.getByRole('group', { name: i18n.t('autopilot.confirm.modeLegend') });
    const radios = within(group).getAllByRole('radio');
    expect(radios.map(r => r.getAttribute('data-testid'))).toEqual(['autopilot-mode-tree', 'autopilot-mode-ticket']);
    expect(screen.getByRole('radio', { name: 'ツリー（このチケットと子孫）' })).toBeChecked();
    expect(screen.getByRole('dialog', { name: modeTitle('tree') })).toHaveAccessibleDescription(
      i18n.t('autopilot.confirm.tree', { id: X })
    );
    if (mode === 'ticket') await user.click(screen.getByRole('radio', { name: 'このチケット単体' }));

    const shown = screen.getByRole('dialog', { name: modeTitle(mode) });
    expect(shown).toHaveAccessibleDescription(i18n.t(`autopilot.confirm.${mode}`, { id: X }));
    expect(screen.getByTestId('autopilot-confirm-confirm')).toHaveTextContent('起動する');
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
    await user.click(within(controls).getByRole('button', { name: 'Autopilot' }));
    expect(screen.getByRole('group', { name: 'What to run' })).toBeInTheDocument();
    expect(screen.getByRole('radio', { name: 'Tree (this ticket and its descendants)' })).toBeChecked();
    expect(screen.getByRole('dialog', { name: 'Start autopilot (tree)' })).toHaveAccessibleDescription(
      i18n.t('autopilot.confirm.tree', { id: X })
    );
    await user.click(screen.getByRole('radio', { name: 'This ticket only' }));
    expect(screen.getByRole('dialog', { name: 'Start autopilot (this ticket only)' })).toHaveAccessibleDescription(
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
    await user.click(startButton(controls));
    await confirmStart(user);
    await waitFor(() => expect(startRequests()).toHaveLength(1));
    expect(confirm).not.toHaveBeenCalled();
  });

  it('does not start when the confirmation is cancelled, and returns focus to the button', async () => {
    seed();
    const user = await renderApp();
    const controls = await expand(user, X);
    const tree = within(controls).getByRole('button', { name: 'オートパイロット' });
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
    await user.click(startButton(controls));
    await user.keyboard('{Escape}');
    expect(dialog()).not.toBeInTheDocument();
    await user.click(startButton(controls));
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
    const tree = startButton(controls);
    await user.click(tree);
    await confirmStart(user);

    await waitFor(() => expect(startRequests()).toHaveLength(1));
    expect(tree).toBeDisabled();
    expect(focusFallback(controls)).toHaveFocus();
    expect(document.body).not.toHaveFocus();
    // What has focus holds the autopilot alone (DFLT-00218).
    for (const el of regularActions(controls)) expect(document.activeElement).not.toContainElement(el);

    release();
    expect(await within(controls).findByTestId('autopilot-message')).toHaveTextContent(
      i18n.t('autopilot.started', { runId: 'run-1' })
    );
    await waitFor(() => expect(tree).toHaveFocus());
  });

  it('leaves focus on the focus fallback when the refreshed runs disable the button after a start', async () => {
    seed();
    const release = holdStarts();
    const user = await renderApp();
    const controls = await expand(user, X);
    const tree = startButton(controls);
    await user.click(tree);
    await confirmStart(user);
    await waitFor(() => expect(startRequests()).toHaveLength(1));
    backend.autopilotRuns = [run({ root: X, members: [X], current: undefined, tickets: {}, state: 'starting' })];

    release();
    await waitFor(() => expect(within(card(X)).getByTestId('autopilot-badge-running')).toBeInTheDocument());
    await within(controls).findByTestId('autopilot-message');
    expect(tree).toBeDisabled();
    expect(focusFallback(controls)).toHaveFocus();
    expect(document.body).not.toHaveFocus();
  });

  it('keeps the text the dialog opened with when a poll changes whether the run would resume', async () => {
    const list = tickets();
    list[4].status = 'DONE';
    seed({ tickets: list, runs: [run({ root: X, mode: 'tree', active: false, state: 'stopped', members: [], current: undefined })] });
    const user = await renderApp();
    const controls = await expand(user, X);
    const tree = startButton(controls);
    await waitFor(() => expect(tree).toBeEnabled());
    await user.click(tree);
    const resumeText = i18n.t('autopilot.confirm.resume', { id: X, mode: i18n.t('autopilot.modes.tree') });
    expect(screen.getByRole('dialog', { name: modeTitle('tree', true) })).toHaveAccessibleDescription(resumeText);
    expect(screen.getByTestId('autopilot-mode-ticket')).toBeDisabled();

    // The poll shows the ticket reopened and the stopped run gone: a start
    // would now be a fresh one (still allowed), so only the text could change.
    backend.tickets = backend.tickets.map(tk => (tk.id === X ? { ...tk, status: 'IN PROGRESS' } : tk));
    backend.autopilotRuns = [];
    await poll();
    // The single choice, disabled on the finished ticket, is enabled again
    // once the new view has arrived; the choice itself does not move.
    await waitFor(() => expect(screen.getByTestId('autopilot-mode-ticket')).toBeEnabled());
    expect(screen.getByTestId('autopilot-mode-tree')).toBeChecked();

    expect(screen.getByRole('dialog', { name: modeTitle('tree', true) })).toHaveAccessibleDescription(resumeText);
    expect(screen.getByTestId('autopilot-confirm-confirm')).toHaveTextContent('再開する');
  });

  it('closes the dialog without starting when a poll shows the start would be refused', async () => {
    seed();
    const user = await renderApp();
    const controls = await expand(user, X);
    const tree = startButton(controls);
    await user.click(tree);
    expect(dialog()).toBeInTheDocument();
    expect(screen.getByTestId('autopilot-mode-tree')).toBeChecked();

    backend.autopilotRuns = [run({ root: X, members: [X], current: undefined, tickets: {}, state: 'starting' })];
    await poll();

    await waitFor(() => expect(dialog()).not.toBeInTheDocument());
    expect(startRequests()).toEqual([]);
    expect(tree).toBeDisabled();
    expect(within(controls).getByTestId('autopilot-disabled-reason')).toHaveTextContent(i18n.t('autopilot.blocked', { root: X }));
    expect(focusFallback(controls)).toContainElement(within(controls).getByTestId('autopilot-disabled-reason'));
    expect(focusFallback(controls)).toHaveFocus();
    expect(document.body).not.toHaveFocus();
  });

  it('closes the dialog when a poll refuses the chosen tree start, and returns focus to the still-enabled button', async () => {
    seed();
    const user = await renderApp();
    const controls = await expand(user, P);
    const button = startButton(controls);
    await user.click(button);
    expect(screen.getByTestId('autopilot-mode-tree')).toBeChecked();

    // A run rooted at P's child R: the tree start from P is refused, the
    // single one is not.
    backend.autopilotRuns = [run()];
    await poll();

    await waitFor(() => expect(dialog()).not.toBeInTheDocument());
    expect(startRequests()).toEqual([]);
    expect(button).toBeEnabled();
    expect(button).toHaveFocus();
    expect(focusFallback(controls)).not.toHaveFocus();
    expect(within(controls).queryByTestId('autopilot-disabled-reason')).not.toBeInTheDocument();
  });

  it('keeps the dialog open and disables the other choice when a poll refuses only the mode not chosen', async () => {
    seed();
    const user = await renderApp();
    const controls = await expand(user, P);
    await user.click(startButton(controls));
    await user.click(screen.getByTestId('autopilot-mode-ticket'));

    backend.autopilotRuns = [run()];
    await poll();

    const treeChoice = screen.getByTestId('autopilot-mode-tree');
    await waitFor(() => expect(treeChoice).toBeDisabled());
    expect(dialog()).toBeInTheDocument();
    expect(treeChoice).toHaveAccessibleDescription(i18n.t('autopilot.blockedDescendant', { root: R }));
    expect(screen.getByTestId('autopilot-mode-ticket')).toBeChecked();
    expect(screen.getByRole('dialog', { name: modeTitle('ticket') })).toBeInTheDocument();
    await confirmStart(user);
    await waitFor(() => expect(startRequests()).toEqual([{ url: `/api/tickets/${P}/autopilot`, body: { mode: 'ticket' } }]));
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

  it('disables the button on a ticket of an active run, with the reason under the button', async () => {
    seed({ runs: [run()] });
    const user = await renderApp();
    await waitFor(() => expect(within(card(C)).getByTestId('autopilot-badge-processing')).toBeInTheDocument());
    const controls = await expand(user, C);
    const reason = i18n.t('autopilot.blocked', { root: R });
    const button = within(controls).getByRole('button', { name: 'オートパイロット' });
    expect(button).toBeDisabled();
    expect(button).toHaveAttribute('title', reason);
    expect(button).toHaveAccessibleDescription(reason);
    const reasons = within(controls).getAllByTestId('autopilot-disabled-reason');
    expect(reasons).toHaveLength(1);
    // Right under the button, inside the focus fallback.
    expect(within(controls).getByTestId('autopilot-group').nextElementSibling).toBe(reasons[0]);
    expect(focusFallback(controls)).toContainElement(reasons[0]);
    await user.click(button);
    expect(dialog()).not.toBeInTheDocument();
  });

  it('lists both reasons when the two modes are refused for different reasons', async () => {
    const list = tickets();
    list[0].status = 'DONE';
    seed({ tickets: list, runs: [run()] });
    const user = await renderApp();
    await waitFor(() => expect(within(card(R)).getByTestId('autopilot-badge-running')).toBeInTheDocument());
    const controls = await expand(user, P);
    const button = startButton(controls);
    const finished = i18n.t('autopilot.finished');
    const descendant = i18n.t('autopilot.blockedDescendant', { root: R });
    expect(button).toBeDisabled();
    const reasons = within(controls).getAllByTestId('autopilot-disabled-reason');
    expect(reasons.map(r => r.textContent)).toEqual([finished, descendant]);
    expect(button.getAttribute('aria-describedby')).toBe(reasons.map(r => r.id).join(' '));
    expect(button).toHaveAccessibleDescription(`${finished} ${descendant}`);
    expect(button.getAttribute('title')).toContain(finished);
    expect(button.getAttribute('title')).toContain(descendant);
  });

  it('disables only the tree choice on an ancestor of an active run root, and starts on this ticket alone', async () => {
    seed({ runs: [run()] });
    const user = await renderApp();
    await waitFor(() => expect(within(card(R)).getByTestId('autopilot-badge-running')).toBeInTheDocument());
    const controls = await expand(user, P);
    const button = within(controls).getByRole('button', { name: 'オートパイロット' });
    expect(button).toBeEnabled();
    expect(within(controls).queryByTestId('autopilot-disabled-reason')).not.toBeInTheDocument();
    await user.click(button);
    const reason = i18n.t('autopilot.blockedDescendant', { root: R });
    const tree = screen.getByTestId('autopilot-mode-tree');
    expect(tree).toBeDisabled();
    expect(tree).toHaveAccessibleDescription(reason);
    expect(screen.getByTestId('autopilot-mode-reason-tree')).toHaveTextContent(reason);
    expect(screen.getByTestId('autopilot-mode-ticket')).toBeChecked();
    expect(screen.getByRole('dialog', { name: modeTitle('ticket') })).toHaveAccessibleDescription(
      i18n.t('autopilot.confirm.ticket', { id: P })
    );
    await confirmStart(user);
    await waitFor(() => expect(startRequests()).toEqual([{ url: `/api/tickets/${P}/autopilot`, body: { mode: 'ticket' } }]));
  });

  it('ignores inactive runs for badges and buttons', async () => {
    seed({ runs: [run({ active: false, state: 'finished', members: [] })] });
    const user = await renderApp();
    const controls = await expand(user, C);
    expect(within(controls).getByRole('button', { name: 'オートパイロット' })).toBeEnabled();
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
    await user.click(startButton(controls));
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

    await user.click(startButton(controls));
    await confirmStart(user);

    expect(await within(controls).findByTestId('autopilot-untrusted')).toHaveTextContent(notice);
    expect(within(controls).getByTestId('autopilot-message')).toHaveTextContent(i18n.t('autopilot.started', { runId: 'run-1' }));
    // Both under the button, inside the focus fallback.
    expect(focusFallback(controls)).toContainElement(within(controls).getByTestId('autopilot-untrusted'));
    expect(focusFallback(controls)).toContainElement(within(controls).getByTestId('autopilot-message'));
    await waitFor(() => expect(within(controls).getAllByRole('status').some(r => r.textContent === notice)).toBe(true));
    const dismiss = within(controls).getByRole('button', { name: i18n.t('autopilot.untrustedDismiss') });
    expect(dismiss).toHaveAccessibleDescription(notice);
    // DFLT-00224: below sm the dismiss button moves under the text once
    // both no longer fit on one line (the text wants at least 6rem), and is
    // never wider than the notice, so with large text on a narrow screen it
    // does not run past the notice or squeeze the text to a letter a line.
    const untrusted = within(controls).getByTestId('autopilot-untrusted');
    expect(untrusted).toHaveClass('flex', 'max-sm:flex-wrap');
    expect(untrusted.querySelector('p')).toHaveClass('flex-1', 'max-sm:basis-24', 'min-w-0');
    expect(dismiss).toHaveClass('shrink-0', 'max-w-full', 'max-sm:[overflow-wrap:anywhere]');

    await user.click(dismiss);
    expect(within(controls).queryByTestId('autopilot-untrusted')).not.toBeInTheDocument();
    expect(within(controls).getAllByRole('status').some(r => r.textContent === notice)).toBe(false);
    expect(focusFallback(controls)).toHaveFocus();
  });

  it('shows no untrusted-folder notice when the start response has none', async () => {
    seed();
    const user = await renderApp();
    const controls = await expand(user, X);
    await user.click(startButton(controls));
    await confirmStart(user);
    expect(await within(controls).findByTestId('autopilot-message')).toHaveTextContent(i18n.t('autopilot.started', { runId: 'run-1' }));
    expect(within(controls).queryByTestId('autopilot-untrusted')).not.toBeInTheDocument();
  });

  it('refreshes the runs right after a start', async () => {
    seed();
    const user = await renderApp();
    const controls = await expand(user, X);
    backend.autopilotRuns = [run({ root: X, members: [X], current: undefined, tickets: {}, state: 'starting' })];
    await user.click(startButton(controls));
    await confirmStart(user);
    await waitFor(() => expect(within(card(X)).getByTestId('autopilot-badge-running')).toBeInTheDocument());
    expect(startButton(controls)).toBeDisabled();
  });

  it('offers only the resumable mode of a DONE ticket', async () => {
    const list = tickets();
    list[4].status = 'DONE';
    seed({ tickets: list, runs: [run({ root: X, mode: 'tree', active: false, state: 'stopped', members: [], current: undefined })] });
    const user = await renderApp();
    const controls = await expand(user, X);
    const button = startButton(controls);
    await waitFor(() => expect(button).toBeEnabled());
    expect(within(controls).queryByTestId('autopilot-disabled-reason')).not.toBeInTheDocument();
    await user.click(button);
    const single = screen.getByTestId('autopilot-mode-ticket');
    expect(single).toBeDisabled();
    expect(single).toHaveAccessibleDescription(i18n.t('autopilot.finished'));
    expect(screen.getByTestId('autopilot-mode-reason-ticket')).toHaveTextContent(i18n.t('autopilot.finished'));
    expect(screen.getByTestId('autopilot-mode-tree')).toBeChecked();
    expect(screen.getByRole('dialog', { name: modeTitle('tree', true) })).toHaveAccessibleDescription(
      i18n.t('autopilot.confirm.resume', { id: X, mode: i18n.t('autopilot.modes.tree') })
    );
    expect(screen.getByTestId('autopilot-confirm-confirm')).toHaveTextContent('再開する');
  });

  it('switches to the resume wording when the chosen mode has a run to resume', async () => {
    seed({ runs: [run({ root: X, mode: 'ticket', active: false, state: 'interrupted', members: [], current: undefined })] });
    const user = await renderApp();
    const controls = await expand(user, X);
    await user.click(startButton(controls));
    expect(screen.getByRole('dialog', { name: modeTitle('tree') })).toBeInTheDocument();
    expect(screen.getByTestId('autopilot-confirm-confirm')).toHaveTextContent('起動する');

    await user.click(screen.getByTestId('autopilot-mode-ticket'));
    expect(screen.getByRole('dialog', { name: 'オートパイロット（単体）を再開' })).toHaveAccessibleDescription(
      i18n.t('autopilot.confirm.resume', { id: X, mode: i18n.t('autopilot.modes.ticket') })
    );
    expect(screen.getByTestId('autopilot-confirm-confirm')).toHaveTextContent('再開する');

    await user.click(screen.getByTestId('autopilot-mode-tree'));
    expect(screen.getByRole('dialog', { name: 'オートパイロット（ツリー）を起動' })).toHaveAccessibleDescription(
      i18n.t('autopilot.confirm.tree', { id: X })
    );
    expect(screen.getByTestId('autopilot-confirm-confirm')).toHaveTextContent('起動する');
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
