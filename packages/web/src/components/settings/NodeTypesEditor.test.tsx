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

import { fetchSettingsNodeType, fetchSettingsNodeTypes } from '../../lib/settingsApi';

const mockedFetchTypes = fetchSettingsNodeTypes as unknown as ReturnType<typeof vi.fn>;
const mockedFetchType = fetchSettingsNodeType as unknown as ReturnType<typeof vi.fn>;

const TYPES: SettingsNodeTypeInfo[] = [
  { type: 'implementation', has_default: true, has_user_override: false, has_team_override: false },
  { type: 'review', has_default: true, has_user_override: false, has_team_override: false }
];

function stubFetchType() {
  mockedFetchType.mockImplementation(async (_t, _scope, _projectId, type: string) => ({
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
    render(<NodeTypesEditor scope="global" projectId="" canEdit onDirtyChange={vi.fn()} />);

    await waitFor(() => expect(mockedFetchType).toHaveBeenCalledWith(expect.anything(), 'global', '', 'implementation'));
    expect(await screen.findByDisplayValue('implementation-tier-text')).toBeInTheDocument();
  });

  // DFLT-00074
  it('labels the tier text textarea', async () => {
    render(<NodeTypesEditor scope="global" projectId="" canEdit onDirtyChange={vi.fn()} />);

    const textarea = await screen.findByDisplayValue('implementation-tier-text');
    expect(screen.getByLabelText(i18n.t('settings.nodeTypes.tierTextLabel'))).toBe(textarea);
  });

  it('labels the "add node type" input, and handles Escape there with preventDefault to cancel the add', async () => {
    const user = userEvent.setup();
    render(<NodeTypesEditor scope="global" projectId="" canEdit onDirtyChange={vi.fn()} />);
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
    render(<NodeTypesEditor scope="global" projectId="" canEdit onDirtyChange={vi.fn()} />);

    await screen.findByDisplayValue('implementation-tier-text');
    expect(mockedFetchTypes).toHaveBeenCalledTimes(1);

    const reviewButton = screen.getByRole('button', { name: new RegExp(i18n.t('nodeType.review')) });
    await user.click(reviewButton);

    await screen.findByDisplayValue('review-tier-text');
    expect(mockedFetchTypes).toHaveBeenCalledTimes(1);
    expect(mockedFetchType).toHaveBeenCalledWith(expect.anything(), 'global', '', 'review');
  });

  it('F-1 non-regression: switching language after editing does not re-fetch and does not discard the unsaved edit', async () => {
    const user = userEvent.setup();
    render(<NodeTypesEditor scope="global" projectId="" canEdit onDirtyChange={vi.fn()} />);

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
});
