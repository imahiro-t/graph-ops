// DFLT-00124, completion criterion 11: this page reads and writes exactly one
// file, the home config, so the "saved to <path>" note names that one file --
// and when the server has no home directory to name, it says so in words
// rather than printing an empty path. The second note that used to sit beside
// it ("artifactsDir actually goes here instead") is gone with the second
// file.
import { render, screen, waitFor, waitForElementToBeRemoved } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../../i18n';
import { AppSettingsEditor } from './AppSettingsEditor';
import { APP_SETTINGS_WARNINGS, AppSettingsResponse } from '../../types';

vi.mock('../../lib/settingsApi', async () => {
  const actual = await vi.importActual<typeof import('../../lib/settingsApi')>('../../lib/settingsApi');
  return { ...actual, fetchAppSettings: vi.fn(), saveAppSettings: vi.fn() };
});

import { fetchAppSettings, saveAppSettings } from '../../lib/settingsApi';

const mockedFetchAppSettings = fetchAppSettings as unknown as ReturnType<typeof vi.fn>;
const mockedSaveAppSettings = saveAppSettings as unknown as ReturnType<typeof vi.fn>;

const HOME_CONFIG = '/home/me/.graph-ops/config.json';

function makeResponse(overrides: Partial<AppSettingsResponse> = {}): AppSettingsResponse {
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
      myName: ''
    },
    effective: {
      dbBackend: 'sqlite',
      dbPath: '/tmp/graph.db',
      artifactsDir: '/tmp/artifacts',
      userExtensionsDir: '/tmp/extensions',
      teamExtensionsDir: '',
      paginationPageSize: 10
    },
    config_path: HOME_CONFIG,
    ...overrides
  };
}

// DFLT-00192: waiting for fetchAppSettings to have been called is not the
// same as waiting for its answer to be on screen. The fetch starts inside
// render()'s own act, so such a waitFor passes on its first check, while the
// re-render that swaps the loading spinner for the page is still to come --
// and under the load of a full run it sometimes had not happened before the
// next synchronous getByText. So wait for the spinner to go instead. It is
// always there when render() returns (load() sets loading before it awaits the
// fetch), which waitForElementToBeRemoved insists on: if that ever stops being
// true, this fails loudly rather than waiting for nothing.
async function renderLoadedEditor() {
  renderEditor();
  await waitForElementToBeRemoved(() => screen.queryByText(i18n.t('settings.common.loading')));
  expect(mockedFetchAppSettings).toHaveBeenCalledTimes(1);
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

describe('AppSettingsEditor save-location note', () => {
  beforeEach(() => {
    mockedFetchAppSettings.mockReset();
    mockedSaveAppSettings.mockReset();
  });

  it('names the home config as the one destination', async () => {
    mockedFetchAppSettings.mockResolvedValueOnce(makeResponse());
    await renderLoadedEditor();

    expect(
      screen.getByText(i18n.t('settings.appSettings.restartNote', { path: HOME_CONFIG }), { exact: false })
    ).toBeInTheDocument();
  });

  // Decision D-4: the server answers config_path: '' rather than a path that
  // does not exist, so the page must have its own wording instead of showing
  // "saved to " with nothing after it.
  it('says where settings would go when the home directory cannot be resolved', async () => {
    mockedFetchAppSettings.mockResolvedValueOnce(makeResponse({ config_path: '' }));
    await renderLoadedEditor();

    expect(screen.getByText(i18n.t('settings.appSettings.restartNoteNoPath'))).toBeInTheDocument();
  });

  // DFLT-00104, non-functional review NF-2. A home config that cannot be
  // parsed is reported on stderr by the CLI, which someone who only opens the
  // Web UI never sees -- and the page would otherwise show empty fields with
  // nothing to explain them. The GET raises the warning, so it is on screen
  // before anything has been saved.
  it('warns about an unreadable home config as soon as the page loads', async () => {
    mockedFetchAppSettings.mockResolvedValueOnce(
      makeResponse({ warnings: [APP_SETTINGS_WARNINGS.homeConfigUnreadable] })
    );
    renderEditor();

    await waitFor(() =>
      expect(
        screen.getByText(i18n.t('settings.appSettings.homeConfigUnreadable', { path: HOME_CONFIG }))
      ).toBeInTheDocument()
    );
  });

  it('says nothing about the home config when the server reports no warnings', async () => {
    mockedFetchAppSettings.mockResolvedValueOnce(makeResponse());
    await renderLoadedEditor();

    // The page itself is up (its save-location note is there), so the absence
    // below is about the warning, not about a page still loading.
    expect(
      screen.getByText(i18n.t('settings.appSettings.restartNote', { path: HOME_CONFIG }), { exact: false })
    ).toBeInTheDocument();
    expect(
      screen.queryByText(i18n.t('settings.appSettings.homeConfigUnreadable', { path: HOME_CONFIG }))
    ).not.toBeInTheDocument();
  });
});
