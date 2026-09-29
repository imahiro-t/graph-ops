// DFLT-00330: a label change is conditioned on the displayed ticket's
// updated_at, and a 409 TICKET_CHANGED reloads the ticket for another try.
// DFLT-00351: a reload that fails says so instead of claiming the latest
// version was loaded.
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest';
import i18n from '../i18n';
import { Label } from '../types';
import { ApiCodeError } from '../lib/apiError';

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
const IMPROVE = mk('label-improve', '改善', 'blue');
const DOCS = mk('label-docs', 'ドキュメント', 'teal');
const PROJECT_LABELS = [BUG, IMPROVE, DOCS];

const U0 = '2026-09-30T00:00:00Z';
const U1 = '2026-09-30T00:00:01Z';
const U2 = '2026-09-30T00:00:02Z';

const conflict = () => new ApiCodeError(i18n.t('errors.TICKET_CHANGED'), 'TICKET_CHANGED');

function setup(initialLabels: Label[], initialUpdatedAt: string, onSaved = vi.fn()) {
  const user = userEvent.setup();
  const tree = (labels: Label[], updatedAt: string) => (
    <LabelSelect ticketId="TEST-00001" labels={labels} projectLabels={PROJECT_LABELS} updatedAt={updatedAt} onSaved={onSaved} />
  );
  const view = render(tree(initialLabels, initialUpdatedAt));
  const rerender = (labels: Label[], updatedAt: string) => view.rerender(tree(labels, updatedAt));
  const open = () => user.click(screen.getByRole('button', { name: `${i18n.t('ticket.labels.edit')}: TEST-00001` }));
  const box = (name: string) => screen.getByRole('checkbox', { name });
  return { user, rerender, open, box, onSaved };
}

const lastIfUpdatedAt = () => mockedSet.mock.calls[mockedSet.mock.calls.length - 1][3];

describe('LabelSelect if_updated_at (DFLT-00330)', () => {
  beforeEach(() => {
    mockedSet.mockReset();
  });
  afterEach(async () => {
    await i18n.changeLanguage('ja');
  });

  it('sends the displayed updated_at with the full set', async () => {
    mockedSet.mockResolvedValue({ updated_at: U1 });
    const { user, open, box } = setup([BUG], U0);
    await open();
    await user.click(box('改善'));
    expect(mockedSet).toHaveBeenCalledWith(expect.anything(), 'TEST-00001', ['label-bug', 'label-improve'], U0);
  });

  it('conditions the next toggle on the updated_at its own save returned', async () => {
    mockedSet.mockResolvedValueOnce({ updated_at: U1 }).mockResolvedValueOnce({ updated_at: U2 });
    const { user, open, box } = setup([BUG], U0);
    await open();
    await user.click(box('改善'));
    await waitFor(() => expect(box('改善')).toHaveAttribute('aria-disabled', 'false'));
    await user.click(box('バグ'));
    expect(mockedSet).toHaveBeenCalledTimes(2);
    expect(lastIfUpdatedAt()).toBe(U1);
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('keeps its own later value when the parent passes an older updated_at', async () => {
    mockedSet.mockResolvedValue({ updated_at: U1 });
    const { user, open, box, rerender } = setup([BUG], U0);
    await open();
    await user.click(box('改善'));
    await waitFor(() => expect(box('改善')).toHaveAttribute('aria-disabled', 'false'));
    rerender([BUG, IMPROVE], U0);
    await user.click(box('ドキュメント'));
    expect(lastIfUpdatedAt()).toBe(U1);
  });

  it('follows a later updated_at from the parent', async () => {
    mockedSet.mockResolvedValue({ updated_at: U1 });
    const { user, open, box, rerender } = setup([BUG], U0);
    await open();
    await user.click(box('改善'));
    await waitFor(() => expect(box('改善')).toHaveAttribute('aria-disabled', 'false'));
    rerender([BUG, IMPROVE], U2);
    mockedSet.mockResolvedValue({ updated_at: '2026-09-30T00:00:03Z' });
    await user.click(box('ドキュメント'));
    expect(lastIfUpdatedAt()).toBe(U2);
    await waitFor(() => expect(box('ドキュメント')).toBeChecked());
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('compares the two as times, not strings', async () => {
    // As text "…01Z" sorts after "…01.5Z"; as a time it is earlier.
    mockedSet.mockResolvedValue({ updated_at: '2026-09-30T00:00:01.5Z' });
    const { user, open, box, rerender } = setup([BUG], U0);
    await open();
    await user.click(box('改善'));
    await waitFor(() => expect(box('改善')).toHaveAttribute('aria-disabled', 'false'));
    rerender([BUG, IMPROVE], '2026-09-30T00:00:01Z');
    await user.click(box('ドキュメント'));
    expect(lastIfUpdatedAt()).toBe('2026-09-30T00:00:01.5Z');
  });

  it('on TICKET_CHANGED says so, reloads, and shows the labels as they are now', async () => {
    mockedSet.mockRejectedValueOnce(conflict());
    const onSaved = vi.fn();
    const { user, open, box, rerender } = setup([BUG], U0, onSaved);
    await open();
    await user.click(box('改善'));

    expect(await screen.findByRole('alert')).toHaveTextContent(i18n.t('errors.TICKET_CHANGED'));
    expect(onSaved).toHaveBeenCalledTimes(1);
    expect(box('改善')).not.toBeChecked();

    // The reload brings another member's labels in.
    rerender([DOCS], U2);
    await waitFor(() => expect(box('ドキュメント')).toBeChecked());
    expect(box('バグ')).not.toBeChecked();
    expect(box('改善')).not.toBeChecked();
  });

  it('retries on top of the reloaded ticket with its updated_at, and succeeds', async () => {
    mockedSet.mockRejectedValueOnce(conflict());
    const { user, open, box, rerender } = setup([BUG], U0);
    await open();
    await user.click(box('改善'));
    await screen.findByRole('alert');
    rerender([DOCS], U2);
    await waitFor(() => expect(box('ドキュメント')).toBeChecked());

    mockedSet.mockResolvedValueOnce({ updated_at: '2026-09-30T00:00:03Z' });
    await user.click(box('改善'));
    expect(mockedSet).toHaveBeenLastCalledWith(expect.anything(), 'TEST-00001', ['label-docs', 'label-improve'], U2);
    await waitFor(() => expect(box('改善')).toBeChecked());
    expect(box('ドキュメント')).toBeChecked();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it.each(['ja', 'en'])('shows the %s TICKET_CHANGED text', async lang => {
    await i18n.changeLanguage(lang);
    mockedSet.mockRejectedValueOnce(conflict());
    const { user, open, box } = setup([BUG], U0);
    await open();
    await user.click(box('改善'));
    const expected = i18n.getResource(lang, 'translation', 'errors.TICKET_CHANGED') as string;
    expect(expected).toBeTruthy();
    expect(await screen.findByRole('alert')).toHaveTextContent(expected);
  });

  // DFLT-00351: the conflict message follows what the reload did.
  const conflictThenReload = async (onSaved: Mock) => {
    mockedSet.mockRejectedValueOnce(conflict());
    const { user, open, box } = setup([BUG], U0, onSaved);
    await open();
    await user.click(box('改善'));
    await screen.findByRole('alert');
    // The save (and its reload) is over once the checkboxes are usable again.
    await waitFor(() => expect(box('改善')).toHaveAttribute('aria-disabled', 'false'));
    expect(onSaved).toHaveBeenCalledTimes(1);
    expect(box('改善')).not.toBeChecked();
    return screen.getByRole('alert');
  };

  it('says the latest could not be loaded when the reload resolves to false', async () => {
    const alert = await conflictThenReload(vi.fn().mockResolvedValue(false));
    expect(alert).toHaveTextContent(i18n.t('ticket.labels.conflictReloadFailed'));
    expect(alert).not.toHaveTextContent(i18n.t('errors.TICKET_CHANGED'));
  });

  it('says the latest could not be loaded when the reload returns false synchronously', async () => {
    const alert = await conflictThenReload(vi.fn().mockReturnValue(false));
    expect(alert).toHaveTextContent(i18n.t('ticket.labels.conflictReloadFailed'));
  });

  it('says the latest could not be loaded when the reload rejects', async () => {
    const alert = await conflictThenReload(vi.fn().mockRejectedValue(new Error('offline')));
    expect(alert).toHaveTextContent(i18n.t('ticket.labels.conflictReloadFailed'));
    expect(alert).not.toHaveTextContent(i18n.t('errors.TICKET_CHANGED'));
  });

  it.each([
    ['true', true],
    ['undefined', undefined]
  ])('keeps the TICKET_CHANGED text when the reload resolves to %s', async (_name, result) => {
    const alert = await conflictThenReload(vi.fn().mockResolvedValue(result));
    expect(alert).toHaveTextContent(i18n.t('errors.TICKET_CHANGED'));
    expect(alert).not.toHaveTextContent(i18n.t('ticket.labels.conflictReloadFailed'));
  });

  it.each(['ja', 'en'])('shows the %s text for a conflict whose reload failed', async lang => {
    await i18n.changeLanguage(lang);
    const expected = i18n.getResource(lang, 'translation', 'ticket.labels.conflictReloadFailed') as string;
    expect(expected).toBeTruthy();
    const alert = await conflictThenReload(vi.fn().mockResolvedValue(false));
    expect(alert).toHaveTextContent(expected);
  });

  it('ignores the reload result after a successful save', async () => {
    mockedSet.mockResolvedValueOnce({ updated_at: U1 });
    const onSaved = vi.fn().mockResolvedValue(false);
    const { user, open, box } = setup([BUG], U0, onSaved);
    await open();
    await user.click(box('改善'));
    await waitFor(() => expect(box('改善')).toHaveAttribute('aria-disabled', 'false'));
    expect(onSaved).toHaveBeenCalledTimes(1);
    expect(box('改善')).toBeChecked();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('keeps the generic save error for any other failure', async () => {
    mockedSet.mockRejectedValueOnce(new ApiCodeError('boom', 'LABEL_NOT_FOUND'));
    const onSaved = vi.fn();
    const { user, open, box } = setup([BUG], U0, onSaved);
    await open();
    await user.click(box('改善'));
    expect(await screen.findByRole('alert')).toHaveTextContent(i18n.t('ticket.labels.saveError', { message: 'boom' }));
    expect(onSaved).not.toHaveBeenCalled();
  });
});
