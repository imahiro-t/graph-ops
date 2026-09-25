// DFLT-00142 phase 2: the "オートパイロット" settings tab. fetch is stubbed
// (not the settingsApi functions) so the tests see the real PUT body and the
// real error translation path.
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi, type MockInstance } from 'vitest';
import i18n from '../../i18n';
import { AutopilotSettingsEditor } from './AutopilotSettingsEditor';
import { AutopilotSettingItem, AutopilotSettingsResponse, AutopilotSettingKey } from '../../types';

const DEFAULTS: Record<AutopilotSettingKey, string | number | boolean> = {
  mainReflection: 'branch',
  permissionMode: 'auto',
  autoApproveGates: true,
  autoCreateTickets: true,
  maxTickets: 20,
  maxDepth: 3,
  onFailure: 'stop',
  stallTimeoutMinutes: 60
};

function response(overrides: Partial<Record<AutopilotSettingKey, Partial<AutopilotSettingItem>>> = {}, warnings: AutopilotSettingsResponse['warnings'] = []): AutopilotSettingsResponse {
  const items = (Object.keys(DEFAULTS) as AutopilotSettingKey[]).map(key => ({
    key,
    value: DEFAULTS[key],
    source: 'default' as const,
    locked: false,
    local: null,
    team: null,
    default: DEFAULTS[key],
    ...overrides[key]
  }));
  const settings = Object.fromEntries(items.map(it => [it.key, it.value])) as unknown as AutopilotSettingsResponse['settings'];
  return { project_id: 'proj-A', settings, items, warnings, team_file: '' };
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

let fetchSpy: MockInstance<typeof fetch>;
let serverState: AutopilotSettingsResponse;
let putBodies: unknown[];

function stubServer(initial: AutopilotSettingsResponse, onPut?: (body: Record<string, unknown>) => Response) {
  serverState = initial;
  putBodies = [];
  fetchSpy.mockImplementation(async (input, init) => {
    const url = String(input);
    expect(url).toBe('/api/projects/proj-A/autopilot-settings');
    if (init?.method === 'PUT') {
      const body = JSON.parse(String(init.body)) as Record<string, unknown>;
      putBodies.push(body);
      if (onPut) return onPut(body);
      const next = response(
        Object.fromEntries(
          Object.entries(body).map(([k, v]) => [
            k,
            v === null
              ? { value: DEFAULTS[k as AutopilotSettingKey], source: 'default', local: null }
              : { value: v, source: 'local', local: v }
          ])
        ) as Partial<Record<AutopilotSettingKey, Partial<AutopilotSettingItem>>>
      );
      serverState = next;
      return jsonResponse(next);
    }
    return jsonResponse(serverState);
  });
}

function renderEditor(onDirtyChange = vi.fn()) {
  return render(<AutopilotSettingsEditor projectId="proj-A" projectName="Project A" onDirtyChange={onDirtyChange} />);
}

const label = (key: AutopilotSettingKey) => i18n.t(`settings.autopilot.keys.${key}.label`);

describe('AutopilotSettingsEditor', () => {
  beforeEach(async () => {
    await i18n.changeLanguage('ja');
    fetchSpy = vi.spyOn(globalThis, 'fetch');
  });

  afterEach(async () => {
    fetchSpy.mockRestore();
    await i18n.changeLanguage('ja');
  });

  it('edits a value and saves only that key, then shows the saved value', async () => {
    const user = userEvent.setup();
    const onDirty = vi.fn();
    stubServer(response());
    renderEditor(onDirty);

    const input = (await screen.findByLabelText(label('maxTickets'))) as HTMLInputElement;
    expect(input.value).toBe('20');
    expect(screen.getByTestId('autopilot-source-maxTickets')).toHaveTextContent(i18n.t('settings.autopilot.source.default'));

    await user.clear(input);
    await user.type(input, '30');
    expect(onDirty).toHaveBeenLastCalledWith(true);
    await user.click(screen.getByRole('button', { name: i18n.t('settings.common.save') }));

    await waitFor(() => expect(putBodies).toEqual([{ maxTickets: 30 }]));
    // Announced by the always-mounted live region (SC 4.1.3); the visible
    // flash is aria-hidden so it is not read twice.
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent(i18n.t('settings.common.saveSuccess')));
    expect(screen.getByTestId('autopilot-source-maxTickets')).toHaveTextContent(i18n.t('settings.autopilot.source.local'));
    expect((screen.getByLabelText(label('maxTickets')) as HTMLInputElement).value).toBe('30');
    expect(onDirty).toHaveBeenLastCalledWith(false);

    // Re-opening (a fresh mount) shows the saved value from the server.
    const { unmount } = renderEditor();
    await waitFor(() => expect(screen.getAllByLabelText(label('maxTickets')).map(e => (e as HTMLInputElement).value)).toEqual(['30', '30']));
    unmount();
  });

  it('disables team-locked keys, shows where they come from, and never sends them', async () => {
    const user = userEvent.setup();
    stubServer(
      response({
        mainReflection: { value: 'pull_request', source: 'team_project', locked: true, team: 'pull_request', local: 'merge' }
      })
    );
    renderEditor();

    const select = (await screen.findByLabelText(label('mainReflection'))) as HTMLSelectElement;
    expect(select).toBeDisabled();
    expect(select.value).toBe('pull_request');
    const source = screen.getByTestId('autopilot-source-mainReflection');
    expect(source).toHaveTextContent(i18n.t('settings.autopilot.source.team_project'));
    expect(source).toHaveTextContent(i18n.t('settings.autopilot.keys.mainReflection.options.merge'));
    expect(within(source.parentElement!.parentElement!).queryByRole('button')).toBeNull();

    await user.selectOptions(screen.getByLabelText(label('onFailure')), 'continue');
    await user.click(screen.getByLabelText(label('autoApproveGates')));
    await user.click(screen.getByRole('button', { name: i18n.t('settings.common.save') }));

    await waitFor(() => expect(putBodies).toHaveLength(1));
    expect(putBodies[0]).toEqual({ onFailure: 'continue', autoApproveGates: false });
    expect(putBodies[0]).not.toHaveProperty('mainReflection');
  });

  it('removes a local value with the reset button (sends null)', async () => {
    const user = userEvent.setup();
    stubServer(response({ maxDepth: { value: 2, source: 'local', local: 2 } }));
    renderEditor();

    await screen.findByLabelText(label('maxDepth'));
    await user.click(screen.getByRole('button', { name: i18n.t('settings.autopilot.clearLocalFor', { key: label('maxDepth') }) }));
    expect((screen.getByLabelText(label('maxDepth')) as HTMLInputElement).value).toBe('3');
    await user.click(screen.getByRole('button', { name: i18n.t('settings.common.save') }));
    await waitFor(() => expect(putBodies).toEqual([{ maxDepth: null }]));
  });

  it('warns when bypassPermissions is selected', async () => {
    const user = userEvent.setup();
    stubServer(response());
    renderEditor();

    const select = await screen.findByLabelText(label('permissionMode'));
    expect(screen.queryByText(i18n.t('settings.autopilot.bypassWarning'))).toBeNull();
    await user.selectOptions(select, 'bypassPermissions');
    expect(screen.getByText(i18n.t('settings.autopilot.bypassWarning'))).toBeInTheDocument();
  });

  it.each(['ja', 'en'])('translates a 400 AUTOPILOT_SETTING_LOCKED into %s with the key names', async lang => {
    await i18n.changeLanguage(lang);
    const user = userEvent.setup();
    stubServer(response(), () =>
      jsonResponse({ error: { code: 'AUTOPILOT_SETTING_LOCKED', message: 'locked: maxTickets', details: { keys: ['maxTickets'] } } }, 400)
    );
    renderEditor();

    const input = await screen.findByLabelText(label('maxTickets'));
    await user.clear(input);
    await user.type(input, '12');
    await user.click(screen.getByRole('button', { name: i18n.t('settings.common.save') }));

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent(i18n.t('errors.AUTOPILOT_SETTING_LOCKED'));
    expect(alert).toHaveTextContent(label('maxTickets'));
    expect(alert).not.toHaveTextContent('locked: maxTickets');
  });

  it('sends an invalid number as typed so the server validation reports it', async () => {
    const user = userEvent.setup();
    stubServer(response(), () =>
      jsonResponse({ error: { code: 'VALIDATION_ERROR', message: 'maxTickets: ...', details: { keys: ['maxTickets'] } } }, 400)
    );
    renderEditor();

    const input = await screen.findByLabelText(label('maxTickets'));
    await user.clear(input);
    await user.type(input, '101');
    await user.click(screen.getByRole('button', { name: i18n.t('settings.common.save') }));
    expect(await screen.findByRole('alert')).toHaveTextContent(i18n.t('errors.VALIDATION_ERROR'));
    expect(putBodies).toEqual([{ maxTickets: 101 }]);
  });

  it('shows translated warnings and the team settings file', async () => {
    const initial = response({}, [
      { code: 'AUTOPILOT_TEAM_BYPASS_PERMISSIONS_IGNORED', source: 'team_defaults', key: 'permissionMode', value: '"bypassPermissions"', message: 'en message' },
      { code: 'AUTOPILOT_INVALID_VALUE', source: 'team_defaults', key: 'maxTickets', value: '1000', message: 'en message' },
      { code: 'SOMETHING_NEW', message: 'fallback english' }
    ]);
    initial.team_file = '/shared/team/autopilot.yaml';
    stubServer(initial);
    renderEditor();

    expect(await screen.findByText(i18n.t('settings.autopilot.teamFile', { path: '/shared/team/autopilot.yaml' }))).toBeInTheDocument();
    expect(screen.getByText(/1000/)).toHaveTextContent(label('maxTickets'));
    expect(screen.getByText(/bypassPermissions/, { selector: 'li' })).toBeInTheDocument();
    expect(screen.getByText('fallback english')).toBeInTheDocument();
  });

  // DFLT-00142 accessibility review (iteration 1).
  it('ties a locked control to where its value comes from, and uses readable colors', async () => {
    const user = userEvent.setup();
    stubServer(
      response({
        mainReflection: { value: 'pull_request', source: 'team_project', locked: true, team: 'pull_request' },
        maxDepth: { value: 2, source: 'local', local: 2 }
      })
    );
    renderEditor();

    const select = await screen.findByLabelText(label('mainReflection'));
    const source = screen.getByTestId('autopilot-source-mainReflection');
    // The reason it cannot be changed is part of the control's description.
    expect(select).toHaveAccessibleDescription(expect.stringContaining(i18n.t('settings.autopilot.source.team_project')));
    expect(select.getAttribute('aria-describedby')?.split(' ')).toContain(source.id);
    expect(source.className).toContain('text-slate-500');
    expect(source.className).not.toContain('text-slate-400 dark:text-slate-500');

    const reset = screen.getByRole('button', { name: i18n.t('settings.autopilot.clearLocalFor', { key: label('maxDepth') }) });
    expect(reset.className).toContain('text-slate-500');

    await user.clear(screen.getByLabelText(label('maxTickets')));
    await user.type(screen.getByLabelText(label('maxTickets')), '30');
    await user.click(screen.getByRole('button', { name: i18n.t('settings.common.save') }));
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent(i18n.t('settings.common.saveSuccess')));
    const flash = screen.getByText(i18n.t('settings.common.saveSuccess'), { selector: 'span[aria-hidden="true"]' });
    expect(flash.className).toContain('text-emerald-700');
    expect(flash.className).toContain('dark:text-emerald-400');
  });

  it('asks for a project when none is selected', () => {
    fetchSpy.mockImplementation(async () => jsonResponse({}));
    render(<AutopilotSettingsEditor projectId="" onDirtyChange={vi.fn()} />);
    expect(screen.getByText(i18n.t('settings.autopilot.noProject'))).toBeInTheDocument();
    expect(fetchSpy).not.toHaveBeenCalled();
  });
});
