// DFLT-00084: the settings modal's label management tab.
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../../i18n';
import { LabelUsage, Project } from '../../types';

// The project list the tab's own selector offers (DFLT-00124).
const testProjects: Project[] = [
  { id: 'proj-A', name: 'Project A', prefix: 'PA', local_path: '', created_at: '', updated_at: '' },
  { id: 'proj-B', name: 'Project B', prefix: 'PB', local_path: '', created_at: '', updated_at: '' }
];

vi.mock('../../lib/labelsApi', () => ({
  fetchLabels: vi.fn(),
  createLabel: vi.fn(),
  updateLabel: vi.fn(),
  deleteLabel: vi.fn(),
  setTicketLabels: vi.fn()
}));

import { createLabel, deleteLabel, fetchLabels, updateLabel } from '../../lib/labelsApi';
import { LabelsEditor } from './LabelsEditor';

type Mock = ReturnType<typeof vi.fn>;
const mockedFetch = fetchLabels as unknown as Mock;
const mockedCreate = createLabel as unknown as Mock;
const mockedUpdate = updateLabel as unknown as Mock;
const mockedDelete = deleteLabel as unknown as Mock;

const label = (id: string, name: string, color: LabelUsage['color'], ticketCount: number): LabelUsage => ({
  id,
  project_id: 'proj-A',
  name,
  color,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
  ticket_count: ticketCount
});

function createForm() {
  return within(screen.getByTestId('label-create-form'));
}

describe('LabelsEditor', () => {
  beforeEach(() => {
    mockedFetch.mockReset();
    mockedCreate.mockReset();
    mockedUpdate.mockReset();
    mockedDelete.mockReset();
    mockedFetch.mockResolvedValue([label('label-bug', 'バグ', 'red', 2), label('label-feat', '機能追加', 'blue', 0)]);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('lists the project labels with chip previews and usage counts', async () => {
    render(<LabelsEditor projects={testProjects} initialProjectId="proj-A" />);

    const bugRow = await screen.findByTestId('label-row-label-bug');
    const featRow = screen.getByTestId('label-row-label-feat');
    expect(mockedFetch).toHaveBeenCalledWith(expect.anything(), 'proj-A');
    expect(within(bugRow).getByTestId('label-chip')).toHaveTextContent('バグ');
    expect(within(bugRow).getByTestId('label-chip')).toHaveAttribute('data-label-color', 'red');
    expect(within(featRow).getByTestId('label-chip')).toHaveAttribute('data-label-color', 'blue');
    expect(within(bugRow).getByTestId('label-usage')).toHaveTextContent(i18n.t('settings.labels.usage', { count: 2 }));
    expect(within(featRow).getByTestId('label-usage')).toHaveTextContent(i18n.t('settings.labels.usage', { count: 0 }));
  });

  it('disables every operation and explains why when there is no project to pick', async () => {
    render(<LabelsEditor projects={[]} initialProjectId="" />);

    expect(screen.getByText(i18n.t('settings.labels.noProjects'))).toBeInTheDocument();
    expect(createForm().getByRole('textbox')).toBeDisabled();
    expect(createForm().getByRole('button', { name: i18n.t('settings.labels.create') })).toBeDisabled();
    for (const button of within(createForm().getByRole('group')).getAllByRole('button')) {
      expect(button).toBeDisabled();
    }
    expect(mockedFetch).not.toHaveBeenCalled();
  });

  it('creates a label with the chosen palette color', async () => {
    mockedCreate.mockResolvedValue({ ...label('label-doc', 'ドキュメント', 'teal', 0) });
    const onLabelsChanged = vi.fn();
    const user = userEvent.setup();
    render(<LabelsEditor projects={testProjects} initialProjectId="proj-A" onLabelsChanged={onLabelsChanged} />);
    await screen.findByTestId('label-row-label-bug');

    await user.type(createForm().getByRole('textbox'), 'ドキュメント');
    await user.click(createForm().getByRole('button', { name: i18n.t('labels.colors.teal') }));
    await user.click(createForm().getByRole('button', { name: i18n.t('settings.labels.create') }));

    expect(mockedCreate).toHaveBeenCalledWith(expect.anything(), 'proj-A', 'ドキュメント', 'teal');
    expect(await screen.findByTestId('label-row-label-doc')).toBeInTheDocument();
    expect(onLabelsChanged).toHaveBeenCalledTimes(1);
  });

  it('offers 10 named palette buttons whose aria-pressed follows the selection', async () => {
    const user = userEvent.setup();
    render(<LabelsEditor projects={testProjects} initialProjectId="proj-A" />);
    await screen.findByTestId('label-row-label-bug');

    const palette = within(createForm().getByRole('group', { name: i18n.t('settings.labels.newColorGroup') }));
    const buttons = palette.getAllByRole('button');
    expect(buttons).toHaveLength(10);
    const names = buttons.map(b => b.getAttribute('aria-label'));
    for (const color of ['gray', 'red', 'orange', 'amber', 'green', 'teal', 'blue', 'indigo', 'purple', 'pink']) {
      expect(names).toContain(i18n.t(`labels.colors.${color}`));
    }

    await user.click(palette.getByRole('button', { name: i18n.t('labels.colors.teal') }));
    for (const button of buttons) {
      const expected = button.getAttribute('aria-label') === i18n.t('labels.colors.teal') ? 'true' : 'false';
      expect(button).toHaveAttribute('aria-pressed', expected);
    }
  });

  it('shows the translated error and adds nothing when the name is taken', async () => {
    mockedCreate.mockRejectedValue(new Error(i18n.t('errors.LABEL_NAME_TAKEN')));
    const onLabelsChanged = vi.fn();
    const user = userEvent.setup();
    render(<LabelsEditor projects={testProjects} initialProjectId="proj-A" onLabelsChanged={onLabelsChanged} />);
    await screen.findByTestId('label-row-label-bug');

    await user.type(createForm().getByRole('textbox'), 'bug');
    await user.click(createForm().getByRole('button', { name: i18n.t('settings.labels.create') }));

    expect(await screen.findByRole('alert')).toHaveTextContent(i18n.t('errors.LABEL_NAME_TAKEN'));
    expect(screen.getAllByRole('listitem')).toHaveLength(2);
    expect(onLabelsChanged).not.toHaveBeenCalled();
  });

  it('renames and recolors a label', async () => {
    mockedUpdate
      .mockResolvedValueOnce(label('label-bug', '不具合', 'red', 0))
      .mockResolvedValueOnce(label('label-bug', '不具合', 'orange', 0));
    const onLabelsChanged = vi.fn();
    const user = userEvent.setup();
    render(<LabelsEditor projects={testProjects} initialProjectId="proj-A" onLabelsChanged={onLabelsChanged} />);
    const row = await screen.findByTestId('label-row-label-bug');

    await user.click(within(row).getByRole('button', { name: `${i18n.t('settings.labels.rename')}: バグ` }));
    const input = within(row).getByRole('textbox');
    await user.clear(input);
    await user.type(input, '不具合');
    await user.click(within(row).getByRole('button', { name: i18n.t('settings.labels.save') }));

    expect(mockedUpdate).toHaveBeenLastCalledWith(expect.anything(), 'label-bug', { name: '不具合' });
    await waitFor(() => expect(within(screen.getByTestId('label-row-label-bug')).getByTestId('label-chip')).toHaveTextContent('不具合'));
    expect(onLabelsChanged).toHaveBeenCalledTimes(1);

    const renamedRow = screen.getByTestId('label-row-label-bug');
    await user.click(within(renamedRow).getByRole('button', { name: i18n.t('labels.colors.orange') }));
    expect(mockedUpdate).toHaveBeenLastCalledWith(expect.anything(), 'label-bug', { color: 'orange' });
    await waitFor(() =>
      expect(within(screen.getByTestId('label-row-label-bug')).getByTestId('label-chip')).toHaveAttribute('data-label-color', 'orange')
    );
    expect(onLabelsChanged).toHaveBeenCalledTimes(2);
  });

  it('confirms deleting an in-use label with its usage count, and deletes on OK', async () => {
    mockedFetch.mockResolvedValue([label('label-bug', 'バグ', 'red', 3)]);
    mockedDelete.mockResolvedValue({ success: true, removed_ticket_count: 3 });
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true);
    const onLabelsChanged = vi.fn();
    const user = userEvent.setup();
    render(<LabelsEditor projects={testProjects} initialProjectId="proj-A" onLabelsChanged={onLabelsChanged} />);
    const row = await screen.findByTestId('label-row-label-bug');

    await user.click(within(row).getByRole('button', { name: `${i18n.t('settings.labels.delete')}: バグ` }));

    expect(confirmSpy).toHaveBeenCalledWith(i18n.t('settings.labels.confirmDeleteInUse', { name: 'バグ', count: 3 }));
    expect(confirmSpy.mock.calls[0][0]).toContain('3');
    expect(mockedDelete).toHaveBeenCalledWith(expect.anything(), 'label-bug');
    await waitFor(() => expect(screen.queryByTestId('label-row-label-bug')).not.toBeInTheDocument());
    expect(onLabelsChanged).toHaveBeenCalledTimes(1);
  });

  it('does nothing when the in-use delete is cancelled', async () => {
    mockedFetch.mockResolvedValue([label('label-bug', 'バグ', 'red', 3)]);
    vi.spyOn(window, 'confirm').mockReturnValue(false);
    const onLabelsChanged = vi.fn();
    const user = userEvent.setup();
    render(<LabelsEditor projects={testProjects} initialProjectId="proj-A" onLabelsChanged={onLabelsChanged} />);
    const row = await screen.findByTestId('label-row-label-bug');

    await user.click(within(row).getByRole('button', { name: `${i18n.t('settings.labels.delete')}: バグ` }));

    expect(mockedDelete).not.toHaveBeenCalled();
    expect(screen.getByTestId('label-row-label-bug')).toBeInTheDocument();
    expect(onLabelsChanged).not.toHaveBeenCalled();
  });

  it('uses the plain confirmation for an unused label', async () => {
    mockedDelete.mockResolvedValue({ success: true, removed_ticket_count: 0 });
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true);
    const user = userEvent.setup();
    render(<LabelsEditor projects={testProjects} initialProjectId="proj-A" />);
    const row = await screen.findByTestId('label-row-label-feat');

    await user.click(within(row).getByRole('button', { name: `${i18n.t('settings.labels.delete')}: 機能追加` }));

    expect(confirmSpy).toHaveBeenCalledWith(i18n.t('settings.labels.confirmDelete', { name: '機能追加' }));
    expect(mockedDelete).toHaveBeenCalledWith(expect.anything(), 'label-feat');
  });

  describe('usage count at delete time', () => {
    it('re-reads the usage count when delete is pressed, so a label that became in use since the tab opened gets the in-use confirmation', async () => {
      mockedFetch
        .mockResolvedValueOnce([label('label-bug', 'バグ', 'red', 0)])
        .mockResolvedValueOnce([label('label-bug', 'バグ', 'red', 4)]);
      mockedDelete.mockResolvedValue({ success: true, removed_ticket_count: 4 });
      const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true);
      const user = userEvent.setup();
      render(<LabelsEditor projects={testProjects} initialProjectId="proj-A" />);
      const row = await screen.findByTestId('label-row-label-bug');
      expect(within(row).getByTestId('label-usage')).toHaveTextContent(i18n.t('settings.labels.usage', { count: 0 }));

      await user.click(within(row).getByRole('button', { name: `${i18n.t('settings.labels.delete')}: バグ` }));

      expect(mockedFetch).toHaveBeenCalledTimes(2);
      expect(confirmSpy).toHaveBeenCalledTimes(1);
      expect(confirmSpy).toHaveBeenCalledWith(i18n.t('settings.labels.confirmDeleteInUse', { name: 'バグ', count: 4 }));
      expect(mockedDelete).toHaveBeenCalledWith(expect.anything(), 'label-bug');
    });

    it('uses the plain confirmation when the label stopped being used since the tab opened, and shows the fresh count on cancel', async () => {
      mockedFetch
        .mockResolvedValueOnce([label('label-bug', 'バグ', 'red', 3)])
        .mockResolvedValueOnce([label('label-bug', 'バグ', 'red', 0)]);
      const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(false);
      const user = userEvent.setup();
      render(<LabelsEditor projects={testProjects} initialProjectId="proj-A" />);
      const row = await screen.findByTestId('label-row-label-bug');

      await user.click(within(row).getByRole('button', { name: `${i18n.t('settings.labels.delete')}: バグ` }));

      expect(confirmSpy).toHaveBeenCalledWith(i18n.t('settings.labels.confirmDelete', { name: 'バグ' }));
      expect(mockedDelete).not.toHaveBeenCalled();
      await waitFor(() =>
        expect(within(screen.getByTestId('label-row-label-bug')).getByTestId('label-usage')).toHaveTextContent(
          i18n.t('settings.labels.usage', { count: 0 })
        )
      );
    });

    it('shows an error and neither confirms nor deletes when re-reading the count fails', async () => {
      mockedFetch.mockResolvedValueOnce([label('label-bug', 'バグ', 'red', 0)]).mockRejectedValueOnce(new Error('network down'));
      const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true);
      const user = userEvent.setup();
      render(<LabelsEditor projects={testProjects} initialProjectId="proj-A" />);
      const row = await screen.findByTestId('label-row-label-bug');
      const deleteButton = within(row).getByRole('button', { name: `${i18n.t('settings.labels.delete')}: バグ` });

      await user.click(deleteButton);

      expect(await screen.findByRole('alert')).toHaveTextContent('network down');
      expect(confirmSpy).not.toHaveBeenCalled();
      expect(mockedDelete).not.toHaveBeenCalled();
      await waitFor(() => expect(deleteButton).toHaveFocus());
    });
  });

  describe('focus management', () => {
    const renameButton = (row: HTMLElement, name: string) =>
      within(row).getByRole('button', { name: `${i18n.t('settings.labels.rename')}: ${name}` });
    const deleteButton = (row: HTMLElement, name: string) =>
      within(row).getByRole('button', { name: `${i18n.t('settings.labels.delete')}: ${name}` });

    it("returns focus to the row's rename button after saving a rename with Enter", async () => {
      mockedUpdate.mockResolvedValue(label('label-bug', '不具合', 'red', 2));
      const user = userEvent.setup();
      render(<LabelsEditor projects={testProjects} initialProjectId="proj-A" />);
      const row = await screen.findByTestId('label-row-label-bug');

      await user.click(renameButton(row, 'バグ'));
      const input = within(row).getByRole('textbox');
      expect(input).toHaveFocus();
      await user.clear(input);
      await user.type(input, '不具合{Enter}');

      await waitFor(() => expect(renameButton(screen.getByTestId('label-row-label-bug'), '不具合')).toHaveFocus());
    });

    it("returns focus to the row's rename button after cancelling with Escape or the cancel button", async () => {
      const user = userEvent.setup();
      render(<LabelsEditor projects={testProjects} initialProjectId="proj-A" />);
      const row = await screen.findByTestId('label-row-label-bug');

      await user.click(renameButton(row, 'バグ'));
      await user.keyboard('{Escape}');
      expect(within(row).queryByRole('textbox')).not.toBeInTheDocument();
      await waitFor(() => expect(renameButton(row, 'バグ')).toHaveFocus());

      await user.click(renameButton(row, 'バグ'));
      await user.click(within(row).getByRole('button', { name: i18n.t('settings.labels.cancel') }));
      await waitFor(() => expect(renameButton(row, 'バグ')).toHaveFocus());
      expect(mockedUpdate).not.toHaveBeenCalled();
    });

    it('keeps the rename open with focus in its input when saving fails', async () => {
      mockedUpdate.mockRejectedValue(new Error(i18n.t('errors.LABEL_NAME_TAKEN')));
      const user = userEvent.setup();
      render(<LabelsEditor projects={testProjects} initialProjectId="proj-A" />);
      const row = await screen.findByTestId('label-row-label-bug');

      await user.click(renameButton(row, 'バグ'));
      const input = within(row).getByRole('textbox');
      await user.clear(input);
      await user.type(input, '機能追加');
      await user.click(within(row).getByRole('button', { name: i18n.t('settings.labels.save') }));

      expect(await screen.findByRole('alert')).toHaveTextContent(i18n.t('errors.LABEL_NAME_TAKEN'));
      await waitFor(() => expect(within(row).getByRole('textbox')).toHaveFocus());
    });

    it("moves focus to the next row's delete button, then the previous one, then the create form's name input", async () => {
      const three = [label('label-a', 'A', 'red', 0), label('label-b', 'B', 'blue', 0), label('label-c', 'C', 'green', 0)];
      mockedFetch
        .mockResolvedValueOnce(three)
        .mockResolvedValueOnce(three)
        .mockResolvedValueOnce([three[0], three[2]])
        .mockResolvedValueOnce([three[0]]);
      mockedDelete.mockResolvedValue({ success: true, removed_ticket_count: 0 });
      vi.spyOn(window, 'confirm').mockReturnValue(true);
      const user = userEvent.setup();
      render(<LabelsEditor projects={testProjects} initialProjectId="proj-A" />);
      await screen.findByTestId('label-row-label-b');

      // B (middle) -> focus goes to C, the next row.
      await user.click(deleteButton(screen.getByTestId('label-row-label-b'), 'B'));
      await waitFor(() => expect(screen.queryByTestId('label-row-label-b')).not.toBeInTheDocument());
      await waitFor(() => expect(deleteButton(screen.getByTestId('label-row-label-c'), 'C')).toHaveFocus());

      // C (now last) -> focus goes to A, the previous row.
      await user.keyboard('{Enter}');
      await waitFor(() => expect(screen.queryByTestId('label-row-label-c')).not.toBeInTheDocument());
      await waitFor(() => expect(deleteButton(screen.getByTestId('label-row-label-a'), 'A')).toHaveFocus());

      // A (the only one) -> focus goes to the create form's name input.
      await user.keyboard('{Enter}');
      await waitFor(() => expect(screen.queryByTestId('label-row-label-a')).not.toBeInTheDocument());
      await waitFor(() => expect(createForm().getByRole('textbox')).toHaveFocus());
      expect(mockedDelete).toHaveBeenCalledTimes(3);
    });

    it('returns focus to the delete button when the deletion is cancelled', async () => {
      vi.spyOn(window, 'confirm').mockReturnValue(false);
      const user = userEvent.setup();
      render(<LabelsEditor projects={testProjects} initialProjectId="proj-A" />);
      const row = await screen.findByTestId('label-row-label-bug');

      await user.click(deleteButton(row, 'バグ'));

      await waitFor(() => expect(deleteButton(screen.getByTestId('label-row-label-bug'), 'バグ')).toHaveFocus());
    });
  });

  it('ignores Enter and Escape pressed during an IME composition in the rename input', async () => {
    const user = userEvent.setup();
    render(<LabelsEditor projects={testProjects} initialProjectId="proj-A" />);
    const row = await screen.findByTestId('label-row-label-bug');

    await user.click(within(row).getByRole('button', { name: `${i18n.t('settings.labels.rename')}: バグ` }));
    const input = within(row).getByRole('textbox');
    fireEvent.keyDown(input, { key: 'Enter', isComposing: true });
    fireEvent.keyDown(input, { key: 'Enter', keyCode: 229 });
    fireEvent.keyDown(input, { key: 'Escape', isComposing: true });

    expect(mockedUpdate).not.toHaveBeenCalled();
    expect(within(row).getByRole('textbox')).toBeInTheDocument();
  });

  it('marks the name input invalid and describes it with the error when creating fails', async () => {
    mockedCreate.mockRejectedValue(new Error(i18n.t('errors.LABEL_NAME_TAKEN')));
    const user = userEvent.setup();
    render(<LabelsEditor projects={testProjects} initialProjectId="proj-A" />);
    await screen.findByTestId('label-row-label-bug');
    const input = createForm().getByRole('textbox');
    expect(input).not.toHaveAttribute('aria-invalid');

    await user.type(input, 'bug{Enter}');

    const alert = await screen.findByRole('alert');
    await waitFor(() => expect(input).toHaveAttribute('aria-invalid', 'true'));
    expect(input).toHaveAttribute('aria-describedby', alert.id);
    expect(input).toHaveFocus();

    await user.type(input, 's');
    expect(input).not.toHaveAttribute('aria-invalid');
    expect(input).not.toHaveAttribute('aria-describedby');
  });

  // DFLT-00124: the tab's own project selector. Switching projects inside the
  // tab only became reachable when the settings modal's scope switcher was
  // replaced by this selector, so these are the first tests to exercise it.
  describe('project selector', () => {
    const selector = () => screen.getByLabelText(i18n.t('settings.labels.projectLabel'));
    const labelOfB = { ...label('label-b-only', 'B専用', 'blue', 0), project_id: 'proj-B' };

    it("replaces the list with the newly selected project's labels", async () => {
      mockedFetch.mockImplementation((_t: unknown, projectId: string) =>
        Promise.resolve(projectId === 'proj-A' ? [label('label-bug', 'バグ', 'red', 2)] : [labelOfB])
      );
      const user = userEvent.setup();
      render(<LabelsEditor projects={testProjects} initialProjectId="proj-A" />);
      await screen.findByTestId('label-row-label-bug');

      await user.selectOptions(selector(), 'proj-B');

      expect(await screen.findByTestId('label-row-label-b-only')).toBeInTheDocument();
      expect(screen.queryByTestId('label-row-label-bug')).not.toBeInTheDocument();
      expect(mockedFetch).toHaveBeenLastCalledWith(expect.anything(), 'proj-B');
      expect(selector()).toHaveValue('proj-B');
    });

    // Non-functional review condition NF-1: the fetch for the project being
    // left is not cancelled, and on a slow backend (MySQL, HTTP data source)
    // it can resolve last. Applying it would show project A's labels under
    // project B's selection -- and rename/delete go by label id, so the next
    // action would edit A's labels from B's screen.
    it("discards project A's response when it resolves after project B's", async () => {
      const resolvers: Record<string, (labels: LabelUsage[]) => void> = {};
      mockedFetch.mockImplementation(
        (_t: unknown, projectId: string) =>
          new Promise<LabelUsage[]>(resolve => {
            resolvers[projectId] = resolve;
          })
      );
      const user = userEvent.setup();
      render(<LabelsEditor projects={testProjects} initialProjectId="proj-A" />);
      await waitFor(() => expect(resolvers['proj-A']).toBeDefined());

      await user.selectOptions(selector(), 'proj-B');
      await waitFor(() => expect(resolvers['proj-B']).toBeDefined());

      // B answers first...
      await act(async () => {
        resolvers['proj-B']([labelOfB]);
      });
      expect(await screen.findByTestId('label-row-label-b-only')).toBeInTheDocument();

      // ...and only then does the request for the abandoned project A.
      await act(async () => {
        resolvers['proj-A']([label('label-bug', 'バグ', 'red', 2)]);
      });

      expect(screen.queryByTestId('label-row-label-bug')).not.toBeInTheDocument();
      expect(screen.getByTestId('label-row-label-b-only')).toBeInTheDocument();
    });

    it("keeps the loading status of the request still in flight when an abandoned project's request fails", async () => {
      const rejecters: Record<string, (err: Error) => void> = {};
      mockedFetch.mockImplementation(
        (_t: unknown, projectId: string) =>
          new Promise<LabelUsage[]>((_resolve, reject) => {
            rejecters[projectId] = reject;
          })
      );
      const user = userEvent.setup();
      render(<LabelsEditor projects={testProjects} initialProjectId="proj-A" />);
      await waitFor(() => expect(rejecters['proj-A']).toBeDefined());

      await user.selectOptions(selector(), 'proj-B');
      await waitFor(() => expect(rejecters['proj-B']).toBeDefined());

      await act(async () => {
        rejecters['proj-A'](new Error('network down'));
      });

      // Neither the error nor the cleared spinner belongs to project B.
      expect(screen.queryByRole('alert')).not.toBeInTheDocument();
      expect(screen.getByRole('status')).toHaveTextContent(i18n.t('settings.labels.loading'));
    });

    // Accessibility review condition A-2: an error raised for one project --
    // together with the aria-invalid and aria-describedby it puts on the name
    // input -- must not survive into a selection whose form is disabled, or
    // assistive technology keeps reporting an input error there is no way to
    // act on.
    it('clears the create error, aria-invalid and aria-describedby when the selection returns to the placeholder', async () => {
      mockedCreate.mockRejectedValue(new Error(i18n.t('errors.LABEL_NAME_TAKEN')));
      const user = userEvent.setup();
      render(<LabelsEditor projects={testProjects} initialProjectId="proj-A" />);
      await screen.findByTestId('label-row-label-bug');
      const input = createForm().getByRole('textbox');

      await user.type(input, 'bug{Enter}');
      const alert = await screen.findByRole('alert');
      await waitFor(() => expect(input).toHaveAttribute('aria-invalid', 'true'));
      expect(input).toHaveAttribute('aria-describedby', alert.id);

      await user.selectOptions(selector(), '');

      await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument());
      const disabled = createForm().getByRole('textbox');
      expect(disabled).toBeDisabled();
      expect(disabled).not.toHaveAttribute('aria-invalid');
      expect(disabled).not.toHaveAttribute('aria-describedby');
      expect(screen.getByRole('status')).toHaveTextContent(i18n.t('settings.labels.selectProject'));
    });
  });

  it('announces loading as a status and limits names to the server maximum of 50', async () => {
    let resolveFetch: (v: LabelUsage[]) => void = () => {};
    mockedFetch.mockImplementation(() => new Promise(r => (resolveFetch = r)));
    render(<LabelsEditor projects={testProjects} initialProjectId="proj-A" />);

    expect(await screen.findByRole('status')).toHaveTextContent(i18n.t('settings.labels.loading'));
    expect(createForm().getByRole('textbox')).toHaveAttribute('maxLength', '50');

    resolveFetch([]);
    await waitFor(() => expect(screen.queryByRole('status')).not.toBeInTheDocument());
  });
});
