// Covers the #3 dependency-array fix (loadTypes must not re-run just
// because `selected` changed) and F-1's structural non-regression (language
// switch must never re-trigger a load and blow away an unsaved edit). See
// this ticket's plan sections 3-2 (#3/#4) and 4-2.
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../../i18n';
import { NodeTypesEditor } from './NodeTypesEditor';
import { Deferred, deferred } from '../../test/deferred';
import { expectSecondSaveReannounced } from '../../test/savedReannouncement';
import { SettingsNodeTypeInfo } from '../../types';
import { openIconButtonTooltip, setupHoverUser, startHoverFakeTimers, waitForHoverOpenDelay } from '../../test/iconButtonTooltip';

vi.mock('../../lib/settingsApi', async () => {
  const actual = await vi.importActual<typeof import('../../lib/settingsApi')>('../../lib/settingsApi');
  return {
    ...actual,
    fetchSettingsNodeTypes: vi.fn(),
    fetchSettingsNodeType: vi.fn(),
    saveSettingsNodeType: vi.fn()
  };
});

import { fetchSettingsNodeType, fetchSettingsNodeTypes, saveSettingsNodeType } from '../../lib/settingsApi';

const mockedFetchTypes = fetchSettingsNodeTypes as unknown as ReturnType<typeof vi.fn>;
const mockedFetchType = fetchSettingsNodeType as unknown as ReturnType<typeof vi.fn>;
const mockedSaveType = saveSettingsNodeType as unknown as ReturnType<typeof vi.fn>;

const TYPES: SettingsNodeTypeInfo[] = [
  { type: 'implementation', has_default: true, has_user_override: false },
  { type: 'review', has_default: true, has_user_override: false }
];

function stubFetchType() {
  mockedFetchType.mockImplementation(async (_t, type: string) => ({
    type,
    tier_text: `${type}-tier-text`,
    merged_text: `${type}-merged-text`
  }));
}

describe('NodeTypesEditor', () => {
  beforeEach(() => {
    mockedFetchTypes.mockReset();
    mockedFetchType.mockReset();
    mockedFetchTypes.mockResolvedValue(TYPES);
    stubFetchType();
  });

  afterEach(async () => {
    await i18n.changeLanguage('ja');
  });

  // DFLT-00343: the first frame (renderToStaticMarkup renders once and runs
  // no effect) must show the loading line, not an empty editor, and the line
  // must go away when the first list load leaves nothing to load.
  describe('initial loading line', () => {
    it('shows the loading line, not an empty editor, before anything has loaded', () => {
      mockedFetchTypes.mockReturnValue(new Promise(() => {}));
      const html = renderToStaticMarkup(<NodeTypesEditor onDirtyChange={vi.fn()} />);
      expect(html).toContain(i18n.t('settings.common.loading'));
      expect(html).not.toContain('<textarea');
      expect(html).not.toContain(`${i18n.t('settings.common.save')}</button>`);
      expect(mockedFetchTypes).not.toHaveBeenCalled();
    });

    // DFLT-00350: a failed list load shows the error and a retry button in
    // place of the editor -- no empty textarea to save over stored text.
    it('drops the loading line and shows the error with a retry button, not the editor, when the list cannot be fetched', async () => {
      mockedFetchTypes.mockReset();
      mockedFetchTypes.mockRejectedValue(new Error('list failed'));
      render(<NodeTypesEditor onDirtyChange={vi.fn()} />);

      expect(screen.getByText(i18n.t('settings.common.loading'), { selector: '[role="status"]' })).toBeInTheDocument();
      expect(await screen.findByRole('alert')).toHaveTextContent('list failed');
      expect(screen.getByRole('button', { name: i18n.t('settings.common.retry') })).toBeInTheDocument();
      expect(screen.queryByText(i18n.t('settings.common.loading'))).not.toBeInTheDocument();
      expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
      expect(screen.queryByRole('button', { name: i18n.t('settings.common.save') })).not.toBeInTheDocument();
      expect(mockedFetchType).not.toHaveBeenCalled();
    });

    it('drops the loading line when the list is empty', async () => {
      mockedFetchTypes.mockReset();
      mockedFetchTypes.mockResolvedValue([]);
      render(<NodeTypesEditor onDirtyChange={vi.fn()} />);

      expect(screen.getByText(i18n.t('settings.common.loading'))).toBeInTheDocument();
      await waitFor(() => expect(screen.queryByText(i18n.t('settings.common.loading'))).not.toBeInTheDocument());
      expect(mockedFetchTypes).toHaveBeenCalledTimes(1);
      expect(mockedFetchType).not.toHaveBeenCalled();
      // DFLT-00357: a note says the list is empty, in place of an editor
      // with nothing to edit.
      expect(screen.getByText(i18n.t('settings.nodeTypes.emptyList'))).toBeInTheDocument();
      expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
      expect(screen.queryByRole('button', { name: i18n.t('settings.common.save') })).not.toBeInTheDocument();
    });
  });

  // DFLT-00357: the saved confirmation is a status, so it is announced.
  it('shows the save confirmation as a status after a successful save', async () => {
    const user = userEvent.setup();
    mockedSaveType.mockReset();
    mockedSaveType.mockResolvedValue({ type: 'implementation', tier_text: 'edited', merged_text: 'edited' });
    render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
    const textarea = await screen.findByDisplayValue('implementation-tier-text');

    await user.clear(textarea);
    await user.type(textarea, 'edited');
    await user.click(screen.getByRole('button', { name: i18n.t('settings.common.save') }));

    // Announced by the always-mounted live region (SC 4.1.3); the visible
    // flash is aria-hidden so it is not read twice.
    expect(await screen.findByText(i18n.t('settings.common.saveSuccess'), { selector: '[role="status"]' })).toBeInTheDocument();
    expect(screen.getByText(i18n.t('settings.common.saveSuccess'), { selector: '[aria-hidden="true"]' })).toHaveClass('text-emerald-700', 'dark:text-emerald-400');
  });

  // DFLT-00359: a second save while the confirmation is still shown is
  // announced again (the live region is emptied and refilled).
  it('announces a second save made while the confirmation is still shown', async () => {
    const user = userEvent.setup();
    mockedSaveType.mockReset();
    mockedSaveType.mockResolvedValue({ type: 'implementation', tier_text: 'edited', merged_text: 'edited' });
    render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
    const textarea = await screen.findByDisplayValue('implementation-tier-text');
    const save = () => screen.getByRole('button', { name: i18n.t('settings.common.save') });

    await user.clear(textarea);
    await user.type(textarea, 'edited');
    await user.click(save());

    await expectSecondSaveReannounced(async () => {
      await user.type(textarea, ' again');
      await user.click(save());
    });
    expect(mockedSaveType).toHaveBeenCalledTimes(2);
  });

  it('selects the first type on initial mount and fetches its detail', async () => {
    render(<NodeTypesEditor onDirtyChange={vi.fn()} />);

    await waitFor(() => expect(mockedFetchType).toHaveBeenCalledWith(expect.anything(), 'implementation'));
    expect(await screen.findByDisplayValue('implementation-tier-text')).toBeInTheDocument();
  });

  // DFLT-00074
  it('labels the tier text textarea', async () => {
    render(<NodeTypesEditor onDirtyChange={vi.fn()} />);

    const textarea = await screen.findByDisplayValue('implementation-tier-text');
    expect(screen.getByLabelText(i18n.t('settings.nodeTypes.tierTextLabel'))).toBe(textarea);
  });

  it('labels the "add node type" input, and handles Escape there with preventDefault to cancel the add', async () => {
    const user = userEvent.setup();
    render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('implementation-tier-text');

    await user.click(screen.getByRole('button', { name: i18n.t('settings.nodeTypes.addType') }));
    const input = screen.getByLabelText(i18n.t('settings.nodeTypes.newTypeLabel'));
    expect(input).toHaveAttribute('placeholder', i18n.t('settings.nodeTypes.newTypePlaceholder'));
    await user.type(input, 'custom_lint');

    // fireEvent returns false when the handler called preventDefault().
    expect(fireEvent.keyDown(input, { key: 'Escape' })).toBe(false);
    expect(screen.queryByLabelText(i18n.t('settings.nodeTypes.newTypeLabel'))).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /^custom_lint/ })).not.toBeInTheDocument();
  });

  it('#3 non-regression: changing the selection does not re-fetch the type list', async () => {
    const user = userEvent.setup();
    render(<NodeTypesEditor onDirtyChange={vi.fn()} />);

    await screen.findByDisplayValue('implementation-tier-text');
    expect(mockedFetchTypes).toHaveBeenCalledTimes(1);

    const reviewButton = screen.getByRole('button', { name: new RegExp(`^${i18n.t('nodeType.review')}`) });
    await user.click(reviewButton);

    await screen.findByDisplayValue('review-tier-text');
    expect(mockedFetchTypes).toHaveBeenCalledTimes(1);
    expect(mockedFetchType).toHaveBeenCalledWith(expect.anything(), 'review');
  });

  it('F-1 non-regression: switching language after editing does not re-fetch and does not discard the unsaved edit', async () => {
    const user = userEvent.setup();
    render(<NodeTypesEditor onDirtyChange={vi.fn()} />);

    const textarea = await screen.findByDisplayValue('implementation-tier-text');
    await user.clear(textarea);
    await user.type(textarea, 'unsaved edit');

    expect(mockedFetchTypes).toHaveBeenCalledTimes(1);
    expect(mockedFetchType).toHaveBeenCalledTimes(1);

    await i18n.changeLanguage('en');
    await waitFor(() => expect(screen.getByDisplayValue('unsaved edit')).toBeInTheDocument());

    expect(mockedFetchTypes).toHaveBeenCalledTimes(1);
    expect(mockedFetchType).toHaveBeenCalledTimes(1);
  });

  // DFLT-00206: the save button is aria-busy while saving. Its visible label
  // already switches to "Saving...", which is in the accessible name, so it
  // gets aria-busy only -- no "(submitting)" suffix on top, which would read
  // the same thing twice.
  describe('save button while saving (aria-busy only, the visible "Saving..." already names it)', () => {
    for (const ok of [true, false]) {
      it(`is busy only while saving and clears after ${ok ? 'success' : 'failure'}, without a duplicated "(submitting)"`, async () => {
        let settle: () => void = () => {};
        mockedSaveType.mockReset();
        mockedSaveType.mockImplementation(
          () =>
            new Promise((resolve, reject) => {
              settle = () =>
                ok
                  ? resolve({ type: 'implementation', tier_text: 'saved edit', merged_text: 'merged' })
                  : reject(new Error(i18n.t('errors.UNKNOWN')));
            })
        );
        const user = userEvent.setup();
        render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
        const textarea = await screen.findByDisplayValue('implementation-tier-text');
        await user.clear(textarea);
        await user.type(textarea, 'saved edit');

        const button = screen.getByRole('button', { name: i18n.t('settings.common.save') });
        expect(button).not.toHaveAttribute('aria-busy');
        await user.click(button);

        expect(button).toHaveAttribute('aria-busy', 'true');
        expect(button).toHaveAccessibleName(i18n.t('settings.common.saving'));
        expect(button).not.toHaveTextContent(i18n.t('common.submitting'));

        await act(async () => {
          settle();
        });

        expect(button).not.toHaveAttribute('aria-busy');
        expect(button).toHaveAccessibleName(i18n.t('settings.common.save'));
      });
    }
  });

  // DFLT-00137: switching the selected type with unsaved edits asks first --
  // through the in-app ConfirmDialog since DFLT-00148.
  describe('switching the selection with unsaved edits', () => {
    const dialog = () => screen.queryByTestId('node-type-discard-confirm');

    const reviewButton = () => screen.getByRole('button', { name: new RegExp(`^${i18n.t('nodeType.review')}`) });
    const implementationButton = () => screen.getByRole('button', { name: new RegExp(`^${i18n.t('nodeType.implementation')}`) });

    it.each([
      ['the cancel button', (user: ReturnType<typeof userEvent.setup>) => user.click(screen.getByTestId('node-type-discard-confirm-cancel'))],
      ['Escape', (user: ReturnType<typeof userEvent.setup>) => user.keyboard('{Escape}')],
      ['a click on the overlay', (user: ReturnType<typeof userEvent.setup>) => user.click(screen.getByTestId('node-type-discard-confirm-overlay'))]
    ])('keeps the selection and the edit when the user dismisses the dialog with %s', async (_how, dismiss) => {
      const user = userEvent.setup();
      render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
      const textarea = await screen.findByDisplayValue('implementation-tier-text');
      await user.clear(textarea);
      await user.type(textarea, 'unsaved edit');

      await user.click(reviewButton());

      const opened = screen.getByRole('alertdialog', { name: i18n.t('settings.unsavedChanges.confirmTitle') });
      expect(opened).toHaveAccessibleDescription(i18n.t('settings.unsavedChanges.confirmMessage'));
      await dismiss(user);

      expect(dialog()).not.toBeInTheDocument();
      expect(reviewButton()).toHaveFocus();
      expect(mockedFetchType).not.toHaveBeenCalledWith(expect.anything(), 'review');
      expect(screen.getByDisplayValue('unsaved edit')).toBeInTheDocument();
    });

    it('switches and clears the relayed dirty flag when the user confirms', async () => {
      const onDirtyChange = vi.fn();
      const user = userEvent.setup();
      render(<NodeTypesEditor onDirtyChange={onDirtyChange} />);
      const textarea = await screen.findByDisplayValue('implementation-tier-text');
      await user.clear(textarea);
      await user.type(textarea, 'unsaved edit');
      expect(onDirtyChange).toHaveBeenLastCalledWith(true);

      await user.click(reviewButton());
      await user.click(screen.getByTestId('node-type-discard-confirm-confirm'));

      expect(dialog()).not.toBeInTheDocument();
      expect(await screen.findByDisplayValue('review-tier-text')).toBeInTheDocument();
      expect(onDirtyChange).toHaveBeenLastCalledWith(false);
      // Never re-reported dirty after the confirm (no second prompt later).
      const lastTrue = onDirtyChange.mock.calls.map(c => c[0]).lastIndexOf(true);
      const lastFalse = onDirtyChange.mock.calls.map(c => c[0]).lastIndexOf(false);
      expect(lastFalse).toBeGreaterThan(lastTrue);
    });

    it('switches without asking when nothing is unsaved, and re-picking the selected type does nothing', async () => {
      const user = userEvent.setup();
      render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
      await screen.findByDisplayValue('implementation-tier-text');

      await user.click(implementationButton());
      expect(mockedFetchType).toHaveBeenCalledTimes(1);

      await user.click(reviewButton());
      expect(await screen.findByDisplayValue('review-tier-text')).toBeInTheDocument();

      const textarea = screen.getByDisplayValue('review-tier-text');
      await user.type(textarea, ' more');
      await user.click(reviewButton());

      expect(dialog()).not.toBeInTheDocument();
      expect(screen.getByDisplayValue('review-tier-text more')).toBeInTheDocument();
    });

    it('keeps the "add node type" row open when the user cancels the switch', async () => {
      const user = userEvent.setup();
      render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
      const textarea = await screen.findByDisplayValue('implementation-tier-text');
      await user.type(textarea, ' edited');

      await user.click(screen.getByRole('button', { name: i18n.t('settings.nodeTypes.addType') }));
      await user.type(screen.getByLabelText(i18n.t('settings.nodeTypes.newTypeLabel')), 'custom_lint{Enter}');

      expect(screen.getByRole('alertdialog', { name: i18n.t('settings.unsavedChanges.confirmTitle') })).toBeInTheDocument();
      await user.click(screen.getByTestId('node-type-discard-confirm-cancel'));

      expect(dialog()).not.toBeInTheDocument();
      const input = screen.getByLabelText(i18n.t('settings.nodeTypes.newTypeLabel'));
      expect(input).toHaveValue('custom_lint');
      expect(input).toHaveFocus();
      expect(screen.queryByRole('button', { name: /^custom_lint/ })).not.toBeInTheDocument();
      expect(screen.getByDisplayValue('implementation-tier-text edited')).toBeInTheDocument();
    });

    it('adds the new type and puts focus on the "add node type" button when the user confirms the switch', async () => {
      const user = userEvent.setup();
      render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
      const textarea = await screen.findByDisplayValue('implementation-tier-text');
      await user.type(textarea, ' edited');

      await user.click(screen.getByRole('button', { name: i18n.t('settings.nodeTypes.addType') }));
      await user.type(screen.getByLabelText(i18n.t('settings.nodeTypes.newTypeLabel')), 'custom_lint{Enter}');
      await user.click(screen.getByTestId('node-type-discard-confirm-confirm'));

      expect(dialog()).not.toBeInTheDocument();
      expect(await screen.findByRole('button', { name: /^custom_lint/ })).toBeInTheDocument();
      expect(screen.queryByLabelText(i18n.t('settings.nodeTypes.newTypeLabel'))).not.toBeInTheDocument();
      expect(screen.getByRole('button', { name: i18n.t('settings.nodeTypes.addType') })).toHaveFocus();
    });
  });

  // DFLT-00137: deleting an override asks first, naming the type -- through
  // the in-app ConfirmDialog since DFLT-00148.
  describe('deleting an override', () => {
    beforeEach(() => {
      mockedSaveType.mockReset();
      mockedSaveType.mockResolvedValue({ type: 'custom_lint', tier_text: '', merged_text: '' });
      mockedFetchTypes.mockResolvedValue([...TYPES, { type: 'custom_lint', has_default: false, has_user_override: true }]);
    });
    const deleteButton = () =>
      screen.getByRole('button', { name: i18n.t('settings.nodeTypes.deleteTypeAriaLabel', { name: 'custom_lint' }) });

    it.each([
      ['the cancel button', (user: ReturnType<typeof userEvent.setup>) => user.click(screen.getByTestId('node-type-delete-confirm-cancel'))],
      ['Escape', (user: ReturnType<typeof userEvent.setup>) => user.keyboard('{Escape}')],
      ['a click on the overlay', (user: ReturnType<typeof userEvent.setup>) => user.click(screen.getByTestId('node-type-delete-confirm-overlay'))]
    ])('asks with the type name and does nothing on %s', async (_how, dismiss) => {
      const user = userEvent.setup();
      render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
      await screen.findByDisplayValue('implementation-tier-text');

      await user.click(deleteButton());

      const dialog = screen.getByRole('alertdialog', { name: i18n.t('settings.nodeTypes.confirmDeleteTypeTitle') });
      expect(dialog).toHaveAccessibleDescription(i18n.t('settings.nodeTypes.confirmDeleteType', { name: 'custom_lint' }));
      expect(dialog).toHaveTextContent('custom_lint');
      await dismiss(user);

      expect(screen.queryByTestId('node-type-delete-confirm')).not.toBeInTheDocument();
      expect(deleteButton()).toHaveFocus();
      expect(mockedSaveType).not.toHaveBeenCalled();
      expect(mockedFetchTypes).toHaveBeenCalledTimes(1);
    });

    it('clears the override when the user confirms', async () => {
      const user = userEvent.setup();
      render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
      await screen.findByDisplayValue('implementation-tier-text');

      await user.click(deleteButton());
      await user.click(screen.getByTestId('node-type-delete-confirm-confirm'));

      await waitFor(() => expect(mockedSaveType).toHaveBeenCalledWith(expect.anything(), 'custom_lint', ''));
    });

    // DFLT-00165: the icon-only delete button rests at text-slate-500 /
    // dark:text-slate-400 for WCAG 1.4.11's 3:1 -- 4.55:1 on the list's
    // slate-50 and 4.76:1 on a selected/hovered white row, 5.71:1 on
    // slate-800 and 6.96:1 on slate-900. The old text-slate-400 /
    // dark:text-slate-500 was 2.45:1 on slate-50. The red hover and the
    // dimmed look of an unavailable button (a disabled control is exempt
    // from 1.4.11) are kept. DFLT-00203: a default type's button is
    // aria-disabled rather than disabled, so its dimming is a plain
    // opacity-30 (the disabled: variant no longer matches) and it gets no
    // red hover.
    it('rests the delete buttons at slate-500 / dark:slate-400, with the red hover only where deleting is possible', async () => {
      render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
      await screen.findByDisplayValue('implementation-tier-text');

      const buttons = [
        deleteButton(),
        screen.getByRole('button', {
          name: i18n.t('settings.nodeTypes.deleteTypeAriaLabel', { name: i18n.t('nodeType.implementation') })
        }),
        screen.getByRole('button', {
          name: i18n.t('settings.nodeTypes.deleteTypeAriaLabel', { name: i18n.t('nodeType.review') })
        })
      ];
      expect(buttons).toHaveLength(3);
      expect(buttons[0]).toBeEnabled();
      expect(buttons[0]).not.toHaveAttribute('aria-disabled');
      for (const b of buttons) {
        expect(b).toHaveClass('text-slate-500', 'dark:text-slate-400');
        expect(b).not.toHaveClass('text-slate-400');
        expect(b).not.toHaveClass('dark:text-slate-500');
      }
      expect(buttons[0]).toHaveClass('hover:text-red-600', 'dark:hover:text-red-400');
      expect(buttons[0]).not.toHaveClass('opacity-30');
      for (const b of buttons.slice(1)) {
        expect(b).toBeEnabled();
        expect(b).toHaveAttribute('aria-disabled', 'true');
        expect(b).toHaveClass('opacity-30');
        expect(b).not.toHaveClass('hover:text-red-600');
        expect(b).not.toHaveClass('dark:hover:text-red-400');
      }
    });
  });
});

// DFLT-00191: once a confirmed delete has removed the row, focus moves to
// the row that took its place (else the one before, else the "add node type"
// button) instead of dropping to <body>.
describe('NodeTypesEditor focus after deleting a type', () => {
  const custom = (type: string): SettingsNodeTypeInfo => ({ type, has_default: false, has_user_override: true });
  const byKey = (container: HTMLElement, key: string) => container.querySelector<HTMLElement>(`[data-focus-key="${key}"]`);

  beforeEach(() => {
    mockedFetchTypes.mockReset();
    mockedFetchType.mockReset();
    mockedSaveType.mockReset();
    mockedSaveType.mockResolvedValue({ type: 'x', tier_text: '', merged_text: '' });
    stubFetchType();
  });

  // Serves `initial` until the delete request goes out, then the list
  // without the deleted type.
  function serveList(initial: SettingsNodeTypeInfo[]) {
    let current = initial;
    mockedFetchTypes.mockImplementation(async () => current);
    mockedSaveType.mockImplementation(async (_t, type: string) => {
      current = current.filter(info => info.type !== type);
      return { type, tier_text: '', merged_text: '' };
    });
  }

  // selectedFirst: the type the editor selects (and loads) on mount.
  async function deleteAndConfirm(container: HTMLElement, type: string, selectedFirst = 'implementation') {
    const user = userEvent.setup();
    await screen.findByDisplayValue(`${selectedFirst}-tier-text`);
    await user.click(byKey(container, `delete-${type}`)!);
    await user.click(screen.getByTestId('node-type-delete-confirm-confirm'));
    await waitFor(() => expect(byKey(container, `delete-${type}`)).not.toBeInTheDocument());
  }

  it('moves focus to the next custom type\'s delete button', async () => {
    serveList([...TYPES, custom('custom_a'), custom('custom_b')]);
    const { container } = render(<NodeTypesEditor onDirtyChange={vi.fn()} />);

    await deleteAndConfirm(container, 'custom_a');

    await waitFor(() => expect(byKey(container, 'delete-custom_b')).toHaveFocus());
    expect(document.activeElement).not.toBe(document.body);
  });

  // DFLT-00203: a plugin default's delete button is aria-disabled, not
  // disabled, so it takes focus like any other row's -- and says why that
  // row cannot be deleted.
  it('moves focus to the next row\'s delete button even when that row is a plugin default', async () => {
    serveList([TYPES[0], custom('custom_a'), TYPES[1]]);
    const { container } = render(<NodeTypesEditor onDirtyChange={vi.fn()} />);

    await deleteAndConfirm(container, 'custom_a');

    const next = byKey(container, 'delete-review');
    await waitFor(() => expect(next).toHaveFocus());
    expect(next).toHaveAttribute('aria-disabled', 'true');
    expect(next).toHaveAccessibleDescription(i18n.t('settings.nodeTypes.cannotDeleteDefaultHint'));
    expect(document.activeElement).not.toBe(document.body);
  });

  it('moves focus to the previous row when the last one is deleted', async () => {
    serveList([...TYPES, custom('custom_a'), custom('custom_b')]);
    const { container } = render(<NodeTypesEditor onDirtyChange={vi.fn()} />);

    await deleteAndConfirm(container, 'custom_b');

    await waitFor(() => expect(byKey(container, 'delete-custom_a')).toHaveFocus());
    expect(document.activeElement).not.toBe(document.body);
  });

  it('moves focus to the "add node type" button when the list is now empty', async () => {
    serveList([custom('custom_a')]);
    const { container } = render(<NodeTypesEditor onDirtyChange={vi.fn()} />);

    await deleteAndConfirm(container, 'custom_a', 'custom_a');

    await waitFor(() => expect(screen.getByRole('button', { name: i18n.t('settings.nodeTypes.addType') })).toHaveFocus());
    expect(document.activeElement).not.toBe(document.body);
    // DFLT-00357: the pane says the list is empty instead of heading the
    // deleted type over an empty editor.
    expect(screen.getByText(i18n.t('settings.nodeTypes.emptyList'))).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'custom_a' })).not.toBeInTheDocument();
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
  });

  // DFLT-00357: adding a type after the last one is gone brings the editor
  // back for the new type.
  it('replaces the empty-list note with the new type\'s editor when a type is added after deleting the last one', async () => {
    serveList([custom('custom_a')]);
    const { container } = render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
    await deleteAndConfirm(container, 'custom_a', 'custom_a');
    await screen.findByText(i18n.t('settings.nodeTypes.emptyList'));

    const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: i18n.t('settings.nodeTypes.addType') }));
    await user.type(screen.getByLabelText(i18n.t('settings.nodeTypes.newTypeLabel')), 'custom_b{Enter}');

    expect(await screen.findByDisplayValue('custom_b-tier-text')).toBeInTheDocument();
    expect(screen.queryByText(i18n.t('settings.nodeTypes.emptyList'))).not.toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'custom_b' })).toBeInTheDocument();
    expect(screen.queryByTestId('node-type-discard-confirm')).not.toBeInTheDocument();
  });

  it('keeps focus on the delete button when the delete request fails', async () => {
    mockedFetchTypes.mockResolvedValue([...TYPES, custom('custom_a'), custom('custom_b')]);
    mockedSaveType.mockRejectedValue(new Error('boom'));
    const user = userEvent.setup();
    const { container } = render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('implementation-tier-text');

    await user.click(byKey(container, 'delete-custom_a')!);
    await user.click(screen.getByTestId('node-type-delete-confirm-confirm'));

    expect(await screen.findByText('boom')).toBeInTheDocument();
    expect(byKey(container, 'delete-custom_a')).toHaveFocus();
    expect(mockedFetchTypes).toHaveBeenCalledTimes(1);
  });
});

// DFLT-00194: a confirmed delete that succeeded is announced, naming the
// type, through an always-mounted live region -- focus moves on to a
// neighbor, and this says why. A cancelled or failed delete announces nothing.
describe('NodeTypesEditor announcing a delete', () => {
  const custom = (type: string): SettingsNodeTypeInfo => ({ type, has_default: false, has_user_override: true });
  const byKey = (container: HTMLElement, key: string) => container.querySelector<HTMLElement>(`[data-focus-key="${key}"]`);
  const successText = (name: string) => i18n.t('settings.nodeTypes.deleteTypeSuccess', { name });
  const statusTexts = () => screen.getAllByRole('status').map(el => el.textContent ?? '');

  beforeEach(() => {
    mockedFetchTypes.mockReset();
    mockedFetchType.mockReset();
    mockedSaveType.mockReset();
    stubFetchType();
  });

  it('announces the deleted type in a live region and still moves focus to the next row', async () => {
    let current = [...TYPES, custom('custom_a'), custom('custom_b')];
    mockedFetchTypes.mockImplementation(async () => current);
    mockedSaveType.mockImplementation(async (_t, type: string) => {
      current = current.filter(info => info.type !== type);
      return { type, tier_text: '', merged_text: '' };
    });
    const user = userEvent.setup();
    const { container } = render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('implementation-tier-text');
    expect(statusTexts()).not.toContain(successText('custom_a'));

    await user.click(byKey(container, 'delete-custom_a')!);
    await user.click(screen.getByTestId('node-type-delete-confirm-confirm'));

    await waitFor(() => expect(statusTexts()).toContain(successText('custom_a')));
    await waitFor(() => expect(byKey(container, 'delete-custom_b')).toHaveFocus());
    const region = screen.getAllByRole('status').find(el => el.textContent === successText('custom_a'))!;
    expect(region).toHaveAttribute('aria-live', 'polite');
    expect(region.contains(document.activeElement)).toBe(false);
  });

  it('announces nothing when the user cancels', async () => {
    mockedFetchTypes.mockResolvedValue([...TYPES, custom('custom_a')]);
    const user = userEvent.setup();
    const { container } = render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('implementation-tier-text');

    await user.click(byKey(container, 'delete-custom_a')!);
    await user.click(screen.getByTestId('node-type-delete-confirm-cancel'));

    expect(byKey(container, 'delete-custom_a')).toHaveFocus();
    expect(statusTexts()).not.toContain(successText('custom_a'));
  });

  it('announces nothing when the delete fails, and keeps the error and focus as they were', async () => {
    mockedFetchTypes.mockResolvedValue([...TYPES, custom('custom_a'), custom('custom_b')]);
    mockedSaveType.mockRejectedValue(new Error('boom'));
    const user = userEvent.setup();
    const { container } = render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('implementation-tier-text');

    await user.click(byKey(container, 'delete-custom_a')!);
    await user.click(screen.getByTestId('node-type-delete-confirm-confirm'));

    expect(await screen.findByText('boom')).toBeInTheDocument();
    expect(byKey(container, 'delete-custom_a')).toHaveFocus();
    expect(statusTexts()).not.toContain(successText('custom_a'));
  });
});

// DFLT-00166: the list's icons are decorative and aria-hidden; the icon-only
// buttons (delete, and the yes/no of the add row) are named -- the delete
// buttons after their type since DFLT-00193, the others by their title.
describe('NodeTypesEditor icon accessibility', () => {
  beforeEach(() => {
    mockedFetchTypes.mockReset();
    mockedFetchType.mockReset();
    mockedFetchTypes.mockResolvedValue([...TYPES, { type: 'security_scan', has_default: false, has_user_override: true }]);
    stubFetchType();
  });

  const expectAllIconsHidden = (root: Element) => {
    const icons = root.querySelectorAll('svg.lucide');
    expect(icons.length).toBeGreaterThan(0);
    icons.forEach(icon => expect(icon).toHaveAttribute('aria-hidden', 'true'));
  };

  it('hides the type icons and names the icon-only buttons', async () => {
    const user = userEvent.setup();
    const { container } = render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('implementation-tier-text');

    // Every icon on screen -- including the per-type icon drawn from
    // nodeTypeMeta through a variable -- is hidden.
    expectAllIconsHidden(container);

    // The custom type's delete button is named after the type (DFLT-00193).
    const del = screen.getByRole('button', {
      name: i18n.t('settings.nodeTypes.deleteTypeAriaLabel', { name: 'security_scan' })
    });
    expect(del).toBeEnabled();
    expectAllIconsHidden(del);
    // Default types' delete buttons are named after the type too, and keep
    // the reason they are unavailable (aria-disabled) as their description.
    for (const type of ['implementation', 'review'] as const) {
      const disabledDel = screen.getByRole('button', {
        name: i18n.t('settings.nodeTypes.deleteTypeAriaLabel', { name: i18n.t(`nodeType.${type}`) })
      });
      expect(disabledDel).toHaveAttribute('aria-disabled', 'true');
      expect(disabledDel).toHaveAccessibleDescription(i18n.t('settings.nodeTypes.cannotDeleteDefaultHint'));
    }

    await user.click(screen.getByRole('button', { name: i18n.t('settings.nodeTypes.addType') }));
    const yes = screen.getByRole('button', { name: i18n.t('settings.nodeTypes.confirmAddType') });
    const no = screen.getByRole('button', { name: i18n.t('settings.nodeTypes.cancelAddType') });
    expectAllIconsHidden(yes);
    expectAllIconsHidden(no);
    expectAllIconsHidden(container);
  });

  // DFLT-00168: the add row's icon-only cancel button sits on the list
  // panel (slate-50 / slate-800). It rests at text-slate-500 /
  // dark:text-slate-400 (4.55:1 / 5.71:1) for WCAG 1.4.11's 3:1 -- the old
  // text-slate-400 was 2.45:1 on slate-50 -- and its hover gets stronger in
  // both themes (slate-700 / slate-200) instead of the old hover:text-slate-600,
  // which faded to 1.93:1 on slate-800.
  it('rests the add row cancel button at slate-500 / dark:slate-400 with a stronger hover', async () => {
    const user = userEvent.setup();
    render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('implementation-tier-text');

    await user.click(screen.getByRole('button', { name: i18n.t('settings.nodeTypes.addType') }));
    const no = screen.getByRole('button', { name: i18n.t('settings.nodeTypes.cancelAddType') });
    expect(no).toHaveClass('text-slate-500', 'dark:text-slate-400', 'hover:text-slate-700', 'dark:hover:text-slate-200');
    expect(no).not.toHaveClass('text-slate-400');
    expect(no).not.toHaveClass('hover:text-slate-600');
  });
});

// DFLT-00193: after a delete, focus lands on a neighboring row's delete
// button, so each delete button's accessible name carries its type -- in
// the "<action>: <target>" form LabelsEditor uses -- while the tooltip stays
// as it was and a default type keeps "why it can't be deleted" as its
// description. DFLT-00171: the tooltip is IconButton's, not a title, and
// only that reason -- not the "delete" tooltip already in the name -- is a
// description.
describe('NodeTypesEditor delete button names', () => {
  beforeEach(() => {
    mockedFetchTypes.mockReset();
    mockedFetchType.mockReset();
    mockedFetchTypes.mockResolvedValue([...TYPES, { type: 'custom_lint', has_default: false, has_user_override: true }]);
    stubFetchType();
  });

  afterEach(async () => {
    await i18n.changeLanguage('ja');
  });

  it.each([
    ['ja', 'ノード種別の上書きを削除: custom_lint', 'ノード種別の上書きを削除: 実装'],
    ['en', 'Delete node type override: custom_lint', 'Delete node type override: Implementation']
  ])('names each delete button after its type in %s, keeping the tooltip and the default hint', async (lng, customName, defaultName) => {
    await i18n.changeLanguage(lng);
    const { container } = render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('implementation-tier-text');

    const custom = screen.getByRole('button', { name: customName });
    expect(custom).toBe(container.querySelector('[data-focus-key="delete-custom_lint"]'));
    expect(custom).toBeEnabled();
    expect(custom).not.toHaveAttribute('title');
    expect(custom).toHaveAccessibleDescription('');
    act(() => custom.focus());
    expect(openIconButtonTooltip()).toHaveTextContent(i18n.t('settings.nodeTypes.deleteType'));
    act(() => custom.blur());

    const byDefault = screen.getByRole('button', { name: defaultName });
    expect(byDefault).toBe(container.querySelector('[data-focus-key="delete-implementation"]'));
    expect(byDefault).toHaveAttribute('aria-disabled', 'true');
    expect(byDefault).not.toHaveAttribute('title');
    expect(byDefault).toHaveAccessibleDescription(i18n.t('settings.nodeTypes.cannotDeleteDefaultHint'));
  });

  // The reason is shown on hover and stays available to assistive
  // technology as the description.
  it('shows why a default type cannot be deleted on hover', async () => {
    startHoverFakeTimers();
    const user = setupHoverUser();
    render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('implementation-tier-text');
    const byDefault = screen.getByRole('button', {
      name: i18n.t('settings.nodeTypes.deleteTypeAriaLabel', { name: i18n.t('nodeType.implementation') })
    });
    await user.hover(byDefault.parentElement as HTMLElement);
    await waitForHoverOpenDelay();
    expect(openIconButtonTooltip()).toHaveTextContent(i18n.t('settings.nodeTypes.cannotDeleteDefaultHint'));
  });

  // DFLT-00203: the button is aria-disabled rather than disabled, so it is
  // in the Tab order and keyboard focus shows the same reason.
  it('shows why a default type cannot be deleted on keyboard focus', async () => {
    const user = userEvent.setup();
    render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('implementation-tier-text');
    const byDefault = screen.getByRole('button', {
      name: i18n.t('settings.nodeTypes.deleteTypeAriaLabel', { name: i18n.t('nodeType.implementation') })
    });
    // Tab to it from the row's select button, the control just before it.
    act(() => (byDefault.parentElement!.previousElementSibling as HTMLElement).focus());
    await user.tab();
    expect(byDefault).toHaveFocus();
    expect(openIconButtonTooltip()).toHaveTextContent(i18n.t('settings.nodeTypes.cannotDeleteDefaultHint'));
    expect(byDefault).toHaveAccessibleDescription(i18n.t('settings.nodeTypes.cannotDeleteDefaultHint'));
  });

  // DFLT-00203: neither a click nor Enter nor Space deletes a default type
  // (no confirmation, no request).
  it('does nothing when a default type\'s delete button is clicked or activated from the keyboard', async () => {
    mockedSaveType.mockReset();
    const user = userEvent.setup();
    render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('implementation-tier-text');
    const byDefault = screen.getByRole('button', {
      name: i18n.t('settings.nodeTypes.deleteTypeAriaLabel', { name: i18n.t('nodeType.implementation') })
    });

    await user.click(byDefault);
    act(() => byDefault.focus());
    expect(byDefault).toHaveFocus();
    await user.keyboard('{Enter}');
    await user.keyboard(' ');

    expect(screen.queryByTestId('node-type-delete-confirm-confirm')).toBeNull();
    expect(mockedSaveType).not.toHaveBeenCalled();
    expect(byDefault).toBeInTheDocument();
  });
});

// DFLT-00171: the add row's confirm / cancel buttons say what they confirm
// or cancel, instead of a bare "yes" / "no".
describe('NodeTypesEditor add row button names', () => {
  afterEach(async () => {
    await i18n.changeLanguage('ja');
  });

  it.each([
    ['ja', 'ノード種別を追加', '追加を取り消す'],
    ['en', 'Add node type', 'Cancel adding']
  ])('names the confirm and cancel buttons in %s, with no title', async (lng, confirmName, cancelName) => {
    await i18n.changeLanguage(lng);
    const user = userEvent.setup();
    render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('implementation-tier-text');

    await user.click(screen.getByRole('button', { name: i18n.t('settings.nodeTypes.addType') }));
    const confirm = screen.getByRole('button', { name: confirmName });
    const cancel = screen.getByRole('button', { name: cancelName });
    for (const button of [confirm, cancel]) {
      expect(button).not.toHaveAttribute('title');
      expect(button).toHaveAccessibleDescription('');
    }
    expect(screen.queryByRole('button', { name: i18n.t('settings.common.yes') })).toBeNull();
    expect(screen.queryByRole('button', { name: i18n.t('settings.common.no') })).toBeNull();

    // Keyboard focus shows the name as a tooltip.
    await user.tab();
    expect(confirm).toHaveFocus();
    expect(openIconButtonTooltip()).toHaveTextContent(confirmName);
    await user.tab();
    expect(cancel).toHaveFocus();
    expect(openIconButtonTooltip()).toHaveTextContent(cancelName);

    await user.click(cancel);
    expect(screen.queryByRole('button', { name: cancelName })).toBeNull();
  });
});

// DFLT-00212: the merged preview's heading was a <label> with no form control
// to label, and the scrolling <pre> could not be reached by keyboard. The
// heading is now a paragraph that names the <pre> as a focusable region (the
// same structure as TemplateTextEditor / ReviewGatesEditor). Only the selected
// type's preview is ever shown, so the heading alone keeps the name unique.
describe('NodeTypesEditor merged preview region', () => {
  beforeEach(() => {
    mockedFetchTypes.mockReset();
    mockedFetchType.mockReset();
    mockedFetchTypes.mockResolvedValue(TYPES);
    stubFetchType();
  });

  afterEach(async () => {
    await i18n.changeLanguage('ja');
  });

  const region = () => screen.getByRole('region', { name: i18n.t('settings.nodeTypes.mergedPreviewLabel') });

  it('exposes the preview as a region named by a paragraph heading, holding the merged text', async () => {
    render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('implementation-tier-text');

    const preview = region();
    expect(preview.tagName).toBe('PRE');
    expect(preview).toHaveTextContent('implementation-merged-text');

    // useId values contain colons, so resolve the reference with getElementById.
    const heading = document.getElementById(preview.getAttribute('aria-labelledby') ?? '');
    expect(heading).not.toBeNull();
    expect(heading!.tagName).toBe('P');
    expect(heading).toHaveTextContent(i18n.t('settings.nodeTypes.mergedPreviewLabel'));
    expect(preview.parentElement!.querySelector('label')).toBeNull();
  });

  it('shows the inherited-from-default text inside the region when the merged text is empty', async () => {
    mockedFetchType.mockImplementation(async (_t, type: string) => ({ type, tier_text: `${type}-tier-text`, merged_text: '' }));
    render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('implementation-tier-text');

    expect(region()).toHaveTextContent(i18n.t('settings.common.inheritedFromDefault'));
  });

  it('is keyboard focusable and draws a focus ring', async () => {
    const user = userEvent.setup();
    render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('implementation-tier-text');

    const preview = region();
    expect(preview).toHaveAttribute('tabindex', '0');
    // jsdom computes no styles, so the focus ring classes are pinned.
    expect(preview).toHaveClass('focus:outline-hidden', 'focus-visible:ring-2', 'focus-visible:ring-blue-500', 'max-h-40');

    // The preview sits right before the tier text textarea in tab order.
    const textarea = screen.getByLabelText(i18n.t('settings.nodeTypes.tierTextLabel'));
    textarea.focus();
    await user.tab({ shift: true });
    expect(preview).toHaveFocus();
  });

  it('stays a single region whose content follows the selection', async () => {
    const user = userEvent.setup();
    render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('implementation-tier-text');

    await user.click(screen.getByRole('button', { name: new RegExp(`^${i18n.t('nodeType.review')}`) }));
    await screen.findByDisplayValue('review-tier-text');

    expect(screen.getAllByRole('region', { name: i18n.t('settings.nodeTypes.mergedPreviewLabel') })).toHaveLength(1);
    expect(region()).toHaveTextContent('review-merged-text');
  });

  it('is named by the English heading after switching the language', async () => {
    render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('implementation-tier-text');

    await act(async () => {
      await i18n.changeLanguage('en');
    });

    expect(region()).toHaveTextContent('implementation-merged-text');
  });
});

// DFLT-00261: at a 200% default font on a 320px screen the fixed-width (w-56)
// list pushed its buttons about 103px past the tab panel. Below 48rem (the
// rem-based `narrow:` variant from index.css) the list stacks above the body
// at full width with a capped height, item names wrap instead of truncating,
// and the save row wraps. The wide classes stay, so a wide window with the
// default font looks as before. jsdom does no layout or media queries, so
// this pins the classes; the widths were measured in a real browser (see the
// ticket's implementation notes).
describe('NodeTypesEditor narrow reflow (DFLT-00261)', () => {
  beforeEach(() => {
    mockedFetchTypes.mockReset();
    mockedFetchType.mockReset();
    mockedFetchTypes.mockResolvedValue(TYPES);
    stubFetchType();
  });

  it('stacks the list above the body below 48rem and keeps the side-by-side classes', async () => {
    const { container } = render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('implementation-tier-text');

    const root = container.firstElementChild as HTMLElement;
    expect(root).toHaveClass('flex', 'h-full', 'min-h-0', 'gap-4', 'narrow:flex-col', 'narrow:h-auto');
    const list = root.querySelector('.w-56') as HTMLElement;
    expect(list).toHaveClass('w-56', 'shrink-0', 'overflow-y-auto', 'narrow:w-full', 'narrow:max-h-40');
    // DFLT-00287: the names no longer wrap below 48rem -- they stay on one
    // line, cut off with an ellipsis (see the DFLT-00287 block below).
    const names = list.querySelectorAll('span.truncate');
    expect(names.length).toBe(TYPES.length);
    for (const name of names) {
      expect(name).not.toHaveClass('narrow:whitespace-normal');
      expect(name).not.toHaveClass('narrow:wrap-anywhere');
    }
    const saveRow = screen.getByRole('button', { name: i18n.t('settings.common.save') }).parentElement as HTMLElement;
    expect(saveRow).toHaveClass('flex', 'justify-end', 'narrow:flex-wrap');
  });

  // A-1: below 48rem the list is a max-h-40 scroll box under a sticky
  // heading, so an item focus scrolls into view must stop below the heading.
  it('keeps a focused list item from scrolling under the sticky heading', async () => {
    const { container } = render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('implementation-tier-text');

    const list = (container.firstElementChild as HTMLElement).querySelector('.w-56') as HTMLElement;
    expect(list).toHaveClass('overflow-y-auto', 'scroll-pt-12');
    expect(list.firstElementChild).toHaveTextContent(i18n.t('settings.nodeTypes.listTitle'));
    expect(list.firstElementChild).toHaveClass('sticky', 'top-0', 'z-10');
  });
});

// DFLT-00287: at a 200% font on a 320px screen the node type names broke
// every letter or two ("Pla/n"), because the name had next to no width left
// on its row and wrap-anywhere let it break anywhere. Now a name is always one
// line with an ellipsis (never broken mid-word); its full text stays in the
// button's accessible name and the title tooltip, and the editor heading
// shows it in full once the type is selected. At the narrowest size the
// "default" badge / override dot move under the name so it gets the row.
// jsdom does no layout or media queries, so the classes and the heading are
// pinned here; the widths were measured in a real browser (implementation
// notes).
describe('NodeTypesEditor names cut off with an ellipsis, full name in the editor heading (DFLT-00287)', () => {
  const MIXED: SettingsNodeTypeInfo[] = [
    { type: 'implementation', has_default: true, has_user_override: false },
    { type: 'gherkin_spec', has_default: true, has_user_override: true },
    { type: 'security_review_extended', has_default: false, has_user_override: true }
  ];

  beforeEach(async () => {
    await i18n.changeLanguage('en');
    mockedFetchTypes.mockReset();
    mockedFetchType.mockReset();
    mockedFetchTypes.mockResolvedValue(MIXED);
    stubFetchType();
  });

  afterEach(async () => {
    await i18n.changeLanguage('ja');
  });

  const itemButton = (name: string) => screen.getByRole('button', { name: new RegExp(`^${name}`) });
  const nameSpan = (name: string) => itemButton(name).querySelector('span.truncate') as HTMLElement;
  const editorHeading = () => screen.getByRole('heading', { level: 3 });

  it('keeps every name on one line with an ellipsis and its full text in the title', async () => {
    render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('implementation-tier-text');

    const name = nameSpan('Implementation');
    expect(name).toHaveTextContent(/^Implementation$/);
    expect(name).toHaveClass('truncate');
    expect(name).not.toHaveClass('narrow:whitespace-normal');
    expect(name).not.toHaveClass('narrow:wrap-anywhere');
    expect(name).not.toHaveClass('wrap-anywhere');
    expect(name).toHaveAttribute('title', 'Implementation');
    expect(nameSpan('security_review_extended')).toHaveAttribute('title', 'security_review_extended');
  });

  it('moves the "default" badge and the override dot under the name at the narrowest size', async () => {
    render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('implementation-tier-text');

    for (const label of ['Implementation', 'Gherkin Spec', 'security_review_extended']) {
      const button = itemButton(label);
      expect(button).toHaveClass('upto-15rem:flex-wrap', 'upto-15rem:gap-y-0.5');
      expect(nameSpan(label)).toHaveClass('flex-1', 'upto-15rem:basis-[calc(100%-1.375rem)]');
    }
    const badge = screen.getByText(i18n.t('settings.nodeTypes.defaultBadge'));
    expect(itemButton('Implementation')).toContainElement(badge);
    expect(badge).toHaveClass('upto-15rem:ml-[1.375rem]');
    // DFLT-00294 / DFLT-00320: the badge text is in rem, so it follows the
    // browser's default font size like the rest of the editor, and is
    // 0.6875rem (11px at the default 16px) so it is readable; leading-none
    // keeps the larger text from making the item taller.
    expect(badge).toHaveClass('text-[0.6875rem]', 'leading-none');
    expect(badge).not.toHaveClass('text-[0.5625rem]');
    // DFLT-00320: the text colour meets WCAG 1.4.3 (4.5:1) on the badge's
    // background: slate-600 on slate-200 is 6.15:1, slate-300 on slate-700
    // (dark) is 6.97:1. The earlier slate-500 / slate-400 were 3.86:1 / 4.04:1.
    expect(badge).toHaveClass('bg-slate-200', 'text-slate-600', 'dark:bg-slate-700', 'dark:text-slate-300');
    expect(badge).not.toHaveClass('text-slate-500');
    expect(badge).not.toHaveClass('dark:text-slate-400');
    const dot = itemButton('Gherkin Spec').querySelector(`[title="${i18n.t('settings.nodeTypes.overrideBadge')}"]`);
    expect(dot).toHaveClass('upto-15rem:ml-[1.375rem]');
  });

  it('keeps the full name in the item button and delete button accessible names', async () => {
    render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('implementation-tier-text');

    expect(itemButton('Implementation')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /^security_review_extended$/ })).toBeInTheDocument();
    const del = screen.getByRole('button', {
      name: i18n.t('settings.nodeTypes.deleteTypeAriaLabel', { name: 'Implementation' })
    });
    expect(del.getAttribute('aria-label') ?? del.textContent).toContain('Implementation');
  });

  it.each([
    ['click', 'Implementation', 'implementation'],
    ['keyboard Enter', 'Gherkin Spec', 'gherkin_spec']
  ] as const)('shows a default type\'s full name and its id in the editor heading (%s)', async (how, label, type) => {
    const user = userEvent.setup();
    // Start on the custom type so selecting the default one is a change.
    mockedFetchTypes.mockResolvedValue([MIXED[2], MIXED[0], MIXED[1]]);
    render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('security_review_extended-tier-text');

    if (how === 'click') {
      await user.click(itemButton(label));
    } else {
      itemButton(label).focus();
      await user.keyboard('{Enter}');
    }
    await screen.findByDisplayValue(`${type}-tier-text`);

    const heading = editorHeading();
    expect(heading.tagName).toBe('H3');
    expect(heading).toHaveTextContent(label);
    const id = heading.querySelector('code');
    expect(id).not.toBeNull();
    expect(id).toHaveTextContent(new RegExp(`^${type}$`));
    expect(id).toHaveClass('font-mono');
    expect(id?.textContent).not.toBe(heading.querySelector('span')?.textContent);
  });

  // DFLT-00287 (Gherkin review carry-over): a label that differs from the id
  // only in case ("Plan" for plan) still counts as different, so the id is
  // shown.
  it('shows the id even when the label differs from it only in case', async () => {
    mockedFetchTypes.mockResolvedValue([{ type: 'plan', has_default: true, has_user_override: false }]);
    render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('plan-tier-text');

    const heading = editorHeading();
    expect(heading.querySelector('span')).toHaveTextContent(/^Plan$/);
    expect(heading.querySelector('code')).toHaveTextContent(/^plan$/);
  });

  it.each(['click', 'keyboard Enter'] as const)('shows a custom type\'s id only once in the editor heading (%s)', async how => {
    const user = userEvent.setup();
    render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('implementation-tier-text');

    const button = screen.getByRole('button', { name: /^security_review_extended$/ });
    if (how === 'click') {
      await user.click(button);
    } else {
      button.focus();
      await user.keyboard('{Enter}');
    }
    await screen.findByDisplayValue('security_review_extended-tier-text');

    const heading = editorHeading();
    expect(heading).toHaveTextContent(/^security_review_extended$/);
    expect(heading.textContent?.split('security_review_extended').length).toBe(2);
    expect(heading.querySelector('code')).toBeNull();
  });

  it('shows the selected name in the heading while its text is still loading', async () => {
    const user = userEvent.setup();
    mockedFetchTypes.mockResolvedValue([MIXED[1], MIXED[0]]);
    render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('gherkin_spec-tier-text');

    let resolve: (value: { type: string; tier_text: string; merged_text: string }) => void = () => {};
    mockedFetchType.mockImplementationOnce(() => new Promise(r => { resolve = r; }));
    await user.click(itemButton('Implementation'));

    expect(await screen.findByText(i18n.t('settings.common.loading'))).toBeInTheDocument();
    expect(editorHeading()).toHaveTextContent('Implementation');

    await act(async () => {
      resolve({ type: 'implementation', tier_text: 'implementation-tier-text', merged_text: 'implementation-merged-text' });
    });
    await screen.findByDisplayValue('implementation-tier-text');
    expect(screen.queryByText(i18n.t('settings.common.loading'))).not.toBeInTheDocument();
    expect(editorHeading()).toHaveTextContent('Implementation');
  });

  it('uses the settings heading level and styling, wrapping only between words where it can', async () => {
    render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('implementation-tier-text');

    const heading = editorHeading();
    expect(heading.tagName).toBe('H3');
    // Same size and weight as AppSettingsEditor's section headings.
    expect(heading).toHaveClass('text-xs', 'font-bold', 'wrap-break-word');
    expect(heading).not.toHaveClass('wrap-anywhere');
  });

  it('shows the translated name in the Japanese UI with the id beside it', async () => {
    await i18n.changeLanguage('ja');
    render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('implementation-tier-text');

    const heading = editorHeading();
    expect(heading.querySelector('span')).toHaveTextContent(i18n.t('nodeType.implementation'));
    expect(heading.querySelector('code')).toHaveTextContent('implementation');
  });

  // DFLT-00321: the selected type's list button carries aria-current="true"
  // (the value TemplatesEditor uses); no other item -- and not the row's
  // delete button -- carries it, and it follows the selection.
  it('marks only the selected type with aria-current, and moves it with the selection', async () => {
    mockedFetchTypes.mockResolvedValue([...TYPES, { type: 'custom_lint', has_default: false, has_user_override: true }]);
    const user = userEvent.setup();
    render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('implementation-tier-text');

    const listButton = (type: string) =>
      screen.getByRole('button', { name: new RegExp(`^${type === 'custom_lint' ? 'custom_lint' : i18n.t(`nodeType.${type}`)}`) });
    const deleteButton = screen.getByRole('button', { name: i18n.t('settings.nodeTypes.deleteTypeAriaLabel', { name: 'custom_lint' }) });

    expect(listButton('implementation')).toHaveAttribute('aria-current', 'true');
    expect(listButton('review')).not.toHaveAttribute('aria-current');
    expect(listButton('custom_lint')).not.toHaveAttribute('aria-current');
    expect(deleteButton).not.toHaveAttribute('aria-current');
    expect(document.querySelectorAll('[aria-current]')).toHaveLength(1);

    await user.click(listButton('custom_lint'));
    await screen.findByDisplayValue('custom_lint-tier-text');
    expect(listButton('custom_lint')).toHaveAttribute('aria-current', 'true');
    expect(listButton('implementation')).not.toHaveAttribute('aria-current');
    expect(deleteButton).not.toHaveAttribute('aria-current');
    expect(document.querySelectorAll('[aria-current]')).toHaveLength(1);
  });
});

// DFLT-00350: load failures (the list, the selected type's text), the retry
// that follows them, and what the right-hand pane shows right after a switch.
describe('NodeTypesEditor load failures and switching', () => {
  const retryButton = () => screen.getByRole('button', { name: i18n.t('settings.common.retry') });
  const reviewItem = () => screen.getByRole('button', { name: new RegExp(`^${i18n.t('nodeType.review')}`) });
  const addButton = () => screen.getByRole('button', { name: i18n.t('settings.nodeTypes.addType') });
  type Detail = { type: string; tier_text: string; merged_text: string };

  beforeEach(() => {
    mockedFetchTypes.mockReset();
    mockedFetchType.mockReset();
    mockedSaveType.mockReset();
    mockedFetchTypes.mockResolvedValue(TYPES);
    stubFetchType();
  });

  it('shows the loading line as a status', () => {
    mockedFetchTypes.mockReturnValue(new Promise(() => {}));
    render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
    expect(screen.getByText(i18n.t('settings.common.loading'), { selector: '[role="status"]' })).toBeInTheDocument();
  });

  it('loads the list and the first type on retry, keeping the retry button busy meanwhile, and moves focus to the editor pane', async () => {
    const user = userEvent.setup();
    const pending = deferred<SettingsNodeTypeInfo[]>();
    mockedFetchTypes.mockRejectedValueOnce(new Error('list failed')).mockReturnValueOnce(pending.promise);
    render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
    await screen.findByRole('alert');

    const button = retryButton();
    button.focus();
    await user.keyboard('{Enter}');
    expect(button).toHaveFocus();
    expect(button).toHaveAttribute('aria-busy', 'true');
    expect(screen.getByRole('alert')).toBeInTheDocument();
    await user.keyboard('{Enter}');
    expect(mockedFetchTypes).toHaveBeenCalledTimes(2);

    await act(async () => { pending.resolve(TYPES); });

    const textarea = await screen.findByDisplayValue('implementation-tier-text');
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    await waitFor(() => expect(document.activeElement).toHaveAttribute('tabindex', '-1'));
    expect(document.activeElement?.contains(textarea)).toBe(true);
  });

  it('keeps the error and the retry button when the list retry fails again', async () => {
    const user = userEvent.setup();
    mockedFetchTypes.mockRejectedValue(new Error('list failed'));
    render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
    const first = await screen.findByRole('alert');

    retryButton().focus();
    await user.keyboard('{Enter}');

    await waitFor(() => expect(mockedFetchTypes).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.getByRole('alert')).not.toBe(first));
    expect(screen.getByRole('alert')).toHaveTextContent('list failed');
    expect(retryButton()).toHaveFocus();
    expect(retryButton()).not.toHaveAttribute('aria-busy');
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
  });

  it('leaves the loading line when the list retry returns an empty list', async () => {
    const user = userEvent.setup();
    mockedFetchTypes.mockRejectedValueOnce(new Error('list failed')).mockResolvedValueOnce([]);
    render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
    await screen.findByRole('alert');

    await user.click(retryButton());

    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument());
    expect(screen.queryByText(i18n.t('settings.common.loading'))).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: i18n.t('settings.common.retry') })).not.toBeInTheDocument();
    expect(mockedFetchType).not.toHaveBeenCalled();
    expect(screen.getByText(i18n.t('settings.nodeTypes.emptyList'))).toBeInTheDocument();
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
  });

  it('disables "add node type" while the list is loading or failed', async () => {
    const user = userEvent.setup();
    const pending = deferred<SettingsNodeTypeInfo[]>();
    mockedFetchTypes.mockReturnValueOnce(pending.promise).mockResolvedValueOnce(TYPES);
    render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
    expect(addButton()).toBeDisabled();

    await act(async () => { pending.reject(new Error('list failed')); });
    await screen.findByRole('alert');
    expect(addButton()).toBeDisabled();

    await user.click(retryButton());
    await screen.findByDisplayValue('implementation-tier-text');
    expect(addButton()).toBeEnabled();
  });

  it('keeps the editor and shows a non-blocking error when the list re-fetch after a save fails', async () => {
    const user = userEvent.setup();
    mockedSaveType.mockResolvedValue({ type: 'implementation', tier_text: 'edited', merged_text: 'edited' });
    render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
    const textarea = await screen.findByDisplayValue('implementation-tier-text');
    mockedFetchTypes.mockRejectedValueOnce(new Error('refresh failed'));

    await user.clear(textarea);
    await user.type(textarea, 'edited');
    await user.click(screen.getByRole('button', { name: i18n.t('settings.common.save') }));

    expect(await screen.findByText('refresh failed')).toBeInTheDocument();
    expect(screen.getByDisplayValue('edited')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: i18n.t('settings.common.retry') })).not.toBeInTheDocument();
  });

  it('shows the error and a retry button, not the editor, when the selected type\'s text cannot be loaded, and loads it on retry', async () => {
    const user = userEvent.setup();
    const pending = deferred<Detail>();
    mockedFetchType.mockRejectedValueOnce(new Error('text failed')).mockReturnValueOnce(pending.promise);
    render(<NodeTypesEditor onDirtyChange={vi.fn()} />);

    expect(await screen.findByRole('alert')).toHaveTextContent('text failed');
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: i18n.t('settings.common.save') })).not.toBeInTheDocument();
    // The list is fine, so adding stays available.
    expect(addButton()).toBeEnabled();

    const button = retryButton();
    button.focus();
    await user.keyboard('{Enter}');
    // The failure and the busy retry button stay up while the retry runs,
    // rather than the loading line.
    expect(screen.getByRole('alert')).toBeInTheDocument();
    expect(button).toHaveFocus();
    expect(button).toHaveAttribute('aria-busy', 'true');
    expect(screen.queryByText(i18n.t('settings.common.loading'))).not.toBeInTheDocument();

    await act(async () => { pending.resolve({ type: 'implementation', tier_text: 'implementation-tier-text', merged_text: 'm' }); });

    const textarea = await screen.findByDisplayValue('implementation-tier-text');
    await waitFor(() => expect(textarea).toHaveFocus());
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('shows the loading line, not the previous type\'s text, right after a switch', async () => {
    const user = userEvent.setup();
    render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('implementation-tier-text');
    mockedFetchType.mockReturnValueOnce(new Promise(() => {}));

    await user.click(reviewItem());

    expect(screen.getByText(i18n.t('settings.common.loading'), { selector: '[role="status"]' })).toBeInTheDocument();
    expect(screen.queryByDisplayValue('implementation-tier-text')).not.toBeInTheDocument();
    expect(screen.queryByText('implementation-merged-text')).not.toBeInTheDocument();
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
  });

  it('does not show the previous type\'s load error after switching to another type', async () => {
    const user = userEvent.setup();
    mockedFetchType.mockRejectedValueOnce(new Error('implementation failed'));
    render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
    await screen.findByRole('alert');
    mockedFetchType.mockReturnValueOnce(new Promise(() => {}));

    await user.click(reviewItem());

    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(screen.queryByText(/implementation failed/)).not.toBeInTheDocument();
    expect(screen.getByText(i18n.t('settings.common.loading'), { selector: '[role="status"]' })).toBeInTheDocument();
  });

  it.each([
    ['succeeds', (d: Deferred<Detail>) => d.resolve({ type: 'implementation', tier_text: 'late-implementation-text', merged_text: 'late' })],
    ['fails', (d: Deferred<Detail>) => d.reject(new Error('late implementation failure'))]
  ])('ignores the previous type\'s answer that arrives after a switch (it %s)', async (_how, settle) => {
    const user = userEvent.setup();
    const first = deferred<Detail>();
    const second = deferred<Detail>();
    mockedFetchType.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
    render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
    await waitFor(() => expect(mockedFetchType).toHaveBeenCalledTimes(1));

    await user.click(reviewItem());
    await waitFor(() => expect(mockedFetchType).toHaveBeenCalledTimes(2));
    await act(async () => { second.resolve({ type: 'review', tier_text: 'review-tier-text', merged_text: 'review-merged' }); });
    await screen.findByDisplayValue('review-tier-text');

    await act(async () => { settle(first); });

    expect(screen.getByDisplayValue('review-tier-text')).toBeInTheDocument();
    expect(screen.queryByDisplayValue('late-implementation-text')).not.toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });
});
