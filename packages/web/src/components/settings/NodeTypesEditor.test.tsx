// Covers the #3 dependency-array fix (loadTypes must not re-run just
// because `selected` changed) and F-1's structural non-regression (language
// switch must never re-trigger a load and blow away an unsaved edit). See
// this ticket's plan sections 3-2 (#3/#4) and 4-2.
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../../i18n';
import { NodeTypesEditor } from './NodeTypesEditor';
import { SettingsNodeTypeInfo } from '../../types';

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
    // disabled opacity (a disabled control is exempt from 1.4.11) are kept.
    it('rests the delete buttons at slate-500 / dark:slate-400, keeping the red hover and disabled opacity', async () => {
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
      expect(buttons[1]).toBeDisabled();
      for (const b of buttons) {
        expect(b).toHaveClass('text-slate-500', 'dark:text-slate-400');
        expect(b).not.toHaveClass('text-slate-400');
        expect(b).not.toHaveClass('dark:text-slate-500');
        expect(b).toHaveClass('hover:text-red-600', 'dark:hover:text-red-400', 'disabled:opacity-30');
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

  it('moves focus to the next row\'s select button when that row is a plugin default (its delete button is disabled)', async () => {
    serveList([TYPES[0], custom('custom_a'), TYPES[1]]);
    const { container } = render(<NodeTypesEditor onDirtyChange={vi.fn()} />);

    await deleteAndConfirm(container, 'custom_a');

    await waitFor(() => expect(byKey(container, 'select-review')).toHaveFocus());
    expect(byKey(container, 'delete-review')).toBeDisabled();
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
    // the reason they are disabled as their description.
    for (const type of ['implementation', 'review'] as const) {
      const disabledDel = screen.getByRole('button', {
        name: i18n.t('settings.nodeTypes.deleteTypeAriaLabel', { name: i18n.t(`nodeType.${type}`) })
      });
      expect(disabledDel).toBeDisabled();
      expect(disabledDel).toHaveAccessibleDescription(i18n.t('settings.nodeTypes.cannotDeleteDefaultHint'));
    }

    await user.click(screen.getByRole('button', { name: i18n.t('settings.nodeTypes.addType') }));
    const yes = screen.getByRole('button', { name: i18n.t('settings.common.yes') });
    const no = screen.getByRole('button', { name: i18n.t('settings.common.no') });
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
    const no = screen.getByRole('button', { name: i18n.t('settings.common.no') });
    expect(no).toHaveClass('text-slate-500', 'dark:text-slate-400', 'hover:text-slate-700', 'dark:hover:text-slate-200');
    expect(no).not.toHaveClass('text-slate-400');
    expect(no).not.toHaveClass('hover:text-slate-600');
  });
});

// DFLT-00193: after a delete, focus lands on a neighboring row's delete
// button, so each delete button's accessible name carries its type -- in
// the "<action>: <target>" form LabelsEditor uses -- while the tooltip stays
// as it was and a default type keeps "why it can't be deleted" as its
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
    expect(custom).toHaveAttribute('title', i18n.t('settings.nodeTypes.deleteType'));

    const byDefault = screen.getByRole('button', { name: defaultName });
    expect(byDefault).toBe(container.querySelector('[data-focus-key="delete-implementation"]'));
    expect(byDefault).toBeDisabled();
    expect(byDefault).toHaveAttribute('title', i18n.t('settings.nodeTypes.cannotDeleteDefaultHint'));
    expect(byDefault).toHaveAccessibleDescription(i18n.t('settings.nodeTypes.cannotDeleteDefaultHint'));
  });
});
