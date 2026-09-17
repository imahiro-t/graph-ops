// DFLT-00084: the settings modal's label management tab.
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../../i18n';
import { LabelUsage } from '../../types';

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
    render(<LabelsEditor projectId="proj-A" />);

    const bugRow = await screen.findByTestId('label-row-label-bug');
    const featRow = screen.getByTestId('label-row-label-feat');
    expect(mockedFetch).toHaveBeenCalledWith(expect.anything(), 'proj-A');
    expect(within(bugRow).getByTestId('label-chip')).toHaveTextContent('バグ');
    expect(within(bugRow).getByTestId('label-chip')).toHaveAttribute('data-label-color', 'red');
    expect(within(featRow).getByTestId('label-chip')).toHaveAttribute('data-label-color', 'blue');
    expect(within(bugRow).getByTestId('label-usage')).toHaveTextContent(i18n.t('settings.labels.usage', { count: 2 }));
    expect(within(featRow).getByTestId('label-usage')).toHaveTextContent(i18n.t('settings.labels.usage', { count: 0 }));
  });

  it('disables every operation and explains why when no project is selected', async () => {
    render(<LabelsEditor projectId="" />);

    expect(screen.getByText(i18n.t('settings.labels.selectProject'))).toBeInTheDocument();
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
    render(<LabelsEditor projectId="proj-A" onLabelsChanged={onLabelsChanged} />);
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
    render(<LabelsEditor projectId="proj-A" />);
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
    render(<LabelsEditor projectId="proj-A" onLabelsChanged={onLabelsChanged} />);
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
    render(<LabelsEditor projectId="proj-A" onLabelsChanged={onLabelsChanged} />);
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
    render(<LabelsEditor projectId="proj-A" onLabelsChanged={onLabelsChanged} />);
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
    render(<LabelsEditor projectId="proj-A" onLabelsChanged={onLabelsChanged} />);
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
    render(<LabelsEditor projectId="proj-A" />);
    const row = await screen.findByTestId('label-row-label-feat');

    await user.click(within(row).getByRole('button', { name: `${i18n.t('settings.labels.delete')}: 機能追加` }));

    expect(confirmSpy).toHaveBeenCalledWith(i18n.t('settings.labels.confirmDelete', { name: '機能追加' }));
    expect(mockedDelete).toHaveBeenCalledWith(expect.anything(), 'label-feat');
  });
});
