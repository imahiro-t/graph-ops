// DFLT-00080 (plan review condition 1): when the selected project has no
// local path in this environment, the settings API answers every
// scope=project request with PROJECT_LOCAL_PATH_NOT_SET. That must stay
// confined to the project-scoped editors -- the modal still opens, the
// global scope still loads, and the App Settings tab (where the
// local path is set) is fully usable.
//
// Unlike SettingsModal.test.tsx this does not mock lib/settingsApi: fetch is
// routed by URL, so the real request helpers turn the error code into the
// localized message exactly as in the running app.
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { SettingsModal } from './SettingsModal';
import { Project } from '../types';

const beta: Project = { id: 'p-beta', name: 'Beta', prefix: 'BETA', local_path: '', created_at: '', updated_at: '' };

const appSettings = {
  file: {
    dbBackend: 'sqlite', dbPath: '', mysqlHost: '', mysqlPort: 3306, mysqlDatabase: '', mysqlUser: '', mysqlPassword: '',
    mysqlTls: 'verify-full', mysqlTlsCa: '', artifactsDir: '', userExtensionsDir: '', paginationPageSize: 10, myName: ''
  },
  effective: { dbBackend: 'sqlite', dbPath: '/tmp/graph.db', artifactsDir: '/tmp/a', userExtensionsDir: '/tmp/u', paginationPageSize: 10 },
  config_path: '/tmp/graph-config.json'
};

function respond(status: number, body: unknown) {
  return Promise.resolve({ ok: status >= 200 && status < 300, status, json: async () => body });
}

type Call = { url: string; method: string; body?: unknown };
const recorded: Call[] = [];

function routeFetch(input: RequestInfo | URL, init?: RequestInit) {
  const url = String(input);
  const method = init?.method ?? 'GET';
  recorded.push({ url, method, body: init?.body ? JSON.parse(init.body as string) : undefined });
  if (url.includes('scope=project') || (init?.body && String(init.body).includes('"scope":"project"'))) {
    return respond(400, { error: { code: 'PROJECT_LOCAL_PATH_NOT_SET', message: 'no local path' } });
  }
  if (url.startsWith('/api/settings/node-types?')) {
    return respond(200, { types: [{ type: 'implementation', has_default: true, has_user_override: false, has_team_override: false }] });
  }
  if (url.startsWith('/api/settings/node-types/implementation')) {
    if (method === 'PUT') return respond(200, { type: 'implementation', tier_text: 'global rule', merged_text: 'merged' });
    return respond(200, { type: 'implementation', tier_text: '', merged_text: 'default text' });
  }
  if (url === '/api/settings/app') return respond(200, appSettings);
  if (url === `/api/projects/${beta.id}` && method === 'PATCH') return respond(200, { ...beta, local_path: '/work/beta' });
  return respond(404, { error: { code: 'NOT_FOUND', message: url } });
}

function renderModal(onProjectsChanged = vi.fn()) {
  return render(
    <SettingsModal
      isOpen
      onClose={vi.fn()}
      projects={[beta]}
      currentProject={beta}
      onProjectsChanged={onProjectsChanged}
      onPaginationPageSizeChanged={vi.fn()}
      onMyNameChanged={vi.fn()}
    />
  );
}

describe('SettingsModal with a project that has no local path', () => {
  beforeEach(() => {
    recorded.length = 0;
    vi.stubGlobal('fetch', vi.fn(routeFetch));
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('keeps the modal, the global scope and project management usable; only the project scope shows guidance', async () => {
    const user = userEvent.setup();
    const onProjectsChanged = vi.fn();
    renderModal(onProjectsChanged);

    // Global scope loads normally.
    expect(await screen.findByText('default text')).toBeInTheDocument();

    // Project scope: a banner plus the editor's own localized error, no crash.
    await user.click(screen.getByRole('button', { name: i18n.t('settings.scope.project') }));
    expect(await screen.findByText(i18n.t('settings.scope.localPathNotSet'))).toBeInTheDocument();
    expect(await screen.findByText(i18n.t('errors.PROJECT_LOCAL_PATH_NOT_SET'))).toBeInTheDocument();
    expect(screen.getByRole('button', { name: i18n.t('settings.tabs.appSettings') })).toBeEnabled();

    // Back to global: loads again without errors.
    recorded.length = 0;
    await user.click(screen.getByRole('button', { name: i18n.t('settings.scope.global') }));
    expect(screen.queryByText(i18n.t('settings.scope.localPathNotSet'))).not.toBeInTheDocument();
    expect(await screen.findByText('default text')).toBeInTheDocument();
    await waitFor(() =>
      expect(recorded.some(c => c.method === 'GET' && c.url.startsWith('/api/settings/node-types/implementation?scope=global'))).toBe(true)
    );

    // App Settings tab: project management lets the user set Beta's local path.
    await user.click(screen.getByRole('button', { name: i18n.t('settings.tabs.appSettings') }));
    const betaRow = within(await screen.findByTestId(`project-row-${beta.id}`));
    expect(betaRow.getAllByText(i18n.t('settings.appSettings.projects.notSet')).length).toBeGreaterThan(0);
    const input = betaRow.getByRole('textbox', { name: new RegExp(i18n.t('settings.appSettings.projects.localPathLabel').replace(/[()]/g, '\\$&')) });
    await user.type(input, '/work/beta');
    await user.click(betaRow.getByRole('button', { name: i18n.t('settings.common.save') }));

    await waitFor(() => expect(onProjectsChanged).toHaveBeenCalled());
    expect(recorded).toContainEqual({ url: `/api/projects/${beta.id}`, method: 'PATCH', body: { name: 'Beta', local_path: '/work/beta' } });
  });
});
