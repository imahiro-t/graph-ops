// DFLT-00104, completion criterion 3: the "saved to <path>" note has to name
// the file each setting really lands in. artifactsDir is a home-only key (see
// packages/core-go/internal/runtimeconfig's HomeOnlyKeys), so when a
// working-directory graph-config.json is in play it is saved to the home
// config while everything else goes to that working-directory file -- and a
// single note naming only config_path would be telling the user the wrong
// one. "I changed it in the UI and nothing happened" must not just become
// "the UI reports the wrong file".
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
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

const WORK_CONFIG = '/work/graph-config.json';
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
      userExtensionsDir: '',
      paginationPageSize: 10,
      myName: ''
    },
    effective: {
      dbBackend: 'sqlite',
      dbPath: '/tmp/graph.db',
      artifactsDir: '/tmp/artifacts',
      userExtensionsDir: '/tmp/extensions',
      paginationPageSize: 10
    },
    config_path: WORK_CONFIG,
    ...overrides
  };
}

function renderEditor() {
  return render(
    <AppSettingsEditor
      scope="global"
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

  it('names the home config as the artifacts directory\'s destination when the two files differ', async () => {
    mockedFetchAppSettings.mockResolvedValueOnce(makeResponse({ home_config_path: HOME_CONFIG }));
    renderEditor();

    await waitFor(() => expect(mockedFetchAppSettings).toHaveBeenCalled());
    expect(screen.getByText(new RegExp(WORK_CONFIG), { exact: false })).toBeInTheDocument();
    expect(
      screen.getByText(i18n.t('settings.appSettings.homeOnlyNote', { path: HOME_CONFIG }), { exact: false })
    ).toBeInTheDocument();
  });

  it('says nothing extra when both settings go to the same file', async () => {
    mockedFetchAppSettings.mockResolvedValueOnce(
      makeResponse({ config_path: HOME_CONFIG, home_config_path: HOME_CONFIG })
    );
    renderEditor();

    await waitFor(() => expect(mockedFetchAppSettings).toHaveBeenCalled());
    expect(
      screen.queryByText(i18n.t('settings.appSettings.homeOnlyNote', { path: HOME_CONFIG }), { exact: false })
    ).not.toBeInTheDocument();
  });

  it('reports that the artifacts directory alone was not saved when there is no home config', async () => {
    const user = userEvent.setup();
    mockedFetchAppSettings.mockResolvedValueOnce(makeResponse({ home_config_path: '' }));
    renderEditor();

    const dbPathInput = await screen.findByPlaceholderText('/tmp/graph.db');
    await user.type(dbPathInput, 'custom.db');
    // Nothing is claimed until a save actually comes back saying so.
    expect(screen.queryByText(i18n.t('settings.appSettings.homeConfigUnavailable'))).not.toBeInTheDocument();

    mockedSaveAppSettings.mockResolvedValueOnce(
      makeResponse({ home_config_path: '', warnings: [APP_SETTINGS_WARNINGS.homeConfigUnavailable] })
    );
    await user.click(screen.getByRole('button', { name: i18n.t('settings.common.save') }));

    await waitFor(() =>
      expect(screen.getByText(i18n.t('settings.appSettings.homeConfigUnavailable'))).toBeInTheDocument()
    );
  });

  // DFLT-00104, non-functional review NF-2. A home config that cannot be
  // parsed is reported on stderr by the CLI, which someone who only opens the
  // Web UI never sees -- and the page would otherwise show an empty artifacts
  // directory with nothing to explain it. The GET raises the warning, so it
  // is on screen before anything has been saved.
  it('warns about an unreadable home config as soon as the page loads', async () => {
    mockedFetchAppSettings.mockResolvedValueOnce(
      makeResponse({ home_config_path: HOME_CONFIG, warnings: [APP_SETTINGS_WARNINGS.homeConfigUnreadable] })
    );
    renderEditor();

    await waitFor(() =>
      expect(
        screen.getByText(i18n.t('settings.appSettings.homeConfigUnreadable', { path: HOME_CONFIG }))
      ).toBeInTheDocument()
    );
  });

  it('says nothing about the home config when the server reports no warnings', async () => {
    mockedFetchAppSettings.mockResolvedValueOnce(makeResponse({ home_config_path: HOME_CONFIG }));
    renderEditor();

    await waitFor(() => expect(mockedFetchAppSettings).toHaveBeenCalled());
    expect(
      screen.queryByText(i18n.t('settings.appSettings.homeConfigUnreadable', { path: HOME_CONFIG }))
    ).not.toBeInTheDocument();
    expect(screen.queryByText(i18n.t('settings.appSettings.homeConfigUnavailable'))).not.toBeInTheDocument();
  });
});
