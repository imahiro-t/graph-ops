// DFLT-00084: the settings modal's labels tab. Labels are DB rows, so the tab
// needs a project selected -- not a local path. Since DFLT-00124 it picks
// that project itself, from a selector of its own, rather than inheriting one
// from a scope switcher. fetch is routed by URL so the real labelsApi helpers
// run.
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { SettingsModal } from './SettingsModal';
import { Project } from '../types';

const alpha: Project = { id: 'p-alpha', name: 'Alpha', prefix: 'ALP', local_path: '', created_at: '', updated_at: '' };

function respond(status: number, body: unknown) {
  return Promise.resolve(new Response(JSON.stringify(body), { status }));
}

type Call = { url: string; method: string; body?: unknown };
let recorded: Call[] = [];

function routeFetch(input: RequestInfo | URL, init?: RequestInit) {
  const url = String(input);
  const method = init?.method ?? 'GET';
  recorded.push({ url, method, body: init?.body ? JSON.parse(init.body as string) : undefined });
  if (url === `/api/projects/${alpha.id}/labels` && method === 'GET') {
    return respond(200, [
      { id: 'label-bug', project_id: alpha.id, name: 'バグ', color: 'red', created_at: '', updated_at: '', ticket_count: 2 },
      { id: 'label-feat', project_id: alpha.id, name: '機能追加', color: 'blue', created_at: '', updated_at: '', ticket_count: 0 }
    ]);
  }
  if (url === `/api/projects/${alpha.id}/labels` && method === 'POST') {
    return respond(201, { id: 'label-doc', project_id: alpha.id, name: 'ドキュメント', color: 'teal', created_at: '', updated_at: '' });
  }
  if (url.startsWith('/api/settings/node-types')) {
    return respond(200, { types: [] });
  }
  return respond(404, { error: { code: 'NOT_FOUND', message: url } });
}

function renderModal(onLabelsChanged = vi.fn()) {
  return render(
    <SettingsModal
      isOpen
      onClose={vi.fn()}
      projects={[alpha]}
      currentProject={alpha}
      onProjectsChanged={vi.fn()}
      onPaginationPageSizeChanged={vi.fn()}
      onMyNameChanged={vi.fn()}
      onLabelsChanged={onLabelsChanged}
    />
  );
}

describe('SettingsModal labels tab', () => {
  beforeEach(() => {
    recorded = [];
    vi.stubGlobal('fetch', vi.fn(routeFetch));
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('opens on the app\'s current project, with its own selector', async () => {
    const user = userEvent.setup();
    renderModal();

    await user.click(screen.getByRole('button', { name: i18n.t('settings.tabs.labels') }));

    const select = screen.getByLabelText(i18n.t('settings.labels.projectLabel'));
    expect(select).toHaveValue(alpha.id);
    expect(screen.queryByText(i18n.t('settings.labels.selectProject'))).not.toBeInTheDocument();
    expect(await screen.findByTestId('label-row-label-bug')).toBeInTheDocument();
  });

  it('lists and creates labels for the selected project even without a local path', async () => {
    const onLabelsChanged = vi.fn();
    const user = userEvent.setup();
    renderModal(onLabelsChanged);

    await user.click(screen.getByRole('button', { name: i18n.t('settings.tabs.labels') }));

    expect(await screen.findByTestId('label-row-label-bug')).toBeInTheDocument();
    expect(screen.getByTestId('label-row-label-feat')).toBeInTheDocument();

    const form = within(screen.getByTestId('label-create-form'));
    expect(form.getByRole('textbox')).toBeEnabled();
    await user.type(form.getByRole('textbox'), 'ドキュメント');
    await user.click(form.getByRole('button', { name: i18n.t('labels.colors.teal') }));
    await user.click(form.getByRole('button', { name: i18n.t('settings.labels.create') }));

    await waitFor(() => expect(onLabelsChanged).toHaveBeenCalledTimes(1));
    const post = recorded.find(c => c.method === 'POST');
    expect(post).toEqual({ url: `/api/projects/${alpha.id}/labels`, method: 'POST', body: { name: 'ドキュメント', color: 'teal' } });
    expect(await screen.findByTestId('label-row-label-doc')).toBeInTheDocument();
  });
});
