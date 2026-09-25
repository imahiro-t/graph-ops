// DFLT-00086: the four toolbar filters as wired into App.tsx -- that they
// really are one control repeated four times, that an empty selection means
// "all" everywhere, and how their selections narrow the ticket list (OR
// within a filter, AND between filters and with the search box).
//
// The individual pieces have their own tests (MultiSelectFilter.test.tsx,
// assigneeFilter.test.ts, statusMeta.test.ts, priorityMeta.test.ts); these
// need App's own state, so fetch is served by the shared in-memory fake in
// test/fakeBackend.ts.
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from './i18n';
import App from './App';
import { Project } from './types';
import { FakeBackend, createFakeBackend, installFakeBackend } from './test/fakeBackend';

const project: Project = {
  id: 'p-default',
  name: 'Default',
  prefix: 'DFLT',
  local_path: '/work/default',
  created_at: '',
  updated_at: ''
};

const ALL_IDS = ['DFLT-00001', 'DFLT-00002', 'DFLT-00003', 'DFLT-00004', 'DFLT-00005'];

let backend: FakeBackend;

function seedBackground(paginationPageSize?: number) {
  backend = createFakeBackend({
    projects: [project],
    currentProjectId: project.id,
    paginationPageSize,
    labels: [
      { id: 'label-ui', project_id: project.id, name: 'UI改善', color: 'green' },
      { id: 'label-bug', project_id: project.id, name: 'バグ修正', color: 'red' }
    ],
    tickets: [
      { id: 'DFLT-00001', project_id: project.id, title: 'チケットA', status: 'TODO', assignee: '佐藤', priority: 'HIGH', labelIds: ['label-ui'] },
      { id: 'DFLT-00002', project_id: project.id, title: 'チケットB', status: 'IN PROGRESS', assignee: '佐藤', priority: 'MEDIUM', labelIds: [] },
      { id: 'DFLT-00003', project_id: project.id, title: 'チケットC', status: 'DONE', assignee: '鈴木', priority: 'LOW', labelIds: ['label-bug'] },
      { id: 'DFLT-00004', project_id: project.id, title: 'チケットD', status: 'TODO', assignee: null, priority: 'MEDIUM', labelIds: ['label-ui'] },
      { id: 'DFLT-00005', project_id: project.id, title: 'チケットE', status: 'IN REVIEW', assignee: '田中', priority: 'HIGH', labelIds: [] }
    ]
  });
}

// The four filters, by the only thing that differs between them: their panel
// id, their i18n keys and the option labels they offer. Every test below
// drives them through the same helpers, which is itself part of what is
// being asserted -- if one filter stopped behaving like the others, these
// would stop working for it.
const FILTERS = {
  status: { panelId: 'toolbar-status-filter-panel', prefix: 'status' },
  assignee: { panelId: 'toolbar-assignee-filter-panel', prefix: 'assignee' },
  priority: { panelId: 'toolbar-priority-filter-panel', prefix: 'priority' },
  label: { panelId: 'toolbar-label-filter-panel', prefix: 'label' }
} as const;
type FilterName = keyof typeof FILTERS;
const FILTER_NAMES = Object.keys(FILTERS) as FilterName[];

const allText = (f: FilterName) => i18n.t(`toolbar.${FILTERS[f].prefix}All`);
const selectedText = (f: FilterName, count: number) =>
  i18n.t(`toolbar.${FILTERS[f].prefix}Selected`, { count });
const groupLabel = (f: FilterName) => i18n.t(`toolbar.${FILTERS[f].prefix}GroupLabel`);

// Found by its `<panelId>-trigger` testid rather than by its text, so a test
// can look the trigger up without already knowing what it currently says.
// (Not by aria-controls: since DFLT-00087 a closed trigger has none, because
// the panel it would point at is not in the DOM.)
function trigger(f: FilterName): HTMLButtonElement {
  return screen.getByTestId(`${FILTERS[f].panelId}-trigger`) as HTMLButtonElement;
}

const panel = (f: FilterName) => screen.getByRole('group', { name: groupLabel(f) });

type User = ReturnType<typeof userEvent.setup>;

async function open(user: User, f: FilterName) {
  if (trigger(f).getAttribute('aria-expanded') !== 'true') await user.click(trigger(f));
  return panel(f);
}

async function close(user: User, f: FilterName) {
  await user.click(screen.getByTestId(`${FILTERS[f].panelId}-overlay`));
}

// Toggles options by their visible text, leaving the panel open.
async function toggle(user: User, f: FilterName, optionLabels: string[]) {
  const p = await open(user, f);
  for (const name of optionLabels) await user.click(within(p).getByRole('checkbox', { name }));
  return p;
}

// Narrows by the given options and closes the panel again.
async function filterBy(user: User, f: FilterName, optionLabels: string[]) {
  await toggle(user, f, optionLabels);
  await close(user, f);
}

const clearButton = (f: FilterName) =>
  within(panel(f)).getByRole('button', { name: i18n.t('toolbar.filterClear') });

// Option labels, spelled the way the panel does. All of these are resolved
// when a test runs, never at module level: setup.ts only pins the language
// in beforeAll, and an it.each table built at collection time would still
// hold the English wording.
const status = (key: string) => i18n.t(`status.${key}`);
const ALL_STATUSES = () =>
  ['todo', 'refined', 'inProgress', 'inReview', 'inRelease', 'done', 'closed'].map(status);
const priority = (key: 'high' | 'medium' | 'low') =>
  `${{ high: '↑', medium: '−', low: '↓' }[key]} ${i18n.t(`priority.${key}`)}`;
const unassigned = () => i18n.t('toolbar.assigneeUnassigned');

function expectVisible(visible: string[]) {
  for (const id of ALL_IDS) {
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
  await screen.findByText('DFLT-00001');
  await waitFor(() =>
    expect(fetchMock.mock.calls.some(c => String(c[0]) === `/api/projects/${project.id}/labels`)).toBe(true)
  );
  return user;
}

let fetchMock: ReturnType<typeof vi.fn>;

describe('App toolbar filters', () => {
  beforeEach(() => {
    seedBackground();
    fetchMock = installFakeBackend(backend);
  });

  afterEach(async () => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    // Some tests switch to English; setup.ts only pins the language once.
    if (i18n.language !== 'ja') await i18n.changeLanguage('ja');
  });

  describe('nothing selected means everything', () => {
    it('starts with all four filters showing "All" and every ticket listed', async () => {
      await renderApp();
      for (const f of FILTER_NAMES) expect(trigger(f)).toHaveTextContent(allText(f));
      expectVisible(ALL_IDS);
    });

    it('starts with no checkbox checked in any panel', async () => {
      const user = await renderApp();
      for (const f of FILTER_NAMES) {
        const p = await open(user, f);
        for (const box of within(p).getAllByRole('checkbox')) expect(box).not.toBeChecked();
        await close(user, f);
      }
    });

    it.each([
      ['status' as const, ALL_STATUSES],
      ['priority' as const, () => [priority('high'), priority('medium'), priority('low')]]
    ])('leaves the list full after checking and unchecking every %s option', async (f, allOptions) => {
      // The old dead end: these two filters started fully checked, so
      // clearing them emptied the list with no way back.
      const user = await renderApp();
      const options = allOptions();
      await toggle(user, f, options);
      expect(trigger(f)).toHaveTextContent(selectedText(f, options.length));
      expectVisible(ALL_IDS);

      await toggle(user, f, options);
      expect(trigger(f)).toHaveTextContent(allText(f));
      expectVisible(ALL_IDS);
    });

    it('never labels a trigger "none selected"', async () => {
      await renderApp();
      for (const text of ['ステータス: 未選択', '優先度: 未選択']) {
        expect(screen.queryByText(text)).not.toBeInTheDocument();
      }
    });
  });

  describe('the same control four times', () => {
    it.each(FILTER_NAMES)('gives the %s filter the shared ARIA wiring', async f => {
      const user = await renderApp();
      // Closed: no aria-controls at all, and indeed nothing to point at.
      expect(trigger(f)).toHaveAttribute('aria-expanded', 'false');
      expect(trigger(f)).not.toHaveAttribute('aria-controls');
      expect(document.getElementById(FILTERS[f].panelId)).toBeNull();

      const p = await open(user, f);
      expect(trigger(f)).toHaveAttribute('aria-expanded', 'true');
      expect(trigger(f)).toHaveAttribute('aria-controls', FILTERS[f].panelId);
      expect(document.getElementById(FILTERS[f].panelId)).toBe(p);
      expect(p).toBeVisible();
    });

    // DFLT-00087: every way of closing leaves no dangling aria-controls, and
    // reopening points it at a panel that exists again. aria-controls depends
    // only on isOpen, but all three closings are run for all four filters so
    // no combination is left to inference.
    const CLOSINGS = {
      Escape: async (user: User) => {
        await user.keyboard('{Escape}');
      },
      'an outside click': async (user: User, f: FilterName) => {
        await close(user, f);
      },
      'pressing the trigger again': async (user: User, f: FilterName) => {
        await user.click(trigger(f));
      }
    } as const;
    it.each(
      FILTER_NAMES.flatMap(f =>
        (Object.keys(CLOSINGS) as (keyof typeof CLOSINGS)[]).map(how => [f, how] as const)
      )
    )('leaves no dangling aria-controls on the %s filter after closing by %s', async (f, how) => {
      const user = await renderApp();
      await open(user, f);
      await CLOSINGS[how](user, f);
      expect(screen.queryByRole('group', { name: groupLabel(f) })).not.toBeInTheDocument();
      expect(trigger(f)).toHaveAttribute('aria-expanded', 'false');
      expect(trigger(f)).not.toHaveAttribute('aria-controls');

      await user.click(trigger(f));
      const id = trigger(f).getAttribute('aria-controls');
      expect(id).toBe(FILTERS[f].panelId);
      expect(document.getElementById(id!)).toBeInTheDocument();
    });

    it('gives each filter its own panel id', async () => {
      const user = await renderApp();
      const ids: string[] = [];
      // One at a time: opening a panel lays an overlay over the toolbar, so
      // two of them are never open together in a real browser.
      for (const f of FILTER_NAMES) {
        ids.push((await open(user, f)).id);
        await close(user, f);
      }
      expect(new Set(ids).size).toBe(FILTER_NAMES.length);
    });

    it.each(FILTER_NAMES)('closes the %s filter on an outside click', async f => {
      const user = await renderApp();
      await open(user, f);
      await close(user, f);
      expect(screen.queryByRole('group', { name: groupLabel(f) })).not.toBeInTheDocument();
      expect(trigger(f)).toHaveAttribute('aria-expanded', 'false');
    });

    it.each(FILTER_NAMES)('closes the %s filter on Escape, back onto its trigger', async f => {
      const user = await renderApp();
      await open(user, f);
      await user.keyboard('{Escape}');
      expect(screen.queryByRole('group', { name: groupLabel(f) })).not.toBeInTheDocument();
      expect(trigger(f)).toHaveFocus();
    });

    it.each([
      ['status' as const, () => [status('todo'), status('done')]],
      ['assignee' as const, () => ['佐藤', unassigned()]],
      ['priority' as const, () => [priority('high'), priority('low')]],
      ['label' as const, () => ['UI改善', 'バグ修正']]
    ])('keeps the %s panel open while several options are ticked', async (f, pick) => {
      // The assignee filter used to close on every pick, being a radio group.
      const user = await renderApp();
      const options = pick();
      const p = await toggle(user, f, options);
      expect(p).toBeInTheDocument();
      for (const name of options) expect(within(p).getByRole('checkbox', { name })).toBeChecked();
    });

    it('offers the assignee filter as checkboxes, with no "all" entry', async () => {
      const user = await renderApp();
      const p = await open(user, 'assignee');
      expect(within(p).getAllByRole('checkbox').length).toBeGreaterThan(0);
      expect(within(p).queryAllByRole('radio')).toHaveLength(0);
      expect(within(p).queryByText(i18n.t('toolbar.assigneeAll'))).not.toBeInTheDocument();
    });
  });

  describe('the clear button', () => {
    it.each([
      ['status' as const, () => [status('todo')]],
      ['assignee' as const, () => ['佐藤']],
      ['priority' as const, () => [priority('high')]],
      ['label' as const, () => ['UI改善']]
    ])('returns the %s filter to "All"', async (f, pick) => {
      const user = await renderApp();
      await toggle(user, f, pick());
      expect(clearButton(f)).toBeEnabled();

      await user.click(clearButton(f));
      expect(trigger(f)).toHaveTextContent(allText(f));
      for (const box of within(panel(f)).getAllByRole('checkbox')) expect(box).not.toBeChecked();
      await close(user, f);
      expectVisible(ALL_IDS);
    });

    // DFLT-00087: the clear button disables itself when pressed; focus used to
    // fall to <body>, out of reach of the panel's Escape handler.
    it.each([
      ['status' as const, () => [status('todo')]],
      ['assignee' as const, () => ['佐藤']],
      ['priority' as const, () => [priority('high')]],
      ['label' as const, () => ['UI改善']]
    ])('moves focus to the %s trigger, so Escape still closes the panel', async (f, pick) => {
      const user = await renderApp();
      await toggle(user, f, pick());
      await user.click(clearButton(f));

      expect(trigger(f)).toHaveFocus();
      expect(document.activeElement).not.toBe(document.body);
      expect(trigger(f)).toHaveTextContent(allText(f));
      expect(panel(f)).toBeInTheDocument();
      expect(clearButton(f)).toBeDisabled();

      await user.keyboard('{Escape}');
      expect(screen.queryByRole('group', { name: groupLabel(f) })).not.toBeInTheDocument();
      expect(trigger(f)).toHaveAttribute('aria-expanded', 'false');
      expect(trigger(f)).toHaveFocus();
      expectVisible(ALL_IDS);
    });

    it.each(FILTER_NAMES)('is present but disabled in the %s panel while nothing is selected', async f => {
      const user = await renderApp();
      await open(user, f);
      expect(clearButton(f)).toBeDisabled();
    });

    it('leaves the other filters alone', async () => {
      const user = await renderApp();
      await filterBy(user, 'status', [status('todo')]);
      await filterBy(user, 'assignee', ['佐藤']);
      expectVisible(['DFLT-00001']);

      await open(user, 'status');
      await user.click(clearButton('status'));
      await close(user, 'status');
      expect(trigger('status')).toHaveTextContent(allText('status'));
      expect(trigger('assignee')).toHaveTextContent(selectedText('assignee', 1));
      expectVisible(['DFLT-00001', 'DFLT-00002']);
    });
  });

  describe('OR within a filter', () => {
    it('matches several statuses', async () => {
      const user = await renderApp();
      await toggle(user, 'status', [status('todo')]);
      expect(trigger('status')).toHaveTextContent(selectedText('status', 1));
      await close(user, 'status');
      expectVisible(['DFLT-00001', 'DFLT-00004']);

      await filterBy(user, 'status', [status('done')]);
      expect(trigger('status')).toHaveTextContent(selectedText('status', 2));
      expectVisible(['DFLT-00001', 'DFLT-00003', 'DFLT-00004']);
    });

    it('matches several priorities', async () => {
      const user = await renderApp();
      await filterBy(user, 'priority', [priority('high'), priority('low')]);
      expect(trigger('priority')).toHaveTextContent(selectedText('priority', 2));
      expectVisible(['DFLT-00001', 'DFLT-00003', 'DFLT-00005']);
    });

    it('matches several labels', async () => {
      const user = await renderApp();
      await filterBy(user, 'label', ['UI改善', 'バグ修正']);
      expect(trigger('label')).toHaveTextContent(selectedText('label', 2));
      expectVisible(['DFLT-00001', 'DFLT-00003', 'DFLT-00004']);
    });

    it('matches several assignees', async () => {
      const user = await renderApp();
      await filterBy(user, 'assignee', ['佐藤', '鈴木']);
      expect(trigger('assignee')).toHaveTextContent(selectedText('assignee', 2));
      expectVisible(['DFLT-00001', 'DFLT-00002', 'DFLT-00003']);
    });

    it('files a ticket whose DB status is not a known one under TODO', async () => {
      backend.tickets.push({
        id: 'DFLT-00006',
        project_id: project.id,
        title: 'チケットF',
        status: 'SOMETHING ELSE' as never,
        assignee: null,
        priority: 'MEDIUM',
        labelIds: []
      });
      const user = await renderApp();
      await filterBy(user, 'status', [status('todo')]);
      expect(screen.getByText('DFLT-00006')).toBeInTheDocument();
    });
  });

  describe('the assignee filter', () => {
    it('lists the unassigned bucket first, then the loaded assignees', async () => {
      const user = await renderApp();
      const p = await open(user, 'assignee');
      expect(within(p).getAllByRole('checkbox').map(b => b.closest('label')!.textContent)).toEqual([
        unassigned(),
        '佐藤',
        '田中',
        '鈴木'
      ]);
    });

    it('narrows to unassigned tickets', async () => {
      const user = await renderApp();
      await filterBy(user, 'assignee', [unassigned()]);
      expect(trigger('assignee')).toHaveTextContent(selectedText('assignee', 1));
      expectVisible(['DFLT-00004']);
    });

    it('combines the unassigned bucket with a named assignee', async () => {
      const user = await renderApp();
      await filterBy(user, 'assignee', [unassigned(), '田中']);
      expectVisible(['DFLT-00004', 'DFLT-00005']);
    });

    it('counts the selection instead of naming it', async () => {
      const user = await renderApp();
      await filterBy(user, 'assignee', ['佐藤']);
      expect(trigger('assignee')).toHaveTextContent(selectedText('assignee', 1));
      expect(trigger('assignee')).not.toHaveTextContent('佐藤');
    });

    it('keeps filtering by an assignee who has dropped out of the options', async () => {
      const user = await renderApp();
      await filterBy(user, 'assignee', ['田中']);
      expectVisible(['DFLT-00005']);

      backend.tickets.find(tk => tk.id === 'DFLT-00005')!.assignee = null;
      await user.click(screen.getByTitle(i18n.t('toolbar.refreshTitle')));

      await waitFor(() => expectVisible([]));
      expect(trigger('assignee')).toHaveTextContent(selectedText('assignee', 1));
      const p = await open(user, 'assignee');
      expect(within(p).queryByRole('checkbox', { name: '田中' })).not.toBeInTheDocument();
    });

    // DFLT-00087: checking another box used to rebuild the selection from the
    // current options only, silently dropping the vanished name.
    it('keeps a vanished assignee selected when another one is checked', async () => {
      const user = await renderApp();
      await filterBy(user, 'assignee', ['田中']);

      backend.tickets.find(tk => tk.id === 'DFLT-00005')!.assignee = null;
      await user.click(screen.getByTitle(i18n.t('toolbar.refreshTitle')));
      await waitFor(() => expectVisible([]));
      expect(within(await open(user, 'assignee')).queryByRole('checkbox', { name: '田中' })).not.toBeInTheDocument();

      await toggle(user, 'assignee', ['佐藤']);
      expect(trigger('assignee')).toHaveTextContent(selectedText('assignee', 2));
      await close(user, 'assignee');
      expectVisible(['DFLT-00001', 'DFLT-00002']);

      // Bring 田中 back: the name must still be selected, which the UI shows
      // as a ticked box and DFLT-00005 reappearing.
      backend.tickets.find(tk => tk.id === 'DFLT-00005')!.assignee = '田中';
      await user.click(screen.getByTitle(i18n.t('toolbar.refreshTitle')));
      await waitFor(() => expectVisible(['DFLT-00001', 'DFLT-00002', 'DFLT-00005']));
      const p = await open(user, 'assignee');
      expect(within(p).getByRole('checkbox', { name: '田中' })).toBeChecked();
      expect(within(p).getByRole('checkbox', { name: '佐藤' })).toBeChecked();
      expect(trigger('assignee')).toHaveTextContent(selectedText('assignee', 2));
    });
  });

  describe('AND between filters', () => {
    it('combines two filters', async () => {
      const user = await renderApp();
      await filterBy(user, 'status', [status('todo')]);
      await filterBy(user, 'priority', [priority('medium')]);
      expectVisible(['DFLT-00004']);
    });

    it('combines three filters', async () => {
      const user = await renderApp();
      await filterBy(user, 'status', [status('todo')]);
      await filterBy(user, 'assignee', ['佐藤']);
      await filterBy(user, 'label', ['UI改善']);
      expectVisible(['DFLT-00001']);
    });

    it('combines with the search box', async () => {
      const user = await renderApp();
      await filterBy(user, 'status', [status('todo')]);
      await user.type(screen.getByPlaceholderText(i18n.t('toolbar.searchPlaceholder')), 'チケットD');
      expectVisible(['DFLT-00004']);
    });
  });

  describe('trigger wording', () => {
    it.each([
      ['status' as const, () => [status('todo'), status('done')]],
      ['assignee' as const, () => ['佐藤', '鈴木']],
      ['priority' as const, () => [priority('high'), priority('low')]],
      ['label' as const, () => ['UI改善', 'バグ修正']]
    ])('goes All -> 1 selected -> 2 selected for the %s filter', async (f, pick) => {
      const user = await renderApp();
      const options = pick();
      expect(trigger(f)).toHaveTextContent(allText(f));
      await toggle(user, f, [options[0]]);
      expect(trigger(f)).toHaveTextContent(selectedText(f, 1));
      await toggle(user, f, [options[1]]);
      expect(trigger(f)).toHaveTextContent(selectedText(f, 2));
    });

    it('is translated, clear button included', async () => {
      await i18n.changeLanguage('en');
      const user = await renderApp();
      expect(trigger('status')).toHaveTextContent('Status: All');
      await open(user, 'status');
      expect(within(panel('status')).getByRole('button', { name: 'Clear selection' })).toBeInTheDocument();
    });
  });

  it('returns to page 1 when a filter changes', async () => {
    // One ticket per page: 5 pages unfiltered, still 2 with TODO selected,
    // so landing on page 1 is the reset, not a clamp onto a shorter list.
    seedBackground(1);
    fetchMock = installFakeBackend(backend);
    const user = await renderApp();

    // The pager's "next" button, found by its accessible name.
    const nextPage = screen.getByRole('button', { name: i18n.t('pagination.next') });
    await user.click(nextPage);
    expect(screen.getByText(i18n.t('pagination.pageOf', { page: 2, total: 5 }))).toBeInTheDocument();

    await filterBy(user, 'status', [status('todo')]);
    expect(screen.getByText(i18n.t('pagination.pageOf', { page: 1, total: 2 }))).toBeInTheDocument();
  });
});
