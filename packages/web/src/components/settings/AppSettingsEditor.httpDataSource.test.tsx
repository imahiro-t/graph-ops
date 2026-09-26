// DFLT-00088: the HTTP custom data source part of the app-settings tab.
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../../i18n';
import { AppSettingsEditor } from './AppSettingsEditor';
import { REDACTED_SECRET_PLACEHOLDER, AppSettingsResponse } from '../../types';
import { translateErrorCode } from '../../lib/apiError';

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
      httpDataSourceUrl: '',
      httpDataSourceToken: '',
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

const urlLabel = () => i18n.t('settings.appSettings.storage.httpUrlLabel');
const tokenLabel = () => i18n.t('settings.appSettings.storage.httpTokenLabel');
const saveButton = () => screen.getByRole('button', { name: i18n.t('settings.common.save') });

describe('AppSettingsEditor: HTTP custom data source', () => {
  beforeEach(() => {
    mockedFetchAppSettings.mockReset();
    mockedSaveAppSettings.mockReset();
  });

  afterEach(async () => {
    await i18n.changeLanguage('ja');
  });

  it('selecting the HTTP backend shows the URL field and a password-type token field', async () => {
    const user = userEvent.setup();
    mockedFetchAppSettings.mockResolvedValueOnce(makeResponse());
    renderEditor();

    const radio = await screen.findByRole('radio', { name: i18n.t('settings.appSettings.storage.dbBackendHttp') });
    expect(screen.queryByLabelText(urlLabel())).not.toBeInTheDocument();
    await user.click(radio);

    expect(screen.getByLabelText(urlLabel())).toBeInTheDocument();
    expect(screen.getByLabelText(tokenLabel())).toHaveAttribute('type', 'password');
  });

  it('a saved token is never shown and is reported as saved', async () => {
    mockedFetchAppSettings.mockResolvedValueOnce(
      makeResponse({ dbBackend: 'http', httpDataSourceUrl: 'https://a.example.com', httpDataSourceToken: REDACTED_SECRET_PLACEHOLDER })
    );
    renderEditor();

    const token = await screen.findByLabelText(tokenLabel());
    expect(token).toHaveValue('');
    expect(token).toHaveAttribute('placeholder', i18n.t('settings.appSettings.storage.mysqlPasswordSavedPlaceholder'));
    expect(screen.getByText(i18n.t('settings.appSettings.storage.httpTokenSavedHint'))).toBeInTheDocument();
  });

  it('an ${ENV_VAR} token reference shows which variable it is read from', async () => {
    mockedFetchAppSettings.mockResolvedValueOnce(
      makeResponse({ dbBackend: 'http', httpDataSourceUrl: 'https://a.example.com', httpDataSourceToken: '${GRAPHOPS_DATASOURCE_TOKEN}' })
    );
    renderEditor();

    expect(
      await screen.findByText(i18n.t('settings.appSettings.storage.httpTokenFromEnvHint', { envVar: 'GRAPHOPS_DATASOURCE_TOKEN' }))
    ).toBeInTheDocument();
  });

  it('changing the URL empties the saved token and asks for it again; reverting restores it', async () => {
    const user = userEvent.setup();
    mockedFetchAppSettings.mockResolvedValueOnce(
      makeResponse({ dbBackend: 'http', httpDataSourceUrl: 'https://a.example.com', httpDataSourceToken: '${GRAPHOPS_DATASOURCE_TOKEN}' })
    );
    renderEditor();

    const url = await screen.findByLabelText(urlLabel());
    await user.clear(url);
    await user.type(url, 'https://b.example.com');

    expect(screen.getByLabelText(tokenLabel())).toHaveValue('');
    expect(screen.getAllByText(i18n.t('settings.appSettings.storage.httpTokenRetypeHint')).length).toBeGreaterThan(0);
    expect(saveButton()).toHaveAttribute('aria-disabled', 'true');

    await user.clear(url);
    await user.type(url, 'https://a.example.com/');
    expect(screen.getByLabelText(tokenLabel())).toHaveValue('${GRAPHOPS_DATASOURCE_TOKEN}');
    expect(screen.queryByText(i18n.t('settings.appSettings.storage.httpTokenRetypeHint'))).not.toBeInTheDocument();
  });

  it.each([
    ['http://example.com', 't', 'settings.appSettings.storage.httpPlaintextRemoteHint'],
    ['https://example.com', '', 'settings.appSettings.storage.httpTokenRequiredHint'],
    ['http://127.0.0.1:8787', '', null]
  ])('URL %s with token %j shows the right input error', async (urlValue, tokenValue, hintKey) => {
    const user = userEvent.setup();
    mockedFetchAppSettings.mockResolvedValueOnce(makeResponse({ dbBackend: 'http' }));
    renderEditor();

    await user.type(await screen.findByLabelText(urlLabel()), urlValue);
    if (tokenValue) await user.type(screen.getByLabelText(tokenLabel()), tokenValue);

    const hints = [
      'settings.appSettings.storage.httpPlaintextRemoteHint',
      'settings.appSettings.storage.httpTokenRequiredHint',
      'settings.appSettings.storage.httpUrlInvalidHint',
      'settings.appSettings.storage.httpUrlRequiredHint'
    ];
    for (const key of hints) {
      if (key === hintKey) {
        expect(screen.getAllByText(i18n.t(key)).length).toBeGreaterThan(0);
      } else {
        expect(screen.queryByText(i18n.t(key))).not.toBeInTheDocument();
      }
    }
    expect(saveButton()).toHaveAttribute('aria-disabled', hintKey ? 'true' : 'false');
  });

  it('saves the URL and the token the user typed', async () => {
    const user = userEvent.setup();
    mockedFetchAppSettings.mockResolvedValueOnce(makeResponse());
    renderEditor();

    await user.click(await screen.findByRole('radio', { name: i18n.t('settings.appSettings.storage.dbBackendHttp') }));
    await user.type(screen.getByLabelText(urlLabel()), 'https://example.com/graphops');
    await user.type(screen.getByLabelText(tokenLabel()), 'new-token');
    mockedSaveAppSettings.mockResolvedValueOnce(
      makeResponse({ dbBackend: 'http', httpDataSourceUrl: 'https://example.com/graphops', httpDataSourceToken: REDACTED_SECRET_PLACEHOLDER })
    );
    await user.click(saveButton());

    await waitFor(() => expect(mockedSaveAppSettings).toHaveBeenCalledTimes(1));
    const submitted = mockedSaveAppSettings.mock.calls[0][1];
    expect(submitted).toMatchObject({ dbBackend: 'http', httpDataSourceUrl: 'https://example.com/graphops', httpDataSourceToken: 'new-token' });
    await waitFor(() => expect(screen.getByLabelText(tokenLabel())).toHaveValue(''));
  });

  it.each(['ja', 'en'])('the server retype error is translated (%s)', async lang => {
    await i18n.changeLanguage(lang);
    const message = translateErrorCode(i18n.t, 'HTTP_DATASOURCE_TOKEN_RETYPE_REQUIRED');
    expect(message).toBe(i18n.t('errors.HTTP_DATASOURCE_TOKEN_RETYPE_REQUIRED'));
    expect(message).not.toBe(i18n.t('errors.UNKNOWN'));
  });
});

// DFLT-00179: the URL/token hints and the URL's "currently in effect" line sit
// on the bg-slate-50/50 box. slate-500 there is 4.64:1 (light) and slate-400 on
// slate-900 is 6.96:1 (dark), both >= 4.5:1 (WCAG 1.4.3); the old slate-400 /
// dark:slate-500 pair was 2.50:1 / 3.75:1. The font size stays text-[10px].
describe('AppSettingsEditor: HTTP custom data source secondary text contrast (DFLT-00179)', () => {
  beforeEach(() => {
    mockedFetchAppSettings.mockReset();
    mockedSaveAppSettings.mockReset();
  });

  it('the URL hint, the token hint and the URL in effect use colours that meet 4.5:1', async () => {
    const response = makeResponse({ dbBackend: 'http', httpDataSourceUrl: 'https://a.example.com' });
    response.effective.dbBackend = 'http';
    response.effective.httpDataSourceUrl = 'https://a.example.com';
    mockedFetchAppSettings.mockResolvedValueOnce(response);
    renderEditor();

    await screen.findByLabelText(urlLabel());
    const lines = [
      screen.getByText(i18n.t('settings.appSettings.storage.httpUrlHint')),
      screen.getByText(i18n.t('settings.appSettings.storage.httpTokenPlaintextHint')),
      screen.getByText(i18n.t('settings.appSettings.currentlyInEffect', { value: 'https://a.example.com' }))
    ];
    for (const el of lines) {
      expect(el).toHaveClass('text-[10px]', 'text-slate-500', 'dark:text-slate-400');
      expect(el).not.toHaveClass('text-slate-400');
      expect(el).not.toHaveClass('dark:text-slate-500');
    }
  });
});

// DFLT-00180: the HTTP block's inline status messages are announced through
// live regions that are in the DOM from the first render -- before the HTTP
// backend is even selected -- and only their text changes (WCAG 2.2 SC 4.1.3).
// The visible copy is aria-hidden, and aria-describedby points at the region.
describe('AppSettingsEditor: HTTP inline status messages use always-mounted live regions (DFLT-00180)', () => {
  beforeEach(() => {
    mockedFetchAppSettings.mockReset();
    mockedSaveAppSettings.mockReset();
  });

  afterEach(async () => {
    await i18n.changeLanguage('ja');
  });

  const httpRadio = () => screen.getByRole('radio', { name: i18n.t('settings.appSettings.storage.dbBackendHttp') });
  const regionEndingWith = (suffix: string) => document.querySelector(`[id$="${suffix}"]`) as HTMLElement | null;

  function expectEmptyLiveRegion(el: HTMLElement | null): asserts el is HTMLElement {
    expect(el).not.toBeNull();
    expect(el).toHaveAttribute('role', 'status');
    expect(el).toHaveAttribute('aria-live', 'polite');
    expect(el!.textContent).toBe('');
  }

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

  it('token retype hint: the region exists before HTTP is selected and fills when the URL change empties the saved token', async () => {
    const user = userEvent.setup();
    mockedFetchAppSettings.mockResolvedValueOnce(
      makeResponse({ dbBackend: 'sqlite', httpDataSourceUrl: 'https://a.example.com', httpDataSourceToken: REDACTED_SECRET_PLACEHOLDER })
    );
    renderEditor();
    await screen.findByLabelText(i18n.t('settings.appSettings.storage.dbPathLabel'));

    const region = regionEndingWith('-http-token-retype');
    expectEmptyLiveRegion(region);

    await user.click(httpRadio());
    expect(region.textContent).toBe('');
    const url = screen.getByLabelText(urlLabel());
    await user.clear(url);
    await user.type(url, 'https://b.example.com');

    const hint = i18n.t('settings.appSettings.storage.httpTokenRetypeHint');
    expect(regionEndingWith('-http-token-retype')).toBe(region);
    expect(region).toHaveTextContent(hint);
    expect(screen.getByLabelText(tokenLabel())).toHaveAccessibleDescription(expect.stringContaining(hint));
    expectVisibleCopyIsAriaHidden(hint, region);

    await user.clear(url);
    await user.type(url, 'https://a.example.com');
    expect(region.textContent).toBe('');
  });

  it('data source problem: the region exists before HTTP is selected and carries the current problem', async () => {
    const user = userEvent.setup();
    mockedFetchAppSettings.mockResolvedValueOnce(makeResponse());
    renderEditor();
    await screen.findByLabelText(i18n.t('settings.appSettings.storage.dbPathLabel'));

    const region = regionEndingWith('-http-problem');
    expectEmptyLiveRegion(region);

    // An empty URL is a problem as soon as HTTP is selected.
    await user.click(httpRadio());
    const urlRequired = i18n.t('settings.appSettings.storage.httpUrlRequiredHint');
    expect(regionEndingWith('-http-problem')).toBe(region);
    expect(region).toHaveTextContent(urlRequired);

    const url = screen.getByLabelText(urlLabel());
    await user.type(url, 'not a url');
    const urlInvalid = i18n.t('settings.appSettings.storage.httpUrlInvalidHint');
    expect(region).toHaveTextContent(urlInvalid);
    expect(url).toHaveAccessibleDescription(expect.stringContaining(urlInvalid));
    expectVisibleCopyIsAriaHidden(urlInvalid, region);

    await user.clear(url);
    await user.type(url, 'https://example.com');
    const tokenRequired = i18n.t('settings.appSettings.storage.httpTokenRequiredHint');
    expect(region).toHaveTextContent(tokenRequired);
    expect(screen.getByLabelText(tokenLabel())).toHaveAccessibleDescription(expect.stringContaining(tokenRequired));
    expectVisibleCopyIsAriaHidden(tokenRequired, region);

    await user.type(screen.getByLabelText(tokenLabel()), 'secret');
    expect(region.textContent).toBe('');
  });
});
