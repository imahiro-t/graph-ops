// DFLT-00080: the project setup dialog `graph-engine ui` opens for a
// directory no project's local path covers -- "create new" vs "choose an
// existing project" -- plus the header's create-only entry.
import React from 'react';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
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
const existingProjectRadio = (id: string) =>
  screen.getAllByRole('radio').find(r => (r as HTMLInputElement).name === 'project-setup-existing' && (r as HTMLInputElement).value === id);

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

  describe('accessibility (iteration 2)', () => {
    it('moves focus into the dialog on open, traps Tab/Shift+Tab, closes on Escape, and restores focus on close', async () => {
      const user = userEvent.setup();
      const onClose = vi.fn();
      const props = {
        directory: '/work/new',
        offerExisting: true,
        projects: [project('p1', 'Alpha', '/work/alpha')],
        onClose,
        onCreated: vi.fn(),
        onLinked: vi.fn()
      };
      const { rerender } = render(
        <>
          <button type="button">opener</button>
          <ProjectSetupModal isOpen={false} {...props} />
        </>
      );
      const opener = screen.getByRole('button', { name: 'opener' });
      opener.focus();

      rerender(
        <>
          <button type="button">opener</button>
          <ProjectSetupModal isOpen {...props} />
        </>
      );

      // Initial focus: the selected mode radio ("create").
      expect(createRadio()).toHaveFocus();

      // Shift+Tab from the first stop wraps to the last one (Cancel: the
      // submit button is disabled while the name is empty).
      const cancel = screen.getByRole('button', { name: i18n.t('createModal.cancel') });
      await user.tab({ shift: true });
      expect(cancel).toHaveFocus();
      // Tab from the last stop wraps back to the first one.
      await user.tab();
      expect(createRadio()).toHaveFocus();
      // Walking forward never leaves the dialog.
      const dialog = screen.getByRole('dialog');
      for (let i = 0; i < 8; i++) {
        await user.tab();
        expect(dialog).toContainElement(document.activeElement as HTMLElement);
      }

      await user.keyboard('{Escape}');
      expect(onClose).toHaveBeenCalledTimes(1);

      rerender(
        <>
          <button type="button">opener</button>
          <ProjectSetupModal isOpen={false} {...props} />
        </>
      );
      expect(screen.getByRole('button', { name: 'opener' })).toHaveFocus();
    });

    it('does not close on Escape while an IME composition is in progress (isComposing / keyCode 229)', () => {
      const { onClose } = renderModal();
      const nameInput = screen.getByLabelText(i18n.t('createProjectModal.nameLabel'));

      fireEvent.keyDown(nameInput, { key: 'Escape', isComposing: true });
      expect(onClose).not.toHaveBeenCalled();
      fireEvent.keyDown(nameInput, { key: 'Escape', keyCode: 229 });
      expect(onClose).not.toHaveBeenCalled();

      // A plain Escape (composition finished) still closes the dialog.
      fireEvent.keyDown(nameInput, { key: 'Escape' });
      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it('create-only entry focuses the name input, and falls back to returnFocusRef when the opener is gone', () => {
      const returnTarget = React.createRef<HTMLButtonElement>();
      const props = {
        directory: '',
        offerExisting: false,
        projects: [],
        onClose: vi.fn(),
        onCreated: vi.fn(),
        onLinked: vi.fn(),
        returnFocusRef: returnTarget
      };
      const { rerender } = render(
        <>
          <button type="button" ref={returnTarget}>switcher</button>
          <ProjectSetupModal isOpen={false} {...props} />
        </>
      );

      rerender(
        <>
          <button type="button" ref={returnTarget}>switcher</button>
          <ProjectSetupModal isOpen {...props} />
        </>
      );
      expect(screen.getByLabelText(i18n.t('createProjectModal.nameLabel'))).toHaveFocus();

      rerender(
        <>
          <button type="button" ref={returnTarget}>switcher</button>
          <ProjectSetupModal isOpen={false} {...props} />
        </>
      );
      expect(screen.getByRole('button', { name: 'switcher' })).toHaveFocus();
    });

    it('announces the overwrite warning via role="status" and describes each radio with its local path', async () => {
      const user = userEvent.setup();
      renderModal({ directory: '/work/alpha-copy', projects: [project('p1', 'Alpha', '/work/alpha'), project('p2', 'Shared', '')] });

      await user.click(existingRadio());
      const status = screen.getByTestId('project-setup-overwrite-status');
      expect(status).toHaveAttribute('role', 'status');
      expect(status).toBeEmptyDOMElement();

      expect(screen.getByRole('radio', { name: 'Alpha' })).toHaveAccessibleDescription('/work/alpha');
      expect(screen.getByRole('radio', { name: 'Shared' })).toHaveAccessibleDescription(i18n.t('projectSetupModal.notSet'));

      await user.click(screen.getByRole('radio', { name: 'Alpha' }));
      expect(screen.getByRole('status')).toHaveTextContent(
        i18n.t('projectSetupModal.overwriteWarning', { current: '/work/alpha', next: '/work/alpha-copy' })
      );
      // Selected card (bg-blue-50): the path uses slate-600 (>= 4.5:1), not slate-500.
      const path = screen.getByTestId('project-setup-local-path-p1');
      expect(path).toHaveClass('text-slate-600');
      expect(path).not.toHaveClass('text-slate-500');
    });
  });

  describe('created in the DB but the local path could not be saved (iteration 2)', () => {
    const partialFailure = () =>
      jsonResponse(500, {
        error: { code: 'PROJECT_CREATED_LOCAL_PATH_NOT_SAVED', message: 'project Theta (p-theta) was created, but saving its local path ... failed' }
      });

    // Stateful parent standing in for App: onProjectsChanged re-fetches the list.
    function Harness({ initial, after, offerExisting, onLinked, onProjectsChanged }: {
      initial: Project[];
      after: Project[];
      offerExisting: boolean;
      onLinked: () => void;
      onProjectsChanged: () => void;
    }) {
      const [projects, setProjects] = React.useState(initial);
      return (
        <ProjectSetupModal
          isOpen
          directory={offerExisting ? '/work/theta' : ''}
          offerExisting={offerExisting}
          projects={projects}
          onClose={vi.fn()}
          onCreated={vi.fn()}
          onLinked={onLinked}
          onProjectsChanged={() => {
            onProjectsChanged();
            setProjects(after);
          }}
        />
      );
    }

    it('`graph-engine ui` entry: re-fetches, says it was created, blocks a second create, and links the new project instead', async () => {
      const alpha = project('p1', 'Alpha', '/work/alpha');
      const theta = project('p-theta', 'Theta', '');
      const linked = { ...theta, local_path: '/work/theta' };
      fetchMock()
        .mockResolvedValueOnce(partialFailure())
        .mockResolvedValueOnce(jsonResponse(200, linked))
        .mockResolvedValueOnce(jsonResponse(200, linked));
      const onLinked = vi.fn();
      const onProjectsChanged = vi.fn();
      const user = userEvent.setup();
      render(<Harness initial={[alpha]} after={[alpha, theta]} offerExisting onLinked={onLinked} onProjectsChanged={onProjectsChanged} />);

      await user.type(screen.getByLabelText(i18n.t('createProjectModal.nameLabel')), 'Theta');
      await user.click(screen.getByRole('button', { name: i18n.t('createModal.submit') }));

      const notice = await screen.findByTestId('project-setup-partial-create');
      expect(notice).toHaveAttribute('role', 'alert');
      expect(notice).toHaveTextContent(i18n.t('projectSetupModal.createdButLocalPathNotSavedChooseExisting', { name: 'Theta' }));
      expect(onProjectsChanged).toHaveBeenCalledTimes(1);
      // Moved to "choose existing" with the new project preselected.
      expect(existingRadio()).toBeChecked();
      await waitFor(() => expect(screen.getByRole('radio', { name: 'Theta' })).toBeChecked());

      // Going back to "create" does not allow a second POST.
      await user.click(createRadio());
      expect(screen.getByTestId('project-setup-partial-create')).toBeInTheDocument();
      expect(screen.getByRole('button', { name: i18n.t('createModal.submit') })).toBeDisabled();

      await user.click(existingRadio());
      await user.click(screen.getByRole('radio', { name: 'Theta' }));
      await user.click(screen.getByRole('button', { name: i18n.t('projectSetupModal.confirmExisting') }));

      await waitFor(() => expect(onLinked).toHaveBeenCalledWith(linked));
      expect(calls().map(c => `${c.method} ${c.url}`)).toEqual([
        'POST /api/projects',
        'PATCH /api/projects/p-theta',
        'PUT /api/current-project'
      ]);
    });

    it('`graph-engine ui` entry: with an async re-fetch and a pre-existing same-named project, preselects only the newly created one', async () => {
      const oldTheta = project('p-old', 'Theta', '/work/old-theta');
      const newTheta = project('p-new', 'Theta', '');
      const linked = { ...newTheta, local_path: '/work/theta' };
      fetchMock()
        .mockResolvedValueOnce(partialFailure())
        .mockResolvedValueOnce(jsonResponse(200, linked))
        .mockResolvedValueOnce(jsonResponse(200, linked));
      const onLinked = vi.fn();
      let finishRefetch: () => void = () => {};

      // Stands in for App.fetchProjects: the list only changes after the
      // fetch resolves, so the dialog first re-renders with the stale list.
      function AsyncHarness() {
        const [projects, setProjects] = React.useState<Project[]>([oldTheta]);
        return (
          <ProjectSetupModal
            isOpen
            directory="/work/theta"
            offerExisting
            projects={projects}
            onClose={vi.fn()}
            onCreated={vi.fn()}
            onLinked={onLinked}
            onProjectsChanged={() =>
              new Promise<void>(resolve => {
                finishRefetch = () => {
                  setProjects([oldTheta, newTheta]);
                  resolve();
                };
              })
            }
          />
        );
      }
      const user = userEvent.setup();
      render(<AsyncHarness />);

      await user.type(screen.getByLabelText(i18n.t('createProjectModal.nameLabel')), 'Theta');
      await user.click(screen.getByRole('button', { name: i18n.t('createModal.submit') }));

      // Before the re-fetch resolves: in "choose existing", nothing is selected
      // (the old same-named project must not be picked).
      await screen.findByTestId('project-setup-partial-create');
      expect(existingRadio()).toBeChecked();
      const oldRadio = screen.getByRole('radio', { name: 'Theta' });
      expect(oldRadio).not.toBeChecked();
      expect(screen.getByTestId('project-setup-overwrite-status')).toBeEmptyDOMElement();
      expect(screen.getByRole('button', { name: i18n.t('projectSetupModal.confirmExisting') })).toBeDisabled();

      await act(async () => {
        finishRefetch();
      });

      // After the re-fetch: the new project (p-new) is selected, the old one is not.
      await waitFor(() => expect(existingProjectRadio('p-new')).toBeChecked());
      expect(existingProjectRadio('p-old')).not.toBeChecked();

      await user.click(screen.getByRole('button', { name: i18n.t('projectSetupModal.confirmExisting') }));
      await waitFor(() => expect(onLinked).toHaveBeenCalledWith(linked));
      expect(calls().map(c => `${c.method} ${c.url}`)).toEqual([
        'POST /api/projects',
        'PATCH /api/projects/p-new',
        'PUT /api/current-project'
      ]);
    });

    it('header entry: says it was created, points to the settings screen, and disables create', async () => {
      const theta = project('p-theta', 'Theta', '');
      fetchMock().mockResolvedValueOnce(partialFailure());
      const onProjectsChanged = vi.fn();
      const user = userEvent.setup();
      render(<Harness initial={[]} after={[theta]} offerExisting={false} onLinked={vi.fn()} onProjectsChanged={onProjectsChanged} />);

      await user.type(screen.getByLabelText(i18n.t('createProjectModal.nameLabel')), 'Theta');
      const submit = screen.getByRole('button', { name: i18n.t('createModal.submit') });
      await user.click(submit);

      expect(await screen.findByTestId('project-setup-partial-create')).toHaveTextContent(
        i18n.t('projectSetupModal.createdButLocalPathNotSavedUseSettings', { name: 'Theta' })
      );
      expect(onProjectsChanged).toHaveBeenCalledTimes(1);
      await waitFor(() => expect(submit).toBeDisabled());
      await user.click(submit);
      expect(calls()).toHaveLength(1);
    });

    it('a plain INTERNAL_ERROR is not treated as "already created": create stays available', async () => {
      fetchMock().mockResolvedValueOnce(jsonResponse(500, { error: { code: 'INTERNAL_ERROR', message: 'db down' } }));
      const onProjectsChanged = vi.fn();
      const user = userEvent.setup();
      render(<Harness initial={[]} after={[]} offerExisting={false} onLinked={vi.fn()} onProjectsChanged={onProjectsChanged} />);

      await user.type(screen.getByLabelText(i18n.t('createProjectModal.nameLabel')), 'Theta');
      const submit = screen.getByRole('button', { name: i18n.t('createModal.submit') });
      await user.click(submit);

      expect(await screen.findByRole('alert')).toHaveTextContent(i18n.t('errors.INTERNAL_ERROR'));
      expect(screen.queryByTestId('project-setup-partial-create')).not.toBeInTheDocument();
      expect(onProjectsChanged).not.toHaveBeenCalled();
      await waitFor(() => expect(submit).toBeEnabled());
    });
  });
});
