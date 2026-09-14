// DFLT-00058: the name input for a not-yet-overridden default gate
// (isOverridden === false) must be disabled with a tooltip explaining why
// (see cannotRenameDefaultHint), while a gate already overridden in this
// scope (isOverridden === true, whether newly added or an existing
// override) must keep working exactly as before. See this ticket's plan --
// the guard/tooltip pattern mirrors the existing delete button
// (disabled={!canEdit || !g.isOverridden} + cannotDeleteDefaultHint).
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../../i18n';
import { ReviewGatesEditor } from './ReviewGatesEditor';
import { SettingsCatalogResponse } from '../../types';

vi.mock('../../lib/settingsApi', async () => {
  const actual = await vi.importActual<typeof import('../../lib/settingsApi')>('../../lib/settingsApi');
  return {
    ...actual,
    fetchSettingsCatalog: vi.fn(),
    saveSettingsCatalog: vi.fn()
  };
});

import { fetchSettingsCatalog } from '../../lib/settingsApi';

const mockedFetchCatalog = fetchSettingsCatalog as unknown as ReturnType<typeof vi.fn>;

// code_review is never overridden in this scope's own tier_document, so it
// only shows up in merged_catalog -- a plugin-default row (isOverridden
// false). qa_review has an entry in tier_document too, so it's an override
// already made in this scope (isOverridden true).
const CATALOG_RESPONSE: SettingsCatalogResponse = {
  tier_document: {
    version: 1,
    review_gates: {
      qa_review: { name: 'QA Review (overridden)', criteria: 'qa criteria', max_iterations: 2, enabled: true }
    }
  },
  merged_catalog: {
    review_gates: {
      code_review: { name: 'Code Review', criteria: 'code criteria', max_iterations: 3, enabled: true },
      qa_review: { name: 'QA Review (overridden)', criteria: 'qa criteria', max_iterations: 2, enabled: true }
    },
    nodes: []
  },
  inherited_catalog: { review_gates: {}, nodes: [] },
  scope: 'global',
  project_id: '',
  team_root_resolved: false
};

describe('ReviewGatesEditor', () => {
  beforeEach(async () => {
    mockedFetchCatalog.mockReset();
    mockedFetchCatalog.mockResolvedValue(CATALOG_RESPONSE);
    await i18n.changeLanguage('ja');
  });

  it('disables the name field for a not-yet-overridden default gate and shows the rename-blocked hint', async () => {
    render(<ReviewGatesEditor scope="global" projectId="" canEdit onDirtyChange={vi.fn()} />);

    const nameInput = await screen.findByDisplayValue('Code Review');
    expect(nameInput).toBeDisabled();
    expect(nameInput).toHaveAttribute('title', i18n.t('settings.reviewGates.cannotRenameDefaultHint'));
  });

  it('keeps the name field editable for an already-overridden gate', async () => {
    const user = userEvent.setup();
    render(<ReviewGatesEditor scope="global" projectId="" canEdit onDirtyChange={vi.fn()} />);

    const nameInput = await screen.findByDisplayValue('QA Review (overridden)');
    expect(nameInput).not.toBeDisabled();
    expect(nameInput).not.toHaveAttribute('title');

    await user.type(nameInput, '!');
    expect(await screen.findByDisplayValue('QA Review (overridden)!')).toBeInTheDocument();
  });

  it('keeps the name field editable for a newly added gate', async () => {
    const user = userEvent.setup();
    render(<ReviewGatesEditor scope="global" projectId="" canEdit onDirtyChange={vi.fn()} />);

    await screen.findByDisplayValue('Code Review');
    const addButton = screen.getByRole('button', { name: i18n.t('settings.reviewGates.addGate') });
    await user.click(addButton);

    // All rows share the same name placeholder, so pick the last (newly
    // added, empty-name) row rather than matching non-uniquely by placeholder.
    const nameInputs = screen.getAllByPlaceholderText(i18n.t('settings.reviewGates.nameLabel'));
    const nameInput = nameInputs[nameInputs.length - 1];
    expect(nameInput).not.toBeDisabled();

    await user.type(nameInput, 'New Gate');
    expect(await screen.findByDisplayValue('New Gate')).toBeInTheDocument();
  });
});
