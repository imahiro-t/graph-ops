// Regression coverage for behaviour DFLT-00023 left unverified (5-5) and for
// F-1's structural non-regression (language switch must never re-trigger
// this tab's load). See this ticket's plan section 4-2.
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../../i18n';
import { AppSettingsEditor } from './AppSettingsEditor';
import { REDACTED_SECRET_PLACEHOLDER, AppSettingsResponse, Project } from '../../types';

vi.mock('../../lib/settingsApi', async () => {
  const actual = await vi.importActual<typeof import('../../lib/settingsApi')>('../../lib/settingsApi');
  return {
    ...actual,
    fetchAppSettings: vi.fn(),
    saveAppSettings: vi.fn(),
    testMySQLConnection: vi.fn()
  };
});

import { fetchAppSettings, saveAppSettings, testMySQLConnection } from '../../lib/settingsApi';

const mockedFetchAppSettings = fetchAppSettings as unknown as ReturnType<typeof vi.fn>;
const mockedSaveAppSettings = saveAppSettings as unknown as ReturnType<typeof vi.fn>;
const mockedTestMySQLConnection = testMySQLConnection as unknown as ReturnType<typeof vi.fn>;

function makeResponse(overrides: Partial<AppSettingsResponse['file']> = {}): AppSettingsResponse {
  return {
    file: {
      dbBackend: 'sqlite',
      dbPath: '',
      mysqlHost: '',
      mysqlPort: 3306,
      mysqlDatabase: '',
      mysqlUser: '',
      mysqlPassword: '',
      mysqlTls: 'verify-full',
      mysqlTlsCa: '',
      artifactsDir: '',
      teamExtensionsDir: '',
      paginationPageSize: 10,
      myName: '',
      ...overrides
    },
    effective: {
      dbBackend: 'sqlite',
      dbPath: '/tmp/graph.db',
      artifactsDir: '/tmp/artifacts',
      userExtensionsDir: '/tmp/extensions',
      teamExtensionsDir: '',
      paginationPageSize: 10
    },
    config_path: '/home/me/.graph-ops/config.json'
  };
}

function renderEditor() {
  return render(
    <AppSettingsEditor
      projects={[]}
      onDirtyChange={vi.fn()}
      onProjectsChanged={vi.fn()}
      onPaginationPageSizeChanged={vi.fn()}
      onMyNameChanged={vi.fn()}
    />
  );
}

describe('AppSettingsEditor', () => {
  beforeEach(() => {
    mockedFetchAppSettings.mockReset();
    mockedSaveAppSettings.mockReset();
  });

  afterEach(async () => {
    // Some tests deliberately switch language -- restore the fixture default
    // (setup.ts's beforeAll) so it never leaks into the next test.
    await i18n.changeLanguage('ja');
  });

  it('M-5: retyping the MySQL password in plaintext and saving returns the field to the redacted state', async () => {
    const user = userEvent.setup();
    mockedFetchAppSettings.mockResolvedValueOnce(
      makeResponse({ dbBackend: 'mysql', mysqlHost: 'db.example.com', mysqlDatabase: 'graphops', mysqlUser: 'admin', mysqlPassword: '' })
    );
    renderEditor();

    const passwordInput = await screen.findByLabelText(i18n.t('settings.appSettings.storage.mysqlPasswordLabel'));
    await user.type(passwordInput, 'plaintext-secret');
    expect(screen.getByText(i18n.t('settings.appSettings.storage.mysqlPasswordPlaintextHint'))).toBeInTheDocument();

    mockedSaveAppSettings.mockResolvedValueOnce(
      makeResponse({ dbBackend: 'mysql', mysqlHost: 'db.example.com', mysqlDatabase: 'graphops', mysqlUser: 'admin', mysqlPassword: REDACTED_SECRET_PLACEHOLDER })
    );

    const saveButton = screen.getByRole('button', { name: i18n.t('settings.common.save') });
    await user.click(saveButton);

    await waitFor(() => expect(mockedSaveAppSettings).toHaveBeenCalledTimes(1));
    await waitFor(() => {
      expect(screen.getByLabelText(i18n.t('settings.appSettings.storage.mysqlPasswordLabel'))).toHaveValue('');
    });
    expect(screen.getByText(i18n.t('settings.appSettings.storage.mysqlPasswordSavedHint'))).toBeInTheDocument();
    expect(screen.queryByText(i18n.t('settings.appSettings.storage.mysqlPasswordPlaintextHint'))).not.toBeInTheDocument();
    expect(screen.getByText(i18n.t('settings.common.noChangesToSave'))).toBeInTheDocument();
  });

  it('Q-1: changing the MySQL target then switching to sqlite still blocks save on the retype requirement', async () => {
    const user = userEvent.setup();
    mockedFetchAppSettings.mockResolvedValueOnce(
      makeResponse({
        dbBackend: 'mysql',
        mysqlHost: 'db.example.com',
        mysqlDatabase: 'graphops',
        mysqlUser: 'admin',
        mysqlPassword: REDACTED_SECRET_PLACEHOLDER
      })
    );
    renderEditor();

    const hostInput = await screen.findByLabelText(i18n.t('settings.appSettings.storage.mysqlHostLabel'));
    await user.clear(hostInput);
    await user.type(hostInput, 'other-host.example.com');

    const sqliteRadio = screen.getByRole('radio', { name: i18n.t('settings.appSettings.storage.dbBackendSqlite') });
    await user.click(sqliteRadio);

    const saveButton = screen.getByRole('button', { name: i18n.t('settings.common.save') });
    expect(saveButton).toHaveAttribute('aria-disabled', 'true');
    expect(saveButton).toHaveAttribute('aria-describedby', 'save-blocked-reason');
    expect(document.getElementById('save-blocked-reason')).toHaveTextContent(
      i18n.t('settings.appSettings.storage.mysqlPasswordRetypeOtherBackendHint')
    );

    await user.click(saveButton);
    expect(mockedSaveAppSettings).not.toHaveBeenCalled();
  });

  it('Q-2: clearing the port field on an already-saved port 3306 connection does not require a password retype', async () => {
    const user = userEvent.setup();
    mockedFetchAppSettings.mockResolvedValueOnce(
      makeResponse({
        dbBackend: 'mysql',
        mysqlHost: 'db.example.com',
        mysqlDatabase: 'graphops',
        mysqlUser: 'admin',
        mysqlPassword: REDACTED_SECRET_PLACEHOLDER,
        mysqlPort: 3306
      })
    );
    renderEditor();

    const portInput = await screen.findByLabelText(i18n.t('settings.appSettings.storage.mysqlPortLabel'));
    await user.clear(portInput);

    expect(screen.queryByText(i18n.t('settings.appSettings.storage.mysqlPasswordRetypeHint'))).not.toBeInTheDocument();
    const passwordInput = screen.getByLabelText(i18n.t('settings.appSettings.storage.mysqlPasswordLabel'));
    expect(passwordInput).toHaveAttribute('aria-invalid', 'false');
  });

  it('A-3: a blocked save button is aria-disabled (not disabled) with its reason wired via aria-describedby, and does not save on click', async () => {
    const user = userEvent.setup();
    mockedFetchAppSettings.mockResolvedValueOnce(makeResponse());
    renderEditor();

    const saveButton = await screen.findByRole('button', { name: i18n.t('settings.common.save') });
    // Freshly loaded: nothing has been edited yet, so save is blocked on
    // "no changes to save" -- this is the A-3 regression this test guards:
    // a future change back to a plain `disabled` attribute would remove the
    // button from the tab order and hide this reason from assistive tech.
    expect(saveButton).not.toHaveAttribute('disabled');
    expect(saveButton).toHaveAttribute('aria-disabled', 'true');
    expect(saveButton).toHaveAttribute('aria-describedby', 'save-blocked-reason');
    expect(document.getElementById('save-blocked-reason')).toHaveTextContent(i18n.t('settings.common.noChangesToSave'));

    await user.click(saveButton);
    expect(mockedSaveAppSettings).not.toHaveBeenCalled();
  });

  it('A-4: focus stays on the save button after a successful save', async () => {
    const user = userEvent.setup();
    mockedFetchAppSettings.mockResolvedValueOnce(makeResponse({ dbPath: '' }));
    renderEditor();

    const dbPathInput = await screen.findByPlaceholderText('/tmp/graph.db');
    await user.type(dbPathInput, 'custom.db');

    mockedSaveAppSettings.mockResolvedValueOnce(makeResponse({ dbPath: 'custom.db' }));
    const saveButton = screen.getByRole('button', { name: i18n.t('settings.common.save') });
    saveButton.focus();
    await user.click(saveButton);

    await waitFor(() => expect(mockedSaveAppSettings).toHaveBeenCalledTimes(1));
    // A11y note (this test's whole point): jsdom does not model the
    // browser's own focus-fixup off a `disabled` element, so this assertion
    // alone cannot catch a regression back to `disabled` -- that regression
    // is instead caught by A-3's "not toHaveAttribute('disabled')" check.
    await waitFor(() => expect(document.activeElement).toBe(saveButton));
  });

  // DFLT-00094: the "saved" confirmation's 2s auto-hide timer used to outlive
  // the component, firing after jsdom was torn down ("window is not defined")
  // and intermittently failing the whole test run.
  it('does not leave the saved-confirmation timer pending after unmount', async () => {
    // shouldAdvanceTime keeps waitFor/userEvent moving on real time while
    // every timer the component registers is still a countable fake one.
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
      mockedFetchAppSettings.mockResolvedValueOnce(makeResponse({ dbPath: '' }));
      const { unmount } = renderEditor();

      const dbPathInput = await screen.findByPlaceholderText('/tmp/graph.db');
      await user.type(dbPathInput, 'custom.db');

      mockedSaveAppSettings.mockResolvedValueOnce(makeResponse({ dbPath: 'custom.db' }));
      await user.click(screen.getByRole('button', { name: i18n.t('settings.common.save') }));

      await waitFor(() => expect(screen.getAllByText(i18n.t('settings.common.saveSuccess')).length).toBeGreaterThan(0));
      expect(vi.getTimerCount()).toBeGreaterThan(0);

      unmount();

      expect(vi.getTimerCount()).toBe(0);
      expect(() => vi.advanceTimersByTime(2000)).not.toThrow();
    } finally {
      vi.useRealTimers();
    }
  });

  // DFLT-00074: fields that had a visible label (or only a placeholder) but
  // no programmatic association are now labelled.
  it('labels the storage, profile, pagination and team settings directory fields and the DB backend radio group', async () => {
    mockedFetchAppSettings.mockResolvedValueOnce(makeResponse({ dbPath: 'a.db', artifactsDir: 'arts', myName: 'me', teamExtensionsDir: '/srv/team' }));
    renderEditor();

    expect(await screen.findByLabelText(i18n.t('settings.appSettings.storage.dbPathLabel'))).toHaveValue('a.db');
    expect(screen.getByLabelText(i18n.t('settings.appSettings.storage.artifactsDirLabel'))).toHaveValue('arts');
    expect(screen.getByLabelText(i18n.t('settings.appSettings.myProfile.nameLabel'))).toHaveValue('me');
    expect(screen.getByLabelText(i18n.t('settings.appSettings.pagination.pageSizeLabel'))).toHaveValue(10);
    expect(screen.getByLabelText(i18n.t('settings.appSettings.teamExtensionsDir.title'))).toHaveValue('/srv/team');

    const group = screen.getByRole('radiogroup', { name: i18n.t('settings.appSettings.storage.dbBackendLabel') });
    // sqlite, mysql and (DFLT-00088) the HTTP custom data source.
    expect(group.querySelectorAll('input[type="radio"]')).toHaveLength(3);
  });

  it('labels each project row name and local path input', async () => {
    mockedFetchAppSettings.mockResolvedValueOnce(makeResponse());
    const projects = [
      { id: 'p1', name: 'Alpha', prefix: 'ALP', local_path: '/a', created_at: '', updated_at: '' },
      { id: 'p2', name: 'Beta', prefix: 'BET', local_path: '', created_at: '', updated_at: '' }
    ];
    render(
      <AppSettingsEditor
        projects={projects}
        onDirtyChange={vi.fn()}
        onProjectsChanged={vi.fn()}
        onPaginationPageSizeChanged={vi.fn()}
        onMyNameChanged={vi.fn()}
      />
    );
    await waitFor(() => expect(mockedFetchAppSettings).toHaveBeenCalled());

    const names = screen.getAllByLabelText(i18n.t('settings.appSettings.projects.nameLabel'));
    expect(names.map(el => (el as HTMLInputElement).value)).toEqual(['Alpha', 'Beta']);
    // The label also carries the "not set" badge for a project without a
    // local path in this environment, so match the label text as a prefix.
    const escaped = i18n.t('settings.appSettings.projects.localPathLabel').replace(/[()]/g, '\\$&');
    const localPaths = screen.getAllByLabelText(new RegExp(`^${escaped}`));
    expect(localPaths.map(el => (el as HTMLInputElement).value)).toEqual(['/a', '']);
  });

  it('F-1 non-regression: switching language after editing does not re-fetch app settings or discard the unsaved edit', async () => {
    const user = userEvent.setup();
    mockedFetchAppSettings.mockResolvedValueOnce(makeResponse({ dbPath: '' }));
    renderEditor();

    const dbPathInput = await screen.findByPlaceholderText('/tmp/graph.db');
    await user.type(dbPathInput, 'unsaved-value.db');
    expect(mockedFetchAppSettings).toHaveBeenCalledTimes(1);

    await i18n.changeLanguage('en');
    await waitFor(() => expect(screen.getByRole('button', { name: i18n.t('settings.common.save') })).toBeInTheDocument());

    expect(mockedFetchAppSettings).toHaveBeenCalledTimes(1);
    expect(screen.getByPlaceholderText('/tmp/graph.db')).toHaveValue('unsaved-value.db');
  });
});

// DFLT-00080: project management edits this environment's local path
// (Project.local_path, stored in the home config's projectPaths).
describe('AppSettingsEditor project local paths', () => {
  const alpha: Project = { id: 'p-alpha', name: 'Alpha', prefix: 'ALPHA', local_path: '/work/alpha', created_at: '', updated_at: '' };
  const beta: Project = { id: 'p-beta', name: 'Beta', prefix: 'BETA', local_path: '', created_at: '', updated_at: '' };

  function renderWithProjects(projects: Project[], onProjectsChanged = vi.fn()) {
    const utils = render(
      <AppSettingsEditor
        projects={projects}
        onDirtyChange={vi.fn()}
        onProjectsChanged={onProjectsChanged}
        onPaginationPageSizeChanged={vi.fn()}
        onMyNameChanged={vi.fn()}
      />
    );
    const rerenderWith = (next: Project[]) =>
      utils.rerender(
        <AppSettingsEditor
          projects={next}
          onDirtyChange={vi.fn()}
          onProjectsChanged={onProjectsChanged}
          onPaginationPageSizeChanged={vi.fn()}
          onMyNameChanged={vi.fn()}
        />
      );
    return { ...utils, rerenderWith };
  }

  const row = (id: string) => within(screen.getByTestId(`project-row-${id}`));
  const localPathInput = (id: string) => row(id).getByLabelText(new RegExp(i18n.t('settings.appSettings.projects.localPathLabel').replace(/[()]/g, '\\$&')));
  const saveButton = (id: string) => row(id).getByRole('button', { name: i18n.t('settings.common.save') });
  const patchBody = (n = 0) => {
    const [url, init] = (fetch as unknown as ReturnType<typeof vi.fn>).mock.calls[n];
    return { url: String(url), method: (init as RequestInit).method, body: JSON.parse((init as RequestInit).body as string) };
  };

  beforeEach(() => {
    mockedFetchAppSettings.mockReset();
    mockedFetchAppSettings.mockResolvedValue(makeResponse());
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => ({}) }));
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('shows "not set" for a project without a local path and the path for one that has it', async () => {
    renderWithProjects([alpha, beta]);
    await waitFor(() => expect(mockedFetchAppSettings).toHaveBeenCalled());

    expect(localPathInput('p-alpha')).toHaveValue('/work/alpha');
    expect(localPathInput('p-beta')).toHaveValue('');
    expect(row('p-beta').getAllByText(i18n.t('settings.appSettings.projects.notSet')).length).toBeGreaterThan(0);
    expect(row('p-alpha').queryByText(i18n.t('settings.appSettings.projects.notSet'))).not.toBeInTheDocument();
  });

  it('labels the field as this environment only and explains it is not stored in the DB', async () => {
    renderWithProjects([alpha]);
    await waitFor(() => expect(mockedFetchAppSettings).toHaveBeenCalled());

    expect(row('p-alpha').getByText(i18n.t('settings.appSettings.projects.localPathLabel'))).toBeInTheDocument();
    expect(i18n.t('settings.appSettings.projects.localPathLabel')).toBe('ローカルパス（この環境）');
    expect(screen.getByText(i18n.t('settings.appSettings.projects.localPathHint'))).toBeInTheDocument();
  });

  it('sets a local path on an unset project', async () => {
    const user = userEvent.setup();
    const onProjectsChanged = vi.fn();
    const { rerenderWith } = renderWithProjects([beta], onProjectsChanged);
    await waitFor(() => expect(mockedFetchAppSettings).toHaveBeenCalled());

    await user.type(localPathInput('p-beta'), '/work/beta');
    await user.click(saveButton('p-beta'));

    await waitFor(() => expect(onProjectsChanged).toHaveBeenCalled());
    expect(patchBody()).toEqual({ url: '/api/projects/p-beta', method: 'PATCH', body: { name: 'Beta', local_path: '/work/beta' } });
    rerenderWith([{ ...beta, local_path: '/work/beta' }]);
    expect(localPathInput('p-beta')).toHaveValue('/work/beta');
  });

  it('changes an existing local path', async () => {
    const user = userEvent.setup();
    const onProjectsChanged = vi.fn();
    const { rerenderWith } = renderWithProjects([alpha], onProjectsChanged);
    await waitFor(() => expect(mockedFetchAppSettings).toHaveBeenCalled());

    await user.clear(localPathInput('p-alpha'));
    await user.type(localPathInput('p-alpha'), '/work/alpha2');
    await user.click(saveButton('p-alpha'));

    await waitFor(() => expect(onProjectsChanged).toHaveBeenCalled());
    expect(patchBody().body).toEqual({ name: 'Alpha', local_path: '/work/alpha2' });
    rerenderWith([{ ...alpha, local_path: '/work/alpha2' }]);
    expect(localPathInput('p-alpha')).toHaveValue('/work/alpha2');
  });

  it('clearing the local path keeps save enabled and unsets it', async () => {
    const user = userEvent.setup();
    const onProjectsChanged = vi.fn();
    const { rerenderWith } = renderWithProjects([alpha], onProjectsChanged);
    await waitFor(() => expect(mockedFetchAppSettings).toHaveBeenCalled());

    await user.clear(localPathInput('p-alpha'));
    expect(saveButton('p-alpha')).toBeEnabled();
    await user.click(saveButton('p-alpha'));

    await waitFor(() => expect(onProjectsChanged).toHaveBeenCalled());
    expect(patchBody().body).toEqual({ name: 'Alpha', local_path: '' });
    rerenderWith([{ ...alpha, local_path: '' }]);
    expect(localPathInput('p-alpha')).toHaveValue('');
    expect(row('p-alpha').getAllByText(i18n.t('settings.appSettings.projects.notSet')).length).toBeGreaterThan(0);
  });
});

// DFLT-00153: the former node/workflow config directory field now edits the
// team settings directory (teamExtensionsDir); the personal directory
// (userExtensionsDir) is no longer edited or sent.
describe('AppSettingsEditor team settings directory', () => {
  beforeEach(() => {
    mockedFetchAppSettings.mockReset();
    mockedSaveAppSettings.mockReset();
  });

  afterEach(async () => {
    await i18n.changeLanguage('ja');
  });

  it('is labelled チーム設定ディレクトリ, describes its purpose and starts from file.teamExtensionsDir', async () => {
    mockedFetchAppSettings.mockResolvedValueOnce(makeResponse({ teamExtensionsDir: '/srv/team-graph-ops' }));
    renderEditor();

    const input = await screen.findByLabelText('チーム設定ディレクトリ');
    expect(input).toHaveValue('/srv/team-graph-ops');
    expect(input).toHaveAccessibleDescription(i18n.t('settings.appSettings.teamExtensionsDir.description'));
    expect(screen.queryByText('ノード/ワークフロー設定ディレクトリ')).not.toBeInTheDocument();
  });

  it('shows "not set (personal settings only)" when no team tier is in effect', async () => {
    mockedFetchAppSettings.mockResolvedValueOnce(makeResponse());
    renderEditor();

    await screen.findByLabelText(i18n.t('settings.appSettings.teamExtensionsDir.title'));
    expect(
      screen.getByText(i18n.t('settings.appSettings.currentlyInEffect', { value: '未設定（個人設定のみ）' }))
    ).toBeInTheDocument();
  });

  it('shows the team directory in effect when there is one', async () => {
    const response = makeResponse({ teamExtensionsDir: '/srv/team' });
    response.effective.teamExtensionsDir = '/srv/team';
    mockedFetchAppSettings.mockResolvedValueOnce(response);
    renderEditor();

    await screen.findByLabelText(i18n.t('settings.appSettings.teamExtensionsDir.title'));
    expect(screen.getByText(i18n.t('settings.appSettings.currentlyInEffect', { value: '/srv/team' }))).toBeInTheDocument();
  });

  it('saves teamExtensionsDir and never sends userExtensionsDir', async () => {
    const user = userEvent.setup();
    mockedFetchAppSettings.mockResolvedValueOnce(makeResponse());
    renderEditor();

    const input = await screen.findByLabelText(i18n.t('settings.appSettings.teamExtensionsDir.title'));
    await user.type(input, '/srv/team-graph-ops');

    mockedSaveAppSettings.mockResolvedValueOnce(makeResponse({ teamExtensionsDir: '/srv/team-graph-ops' }));
    await user.click(screen.getByRole('button', { name: i18n.t('settings.common.save') }));

    await waitFor(() => expect(mockedSaveAppSettings).toHaveBeenCalledTimes(1));
    const sent = mockedSaveAppSettings.mock.calls[0][1] as Record<string, unknown>;
    expect(sent.teamExtensionsDir).toBe('/srv/team-graph-ops');
    expect(sent).not.toHaveProperty('userExtensionsDir');
  });

  it('warns about a relative path and blocks saving until it is absolute', async () => {
    const user = userEvent.setup();
    mockedFetchAppSettings.mockResolvedValueOnce(makeResponse());
    renderEditor();

    const input = await screen.findByLabelText(i18n.t('settings.appSettings.teamExtensionsDir.title'));
    await user.type(input, 'shared/team');

    const hint = i18n.t('settings.appSettings.teamExtensionsDir.notAbsolute');
    expect(input).toHaveAttribute('aria-invalid', 'true');
    expect(input).toHaveAccessibleDescription(expect.stringContaining(hint));
    // The inline warning is a polite status message, so a screen reader
    // announces it as it appears while typing (WCAG 2.2 SC 4.1.3).
    const inlineWarning = screen
      .getAllByRole('status')
      .find((el) => el.id.endsWith('-team-extensions-dir-invalid'));
    expect(inlineWarning).toBeDefined();
    expect(inlineWarning).toHaveTextContent(hint);
    expect(inlineWarning).toHaveAttribute('aria-live', 'polite');
    const saveButton = screen.getByRole('button', { name: i18n.t('settings.common.save') });
    expect(saveButton).toHaveAttribute('aria-disabled', 'true');
    expect(document.getElementById('save-blocked-reason')).toHaveTextContent(hint);
    await user.click(saveButton);
    expect(mockedSaveAppSettings).not.toHaveBeenCalled();

    await user.clear(input);
    await user.type(input, '/srv/team');
    expect(input).toHaveAttribute('aria-invalid', 'false');
    expect(saveButton).toHaveAttribute('aria-disabled', 'false');
  });

  it('accepts a blank value (no team tier) as valid', async () => {
    const user = userEvent.setup();
    mockedFetchAppSettings.mockResolvedValueOnce(makeResponse({ teamExtensionsDir: '/srv/team' }));
    renderEditor();

    const input = await screen.findByLabelText(i18n.t('settings.appSettings.teamExtensionsDir.title'));
    await user.clear(input);

    expect(input).toHaveAttribute('aria-invalid', 'false');
    mockedSaveAppSettings.mockResolvedValueOnce(makeResponse());
    await user.click(screen.getByRole('button', { name: i18n.t('settings.common.save') }));
    await waitFor(() => expect(mockedSaveAppSettings).toHaveBeenCalledTimes(1));
    expect((mockedSaveAppSettings.mock.calls[0][1] as Record<string, unknown>).teamExtensionsDir).toBe('');
  });
});

// DFLT-00179: the 10px secondary lines (the "currently in effect" rows and the
// hint rows) must meet WCAG 1.4.3 (>= 4.5:1) against the backgrounds they
// actually sit on. Light: slate-500 on white (modal body) 4.76:1, on the
// bg-slate-50/50 MySQL/HTTP box (composited ~#fbfcfe) 4.64:1. Dark: slate-400
// on slate-900 6.96:1. The previous slate-400 (light, 2.56:1) / slate-500
// (dark, 3.75:1) fell short. Darkening either box's background later would
// push the light ratio below 4.5:1, so revisit these classes if it changes.
// The font size must stay text-[10px] -- only the colour changed.
function expectSecondaryTextContrast(el: HTMLElement | null) {
  expect(el).toHaveClass('text-[10px]', 'text-slate-500', 'dark:text-slate-400');
  expect(el).not.toHaveClass('text-slate-400');
  expect(el).not.toHaveClass('dark:text-slate-500');
}

describe('AppSettingsEditor secondary text contrast (DFLT-00179)', () => {
  beforeEach(() => {
    mockedFetchAppSettings.mockReset();
    mockedSaveAppSettings.mockReset();
  });

  afterEach(async () => {
    await i18n.changeLanguage('ja');
  });

  it('the "not set (personal settings only)" line uses colours that meet 4.5:1 in light and dark', async () => {
    mockedFetchAppSettings.mockResolvedValueOnce(makeResponse());
    renderEditor();

    await screen.findByLabelText(i18n.t('settings.appSettings.teamExtensionsDir.title'));
    expectSecondaryTextContrast(
      screen.getByText(
        i18n.t('settings.appSettings.currentlyInEffect', { value: i18n.t('settings.appSettings.teamExtensionsDir.notSet') })
      )
    );
  });

  it('the other "currently in effect" lines and the DB backend switch hint meet 4.5:1', async () => {
    const response = makeResponse({ teamExtensionsDir: '/srv/team' });
    response.effective.teamExtensionsDir = '/srv/team';
    mockedFetchAppSettings.mockResolvedValueOnce(response);
    renderEditor();

    await screen.findByLabelText(i18n.t('settings.appSettings.teamExtensionsDir.title'));
    for (const value of ['sqlite', '/tmp/graph.db', '/tmp/artifacts', '/srv/team']) {
      expectSecondaryTextContrast(screen.getByText(i18n.t('settings.appSettings.currentlyInEffect', { value })));
    }
    expectSecondaryTextContrast(screen.getByText(i18n.t('settings.appSettings.storage.dbBackendSwitchHint')));
  });

  it('the MySQL password, TLS mode and TLS CA hints meet 4.5:1, and the TLS-disabled warning stays red', async () => {
    const user = userEvent.setup();
    mockedFetchAppSettings.mockResolvedValueOnce(
      makeResponse({ dbBackend: 'mysql', mysqlHost: 'db.example.com', mysqlDatabase: 'graphops', mysqlUser: 'admin' })
    );
    renderEditor();

    await screen.findByLabelText(i18n.t('settings.appSettings.storage.mysqlPasswordLabel'));
    expectSecondaryTextContrast(screen.getByText(i18n.t('settings.appSettings.storage.mysqlPasswordPlaintextHint')));
    expectSecondaryTextContrast(screen.getByText(i18n.t('settings.appSettings.storage.mysqlTlsVerifyFullHint')));
    expectSecondaryTextContrast(screen.getByText(i18n.t('settings.appSettings.storage.mysqlTlsCaHint')));

    await user.click(screen.getByRole('radio', { name: i18n.t('settings.appSettings.storage.mysqlTlsDisabled') }));
    const warning = screen.getByText(i18n.t('settings.appSettings.storage.mysqlTlsDisabledWarning'));
    expect(warning).toHaveClass('text-[10px]', 'text-red-600', 'dark:text-red-400');
    expect(warning).not.toHaveClass('text-slate-500');
  });
});

// DFLT-00180: every inline status message of this tab is announced through a
// live region that is in the DOM from the first render, and only its text
// changes -- a live region mounted together with its text is not reliably
// announced (WCAG 2.2 SC 4.1.3). The visible copy next to the field is
// aria-hidden so the message is not read twice, and aria-describedby points
// at the live region, which holds the text whenever the message is shown.
function liveRegionById(id: string): HTMLElement {
  const el = document.getElementById(id);
  expect(el).not.toBeNull();
  return el as HTMLElement;
}

function expectEmptyLiveRegion(el: HTMLElement) {
  expect(el).toHaveAttribute('role', 'status');
  expect(el).toHaveAttribute('aria-live', 'polite');
  expect(el.textContent).toBe('');
}

// The one visible copy of `message` (besides the live region itself and the
// save button's blocked reason, which repeats some of these on purpose) is
// hidden from assistive technology and is not a live region of its own.
function expectVisibleCopyIsAriaHidden(message: string, region: HTMLElement) {
  const copies = screen
    .getAllByText(message)
    .filter((el) => el !== region && el.id !== 'save-blocked-reason');
  expect(copies).toHaveLength(1);
  expect(copies[0]).toHaveAttribute('aria-hidden', 'true');
  expect(copies[0]).not.toHaveAttribute('role');
  expect(copies[0]).not.toHaveAttribute('aria-live');
  expect(copies[0]).not.toHaveAttribute('id');
}

describe('AppSettingsEditor inline status messages use always-mounted live regions (DFLT-00180)', () => {
  beforeEach(() => {
    mockedFetchAppSettings.mockReset();
    mockedSaveAppSettings.mockReset();
    mockedTestMySQLConnection.mockReset();
  });

  afterEach(async () => {
    await i18n.changeLanguage('ja');
  });

  const mysqlRadio = () => screen.getByRole('radio', { name: i18n.t('settings.appSettings.storage.dbBackendMysql') });
  const sqliteRadio = () => screen.getByRole('radio', { name: i18n.t('settings.appSettings.storage.dbBackendSqlite') });

  it('MySQL password retype hint: the region exists before MySQL is selected and fills when the saved password would be resent to another host', async () => {
    const user = userEvent.setup();
    mockedFetchAppSettings.mockResolvedValueOnce(
      makeResponse({
        dbBackend: 'sqlite',
        mysqlHost: 'db.example.com',
        mysqlDatabase: 'graphops',
        mysqlUser: 'admin',
        mysqlPassword: REDACTED_SECRET_PLACEHOLDER
      })
    );
    renderEditor();
    await screen.findByLabelText(i18n.t('settings.appSettings.storage.dbPathLabel'));

    const region = liveRegionById('mysql-password-retype-hint');
    expectEmptyLiveRegion(region);

    await user.click(mysqlRadio());
    expect(document.getElementById('mysql-password-retype-hint')).toBe(region);
    expect(region.textContent).toBe('');

    const hostInput = screen.getByLabelText(i18n.t('settings.appSettings.storage.mysqlHostLabel'));
    await user.clear(hostInput);
    await user.type(hostInput, 'other-host.example.com');

    const hint = i18n.t('settings.appSettings.storage.mysqlPasswordRetypeHint');
    expect(document.getElementById('mysql-password-retype-hint')).toBe(region);
    expect(region).toHaveTextContent(hint);
    expect(screen.getByLabelText(i18n.t('settings.appSettings.storage.mysqlPasswordLabel'))).toHaveAccessibleDescription(
      expect.stringContaining(hint)
    );
    expectVisibleCopyIsAriaHidden(hint, region);

    // The retype requirement survives the switch to sqlite (see Q-1), but its
    // MySQL-block hint is off screen there, so nothing is announced for it.
    await user.click(sqliteRadio());
    expect(document.getElementById('mysql-password-retype-hint')).toBe(region);
    expect(region.textContent).toBe('');
  });

  it('MySQL TLS CA required hint: the region exists from the start and fills when verify-ca has no CA file', async () => {
    const user = userEvent.setup();
    mockedFetchAppSettings.mockResolvedValueOnce(makeResponse());
    renderEditor();
    await screen.findByLabelText(i18n.t('settings.appSettings.storage.dbPathLabel'));

    const region = liveRegionById('mysql-tls-ca-required-hint');
    expectEmptyLiveRegion(region);

    await user.click(mysqlRadio());
    expect(region.textContent).toBe('');
    await user.click(screen.getByRole('radio', { name: i18n.t('settings.appSettings.storage.mysqlTlsVerifyCa') }));

    const hint = i18n.t('settings.appSettings.storage.mysqlTlsCaRequiredHint');
    expect(document.getElementById('mysql-tls-ca-required-hint')).toBe(region);
    expect(region).toHaveTextContent(hint);
    const caInput = screen.getByLabelText(i18n.t('settings.appSettings.storage.mysqlTlsCaLabel'));
    expect(caInput).toHaveAccessibleDescription(expect.stringContaining(hint));
    expectVisibleCopyIsAriaHidden(hint, region);

    await user.type(caInput, '/etc/ssl/ca.pem');
    expect(region.textContent).toBe('');
    expect(screen.queryByText(hint)).not.toBeInTheDocument();
  });

  it('MySQL required fields hint: the region exists before MySQL is selected and fills as soon as it is', async () => {
    const user = userEvent.setup();
    mockedFetchAppSettings.mockResolvedValueOnce(makeResponse());
    renderEditor();
    await screen.findByLabelText(i18n.t('settings.appSettings.storage.dbPathLabel'));

    const region = liveRegionById('mysql-required-fields-hint');
    expectEmptyLiveRegion(region);

    await user.click(mysqlRadio());

    const hint = i18n.t('settings.appSettings.storage.mysqlRequiredFieldsHint');
    expect(document.getElementById('mysql-required-fields-hint')).toBe(region);
    expect(region).toHaveTextContent(hint);
    const hostInput = screen.getByLabelText(i18n.t('settings.appSettings.storage.mysqlHostLabel'));
    expect(hostInput).toHaveAccessibleDescription(expect.stringContaining(hint));
    expect(screen.getByRole('button', { name: i18n.t('settings.appSettings.storage.testConnection') })).toHaveAccessibleDescription(
      expect.stringContaining(hint)
    );
    expectVisibleCopyIsAriaHidden(hint, region);

    await user.type(hostInput, 'db.example.com');
    await user.type(screen.getByLabelText(i18n.t('settings.appSettings.storage.mysqlDatabaseLabel')), 'graphops');
    await user.type(screen.getByLabelText(i18n.t('settings.appSettings.storage.mysqlUserLabel')), 'admin');
    expect(region.textContent).toBe('');
  });

  it('MySQL connection test result: announced through a region that was there before the test ran, for success and failure alike', async () => {
    const user = userEvent.setup();
    mockedFetchAppSettings.mockResolvedValueOnce(
      makeResponse({ dbBackend: 'mysql', mysqlHost: 'db.example.com', mysqlDatabase: 'graphops', mysqlUser: 'admin' })
    );
    renderEditor();
    const testButton = await screen.findByRole('button', { name: i18n.t('settings.appSettings.storage.testConnection') });

    const regionsBefore = screen.getAllByRole('status');
    for (const el of regionsBefore) expect(el).toHaveAttribute('aria-live', 'polite');

    mockedTestMySQLConnection.mockResolvedValueOnce({ ok: true });
    await user.click(testButton);

    const success = i18n.t('settings.appSettings.storage.testConnectionSuccess');
    await waitFor(() => expect(screen.getAllByRole('status').some((el) => el.textContent === success)).toBe(true));
    const region = screen.getAllByRole('status').find((el) => el.textContent === success) as HTMLElement;
    expect(regionsBefore).toContain(region);
    expectVisibleCopyIsAriaHidden(success, region);

    // A new test empties the region while it runs, so even a result identical
    // to the previous one is a text change and gets announced again.
    let resolveSecond: (r: { ok: boolean; error?: string }) => void = () => {};
    mockedTestMySQLConnection.mockReturnValueOnce(new Promise((resolve) => { resolveSecond = resolve; }));
    await user.click(screen.getByRole('button', { name: i18n.t('settings.appSettings.storage.testConnection') }));
    await waitFor(() => expect(region.textContent).toBe(''));
    expect(screen.getAllByRole('status')).toContain(region);
    resolveSecond({ ok: false, error: 'access denied' });

    const failure = i18n.t('settings.appSettings.storage.testConnectionFailure', { error: 'access denied' });
    await waitFor(() => expect(region).toHaveTextContent(failure));
    expect(screen.getAllByRole('status')).toContain(region);
    expectVisibleCopyIsAriaHidden(failure, region);

    // Switching the backend clears the result, and the region stays behind empty.
    await user.click(sqliteRadio());
    expect(screen.getAllByRole('status')).toContain(region);
    expect(region.textContent).toBe('');
  });

  it('team settings directory warning: the region exists from the start and fills for a relative path', async () => {
    const user = userEvent.setup();
    mockedFetchAppSettings.mockResolvedValueOnce(makeResponse());
    renderEditor();

    const input = await screen.findByLabelText(i18n.t('settings.appSettings.teamExtensionsDir.title'));
    const region = document.querySelector('[id$="-team-extensions-dir-invalid"]') as HTMLElement;
    expect(region).not.toBeNull();
    expectEmptyLiveRegion(region);

    await user.type(input, 'shared/team');

    const hint = i18n.t('settings.appSettings.teamExtensionsDir.notAbsolute');
    expect(document.querySelector('[id$="-team-extensions-dir-invalid"]')).toBe(region);
    expect(region).toHaveTextContent(hint);
    expect(input).toHaveAccessibleDescription(expect.stringContaining(hint));
    expectVisibleCopyIsAriaHidden(hint, region);

    await user.clear(input);
    await user.type(input, '/srv/team');
    expect(region.textContent).toBe('');
  });
});
