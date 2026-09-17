// DFLT-00084: the ticket detail's label picker.
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { Label } from '../types';

vi.mock('../lib/labelsApi', () => ({
  fetchLabels: vi.fn(),
  createLabel: vi.fn(),
  updateLabel: vi.fn(),
  deleteLabel: vi.fn(),
  setTicketLabels: vi.fn()
}));

import { setTicketLabels } from '../lib/labelsApi';
import { LabelSelect } from './LabelSelect';

const mockedSet = setTicketLabels as unknown as ReturnType<typeof vi.fn>;

const mk = (id: string, name: string, color: Label['color']): Label => ({
  id,
  project_id: 'proj-A',
  name,
  color,
  created_at: '',
  updated_at: ''
});
const BUG = mk('label-bug', 'バグ', 'red');
const FEAT = mk('label-feat', '機能追加', 'blue');
const UI = mk('label-ui', 'UI', 'purple');
const PERF = mk('label-perf', '性能', 'amber');
const PROJECT_LABELS = [BUG, FEAT, UI, PERF];

async function openPanel(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole('button', { name: i18n.t('ticket.labels.edit') }));
  return screen.getByRole('group', { name: i18n.t('ticket.labels.groupLabel', { id: 'TEST-00001' }) });
}

describe('LabelSelect', () => {
  beforeEach(() => {
    mockedSet.mockReset();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('shows every project label with the current ones checked, and PATCHes the new set when one is added', async () => {
    let resolveSave: (v: unknown) => void = () => {};
    mockedSet.mockImplementation(() => new Promise(r => (resolveSave = r)));
    const onSaved = vi.fn();
    const user = userEvent.setup();
    render(<LabelSelect ticketId="TEST-00001" labels={[BUG]} projectLabels={PROJECT_LABELS} onSaved={onSaved} />);

    await openPanel(user);
    const boxes = screen.getAllByRole('checkbox');
    expect(boxes).toHaveLength(4);
    expect(screen.getByRole('checkbox', { name: 'バグ' })).toBeChecked();
    for (const name of ['機能追加', 'UI', '性能']) {
      expect(screen.getByRole('checkbox', { name })).not.toBeChecked();
    }

    await user.click(screen.getByRole('checkbox', { name: 'UI' }));
    expect(mockedSet).toHaveBeenCalledTimes(1);
    const [, ticketId, ids] = mockedSet.mock.calls[0];
    expect(ticketId).toBe('TEST-00001');
    expect([...ids].sort()).toEqual(['label-bug', 'label-ui']);
    for (const box of screen.getAllByRole('checkbox')) {
      expect(box).toBeDisabled();
    }

    resolveSave({});
    await waitFor(() => expect(onSaved).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(screen.getByRole('checkbox', { name: 'UI' })).not.toBeDisabled());
  });

  it('PATCHes the remaining labels when one is removed', async () => {
    mockedSet.mockResolvedValue({});
    const user = userEvent.setup();
    render(<LabelSelect ticketId="TEST-00001" labels={[BUG, UI]} projectLabels={PROJECT_LABELS} onSaved={vi.fn()} />);

    await openPanel(user);
    await user.click(screen.getByRole('checkbox', { name: 'バグ' }));

    expect(mockedSet).toHaveBeenCalledWith(expect.anything(), 'TEST-00001', ['label-ui']);
  });

  it('shows a save error and does not call onSaved when the PATCH fails', async () => {
    mockedSet.mockRejectedValue(new Error(i18n.t('errors.LABEL_NOT_FOUND')));
    const onSaved = vi.fn();
    const user = userEvent.setup();
    render(<LabelSelect ticketId="TEST-00001" labels={[]} projectLabels={PROJECT_LABELS} onSaved={onSaved} />);

    await openPanel(user);
    await user.click(screen.getByRole('checkbox', { name: 'UI' }));

    expect(await screen.findByRole('alert')).toHaveTextContent(
      i18n.t('ticket.labels.saveError', { message: i18n.t('errors.LABEL_NOT_FOUND') })
    );
    expect(onSaved).not.toHaveBeenCalled();
  });

  it('points to Settings when the project has no labels', async () => {
    const user = userEvent.setup();
    render(<LabelSelect ticketId="TEST-00001" labels={[]} projectLabels={[]} onSaved={vi.fn()} />);

    await openPanel(user);
    expect(screen.getByText(i18n.t('ticket.labels.noRegistered'))).toBeInTheDocument();
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument();
  });

  it('does not propagate clicks, and Escape closes the panel returning focus to the button', async () => {
    const onParentClick = vi.fn();
    const user = userEvent.setup();
    render(
      <div onClick={onParentClick}>
        <LabelSelect ticketId="TEST-00001" labels={[]} projectLabels={PROJECT_LABELS} onSaved={vi.fn()} />
      </div>
    );

    const button = screen.getByRole('button', { name: i18n.t('ticket.labels.edit') });
    await user.click(button);
    expect(button).toHaveAttribute('aria-expanded', 'true');
    expect(onParentClick).not.toHaveBeenCalled();

    await user.click(screen.getByRole('checkbox', { name: 'UI' }));
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('group')).not.toBeInTheDocument();
    expect(button).toHaveFocus();
    expect(onParentClick).not.toHaveBeenCalled();
  });
});
