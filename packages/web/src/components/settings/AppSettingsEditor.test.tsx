// Regression coverage for behaviour DFLT-00023 left unverified (5-5) and for
// F-1's structural non-regression (language switch must never re-trigger
// this tab's load). See this ticket's plan section 4-2.
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../../i18n';
import { AppSettingsEditor } from './AppSettingsEditor';
import { REDACTED_SECRET_PLACEHOLDER, AppSettingsResponse } from '../../types';

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
    config_path: '/tmp/graph-config.json'
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
