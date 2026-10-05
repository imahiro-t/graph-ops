// DFLT-00142 phase 2: the "オートパイロット" settings tab. fetch is stubbed
// (not the settingsApi functions) so the tests see the real PUT body and the
// real error translation path.
import { act, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, beforeEach, describe, expect, it, vi, type MockInstance } from 'vitest';
import i18n from '../../i18n';
import { AutopilotSettingsEditor } from './AutopilotSettingsEditor';
import { AutopilotSettingItem, AutopilotSettingsResponse, AutopilotSettingKey, Project } from '../../types';
import { expectSecondSaveReannounced } from '../../test/savedReannouncement';

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

const project = (id: string, name: string): Project => ({ id, name, prefix: '', local_path: '', created_at: '', updated_at: '' });
// What the tab's own project selector offers (DFLT-00374).
const PROJECTS: Project[] = [project('proj-A', 'Project A'), project('proj-B', 'Project B'), project('proj-C', 'Project C')];
const projectSelect = () => screen.getByLabelText(i18n.t('settings.autopilot.projectLabel')) as HTMLSelectElement;

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
  return render(<AutopilotSettingsEditor projects={PROJECTS} initialProjectId="proj-A" onDirtyChange={onDirtyChange} />);
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

  // DFLT-00343: the first render must already be the loading line, not the
  // editor with its empty defaults. render() runs effects straight away, so
  // the DOM is already past the first frame; renderToStaticMarkup renders
  // once and runs no effect, which is exactly that first frame.
  it('shows the loading line, not the default settings, before they have loaded', () => {
    fetchSpy.mockReturnValue(new Promise(() => {}));
    const html = renderToStaticMarkup(
      <AutopilotSettingsEditor projects={PROJECTS} initialProjectId="proj-A" onDirtyChange={vi.fn()} />
    );
    expect(html).toContain(i18n.t('settings.common.loading'));
    expect(html).not.toContain(label('maxTickets'));
    expect(html).not.toContain('<input');
    // Only the project selector, none of the settings' selects.
    expect(html.split('<select').length - 1).toBe(1);
    // The button's own text, not the word inside the intro paragraph.
    expect(html).not.toContain(`${i18n.t('settings.common.save')}</button>`);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it('keeps showing the no-project message, not the loading line, on the first frame with no project', () => {
    fetchSpy.mockReturnValue(new Promise(() => {}));
    const html = renderToStaticMarkup(<AutopilotSettingsEditor projects={[]} initialProjectId="" onDirtyChange={vi.fn()} />);
    expect(html).toContain(i18n.t('settings.autopilot.noProjects'));
    expect(html).not.toContain(i18n.t('settings.common.loading'));
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  // DFLT-00359: a second save while the confirmation is still shown is
  // announced again (the live region is emptied and refilled).
  it('announces a second save made while the confirmation is still shown', async () => {
    const user = userEvent.setup();
    stubServer(response());
    renderEditor();
    const input = (await screen.findByLabelText(label('maxTickets'))) as HTMLInputElement;
    const save = () => screen.getByRole('button', { name: i18n.t('settings.common.save') });

    await user.clear(input);
    await user.type(input, '30');
    await user.click(save());

    await expectSecondSaveReannounced(async () => {
      await user.clear(input);
      await user.type(input, '25');
      await user.click(save());
    });
    expect(putBodies).toEqual([{ maxTickets: 30 }, { maxTickets: 25 }]);
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
    await waitFor(() => expect(screen.getByText(i18n.t('settings.common.saveSuccess'), { selector: '[role="status"]' })).toBeInTheDocument());
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
    await waitFor(() => expect(screen.getByText(i18n.t('settings.common.saveSuccess'), { selector: '[role="status"]' })).toBeInTheDocument());
    const flash = screen.getByText(i18n.t('settings.common.saveSuccess'), { selector: 'span[aria-hidden="true"]' });
    expect(flash.className).toContain('text-emerald-700');
    expect(flash.className).toContain('dark:text-emerald-400');
  });

  // DFLT-00374 completion criterion 3: with no project at all, the selector
  // is disabled and nothing can be edited -- as on the labels tab.
  it('disables the project selector and shows no form when there are no projects', () => {
    fetchSpy.mockImplementation(async () => jsonResponse({}));
    render(<AutopilotSettingsEditor projects={[]} initialProjectId="" onDirtyChange={vi.fn()} />);
    expect(projectSelect()).toBeDisabled();
    expect(projectSelect().value).toBe('');
    expect(screen.getByRole('status')).toHaveTextContent(i18n.t('settings.autopilot.noProjects'));
    expect(screen.queryByText(i18n.t('settings.autopilot.selectProject'))).not.toBeInTheDocument();
    expect(screen.queryByLabelText(label('maxTickets'))).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: i18n.t('settings.common.save') })).not.toBeInTheDocument();
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  // DFLT-00374 completion criterion 2: with no project open in the app, the
  // tab starts on the first one rather than on an empty selection.
  it('starts on the first project and loads its settings when the app has none open', async () => {
    stubServer(response({ maxTickets: { value: 33, source: 'local', local: 33 } }));
    render(<AutopilotSettingsEditor projects={PROJECTS} initialProjectId="" onDirtyChange={vi.fn()} />);
    expect(projectSelect().value).toBe('proj-A');
    expect(await screen.findByDisplayValue('33')).toBeInTheDocument();
    expect(fetchSpy).toHaveBeenCalledWith('/api/projects/proj-A/autopilot-settings', expect.anything());
    expect(screen.queryByText(i18n.t('settings.autopilot.selectProject'))).not.toBeInTheDocument();
  });

  it('shows a heading without the project name, then the project selector with every project', async () => {
    stubServer(response());
    renderEditor();
    await screen.findByLabelText(label('maxTickets'));
    expect(screen.getByRole('heading', { level: 3 })).toHaveTextContent(i18n.t('settings.autopilot.title'));
    expect(screen.getByRole('heading', { level: 3 })).not.toHaveTextContent('Project A');
    const select = projectSelect();
    expect(select).toBeEnabled();
    expect(select.value).toBe('proj-A');
    expect(Array.from(select.options).map(o => [o.value, o.textContent])).toEqual([
      ['', i18n.t('settings.autopilot.projectPlaceholder')],
      ['proj-A', 'Project A'],
      ['proj-B', 'Project B'],
      ['proj-C', 'Project C']
    ]);
  });
});

// DFLT-00261: at a 200% default font on a 320px screen each setting row kept
// its controls (shrink-0) beside the description, so the selects and reset
// buttons ran past the tab panel. Below 48rem (the rem-based `narrow:`
// variant from index.css) the row wraps with the description on a line of
// its own and the controls below it, the controls may shrink (a select
// fills the line), the save row wraps, the tab drops its own full-height
// scroll so the tab panel scrolls instead, and the team settings path breaks
// anywhere if it has to. The wide classes stay. jsdom does no layout, so the
// classes are pinned.
describe('AutopilotSettingsEditor narrow reflow (DFLT-00261)', () => {
  beforeEach(async () => {
    await i18n.changeLanguage('ja');
    fetchSpy = vi.spyOn(globalThis, 'fetch');
  });

  afterEach(async () => {
    fetchSpy.mockRestore();
    await i18n.changeLanguage('ja');
  });

  it('puts the controls under the description and wraps the rows below 48rem, keeping the wide classes', async () => {
    const initial = response();
    initial.team_file = '/shared/team/autopilot.yaml';
    stubServer(initial);
    const { container } = renderEditor();

    const select = (await screen.findByLabelText(label('mainReflection'))) as HTMLSelectElement;
    const root = container.firstElementChild as HTMLElement;
    expect(root).toHaveClass('h-full', 'min-h-0', 'overflow-y-auto', 'narrow:h-auto', 'narrow:overflow-visible');

    expect(select).toHaveClass('narrow:min-w-0', 'narrow:max-w-full', 'narrow:flex-1');
    const controls = select.parentElement as HTMLElement;
    expect(controls).toHaveClass('flex', 'shrink-0', 'narrow:shrink', 'narrow:min-w-0', 'narrow:w-full');
    const settingRow = controls.parentElement as HTMLElement;
    expect(settingRow).toHaveClass('flex', 'items-start', 'gap-3', 'narrow:flex-wrap');
    expect(settingRow.firstElementChild).toHaveClass('flex-1', 'min-w-0', 'narrow:basis-full');

    const number = screen.getByLabelText(label('maxTickets'));
    expect(number).toHaveClass('w-24', 'narrow:min-w-0', 'narrow:max-w-full');
    expect(number).not.toHaveClass('narrow:flex-1');

    const saveButton = screen.getByRole('button', { name: i18n.t('settings.common.save') });
    expect(saveButton.parentElement).toHaveClass('flex', 'justify-end', 'narrow:flex-wrap');

    const teamFile = screen.getByText(i18n.t('settings.autopilot.teamFile', { path: '/shared/team/autopilot.yaml' }));
    expect(teamFile).toHaveClass('wrap-anywhere');
    expect(teamFile).not.toHaveClass('wrap-break-word');
  });
});

// DFLT-00287: at the narrowest size (upto-15rem: -- 480px and below at 200%
// text) a select takes the row's full width and its reset button moves to
// the next line, right-aligned, so enough of the chosen option shows to tell
// the options apart. Label, description, reset button and tab order stay as
// they were. jsdom does no layout or media queries, so the classes are
// pinned; the real widths are measured in a browser (implementation notes).
describe('AutopilotSettingsEditor select stacking at the narrowest size (DFLT-00287)', () => {
  const SELECT_KEYS: AutopilotSettingKey[] = ['mainReflection', 'permissionMode', 'onFailure'];

  beforeEach(async () => {
    await i18n.changeLanguage('ja');
    fetchSpy = vi.spyOn(globalThis, 'fetch');
  });

  afterEach(async () => {
    fetchSpy.mockRestore();
    await i18n.changeLanguage('ja');
  });

  it.each(SELECT_KEYS)('stacks the %s select above its reset button', async key => {
    stubServer(response());
    renderEditor();

    const select = (await screen.findByLabelText(label(key))) as HTMLSelectElement;
    expect(select.tagName).toBe('SELECT');
    expect(select).toHaveClass('upto-15rem:basis-full', 'upto-15rem:w-full');
    const controls = select.parentElement as HTMLElement;
    expect(controls).toHaveClass('upto-15rem:flex-wrap', 'upto-15rem:justify-end');
    const selectCls = controls.getAttribute('class') ?? '';
    expect(selectCls).toBe(selectCls.trim());
    const reset = screen.getByRole('button', { name: i18n.t('settings.autopilot.clearLocalFor', { key: label(key) }) });
    expect(controls).toContainElement(reset);
    const row = controls.parentElement as HTMLElement;
    expect(row).toHaveClass('px-3', 'upto-15rem:px-2');
  });

  it('leaves the checkbox and number controls unstacked', async () => {
    stubServer(response());
    renderEditor();

    for (const key of ['autoApproveGates', 'maxTickets'] as AutopilotSettingKey[]) {
      const input = await screen.findByLabelText(label(key));
      const controls = input.parentElement as HTMLElement;
      expect(controls).not.toHaveClass('upto-15rem:flex-wrap');
      expect(controls).not.toHaveClass('upto-15rem:justify-end');
      // No trailing space left by the unused select-only classes (DFLT-00297).
      const cls = controls.getAttribute('class') ?? '';
      expect(cls).toBe(cls.trim());
      expect(input).not.toHaveClass('upto-15rem:basis-full');
    }
  });

  it('keeps each select labelled, described, keyboard-operable and followed by its reset button', async () => {
    const user = userEvent.setup();
    stubServer(response({ onFailure: { value: 'continue', source: 'local', local: 'continue' } }));
    renderEditor();

    for (const key of SELECT_KEYS) {
      const select = (await screen.findByLabelText(label(key))) as HTMLSelectElement;
      const describedBy = select.getAttribute('aria-describedby')?.split(' ') ?? [];
      expect(describedBy).toHaveLength(2);
      for (const id of describedBy) expect(document.getElementById(id)).not.toBeNull();
      expect(document.getElementById(describedBy[0])).toHaveTextContent(i18n.t(`settings.autopilot.keys.${key}.hint`));
    }

    // Changing the value (jsdom has no native select keyboard handling, so
    // the option is picked directly) enables the reset button, which is the
    // next tab stop after the select.
    const select = screen.getByLabelText(label('mainReflection')) as HTMLSelectElement;
    await user.selectOptions(select, 'merge');
    expect(select.value).toBe('merge');
    const reset = screen.getByRole('button', { name: i18n.t('settings.autopilot.clearLocalFor', { key: label('mainReflection') }) });
    expect(reset).toBeEnabled();
    select.focus();
    await user.tab();
    expect(reset).toHaveFocus();
    await user.click(reset);
    expect(select.value).toBe('branch');

    // A key with a saved local value: its reset clears it back to the default.
    const onFailure = screen.getByLabelText(label('onFailure')) as HTMLSelectElement;
    expect(onFailure.value).toBe('continue');
    await user.click(screen.getByRole('button', { name: i18n.t('settings.autopilot.clearLocalFor', { key: label('onFailure') }) }));
    expect(onFailure.value).toBe('stop');
  });
});

// DFLT-00350: a failed load shows the error and a retry button instead of the
// form, and a project switch never shows the previous project's settings or
// error, nor applies an answer that arrives for it late.
describe('AutopilotSettingsEditor load failure and project switch', () => {
  // method tells a load (GET) from a save (PUT): both use the same URL.
  type Pending = { url: string; method: string; resolve: (r: Response) => void; reject: (e: unknown) => void };
  let pending: Pending[];
  const retryButton = () => screen.getByRole('button', { name: i18n.t('settings.common.retry') });
  const loadingLine = () => screen.queryByText(i18n.t('settings.common.loading'), { selector: '[role="status"]' });
  const errorBody = (message: string) => jsonResponse({ error: { code: 'INTERNAL', message } }, 500);
  const forProject = (id: string, overrides: Parameters<typeof response>[0] = {}) => ({
    ...response(overrides),
    project_id: id
  });
  const settle = (url: string, method = 'GET') => {
    const i = pending.findIndex(p => p.url === url && p.method === method);
    expect(i).toBeGreaterThanOrEqual(0);
    return pending.splice(i, 1)[0];
  };
  // The editor starting on initialProjectId. Projects are switched from its
  // own selector (DFLT-00374), not by re-rendering with another project.
  const editor = (initialProjectId: string, onDirtyChange: (dirty: boolean) => void = vi.fn()) => (
    <AutopilotSettingsEditor projects={PROJECTS} initialProjectId={initialProjectId} onDirtyChange={onDirtyChange} />
  );
  const urlOf = (id: string) => `/api/projects/${id}/autopilot-settings`;
  const confirmDialog = () => screen.queryByTestId('autopilot-project-discard-confirm');
  // Picks a project from the selector. discard: there are unsaved edits, so
  // the confirmation must come up -- and is answered with "discard".
  const switchTo = async (user: ReturnType<typeof userEvent.setup>, id: string, { discard = false } = {}) => {
    await user.selectOptions(projectSelect(), id);
    if (discard) {
      expect(confirmDialog()).toBeInTheDocument();
      await user.click(screen.getByTestId('autopilot-project-discard-confirm-confirm'));
    }
    expect(confirmDialog()).not.toBeInTheDocument();
    expect(projectSelect().value).toBe(id);
  };

  beforeEach(async () => {
    await i18n.changeLanguage('ja');
    fetchSpy = vi.spyOn(globalThis, 'fetch');
    pending = [];
    fetchSpy.mockImplementation(
      (input: RequestInfo | URL, init?: RequestInit) =>
        new Promise<Response>((resolve, reject) => {
          pending.push({ url: String(input), method: init?.method ?? 'GET', resolve, reject });
        })
    );
  });

  afterEach(async () => {
    fetchSpy.mockRestore();
    await i18n.changeLanguage('ja');
  });

  it('shows the loading line as a status', () => {
    render(editor('proj-A'));
    expect(screen.getByRole('status')).toHaveTextContent(i18n.t('settings.common.loading'));
  });

  it('shows the error and a retry button, not the form, and loads the settings on retry', async () => {
    const user = userEvent.setup();
    render(editor('proj-A'));
    await act(async () => { settle(urlOf('proj-A')).resolve(errorBody('settings exploded')); });

    expect(await screen.findByRole('alert')).toBeInTheDocument();
    expect(screen.queryByLabelText(label('maxTickets'))).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: i18n.t('settings.common.save') })).not.toBeInTheDocument();
    expect(screen.getByText(i18n.t('settings.autopilot.description'))).toBeInTheDocument();

    const button = retryButton();
    button.focus();
    await user.keyboard('{Enter}');
    expect(button).toHaveFocus();
    expect(button).toHaveAttribute('aria-busy', 'true');
    expect(screen.getByRole('alert')).toBeInTheDocument();
    await user.keyboard('{Enter}');
    expect(pending).toHaveLength(1);

    await act(async () => { settle(urlOf('proj-A')).resolve(jsonResponse(forProject('proj-A'))); });

    const input = await screen.findByLabelText(label('maxTickets'));
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    await waitFor(() => expect(document.activeElement).toHaveAttribute('tabindex', '-1'));
    expect(document.activeElement?.contains(input)).toBe(true);
    // ...which is a named group, so a screen reader says what appeared.
    expect(document.activeElement).toHaveAccessibleName(i18n.t('settings.tabs.autopilot'));
    expect(document.activeElement).toBe(screen.getByRole('group', { name: i18n.t('settings.tabs.autopilot') }));
    expect(document.activeElement).toHaveClass('focus:outline-hidden');
  });

  it('keeps the error and the retry button when the retry fails again', async () => {
    const user = userEvent.setup();
    render(editor('proj-A'));
    await act(async () => { settle(urlOf('proj-A')).resolve(errorBody('first')); });
    const first = await screen.findByRole('alert');

    retryButton().focus();
    await user.keyboard('{Enter}');
    await act(async () => { settle(urlOf('proj-A')).resolve(errorBody('second')); });

    await waitFor(() => expect(screen.getByRole('alert')).not.toBe(first));
    expect(retryButton()).toHaveFocus();
    expect(retryButton()).not.toHaveAttribute('aria-busy');
    expect(screen.queryByLabelText(label('maxTickets'))).not.toBeInTheDocument();
  });

  it('shows the loading line, not the previous project\'s settings, right after the project changes', async () => {
    const user = userEvent.setup();
    render(editor('proj-A'));
    await act(async () => {
      settle(urlOf('proj-A')).resolve(
        jsonResponse({ ...forProject('proj-A', { maxTickets: { value: 77, local: 77, source: 'local' } }), team_file: '/team/a.json' })
      );
    });
    expect(await screen.findByDisplayValue('77')).toBeInTheDocument();

    await switchTo(user, 'proj-B');

    expect(loadingLine()).toBeInTheDocument();
    expect(screen.queryByDisplayValue('77')).not.toBeInTheDocument();
    expect(screen.queryByText(/\/team\/a\.json/)).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: i18n.t('settings.common.save') })).not.toBeInTheDocument();
  });

  it('does not show the previous project\'s load error after the project changes', async () => {
    const user = userEvent.setup();
    render(editor('proj-A'));
    await act(async () => { settle(urlOf('proj-A')).resolve(errorBody('A failed')); });
    await screen.findByRole('alert');

    await switchTo(user, 'proj-B');

    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(loadingLine()).toBeInTheDocument();
  });

  it.each([
    ['succeeds', (p: Pending) => p.resolve(jsonResponse(forProject('proj-A', { maxTickets: { value: 55, local: 55, source: 'local' } })))],
    ['fails', (p: Pending) => p.resolve(errorBody('late A failure'))]
  ])('ignores a late answer for the previous project (it %s)', async (_how, finish) => {
    const user = userEvent.setup();
    render(editor('proj-A'));
    await switchTo(user, 'proj-B');
    await act(async () => {
      settle(urlOf('proj-B')).resolve(jsonResponse(forProject('proj-B', { maxTickets: { value: 44, local: 44, source: 'local' } })));
    });
    expect(await screen.findByDisplayValue('44')).toBeInTheDocument();

    await act(async () => { finish(settle(urlOf('proj-A'))); });

    expect(screen.getByDisplayValue('44')).toBeInTheDocument();
    expect(screen.queryByDisplayValue('55')).not.toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  // DFLT-00355: the save in progress belongs to the project it was started
  // for. Its busy state never reaches another project's form, and its answer
  // is dropped once the project has changed.
  const saveText = () => i18n.t('settings.common.save');
  const savingText = () => i18n.t('settings.common.saving');
  // The save button, whether idle ("保存") or busy ("保存中").
  const saveButton = () => {
    const buttons = screen.getAllByRole('button').filter(b => {
      const text = b.textContent?.trim() ?? '';
      return text === saveText() || text.includes(savingText());
    });
    expect(buttons).toHaveLength(1);
    return buttons[0];
  };
  const expectBusy = (busy: boolean) => {
    const button = saveButton();
    if (busy) {
      expect(button).toBeDisabled();
      expect(button).toHaveAttribute('aria-busy', 'true');
      expect(button).toHaveTextContent(savingText());
    } else {
      expect(button).not.toHaveAttribute('aria-busy');
      expect(button).not.toHaveTextContent(savingText());
    }
  };
  const maxTicketsInput = () => screen.getByLabelText(label('maxTickets')) as HTMLInputElement;
  const loadWith = async (id: string, maxTickets: number) => {
    await act(async () => {
      settle(urlOf(id)).resolve(jsonResponse(forProject(id, { maxTickets: { value: maxTickets, local: maxTickets, source: 'local' } })));
    });
    expect(await screen.findByDisplayValue(String(maxTickets))).toBeInTheDocument();
  };
  const typeMaxTickets = async (user: ReturnType<typeof userEvent.setup>, value: string) => {
    await user.clear(maxTicketsInput());
    await user.type(maxTicketsInput(), value);
  };

  it('keeps the form busy while its own save runs, and releases it with the saved value', async () => {
    const user = userEvent.setup();
    render(editor('proj-A'));
    await loadWith('proj-A', 20);

    await typeMaxTickets(user, '30');
    await user.click(saveButton());

    expectBusy(true);
    expect(maxTicketsInput()).toBeDisabled();
    expect(pending.map(p => p.method)).toEqual(['PUT']);

    await act(async () => {
      settle(urlOf('proj-A'), 'PUT').resolve(
        jsonResponse(forProject('proj-A', { maxTickets: { value: 30, local: 30, source: 'local' } }))
      );
    });

    expectBusy(false);
    expect(saveButton()).toBeDisabled(); // nothing left to save
    expect(maxTicketsInput()).not.toBeDisabled();
    expect(maxTicketsInput().value).toBe('30');
    expect(screen.getByText(i18n.t('settings.common.saveSuccess'), { selector: '[role="status"]' })).toBeInTheDocument();
  });

  it.each([
    ['succeeds', (p: Pending) => p.resolve(jsonResponse(forProject('proj-A', { maxTickets: { value: 55, local: 55, source: 'local' } })))],
    ['fails', (p: Pending) => p.resolve(errorBody('late A save failure'))]
  ])('does not show the previous project\'s save as busy, and ignores its late answer (it %s)', async (_how, finish) => {
    const user = userEvent.setup();
    const onDirty = vi.fn();
    render(editor('proj-A', onDirty));
    await loadWith('proj-A', 20);
    await typeMaxTickets(user, '30');
    await user.click(saveButton());
    expectBusy(true);
    expect(maxTicketsInput()).toBeDisabled();

    // The edit is still unsaved until the answer is in, so leaving asks.
    await switchTo(user, 'proj-B', { discard: true });
    await loadWith('proj-B', 44);

    // B is not busy while A's save is still running.
    expectBusy(false);
    expect(maxTicketsInput()).not.toBeDisabled();
    await typeMaxTickets(user, '46');
    expect(saveButton()).toBeEnabled();
    expect(onDirty).toHaveBeenLastCalledWith(true);
    const dirtyCalls = onDirty.mock.calls.length;

    await act(async () => { finish(settle(urlOf('proj-A'), 'PUT')); });

    // A's answer changes nothing in B: value, error, busy or dirty state.
    expect(maxTicketsInput().value).toBe('46');
    expect(screen.queryByDisplayValue('55')).not.toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expectBusy(false);
    expect(saveButton()).toBeEnabled();
    expect(maxTicketsInput()).not.toBeDisabled();
    expect(onDirty.mock.calls.length).toBe(dirtyCalls);
    expect(onDirty).toHaveBeenLastCalledWith(true);
    expect(screen.queryByText(i18n.t('settings.common.saveSuccess'))).not.toBeInTheDocument();
    expect(pending).toHaveLength(0);
  });

  it('keeps the new project\'s own save busy when the previous project\'s save answers first', async () => {
    const user = userEvent.setup();
    render(editor('proj-A'));
    await loadWith('proj-A', 20);
    await typeMaxTickets(user, '30');
    await user.click(saveButton());

    await switchTo(user, 'proj-B', { discard: true });
    await loadWith('proj-B', 44);
    await typeMaxTickets(user, '46');
    await user.click(saveButton());
    expectBusy(true);

    await act(async () => {
      settle(urlOf('proj-A'), 'PUT').resolve(
        jsonResponse(forProject('proj-A', { maxTickets: { value: 30, local: 30, source: 'local' } }))
      );
    });

    expectBusy(true);
    expect(maxTicketsInput()).toBeDisabled();

    await act(async () => {
      settle(urlOf('proj-B'), 'PUT').resolve(
        jsonResponse(forProject('proj-B', { maxTickets: { value: 46, local: 46, source: 'local' } }))
      );
    });

    expectBusy(false);
    expect(maxTicketsInput()).not.toBeDisabled();
    expect(maxTicketsInput().value).toBe('46');
  });

  it('shows a project busy again when switched back to while its own save still runs', async () => {
    const user = userEvent.setup();
    render(editor('proj-A'));
    await loadWith('proj-A', 20);
    await typeMaxTickets(user, '30');
    await user.click(saveButton());

    await switchTo(user, 'proj-B', { discard: true });
    await loadWith('proj-B', 44);
    await typeMaxTickets(user, '46');
    await user.click(saveButton());
    expectBusy(true);

    await switchTo(user, 'proj-A', { discard: true });
    await loadWith('proj-A', 20);

    // A's first save is still running, so A is busy and no second save of
    // A can start (its answer would be overwritten by the first one's).
    expectBusy(true);
    expect(maxTicketsInput()).toBeDisabled();
    expect(pending.map(p => `${p.method} ${p.url}`).sort()).toEqual([`PUT ${urlOf('proj-A')}`, `PUT ${urlOf('proj-B')}`]);

    // B's save answering changes nothing in A.
    await act(async () => {
      settle(urlOf('proj-B'), 'PUT').resolve(
        jsonResponse(forProject('proj-B', { maxTickets: { value: 46, local: 46, source: 'local' } }))
      );
    });
    expectBusy(true);
    expect(maxTicketsInput()).toBeDisabled();
    expect(maxTicketsInput().value).toBe('20');

    // A's own save answering releases A with the saved value.
    await act(async () => {
      settle(urlOf('proj-A'), 'PUT').resolve(
        jsonResponse(forProject('proj-A', { maxTickets: { value: 30, local: 30, source: 'local' } }))
      );
    });
    expectBusy(false);
    expect(maxTicketsInput()).not.toBeDisabled();
    expect(maxTicketsInput().value).toBe('30');
    expect(pending).toHaveLength(0);
  });

  // DFLT-00374: the tab's own project selector. Switching loads the picked
  // project's settings (showing the loading line, never the previous
  // project's values, until they are in) and saves go to it; unsaved edits
  // are only dropped once the user agrees to.
  const pendingUrls = () => pending.map(p => `${p.method} ${p.url}`);

  it('loads the picked project, shows the loading line until it is in, and saves to it', async () => {
    const user = userEvent.setup();
    render(editor('proj-A'));
    await loadWith('proj-A', 20);

    await switchTo(user, 'proj-B');
    expect(pendingUrls()).toEqual([`GET ${urlOf('proj-B')}`]);
    expect(loadingLine()).toBeInTheDocument();
    expect(screen.queryByDisplayValue('20')).not.toBeInTheDocument();
    expect(screen.queryByLabelText(label('maxTickets'))).not.toBeInTheDocument();

    await loadWith('proj-B', 44);
    expect(loadingLine()).not.toBeInTheDocument();

    await typeMaxTickets(user, '46');
    await user.click(saveButton());
    expect(pendingUrls()).toEqual([`PUT ${urlOf('proj-B')}`]);
    await act(async () => {
      settle(urlOf('proj-B'), 'PUT').resolve(
        jsonResponse(forProject('proj-B', { maxTickets: { value: 46, local: 46, source: 'local' } }))
      );
    });
    expect(maxTicketsInput().value).toBe('46');
    expect(fetchSpy.mock.calls.map(([url, init]) => `${init?.method ?? 'GET'} ${String(url)}`)).toEqual([
      `GET ${urlOf('proj-A')}`,
      `GET ${urlOf('proj-B')}`,
      `PUT ${urlOf('proj-B')}`
    ]);
  });

  it('asks before switching away from unsaved edits, and switches on discard', async () => {
    const user = userEvent.setup();
    const onDirty = vi.fn();
    render(editor('proj-A', onDirty));
    await loadWith('proj-A', 20);
    await typeMaxTickets(user, '30');
    expect(onDirty).toHaveBeenLastCalledWith(true);

    await user.selectOptions(projectSelect(), 'proj-B');
    const dialog = screen.getByTestId('autopilot-project-discard-confirm');
    // The same wording as the settings modal's tab change.
    expect(dialog).toHaveTextContent(i18n.t('settings.unsavedChanges.confirmTitle'));
    expect(dialog).toHaveTextContent(i18n.t('settings.unsavedChanges.confirmMessage'));
    expect(screen.getByTestId('autopilot-project-discard-confirm-confirm')).toHaveTextContent(
      i18n.t('settings.unsavedChanges.discardButton')
    );
    // Nothing is loaded before the answer.
    expect(pending).toHaveLength(0);

    await user.click(screen.getByTestId('autopilot-project-discard-confirm-confirm'));

    expect(projectSelect().value).toBe('proj-B');
    expect(pendingUrls()).toEqual([`GET ${urlOf('proj-B')}`]);
    // Still loading B, and already no longer dirty: the modal must not ask
    // again on its next tab change or close.
    expect(loadingLine()).toBeInTheDocument();
    expect(onDirty).toHaveBeenLastCalledWith(false);

    await loadWith('proj-B', 44);
    expect(maxTicketsInput().value).toBe('44');
    expect(onDirty).toHaveBeenLastCalledWith(false);
  });

  it('stays on the project with the edits kept when the confirmation is cancelled', async () => {
    const user = userEvent.setup();
    const onDirty = vi.fn();
    render(editor('proj-A', onDirty));
    await loadWith('proj-A', 20);
    await typeMaxTickets(user, '30');

    await user.selectOptions(projectSelect(), 'proj-B');
    expect(confirmDialog()).toBeInTheDocument();
    await user.click(screen.getByTestId('autopilot-project-discard-confirm-cancel'));

    expect(confirmDialog()).not.toBeInTheDocument();
    expect(projectSelect().value).toBe('proj-A');
    expect(maxTicketsInput().value).toBe('30');
    expect(saveButton()).toBeEnabled();
    expect(onDirty).toHaveBeenLastCalledWith(true);
    expect(pending).toHaveLength(0);
    expect(fetchSpy).toHaveBeenCalledTimes(1);
  });

  it('switches without asking when nothing is unsaved', async () => {
    const user = userEvent.setup();
    render(editor('proj-A'));
    await loadWith('proj-A', 20);

    await user.selectOptions(projectSelect(), 'proj-C');

    expect(confirmDialog()).not.toBeInTheDocument();
    expect(projectSelect().value).toBe('proj-C');
    expect(pendingUrls()).toEqual([`GET ${urlOf('proj-C')}`]);
  });

  it('drops the discarded edits when the placeholder is picked, and asks nothing when coming back', async () => {
    const user = userEvent.setup();
    const onDirty = vi.fn();
    render(editor('proj-A', onDirty));
    await loadWith('proj-A', 20);
    await typeMaxTickets(user, '30');

    await switchTo(user, '', { discard: true });

    expect(screen.getByText(i18n.t('settings.autopilot.selectProject'))).toBeInTheDocument();
    expect(screen.queryByText(i18n.t('settings.autopilot.noProjects'))).not.toBeInTheDocument();
    expect(screen.queryByLabelText(label('maxTickets'))).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: i18n.t('settings.common.save') })).not.toBeInTheDocument();
    expect(projectSelect()).toBeEnabled();
    expect(onDirty).toHaveBeenLastCalledWith(false);
    expect(pending).toHaveLength(0);

    await user.selectOptions(projectSelect(), 'proj-A');
    expect(confirmDialog()).not.toBeInTheDocument();
    expect(projectSelect().value).toBe('proj-A');
    expect(pendingUrls()).toEqual([`GET ${urlOf('proj-A')}`]);
    await loadWith('proj-A', 20);
    expect(onDirty).toHaveBeenLastCalledWith(false);
  });
});
