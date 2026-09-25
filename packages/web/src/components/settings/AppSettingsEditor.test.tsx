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
    saveAppSettings: vi.fn()
  };
});

import { fetchAppSettings, saveAppSettings } from '../../lib/settingsApi';

const mockedFetchAppSettings = fetchAppSettings as unknown as ReturnType<typeof vi.fn>;
const mockedSaveAppSettings = saveAppSettings as unknown as ReturnType<typeof vi.fn>;

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
      userExtensionsDir: '',
      paginationPageSize: 10,
      myName: '',
      ...overrides
    },
    effective: {
      dbBackend: 'sqlite',
      dbPath: '/tmp/graph.db',
      artifactsDir: '/tmp/artifacts',
      userExtensionsDir: '/tmp/extensions',
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
  it('labels the storage, profile, pagination and extensions-dir fields and the DB backend radio group', async () => {
    mockedFetchAppSettings.mockResolvedValueOnce(makeResponse({ dbPath: 'a.db', artifactsDir: 'arts', myName: 'me', userExtensionsDir: 'ext' }));
    renderEditor();

    expect(await screen.findByLabelText(i18n.t('settings.appSettings.storage.dbPathLabel'))).toHaveValue('a.db');
    expect(screen.getByLabelText(i18n.t('settings.appSettings.storage.artifactsDirLabel'))).toHaveValue('arts');
    expect(screen.getByLabelText(i18n.t('settings.appSettings.myProfile.nameLabel'))).toHaveValue('me');
    expect(screen.getByLabelText(i18n.t('settings.appSettings.pagination.pageSizeLabel'))).toHaveValue(10);
    expect(screen.getByLabelText(i18n.t('settings.appSettings.extensionsDir.title'))).toHaveValue('ext');

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

  // DFLT-00165: the icon-only delete button rests at text-slate-500 /
  // dark:text-slate-400 for WCAG 1.4.11's 3:1 on the project row's white /
  // slate-900 (4.76:1 / 6.96:1; the old text-slate-400 / dark:text-slate-500
  // was 2.56:1 / 3.75:1). The red hover and the disabled opacity shown while
  // deleting (a disabled control is exempt from 1.4.11) are kept.
  it('rests the project delete button at slate-500 / dark:slate-400, keeping the red hover and disabled opacity', async () => {
    renderWithProjects([alpha]);
    await waitFor(() => expect(mockedFetchAppSettings).toHaveBeenCalled());

    const del = row('p-alpha').getByTitle(i18n.t('settings.appSettings.projects.delete'));
    expect(del).toHaveClass('text-slate-500', 'dark:text-slate-400');
    expect(del).not.toHaveClass('text-slate-400');
    expect(del).not.toHaveClass('dark:text-slate-500');
    expect(del).toHaveClass('hover:text-red-600', 'dark:hover:text-red-400', 'disabled:opacity-40');
  });
});
