// Covers the #3 dependency-array fix (loadTypes must not re-run just
// because `selected` changed) and F-1's structural non-regression (language
// switch must never re-trigger a load and blow away an unsaved edit). See
// this ticket's plan sections 3-2 (#3/#4) and 4-2.
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi, type MockInstance } from 'vitest';
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
    expect(screen.queryByRole('button', { name: /custom_lint/ })).not.toBeInTheDocument();
  });

  it('#3 non-regression: changing the selection does not re-fetch the type list', async () => {
    const user = userEvent.setup();
    render(<NodeTypesEditor onDirtyChange={vi.fn()} />);

    await screen.findByDisplayValue('implementation-tier-text');
    expect(mockedFetchTypes).toHaveBeenCalledTimes(1);

    const reviewButton = screen.getByRole('button', { name: new RegExp(i18n.t('nodeType.review')) });
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

  // DFLT-00137: switching the selected type with unsaved edits asks first.
  describe('switching the selection with unsaved edits', () => {
    let confirmSpy: MockInstance<typeof window.confirm>;
    beforeEach(() => {
      confirmSpy = vi.spyOn(window, 'confirm');
    });
    afterEach(() => {
      confirmSpy.mockRestore();
    });

    const reviewButton = () => screen.getByRole('button', { name: new RegExp(i18n.t('nodeType.review')) });
    const implementationButton = () => screen.getByRole('button', { name: new RegExp(i18n.t('nodeType.implementation')) });

    it('keeps the selection and the edit when the user cancels', async () => {
      confirmSpy.mockReturnValue(false);
      const user = userEvent.setup();
      render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
      const textarea = await screen.findByDisplayValue('implementation-tier-text');
      await user.clear(textarea);
      await user.type(textarea, 'unsaved edit');

      await user.click(reviewButton());

      expect(confirmSpy).toHaveBeenCalledWith(i18n.t('settings.unsavedChanges.confirmMessage'));
      expect(mockedFetchType).not.toHaveBeenCalledWith(expect.anything(), 'review');
      expect(screen.getByDisplayValue('unsaved edit')).toBeInTheDocument();
    });

    it('switches and clears the relayed dirty flag when the user confirms', async () => {
      confirmSpy.mockReturnValue(true);
      const onDirtyChange = vi.fn();
      const user = userEvent.setup();
      render(<NodeTypesEditor onDirtyChange={onDirtyChange} />);
      const textarea = await screen.findByDisplayValue('implementation-tier-text');
      await user.clear(textarea);
      await user.type(textarea, 'unsaved edit');
      expect(onDirtyChange).toHaveBeenLastCalledWith(true);

      await user.click(reviewButton());

      expect(confirmSpy).toHaveBeenCalledTimes(1);
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

      expect(confirmSpy).not.toHaveBeenCalled();
      expect(screen.getByDisplayValue('review-tier-text more')).toBeInTheDocument();
    });

    it('keeps the "add node type" row open when the user cancels the switch', async () => {
      confirmSpy.mockReturnValue(false);
      const user = userEvent.setup();
      render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
      const textarea = await screen.findByDisplayValue('implementation-tier-text');
      await user.type(textarea, ' edited');

      await user.click(screen.getByRole('button', { name: i18n.t('settings.nodeTypes.addType') }));
      await user.type(screen.getByLabelText(i18n.t('settings.nodeTypes.newTypeLabel')), 'custom_lint{Enter}');

      expect(confirmSpy).toHaveBeenCalledWith(i18n.t('settings.unsavedChanges.confirmMessage'));
      expect(screen.getByLabelText(i18n.t('settings.nodeTypes.newTypeLabel'))).toHaveValue('custom_lint');
      expect(screen.queryByRole('button', { name: /custom_lint/ })).not.toBeInTheDocument();
      expect(screen.getByDisplayValue('implementation-tier-text edited')).toBeInTheDocument();
    });
  });

  // DFLT-00137: deleting an override asks first, naming the type.
  describe('deleting an override', () => {
    let confirmSpy: MockInstance<typeof window.confirm>;
    beforeEach(() => {
      confirmSpy = vi.spyOn(window, 'confirm');
      mockedSaveType.mockReset();
      mockedSaveType.mockResolvedValue({ type: 'custom_lint', tier_text: '', merged_text: '' });
      mockedFetchTypes.mockResolvedValue([...TYPES, { type: 'custom_lint', has_default: false, has_user_override: true }]);
    });
    afterEach(() => {
      confirmSpy.mockRestore();
    });

    const deleteButton = () => screen.getByRole('button', { name: i18n.t('settings.nodeTypes.deleteType') });

    it('asks with the type name and does nothing on cancel', async () => {
      confirmSpy.mockReturnValue(false);
      const user = userEvent.setup();
      render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
      await screen.findByDisplayValue('implementation-tier-text');

      await user.click(deleteButton());

      expect(confirmSpy).toHaveBeenCalledTimes(1);
      expect(confirmSpy).toHaveBeenCalledWith(i18n.t('settings.nodeTypes.confirmDeleteType', { name: 'custom_lint' }));
      expect(String(confirmSpy.mock.calls[0][0])).toContain('custom_lint');
      expect(mockedSaveType).not.toHaveBeenCalled();
      expect(mockedFetchTypes).toHaveBeenCalledTimes(1);
    });

    it('clears the override when the user confirms', async () => {
      confirmSpy.mockReturnValue(true);
      const user = userEvent.setup();
      render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
      await screen.findByDisplayValue('implementation-tier-text');

      await user.click(deleteButton());

      await waitFor(() => expect(mockedSaveType).toHaveBeenCalledWith(expect.anything(), 'custom_lint', ''));
    });
  });
});
