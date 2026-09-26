// DFLT-00084: the settings modal's label management tab.
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { TRANSIENT_ANNOUNCEMENT_DURATION_MS } from '../../hooks/useTransientAnnouncement';
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
import { submittingName } from '../../test/submittingName';

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

// DFLT-00148: deletion is confirmed through the in-app ConfirmDialog. It
// opens once the usage count has been re-read, so these wait for it.
type User = ReturnType<typeof userEvent.setup>;
const findDeleteDialog = () => screen.findByRole('alertdialog', { name: i18n.t('settings.labels.confirmDeleteTitle') });
async function answerDelete(user: User, confirmed: boolean) {
  await findDeleteDialog();
  await user.click(screen.getByTestId(confirmed ? 'label-delete-confirm-confirm' : 'label-delete-confirm-cancel'));
}

// Texts of every role="status" element. The tab always mounts an (initially
// empty) StatusLiveRegion for delete announcements (DFLT-00197), so a single
// getByRole('status') would match more than one element.
const statusTexts = () => screen.getAllByRole('status').map(el => el.textContent ?? '');

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

  // DFLT-00206: the create button is aria-busy and says "(submitting)" in its
  // name while the label is being created; both go away once it settles.
  describe('create button while submitting', () => {
    for (const ok of [true, false]) {
      it(`is busy only while creating and clears after ${ok ? 'success' : 'failure'}`, async () => {
        let settle: () => void = () => {};
        mockedCreate.mockImplementation(
          () =>
            new Promise((resolve, reject) => {
              settle = () =>
                ok ? resolve(label('label-doc', 'ドキュメント', 'gray', 0)) : reject(new Error(i18n.t('errors.LABEL_NAME_TAKEN')));
            })
        );
        const user = userEvent.setup();
        render(<LabelsEditor projects={testProjects} initialProjectId="proj-A" />);
        await screen.findByTestId('label-row-label-bug');

        await user.type(createForm().getByRole('textbox'), 'ドキュメント');
        const button = createForm().getByRole('button', { name: i18n.t('settings.labels.create') });
        expect(button).not.toHaveAttribute('aria-busy');
        await user.click(button);

        expect(button).toBeDisabled();
        expect(button).toHaveAttribute('aria-busy', 'true');
        expect(button).toHaveAccessibleName(submittingName(i18n.t('settings.labels.create')));

        await act(async () => {
          settle();
        });

        expect(button).not.toHaveAttribute('aria-busy');
        expect(button).toHaveAccessibleName(i18n.t('settings.labels.create'));
      });
    }
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
    const onLabelsChanged = vi.fn();
    const user = userEvent.setup();
    render(<LabelsEditor projects={testProjects} initialProjectId="proj-A" onLabelsChanged={onLabelsChanged} />);
    const row = await screen.findByTestId('label-row-label-bug');

    await user.click(within(row).getByRole('button', { name: `${i18n.t('settings.labels.delete')}: バグ` }));

    const dialog = await findDeleteDialog();
    expect(dialog).toHaveAccessibleDescription(i18n.t('settings.labels.confirmDeleteInUse', { name: 'バグ', count: 3 }));
    expect(dialog).toHaveTextContent('3');
    expect(screen.getByTestId('label-delete-confirm-confirm')).toHaveTextContent(i18n.t('settings.labels.confirmDeleteButton'));
    expect(mockedDelete).not.toHaveBeenCalled();
    await user.click(screen.getByTestId('label-delete-confirm-confirm'));
    expect(mockedDelete).toHaveBeenCalledWith(expect.anything(), 'label-bug');
    await waitFor(() => expect(screen.queryByTestId('label-row-label-bug')).not.toBeInTheDocument());
    expect(onLabelsChanged).toHaveBeenCalledTimes(1);
  });

  it.each([
    ['the cancel button', (user: User) => user.click(screen.getByTestId('label-delete-confirm-cancel'))],
    ['Escape', (user: User) => user.keyboard('{Escape}')],
    ['a click on the overlay', (user: User) => user.click(screen.getByTestId('label-delete-confirm-overlay'))]
  ])('does nothing when the in-use delete is cancelled with %s', async (_how, dismiss) => {
    mockedFetch.mockResolvedValue([label('label-bug', 'バグ', 'red', 3)]);
    const onLabelsChanged = vi.fn();
    const user = userEvent.setup();
    render(<LabelsEditor projects={testProjects} initialProjectId="proj-A" onLabelsChanged={onLabelsChanged} />);
    const row = await screen.findByTestId('label-row-label-bug');

    await user.click(within(row).getByRole('button', { name: `${i18n.t('settings.labels.delete')}: バグ` }));
    await findDeleteDialog();
    await dismiss(user);

    expect(screen.queryByTestId('label-delete-confirm')).not.toBeInTheDocument();
    expect(mockedDelete).not.toHaveBeenCalled();
    expect(screen.getByTestId('label-row-label-bug')).toBeInTheDocument();
    expect(onLabelsChanged).not.toHaveBeenCalled();
    await waitFor(() =>
      expect(
        within(screen.getByTestId('label-row-label-bug')).getByRole('button', { name: `${i18n.t('settings.labels.delete')}: バグ` })
      ).toHaveFocus()
    );
  });

  it('uses the plain confirmation for an unused label', async () => {
    mockedDelete.mockResolvedValue({ success: true, removed_ticket_count: 0 });
    const user = userEvent.setup();
    render(<LabelsEditor projects={testProjects} initialProjectId="proj-A" />);
    const row = await screen.findByTestId('label-row-label-feat');

    await user.click(within(row).getByRole('button', { name: `${i18n.t('settings.labels.delete')}: 機能追加` }));

    expect(await findDeleteDialog()).toHaveAccessibleDescription(i18n.t('settings.labels.confirmDelete', { name: '機能追加' }));
    await user.click(screen.getByTestId('label-delete-confirm-confirm'));
    expect(mockedDelete).toHaveBeenCalledWith(expect.anything(), 'label-feat');
  });

  describe('usage count at delete time', () => {
    it('re-reads the usage count when delete is pressed, so a label that became in use since the tab opened gets the in-use confirmation', async () => {
      mockedFetch
        .mockResolvedValueOnce([label('label-bug', 'バグ', 'red', 0)])
        .mockResolvedValueOnce([label('label-bug', 'バグ', 'red', 4)]);
      mockedDelete.mockResolvedValue({ success: true, removed_ticket_count: 4 });
      const user = userEvent.setup();
      render(<LabelsEditor projects={testProjects} initialProjectId="proj-A" />);
      const row = await screen.findByTestId('label-row-label-bug');
      expect(within(row).getByTestId('label-usage')).toHaveTextContent(i18n.t('settings.labels.usage', { count: 0 }));

      await user.click(within(row).getByRole('button', { name: `${i18n.t('settings.labels.delete')}: バグ` }));

      expect(await findDeleteDialog()).toHaveAccessibleDescription(
        i18n.t('settings.labels.confirmDeleteInUse', { name: 'バグ', count: 4 })
      );
      expect(mockedFetch).toHaveBeenCalledTimes(2);
      await user.click(screen.getByTestId('label-delete-confirm-confirm'));
      expect(mockedDelete).toHaveBeenCalledWith(expect.anything(), 'label-bug');
    });

    it('uses the plain confirmation when the label stopped being used since the tab opened, and shows the fresh count on cancel', async () => {
      mockedFetch
        .mockResolvedValueOnce([label('label-bug', 'バグ', 'red', 3)])
        .mockResolvedValueOnce([label('label-bug', 'バグ', 'red', 0)]);
      const user = userEvent.setup();
      render(<LabelsEditor projects={testProjects} initialProjectId="proj-A" />);
      const row = await screen.findByTestId('label-row-label-bug');

      await user.click(within(row).getByRole('button', { name: `${i18n.t('settings.labels.delete')}: バグ` }));

      expect(await findDeleteDialog()).toHaveAccessibleDescription(i18n.t('settings.labels.confirmDelete', { name: 'バグ' }));
      await user.click(screen.getByTestId('label-delete-confirm-cancel'));
      expect(mockedDelete).not.toHaveBeenCalled();
      await waitFor(() =>
        expect(within(screen.getByTestId('label-row-label-bug')).getByTestId('label-usage')).toHaveTextContent(
          i18n.t('settings.labels.usage', { count: 0 })
        )
      );
    });

    it('shows an error and neither confirms nor deletes when re-reading the count fails', async () => {
      mockedFetch.mockResolvedValueOnce([label('label-bug', 'バグ', 'red', 0)]).mockRejectedValueOnce(new Error('network down'));
      const user = userEvent.setup();
      render(<LabelsEditor projects={testProjects} initialProjectId="proj-A" />);
      const row = await screen.findByTestId('label-row-label-bug');
      const deleteButton = within(row).getByRole('button', { name: `${i18n.t('settings.labels.delete')}: バグ` });

      await user.click(deleteButton);

      expect(await screen.findByRole('alert')).toHaveTextContent('network down');
      expect(screen.queryByTestId('label-delete-confirm')).not.toBeInTheDocument();
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
      const user = userEvent.setup();
      render(<LabelsEditor projects={testProjects} initialProjectId="proj-A" />);
      await screen.findByTestId('label-row-label-b');

      // B (middle) -> focus goes to C, the next row.
      await user.click(deleteButton(screen.getByTestId('label-row-label-b'), 'B'));
      await answerDelete(user, true);
      await waitFor(() => expect(screen.queryByTestId('label-row-label-b')).not.toBeInTheDocument());
      await waitFor(() => expect(deleteButton(screen.getByTestId('label-row-label-c'), 'C')).toHaveFocus());

      // C (now last) -> focus goes to A, the previous row.
      await user.keyboard('{Enter}');
      await answerDelete(user, true);
      await waitFor(() => expect(screen.queryByTestId('label-row-label-c')).not.toBeInTheDocument());
      await waitFor(() => expect(deleteButton(screen.getByTestId('label-row-label-a'), 'A')).toHaveFocus());

      // A (the only one) -> focus goes to the create form's name input.
      await user.keyboard('{Enter}');
      await answerDelete(user, true);
      await waitFor(() => expect(screen.queryByTestId('label-row-label-a')).not.toBeInTheDocument());
      await waitFor(() => expect(createForm().getByRole('textbox')).toHaveFocus());
      expect(mockedDelete).toHaveBeenCalledTimes(3);
    });

    it('returns focus to the delete button when the deletion is cancelled', async () => {
      const user = userEvent.setup();
      render(<LabelsEditor projects={testProjects} initialProjectId="proj-A" />);
      const row = await screen.findByTestId('label-row-label-bug');

      await user.click(deleteButton(row, 'バグ'));
      await answerDelete(user, false);

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
      expect(statusTexts()).toContain(i18n.t('settings.labels.loading'));
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
      expect(statusTexts()).toContain(i18n.t('settings.labels.selectProject'));
    });
  });

  // DFLT-00168: the row's saving spinner is the only direct sign that the
  // save is in progress, so it rests at text-slate-500 / dark:text-slate-400
  // for WCAG 1.4.11's 3:1 -- 4.76:1 on white and 6.96:1 on slate-900. The old
  // text-slate-400 was 2.56:1 on white.
  it('shows the saving spinner at slate-500 / dark:slate-400', async () => {
    let resolveUpdate: (v: LabelUsage) => void = () => {};
    mockedUpdate.mockImplementation(() => new Promise(r => (resolveUpdate = r)));
    const user = userEvent.setup();
    render(<LabelsEditor projects={testProjects} initialProjectId="proj-A" />);
    const row = await screen.findByTestId('label-row-label-bug');

    await user.click(within(row).getByRole('button', { name: i18n.t('labels.colors.orange') }));
    const spinner = await waitFor(() => {
      const el = row.querySelector('svg.animate-spin');
      expect(el).not.toBeNull();
      return el!;
    });
    expect(spinner).toHaveAttribute('aria-hidden', 'true');
    expect(spinner).toHaveClass('text-slate-500', 'dark:text-slate-400');
    expect(spinner).not.toHaveClass('text-slate-400');

    resolveUpdate(label('label-bug', 'バグ', 'orange', 2));
    await waitFor(() => expect(screen.getByTestId('label-row-label-bug').querySelector('svg.animate-spin')).toBeNull());
  });

  it('announces loading as a status and limits names to the server maximum of 50', async () => {
    let resolveFetch: (v: LabelUsage[]) => void = () => {};
    mockedFetch.mockImplementation(() => new Promise(r => (resolveFetch = r)));
    render(<LabelsEditor projects={testProjects} initialProjectId="proj-A" />);

    await waitFor(() => expect(statusTexts()).toContain(i18n.t('settings.labels.loading')));
    expect(createForm().getByRole('textbox')).toHaveAttribute('maxLength', '50');

    resolveFetch([]);
    await waitFor(() => expect(statusTexts()).not.toContain(i18n.t('settings.labels.loading')));
  });
});

// DFLT-00197: a confirmed, successful delete is announced in an always-mounted
// live region, as DFLT-00194 did for node types, projects and tickets.
describe('LabelsEditor announcing a delete', () => {
  const successText = (name: string) => i18n.t('settings.labels.deleteSuccess', { name });
  const goneText = (name: string) => i18n.t('settings.labels.deleteAlreadyGone', { name });
  const deleteButton = (id: string, name: string) =>
    within(screen.getByTestId(`label-row-${id}`)).getByRole('button', { name: `${i18n.t('settings.labels.delete')}: ${name}` });
  const bug = label('label-bug', 'バグ', 'red', 2);
  const feat = label('label-feat', '機能追加', 'blue', 0);

  beforeEach(() => {
    mockedFetch.mockReset();
    mockedDelete.mockReset();
    mockedFetch.mockResolvedValue([bug, feat]);
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('announces the deleted label in a polite live region and still moves focus to the next row', async () => {
    mockedDelete.mockResolvedValue({ success: true, removed_ticket_count: 2 });
    const user = userEvent.setup();
    render(<LabelsEditor projects={testProjects} initialProjectId="proj-A" />);
    await screen.findByTestId('label-row-label-bug');
    expect(statusTexts()).not.toContain(successText('バグ'));

    await user.click(deleteButton('label-bug', 'バグ'));
    await answerDelete(user, true);

    await waitFor(() => expect(statusTexts()).toContain(successText('バグ')));
    await waitFor(() => expect(deleteButton('label-feat', '機能追加')).toHaveFocus());
    const region = screen.getAllByRole('status').find(el => el.textContent === successText('バグ'))!;
    expect(region).toHaveAttribute('aria-live', 'polite');
    expect(region.contains(document.activeElement)).toBe(false);
  });

  it('names the label as the confirmation did, with the re-read name', async () => {
    mockedFetch.mockResolvedValueOnce([bug, feat]).mockResolvedValueOnce([{ ...bug, name: '不具合' }, feat]);
    mockedDelete.mockResolvedValue({ success: true, removed_ticket_count: 2 });
    const user = userEvent.setup();
    render(<LabelsEditor projects={testProjects} initialProjectId="proj-A" />);
    await screen.findByTestId('label-row-label-bug');

    await user.click(deleteButton('label-bug', 'バグ'));
    await answerDelete(user, true);

    await waitFor(() => expect(statusTexts()).toContain(successText('不具合')));
  });

  it('announces nothing when the user cancels', async () => {
    const user = userEvent.setup();
    render(<LabelsEditor projects={testProjects} initialProjectId="proj-A" />);
    await screen.findByTestId('label-row-label-bug');

    await user.click(deleteButton('label-bug', 'バグ'));
    await answerDelete(user, false);

    await waitFor(() => expect(deleteButton('label-bug', 'バグ')).toHaveFocus());
    expect(statusTexts()).not.toContain(successText('バグ'));
    expect(statusTexts()).not.toContain(goneText('バグ'));
  });

  it('announces nothing when the delete fails, and keeps the error and focus as they were', async () => {
    mockedDelete.mockRejectedValue(new Error('boom'));
    const user = userEvent.setup();
    render(<LabelsEditor projects={testProjects} initialProjectId="proj-A" />);
    await screen.findByTestId('label-row-label-bug');

    await user.click(deleteButton('label-bug', 'バグ'));
    await answerDelete(user, true);

    expect(await screen.findByRole('alert')).toHaveTextContent('boom');
    await waitFor(() => expect(deleteButton('label-bug', 'バグ')).toHaveFocus());
    expect(statusTexts()).not.toContain(successText('バグ'));
  });

  it('announces nothing when re-reading the labels fails', async () => {
    mockedFetch.mockResolvedValueOnce([bug, feat]).mockRejectedValueOnce(new Error('network down'));
    const user = userEvent.setup();
    render(<LabelsEditor projects={testProjects} initialProjectId="proj-A" />);
    await screen.findByTestId('label-row-label-bug');

    await user.click(deleteButton('label-bug', 'バグ'));

    expect(await screen.findByRole('alert')).toHaveTextContent('network down');
    expect(mockedDelete).not.toHaveBeenCalled();
    expect(statusTexts()).not.toContain(successText('バグ'));
    expect(statusTexts()).not.toContain(goneText('バグ'));
  });

  it('says the label had already been deleted, without claiming this user deleted it, when it is gone on re-read', async () => {
    mockedFetch.mockResolvedValueOnce([bug, feat]).mockResolvedValueOnce([feat]);
    const user = userEvent.setup();
    render(<LabelsEditor projects={testProjects} initialProjectId="proj-A" />);
    await screen.findByTestId('label-row-label-bug');

    await user.click(deleteButton('label-bug', 'バグ'));

    await waitFor(() => expect(statusTexts()).toContain(goneText('バグ')));
    expect(statusTexts()).not.toContain(successText('バグ'));
    expect(screen.queryByTestId('label-delete-confirm')).not.toBeInTheDocument();
    expect(mockedDelete).not.toHaveBeenCalled();
    await waitFor(() => expect(deleteButton('label-feat', '機能追加')).toHaveFocus());
  });

  it('keeps the announcement when the last label is deleted and the list is replaced by the empty state', async () => {
    mockedFetch.mockResolvedValue([bug]);
    mockedDelete.mockResolvedValue({ success: true, removed_ticket_count: 2 });
    const user = userEvent.setup();
    render(<LabelsEditor projects={testProjects} initialProjectId="proj-A" />);
    await screen.findByTestId('label-row-label-bug');

    await user.click(deleteButton('label-bug', 'バグ'));
    await answerDelete(user, true);

    expect(await screen.findByText(i18n.t('settings.labels.empty'))).toBeInTheDocument();
    expect(statusTexts()).toContain(successText('バグ'));
    await waitFor(() => expect(createForm().getByRole('textbox')).toHaveFocus());
  });

  it('places the live region last in the space-y container, so the heading gains no top margin', async () => {
    render(<LabelsEditor projects={testProjects} initialProjectId="proj-A" />);
    await screen.findByTestId('label-row-label-bug');

    const region = screen.getAllByRole('status').find(el => el.getAttribute('aria-live') === 'polite')!;
    const container = region.parentElement!;
    expect(container).toHaveClass('space-y-4');
    expect(container.lastElementChild).toBe(region);
    // space-y-4 adds margin-top to every child that has a preceding sibling;
    // the heading must stay the first child.
    const heading = screen.getByRole('heading', { name: i18n.t('settings.labels.title') });
    expect(container.firstElementChild).toContainElement(heading);
  });

  it('clears the announcement after a while', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    mockedDelete.mockResolvedValue({ success: true, removed_ticket_count: 2 });
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    render(<LabelsEditor projects={testProjects} initialProjectId="proj-A" />);
    await screen.findByTestId('label-row-label-bug');

    await user.click(deleteButton('label-bug', 'バグ'));
    await answerDelete(user, true);
    await waitFor(() => expect(statusTexts()).toContain(successText('バグ')));

    act(() => {
      vi.advanceTimersByTime(TRANSIENT_ANNOUNCEMENT_DURATION_MS);
    });
    expect(statusTexts()).not.toContain(successText('バグ'));
  });
});
