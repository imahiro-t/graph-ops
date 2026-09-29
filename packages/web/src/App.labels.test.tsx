// DFLT-00084: the label filter as wired into App.tsx -- the actual narrowing
// of the ticket list, AND with the other filters and the search box, the
// page reset, and how the selection follows label deletion and project
// switching -- plus the settings-to-ticket-row propagation of a rename. The
// individual components have their own tests; these need App's state.
//
// fetch is served by test/fakeBackend.ts, a small in-memory fake of the
// backend shared with App.filters.test.tsx, so a change made through the UI
// is visible in the next read.
import { act, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from './i18n';
import App from './App';
import { Project } from './types';
import { FakeBackend, createFakeBackend, installFakeBackend } from './test/fakeBackend';
import { dispatchCopy, selectContents } from './test/copyEvent';
import { PLAIN_COPY_ATTR } from './lib/plainCopy';
import { plainText, WORD_JOINER, ZWSP } from './lib/wbr';

const alpha: Project = { id: 'p-alpha', name: 'Alpha', prefix: 'ALP', local_path: '/work/alpha', created_at: '', updated_at: '' };
const beta: Project = { id: 'p-beta', name: 'Beta', prefix: 'BETA', local_path: '/work/beta', created_at: '', updated_at: '' };

let backend: FakeBackend;

function seedBackground() {
  backend = createFakeBackend({
    projects: [alpha, beta],
    currentProjectId: alpha.id,
    labels: [
      { id: 'label-bug', project_id: alpha.id, name: 'バグ', color: 'red' },
      { id: 'label-feat', project_id: alpha.id, name: '機能追加', color: 'blue' },
      { id: 'label-ui', project_id: alpha.id, name: 'UI', color: 'purple' },
      { id: 'label-beta-bug', project_id: beta.id, name: 'Bug', color: 'red' }
    ],
    tickets: [
      { id: 'ALP-00001', project_id: alpha.id, title: 'ログイン画面の不具合', status: 'TODO', priority: 'HIGH', labelIds: ['label-bug'] },
      { id: 'ALP-00002', project_id: alpha.id, title: 'エクスポート機能', status: 'TODO', priority: 'LOW', labelIds: ['label-feat'] },
      { id: 'ALP-00003', project_id: alpha.id, title: 'ダッシュボード崩れ ZQXW', status: 'IN PROGRESS', priority: 'HIGH', labelIds: ['label-bug', 'label-ui'] },
      { id: 'ALP-00004', project_id: alpha.id, title: 'ボタン配色', status: 'TODO', priority: 'MEDIUM', labelIds: ['label-ui'] },
      { id: 'ALP-00005', project_id: alpha.id, title: 'ドキュメント整備', status: 'TODO', priority: 'HIGH', labelIds: [] },
      { id: 'BETA-00001', project_id: beta.id, title: 'Beta のチケット', status: 'TODO', priority: 'MEDIUM', labelIds: ['label-beta-bug'] }
    ]
  });
}

const ALPHA_IDS = ['ALP-00001', 'ALP-00002', 'ALP-00003', 'ALP-00004', 'ALP-00005'];

function expectVisible(visible: string[], all: string[] = ALPHA_IDS) {
  for (const id of all) {
    if (visible.includes(id)) {
      expect(screen.queryByText(id), `${id} should be shown`).toBeInTheDocument();
    } else {
      expect(screen.queryByText(id), `${id} should be hidden`).not.toBeInTheDocument();
    }
  }
}

async function renderApp() {
  const user = userEvent.setup();
  render(<App />);
  await screen.findByText('ALP-00001');
  return user;
}

function labelFilterButton() {
  return screen.getByRole('button', { name: /^ラベル: / });
}

async function toggleLabelInFilter(user: ReturnType<typeof userEvent.setup>, names: string[]) {
  if (labelFilterButton().getAttribute('aria-expanded') !== 'true') {
    await user.click(labelFilterButton());
  }
  const panel = screen.getByRole('group', { name: i18n.t('toolbar.labelGroupLabel') });
  for (const name of names) {
    // The options are the project's labels, which come from their own
    // request (GET /api/projects/{id}/labels), not from the ticket list that
    // renderApp waits for. Seeing a ticket, or that request having been
    // made, does not mean its answer is on screen yet, so wait for the
    // checkbox itself (DFLT-00284).
    await user.click(await within(panel).findByRole('checkbox', { name }));
  }
}

// Opens the settings modal's labels tab on Alpha. The project selector's
// options are the project list (GET /api/projects), another request of its
// own that seeing a ticket does not imply has been answered, so wait for
// Alpha's option before selecting it (DFLT-00284).
async function openLabelsSettings(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole('button', { name: i18n.t('header.settings') }));
  await user.click(screen.getByRole('tab', { name: i18n.t('settings.tabs.labels') }));
  const projectSelect = screen.getByLabelText(i18n.t('settings.labels.projectLabel'));
  await within(projectSelect).findByRole('option', { name: alpha.name });
  await user.selectOptions(projectSelect, alpha.id);
}

describe('App label filter', () => {
  let fetchMock: ReturnType<typeof installFakeBackend>;

  beforeEach(() => {
    seedBackground();
    fetchMock = installFakeBackend(backend);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it('starts unfiltered, showing unlabeled tickets too', async () => {
    await renderApp();
    expect(labelFilterButton()).toHaveTextContent(i18n.t('toolbar.labelAll'));
    expectVisible(ALPHA_IDS);
  });

  it('narrows to tickets with the selected label, OR across several', async () => {
    const user = await renderApp();

    await toggleLabelInFilter(user, ['バグ']);
    expect(labelFilterButton()).toHaveTextContent(i18n.t('toolbar.labelSelected', { count: 1 }));
    expectVisible(['ALP-00001', 'ALP-00003']);

    await toggleLabelInFilter(user, ['機能追加']);
    expect(labelFilterButton()).toHaveTextContent(i18n.t('toolbar.labelSelected', { count: 2 }));
    expectVisible(['ALP-00001', 'ALP-00002', 'ALP-00003']);

    // Unchecking one label removes just its contribution.
    await toggleLabelInFilter(user, ['機能追加']);
    expect(labelFilterButton()).toHaveTextContent(i18n.t('toolbar.labelSelected', { count: 1 }));
    expectVisible(['ALP-00001', 'ALP-00003']);
  });

  it('clears with "clear selection"', async () => {
    const user = await renderApp();
    await toggleLabelInFilter(user, ['バグ', 'UI']);
    expectVisible(['ALP-00001', 'ALP-00003', 'ALP-00004']);

    await user.click(screen.getByRole('button', { name: i18n.t('toolbar.filterClear') }));
    expect(labelFilterButton()).toHaveTextContent(i18n.t('toolbar.labelAll'));
    expectVisible(ALPHA_IDS);
  });

  it('combines with the priority and status filters using AND', async () => {
    // Since DFLT-00086 every filter narrows by CHECKING what to keep; this
    // used to start from "all checked" and uncheck everything else, which is
    // how the same three assertions were reached before.
    const user = await renderApp();

    await user.click(screen.getByRole('button', { name: i18n.t('toolbar.priorityAll') }));
    const priorityPanel = screen.getByRole('group', { name: i18n.t('toolbar.priorityGroupLabel') });
    await user.click(within(priorityPanel).getByRole('checkbox', { name: `↑ ${i18n.t('priority.high')}` }));
    await user.keyboard('{Escape}');
    expectVisible(['ALP-00001', 'ALP-00003', 'ALP-00005']);

    await toggleLabelInFilter(user, ['バグ', 'UI']);
    await user.keyboard('{Escape}');
    expectVisible(['ALP-00001', 'ALP-00003']);

    await user.click(screen.getByRole('button', { name: i18n.t('toolbar.statusAll') }));
    const statusPanel = screen.getByRole('group', { name: i18n.t('toolbar.statusGroupLabel') });
    await user.click(within(statusPanel).getByRole('checkbox', { name: i18n.t('status.todo') }));
    expectVisible(['ALP-00001']);
  });

  it('combines with the search box', async () => {
    const user = await renderApp();
    await user.type(screen.getByPlaceholderText(i18n.t('toolbar.searchPlaceholder')), 'ZQXW');
    await toggleLabelInFilter(user, ['UI']);
    expectVisible(['ALP-00003']);
  });

  it('goes back to page 1 when the label filter changes', async () => {
    // 25 bug tickets plus 5 others: 3 pages unfiltered, and still 3 pages
    // with "バグ" selected, so landing on page 1 is the reset, not a clamp.
    backend.tickets = [];
    for (let i = 1; i <= 30; i++) {
      backend.tickets.push({
        id: `ALP-${String(i).padStart(5, '0')}`,
        project_id: alpha.id,
        title: `チケット ${i}`,
        status: 'TODO',
        priority: 'MEDIUM',
        labelIds: i <= 25 ? ['label-bug'] : []
      });
    }
    const user = await renderApp();

    // The pager's "next" button, found by its accessible name.
    const nextPage = () => screen.getByRole('button', { name: i18n.t('pagination.next') });
    await user.click(nextPage());
    expect(screen.getByText(i18n.t('pagination.pageOf', { page: 2, total: 3 }))).toBeInTheDocument();

    await toggleLabelInFilter(user, ['バグ']);
    expect(screen.getByText(i18n.t('pagination.pageOf', { page: 1, total: 3 }))).toBeInTheDocument();
  });

  it('drops a deleted label from the selection', async () => {
    const user = await renderApp();
    await toggleLabelInFilter(user, ['バグ', 'UI']);
    await user.keyboard('{Escape}');
    expect(labelFilterButton()).toHaveTextContent(i18n.t('toolbar.labelSelected', { count: 2 }));

    await openLabelsSettings(user);
    const row = await screen.findByTestId('label-row-label-ui');
    await user.click(within(row).getByRole('button', { name: `${i18n.t('settings.labels.delete')}: UI` }));
    // The in-app confirmation (DFLT-00148) opens once the usage count is re-read.
    await user.click(await screen.findByTestId('label-delete-confirm-confirm'));

    await waitFor(() => expect(labelFilterButton()).toHaveTextContent(i18n.t('toolbar.labelSelected', { count: 1 })));
    await user.click(screen.getByRole('button', { name: i18n.t('common.closeDialog') }));
    await user.click(labelFilterButton());
    const panel = screen.getByRole('group', { name: i18n.t('toolbar.labelGroupLabel') });
    expect(within(panel).getByRole('checkbox', { name: 'バグ' })).toBeChecked();
    expect(within(panel).queryByRole('checkbox', { name: 'UI' })).not.toBeInTheDocument();
  });

  it('drops the previous project selection on a project switch', async () => {
    const user = await renderApp();
    await toggleLabelInFilter(user, ['バグ']);
    await user.keyboard('{Escape}');

    await user.click(screen.getByRole('button', { name: /Alpha/ }));
    // The switcher lists the project list's answer (GET /api/projects); see
    // openLabelsSettings.
    await user.click(await screen.findByRole('button', { name: /Beta/ }));

    await screen.findByText('BETA-00001');
    await waitFor(() => expect(labelFilterButton()).toHaveTextContent(i18n.t('toolbar.labelAll')));
    await user.click(labelFilterButton());
    const panel = screen.getByRole('group', { name: i18n.t('toolbar.labelGroupLabel') });
    await waitFor(() => expect(within(panel).getAllByRole('checkbox').map(c => c.getAttribute('aria-label'))).toEqual(['Bug']));
  });

  it('shows a label renamed and recolored in settings on every ticket row', async () => {
    const user = await renderApp();
    const rowChip = (id: string) =>
      within(screen.getByText(id).closest('.rounded-xl') as HTMLElement)
        .getAllByTestId('label-chip')
        .find(c => c.textContent === 'バグ' || c.textContent === '不具合');
    expect(rowChip('ALP-00001')).toHaveAttribute('data-label-color', 'red');

    await openLabelsSettings(user);
    const row = await screen.findByTestId('label-row-label-bug');
    await user.click(within(row).getByRole('button', { name: `${i18n.t('settings.labels.rename')}: バグ` }));
    const input = within(row).getByRole('textbox');
    await user.clear(input);
    await user.type(input, '不具合');
    await user.click(within(row).getByRole('button', { name: i18n.t('settings.labels.save') }));
    await waitFor(() => expect(within(screen.getByTestId('label-row-label-bug')).getByTestId('label-chip')).toHaveTextContent('不具合'));
    await user.click(within(screen.getByTestId('label-row-label-bug')).getByRole('button', { name: i18n.t('labels.colors.pink') }));
    await user.click(screen.getByRole('button', { name: i18n.t('common.closeDialog') }));

    for (const id of ['ALP-00001', 'ALP-00003']) {
      await waitFor(() => {
        const chip = rowChip(id);
        expect(chip).toHaveTextContent('不具合');
        expect(chip).toHaveAttribute('data-label-color', 'pink');
      });
    }
  });

  // DFLT-00310: App installs the copy listener (lib/plainCopy), so copying a
  // metadata bar label puts it on the clipboard without U+200B / U+2060.
  it('copies a metadata bar label without its invisible characters', async () => {
    // TicketItem measures its graph panel with a ResizeObserver, which jsdom lacks.
    vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
    const user = await renderApp();
    const card = document.getElementById('ticket-ALP-00001') as HTMLElement;
    await user.click(within(card).getByTestId('ticket-header-row'));
    const details = await within(card).findByTestId('ticket-details');
    const label = Array.from(details.querySelectorAll<HTMLElement>(`[${PLAIN_COPY_ATTR}]`)).find(
      el => plainText(el.textContent ?? '') === '作成日時:'
    ) as HTMLElement;
    expect(label.textContent).toContain(WORD_JOINER);
    selectContents(label);
    const { event, written } = dispatchCopy(label);
    document.getSelection()?.removeAllRanges();
    expect(event.defaultPrevented).toBe(true);
    expect(written['text/plain']).toBe('作成日時:');
    for (const ch of [ZWSP, WORD_JOINER]) {
      expect(written['text/plain']).not.toContain(ch);
      expect(written['text/html']).not.toContain(ch);
    }
    });

  // DFLT-00296: two label re-fetches in flight at once. The older one's
  // answer arrives last but must not replace the newer list.
  it('keeps the newer label list when an older label request answers last', async () => {
    const labelsURL = `/api/projects/${alpha.id}/labels`;
    // Startup asks for the labels twice: once from the current-project
    // effect and once from the lastFetchedAt effect after the first ticket
    // fetch (App.tsx). Hold nothing until both have been made, so the second
    // startup request cannot be mistaken for the one this test holds.
    const STARTUP_LABEL_CALLS = 2;
    let labelCalls = 0;
    let armed = false;
    let heldReached = false;
    let release!: () => void;
    const gate = new Promise<void>(resolve => (release = resolve));
    let heldPromise: Promise<Response> | null = null;
    fetchMock.mockImplementation((input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input) !== labelsURL) return backend.fetch(input, init);
      labelCalls++;
      if (!armed || heldPromise) return backend.fetch(input, init);
      heldPromise = (async () => {
        // Build the answer now, from the data as it is at request time, and
        // only then wait: the held request answers with the OLD list.
        const res = await backend.fetch(input, init);
        heldReached = true;
        await gate;
        return res;
      })();
      return heldPromise;
    });

    const user = await renderApp();
    const refreshButton = screen.getByRole('button', { name: i18n.t('toolbar.refreshTitle') });
    await user.click(labelFilterButton());
    await within(screen.getByRole('group', { name: i18n.t('toolbar.labelGroupLabel') })).findByRole('checkbox', { name: 'UI' });
    await user.keyboard('{Escape}');
    await waitFor(() => {
      expect(refreshButton).toBeEnabled();
      expect(labelCalls).toBe(STARTUP_LABEL_CALLS);
    });
    armed = true;

    // Request A: the labels re-fetch that follows this refresh's ticket
    // fetch. Wait until it has reached the backend and been answered with
    // the current (old) list, and is held there.
    await user.click(refreshButton);
    await waitFor(() => expect(heldReached).toBe(true));

    // Only now change the data, so A's answer is the old list.
    backend.labels.find(l => l.id === 'label-ui')!.name = '画面';

    // Request B: a later refresh, whose labels request is not held.
    await waitFor(() => expect(refreshButton).toBeEnabled());
    await user.click(refreshButton);
    await user.click(labelFilterButton());
    const panel = screen.getByRole('group', { name: i18n.t('toolbar.labelGroupLabel') });
    await within(panel).findByRole('checkbox', { name: '画面' });

    // Let A answer, and let whatever follows its await run to completion.
    await act(async () => {
      release();
      await heldPromise;
    });
    await act(async () => {});

    expect(within(panel).getByRole('checkbox', { name: '画面' })).toBeInTheDocument();
    expect(within(panel).queryByRole('checkbox', { name: 'UI' })).not.toBeInTheDocument();
  });
});
