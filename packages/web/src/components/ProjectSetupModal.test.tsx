// DFLT-00080: the project setup dialog `graph-engine ui` opens for a
// directory no project's local path covers -- "create new" vs "choose an
// existing project" -- plus the header's create-only entry.
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { ProjectSetupModal } from './ProjectSetupModal';
import { Project } from '../types';

const fetchMock = () => fetch as unknown as ReturnType<typeof vi.fn>;

function project(id: string, name: string, localPath: string): Project {
  return { id, name, prefix: id.toUpperCase().slice(0, 5), local_path: localPath, created_at: '', updated_at: '' };
}

function jsonResponse(status: number, body: unknown) {
  return { ok: status >= 200 && status < 300, status, json: async () => body };
}

function calls() {
  return fetchMock().mock.calls.map(([url, init]) => ({
    url: String(url),
    method: (init as RequestInit | undefined)?.method ?? 'GET',
    body: (init as RequestInit | undefined)?.body ? JSON.parse((init as RequestInit).body as string) : undefined
  }));
}

function renderModal(props: Partial<React.ComponentProps<typeof ProjectSetupModal>> = {}) {
  const onCreated = vi.fn();
  const onLinked = vi.fn();
  const onClose = vi.fn();
  const utils = render(
    <ProjectSetupModal
      isOpen
      directory="/work/new"
      offerExisting
      projects={[]}
      onClose={onClose}
      onCreated={onCreated}
      onLinked={onLinked}
      {...props}
    />
  );
  return { ...utils, onCreated, onLinked, onClose };
}

const createRadio = () => screen.getByRole('radio', { name: i18n.t('projectSetupModal.modeCreate') });
const existingRadio = () => screen.getByRole('radio', { name: i18n.t('projectSetupModal.modeExisting') });

describe('ProjectSetupModal', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn());
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('offers both "create new" and "choose existing" for an unmapped directory', () => {
    renderModal({ projects: [project('p1', 'Alpha', '/work/alpha')] });

    expect(createRadio()).toBeEnabled();
    expect(existingRadio()).toBeEnabled();
    expect(screen.getByText(/\/work\/new/)).toBeInTheDocument();
  });

  it('disables "choose existing" when the DB has no projects, while "create new" stays available', () => {
    renderModal({ projects: [] });

    expect(existingRadio()).toBeDisabled();
    expect(createRadio()).toBeEnabled();
    expect(createRadio()).toBeChecked();
  });

  it('creates a project with the directory as local_path', async () => {
    const created = project('p-new', 'NewProj', '/work/new');
    fetchMock().mockResolvedValueOnce(jsonResponse(201, created));
    const user = userEvent.setup();
    const { onCreated } = renderModal({ projects: [project('p1', 'Alpha', '/work/alpha')] });

    expect(screen.getByLabelText(i18n.t('createProjectModal.localPathLabel'))).toHaveValue('/work/new');
    await user.type(screen.getByLabelText(i18n.t('createProjectModal.nameLabel')), 'NewProj');
    await user.click(screen.getByRole('button', { name: i18n.t('createModal.submit') }));

    await waitFor(() => expect(onCreated).toHaveBeenCalledWith(created));
    expect(calls()).toEqual([{ url: '/api/projects', method: 'POST', body: { name: 'NewProj', local_path: '/work/new' } }]);
  });

  it('lists existing projects with their local path or "not set"', async () => {
    const user = userEvent.setup();
    renderModal({ projects: [project('p1', 'Alpha', '/work/alpha'), project('p2', 'Shared', '')] });

    await user.click(existingRadio());

    expect(screen.getByTestId('project-setup-local-path-p1')).toHaveTextContent('/work/alpha');
    expect(screen.getByTestId('project-setup-local-path-p2')).toHaveTextContent(i18n.t('projectSetupModal.notSet'));
  });

  it('choosing an existing project PATCHes its local_path, then switches the current project', async () => {
    const shared = project('p2', 'Shared', '');
    const linked = { ...shared, local_path: '/work/shared' };
    fetchMock()
      .mockResolvedValueOnce(jsonResponse(200, linked))
      .mockResolvedValueOnce(jsonResponse(200, linked));
    const user = userEvent.setup();
    const { onLinked, onCreated } = renderModal({ directory: '/work/shared', projects: [shared] });

    await user.click(existingRadio());
    await user.click(screen.getByRole('radio', { name: 'Shared' }));
    await user.click(screen.getByRole('button', { name: i18n.t('projectSetupModal.confirmExisting') }));

    await waitFor(() => expect(onLinked).toHaveBeenCalledWith(linked));
    expect(onCreated).not.toHaveBeenCalled();
    expect(calls()).toEqual([
      { url: '/api/projects/p2', method: 'PATCH', body: { local_path: '/work/shared' } },
      { url: '/api/current-project', method: 'PUT', body: { project_id: 'p2' } }
    ]);
  });

  it('warns that an existing, different local path will be overwritten', async () => {
    const user = userEvent.setup();
    renderModal({ directory: '/work/alpha-copy', projects: [project('p1', 'Alpha', '/work/alpha')] });

    await user.click(existingRadio());
    await user.click(screen.getByRole('radio', { name: 'Alpha' }));

    expect(
      screen.getByText(i18n.t('projectSetupModal.overwriteWarning', { current: '/work/alpha', next: '/work/alpha-copy' }))
    ).toBeInTheDocument();
  });

  it('keeps the dialog open with an error when the switch fails, and a retry re-sends PATCH then PUT', async () => {
    const shared = project('p2', 'Shared', '');
    const linked = { ...shared, local_path: '/work/shared' };
    fetchMock()
      .mockResolvedValueOnce(jsonResponse(200, linked))
      .mockResolvedValueOnce(jsonResponse(500, { error: { code: 'INTERNAL_ERROR', message: 'boom' } }));
    const user = userEvent.setup();
    const { onLinked, onClose } = renderModal({ directory: '/work/shared', projects: [shared] });

    await user.click(existingRadio());
    await user.click(screen.getByRole('radio', { name: 'Shared' }));
    const confirm = screen.getByRole('button', { name: i18n.t('projectSetupModal.confirmExisting') });
    await user.click(confirm);

    expect(await screen.findByRole('alert')).toHaveTextContent(i18n.t('errors.INTERNAL_ERROR'));
    expect(onLinked).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();
    expect(screen.getByRole('radio', { name: 'Shared' })).toBeChecked();
    await waitFor(() => expect(confirm).toBeEnabled());

    fetchMock()
      .mockResolvedValueOnce(jsonResponse(200, linked))
      .mockResolvedValueOnce(jsonResponse(200, linked));
    await user.click(confirm);

    await waitFor(() => expect(onLinked).toHaveBeenCalledWith(linked));
    expect(calls().map(c => `${c.method} ${c.url}`)).toEqual([
      'PATCH /api/projects/p2',
      'PUT /api/current-project',
      'PATCH /api/projects/p2',
      'PUT /api/current-project'
    ]);
    expect(calls()[2].body).toEqual({ local_path: '/work/shared' });
  });

  it('opened from the header: create-only, local path optional and omitted from the body when blank', async () => {
    const created = project('p-np', 'NoPathProj', '');
    fetchMock().mockResolvedValueOnce(jsonResponse(201, created));
    const user = userEvent.setup();
    const { onCreated } = renderModal({ directory: '', offerExisting: false, projects: [project('p1', 'Alpha', '/work/alpha')] });

    expect(screen.queryByRole('radio', { name: i18n.t('projectSetupModal.modeExisting') })).not.toBeInTheDocument();
    const localPath = screen.getByLabelText(i18n.t('createProjectModal.localPathLabel'));
    expect(localPath).not.toBeRequired();
    expect(localPath).toHaveValue('');

    await user.type(screen.getByLabelText(i18n.t('createProjectModal.nameLabel')), 'NoPathProj');
    await user.click(screen.getByRole('button', { name: i18n.t('createModal.submit') }));

    await waitFor(() => expect(onCreated).toHaveBeenCalledWith(created));
    expect(calls()[0].body).toEqual({ name: 'NoPathProj' });
  });
});
