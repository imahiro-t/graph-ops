// DFLT-00058: the name input for a not-yet-overridden default gate
// (isOverridden === false) must be disabled with a tooltip explaining why
// (see cannotRenameDefaultHint), while a gate already overridden in this
// scope (isOverridden === true, whether newly added or an existing
// override) must keep working exactly as before. See this ticket's plan --
// the guard/tooltip pattern mirrors the existing delete button
// (disabled={!canEdit || !g.isOverridden} + cannotDeleteDefaultHint).
import { render, screen, waitFor } from '@testing-library/react';
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

import { fetchSettingsCatalog, saveSettingsCatalog } from '../../lib/settingsApi';

const mockedFetchCatalog = fetchSettingsCatalog as unknown as ReturnType<typeof vi.fn>;
const mockedSaveCatalog = saveSettingsCatalog as unknown as ReturnType<typeof vi.fn>;

// code_review is never overridden in this scope's own tier_document, so it
// only shows up in merged_catalog -- a plugin-default row (isOverridden
// false). qa_review has an entry in tier_document too, so it's an override
// already made in this scope (isOverridden true). Both are plugin defaults,
// so both are in inherited_catalog.
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
  inherited_catalog: {
    review_gates: {
      code_review: { name: 'Code Review', criteria: 'code criteria', max_iterations: 3, enabled: true },
      qa_review: { name: 'QA Review', criteria: 'default qa criteria', max_iterations: 3, enabled: true }
    },
    nodes: []
  }
};

// CATALOG_RESPONSE plus an override of a custom gate (custom_review) that
// has no plugin default behind it.
const WITH_CUSTOM_GATE: SettingsCatalogResponse = {
  ...CATALOG_RESPONSE,
  tier_document: {
    version: 1,
    review_gates: {
      ...CATALOG_RESPONSE.tier_document.review_gates,
      custom_review: { name: 'Custom Review', criteria: 'custom criteria' }
    }
  },
  merged_catalog: {
    ...CATALOG_RESPONSE.merged_catalog,
    review_gates: {
      ...CATALOG_RESPONSE.merged_catalog.review_gates,
      custom_review: { name: 'Custom Review', criteria: 'custom criteria' }
    }
  }
};

// code_review with a partial override (only max_iterations) in this scope --
// what saving a single changed field of the default gate produces.
const WITH_PARTIAL_DEFAULT_OVERRIDE: SettingsCatalogResponse = {
  ...CATALOG_RESPONSE,
  tier_document: {
    version: 1,
    review_gates: { ...CATALOG_RESPONSE.tier_document.review_gates, code_review: { max_iterations: 5 } }
  },
  merged_catalog: {
    ...CATALOG_RESPONSE.merged_catalog,
    review_gates: {
      ...CATALOG_RESPONSE.merged_catalog.review_gates,
      code_review: { name: 'Code Review', criteria: 'code criteria', max_iterations: 5, enabled: true }
    }
  }
};

describe('ReviewGatesEditor', () => {
  beforeEach(async () => {
    mockedFetchCatalog.mockReset();
    mockedFetchCatalog.mockResolvedValue(CATALOG_RESPONSE);
    mockedSaveCatalog.mockReset();
    mockedSaveCatalog.mockResolvedValue(undefined);
    await i18n.changeLanguage('ja');
  });

  it('disables the name field for a not-yet-overridden default gate and shows the rename-blocked hint', async () => {
    render(<ReviewGatesEditor onDirtyChange={vi.fn()} />);

    const nameInput = await screen.findByDisplayValue('Code Review');
    expect(nameInput).toBeDisabled();
    expect(nameInput).toHaveAttribute('title', i18n.t('settings.reviewGates.cannotRenameDefaultHint'));
  });

  it('keeps the name field editable for an already-overridden gate', async () => {
    const user = userEvent.setup();
    render(<ReviewGatesEditor onDirtyChange={vi.fn()} />);

    const nameInput = await screen.findByDisplayValue('QA Review (overridden)');
    expect(nameInput).not.toBeDisabled();
    expect(nameInput).not.toHaveAttribute('title');

    await user.type(nameInput, '!');
    expect(await screen.findByDisplayValue('QA Review (overridden)!')).toBeInTheDocument();
  });

  // DFLT-00074: every row's fields have visible labels tied to their inputs.
  it('labels the id, name, max iterations and criteria fields of each row', async () => {
    render(<ReviewGatesEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('Code Review');

    const ids = screen.getAllByLabelText(i18n.t('settings.reviewGates.idLabel'));
    expect(ids.map(el => (el as HTMLInputElement).value)).toEqual(['code_review', 'qa_review']);
    const names = screen.getAllByLabelText(i18n.t('settings.reviewGates.nameLabel'));
    expect(names.map(el => (el as HTMLInputElement).value)).toEqual(['Code Review', 'QA Review (overridden)']);
    const maxIterations = screen.getAllByLabelText(i18n.t('settings.reviewGates.maxIterationsLabel'));
    expect(maxIterations.map(el => (el as HTMLInputElement).value)).toEqual(['3', '2']);
    const criteria = screen.getAllByLabelText(i18n.t('settings.reviewGates.criteriaLabel'));
    expect(criteria.map(el => (el as HTMLTextAreaElement).value)).toEqual(['code criteria', 'qa criteria']);
    expect(screen.getAllByLabelText(i18n.t('settings.reviewGates.additionalCriteriaLabel'))).toHaveLength(2);
  });

  it('keeps the name field editable for a newly added gate', async () => {
    const user = userEvent.setup();
    render(<ReviewGatesEditor onDirtyChange={vi.fn()} />);

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

  // DFLT-00137 -----------------------------------------------------------

  // Row inputs by label, in row order (code_review, qa_review, then any
  // added rows).
  const fieldsOf = (label: string) => screen.getAllByLabelText(i18n.t(label)) as HTMLInputElement[];
  const saveButton = () => screen.getByRole('button', { name: i18n.t('settings.common.save') });
  const savedGates = () => {
    expect(mockedSaveCatalog).toHaveBeenCalledTimes(1);
    return mockedSaveCatalog.mock.calls[0][1].review_gates;
  };

  describe('a row with an empty gate ID', () => {
    it.each([
      ['empty', ''],
      ['whitespace-only', '   ']
    ])('refuses to save (%s), shows an error and keeps the input', async (_label, id) => {
      const user = userEvent.setup();
      render(<ReviewGatesEditor onDirtyChange={vi.fn()} />);
      await screen.findByDisplayValue('Code Review');
      await user.click(screen.getByRole('button', { name: i18n.t('settings.reviewGates.addGate') }));
      const ids = fieldsOf('settings.reviewGates.idLabel');
      if (id) await user.type(ids[ids.length - 1], id);
      const names = fieldsOf('settings.reviewGates.nameLabel');
      await user.type(names[names.length - 1], 'Unnamed Gate');
      const fetchCallsBefore = mockedFetchCatalog.mock.calls.length;

      await user.click(saveButton());

      // Announced to assistive technology, and tied to the offending field.
      const alert = await screen.findByRole('alert');
      expect(alert).toHaveTextContent(i18n.t('settings.reviewGates.emptyIdError'));
      const idsAfter = fieldsOf('settings.reviewGates.idLabel');
      const emptyId = idsAfter[idsAfter.length - 1];
      expect(emptyId).toHaveAttribute('aria-invalid', 'true');
      expect(emptyId).toHaveAttribute('aria-describedby', alert.id);
      expect(idsAfter[0]).not.toHaveAttribute('aria-invalid');
      expect(idsAfter[0]).not.toHaveAttribute('aria-describedby');
      expect(mockedSaveCatalog).not.toHaveBeenCalled();
      expect(mockedFetchCatalog.mock.calls.length).toBe(fetchCallsBefore);
      expect(screen.queryByText(i18n.t('settings.common.saveSuccess'))).not.toBeInTheDocument();
      expect(screen.getByDisplayValue('Unnamed Gate')).toBeInTheDocument();
    });

    it('stops marking the ID field invalid once an ID is entered', async () => {
      const user = userEvent.setup();
      render(<ReviewGatesEditor onDirtyChange={vi.fn()} />);
      await screen.findByDisplayValue('Code Review');
      await user.click(screen.getByRole('button', { name: i18n.t('settings.reviewGates.addGate') }));
      await user.click(saveButton());
      await screen.findByRole('alert');

      const id = fieldsOf('settings.reviewGates.idLabel')[2];
      expect(id).toHaveAttribute('aria-invalid', 'true');
      await user.type(id, 'security_review');
      expect(id).not.toHaveAttribute('aria-invalid');
      expect(id).not.toHaveAttribute('aria-describedby');
    });

    it('does not mark any ID field invalid for an error from the API', async () => {
      mockedSaveCatalog.mockRejectedValue(new Error('boom'));
      const user = userEvent.setup();
      render(<ReviewGatesEditor onDirtyChange={vi.fn()} />);
      await screen.findByDisplayValue('Code Review');
      await user.type(fieldsOf('settings.reviewGates.criteriaLabel')[0], ' extra');
      await user.click(saveButton());

      expect(await screen.findByRole('alert')).toHaveTextContent('boom');
      for (const id of fieldsOf('settings.reviewGates.idLabel')) expect(id).not.toHaveAttribute('aria-invalid');
    });
  });

  describe('saving only the fields that changed', () => {
    it('writes just max_iterations for a not-yet-overridden gate', async () => {
      const user = userEvent.setup();
      render(<ReviewGatesEditor onDirtyChange={vi.fn()} />);
      await screen.findByDisplayValue('Code Review');

      const max = fieldsOf('settings.reviewGates.maxIterationsLabel')[0];
      await user.clear(max);
      await user.type(max, '5');
      await user.click(saveButton());

      const gates = await waitFor(savedGates);
      expect(gates.code_review).toEqual({ max_iterations: 5 });
      expect(await screen.findByText(i18n.t('settings.common.saveSuccess'))).toBeInTheDocument();
    });

    it('does not write a field changed and then changed back (including toggling enabled twice)', async () => {
      const user = userEvent.setup();
      render(<ReviewGatesEditor onDirtyChange={vi.fn()} />);
      await screen.findByDisplayValue('Code Review');

      const criteria = fieldsOf('settings.reviewGates.criteriaLabel')[0];
      await user.type(criteria, ' extra');
      await user.clear(criteria);
      await user.type(criteria, 'code criteria');
      const enabled = screen.getAllByRole('checkbox', { name: i18n.t('settings.reviewGates.enabledLabel') })[0];
      await user.click(enabled);
      await user.click(enabled);
      await user.type(fieldsOf('settings.reviewGates.additionalCriteriaLabel')[0], 'also check docs');
      await user.click(saveButton());

      const gates = await waitFor(savedGates);
      expect(gates.code_review).toEqual({ additional_criteria: 'also check docs' });
    });

    it('writes nothing for a not-yet-overridden gate whose changes were all reverted', async () => {
      const user = userEvent.setup();
      render(<ReviewGatesEditor onDirtyChange={vi.fn()} />);
      await screen.findByDisplayValue('Code Review');

      const enabled = screen.getAllByRole('checkbox', { name: i18n.t('settings.reviewGates.enabledLabel') })[0];
      await user.click(enabled);
      await user.click(enabled);
      await user.click(saveButton());

      const gates = await waitFor(savedGates);
      expect(gates).not.toHaveProperty('code_review');
    });

    it('writes enabled: false when a default gate is disabled', async () => {
      const user = userEvent.setup();
      render(<ReviewGatesEditor onDirtyChange={vi.fn()} />);
      await screen.findByDisplayValue('Code Review');

      await user.click(screen.getAllByRole('checkbox', { name: i18n.t('settings.reviewGates.enabledLabel') })[0]);
      await user.click(saveButton());

      const gates = await waitFor(savedGates);
      expect(gates.code_review).toEqual({ enabled: false });
    });

    it('keeps the fields of an existing override and sends an untouched override as-is', async () => {
      const user = userEvent.setup();
      render(<ReviewGatesEditor onDirtyChange={vi.fn()} />);
      await screen.findByDisplayValue('Code Review');

      const criteria = fieldsOf('settings.reviewGates.criteriaLabel')[1];
      await user.clear(criteria);
      await user.type(criteria, 'new qa criteria');
      await user.click(saveButton());

      const gates = await waitFor(savedGates);
      expect(gates.qa_review).toEqual({ name: 'QA Review (overridden)', criteria: 'new qa criteria', max_iterations: 2, enabled: true });
      expect(gates).not.toHaveProperty('code_review');
    });

    it('keeps a partial override partial and drops a field that is emptied', async () => {
      mockedFetchCatalog.mockResolvedValue({
        ...CATALOG_RESPONSE,
        tier_document: { version: 1, review_gates: { qa_review: { max_iterations: 4, criteria: 'own qa criteria' } } },
        merged_catalog: {
          ...CATALOG_RESPONSE.merged_catalog,
          review_gates: {
            ...CATALOG_RESPONSE.merged_catalog.review_gates,
            qa_review: { name: 'QA Review', criteria: 'own qa criteria', max_iterations: 4, enabled: true }
          }
        }
      });
      const user = userEvent.setup();
      render(<ReviewGatesEditor onDirtyChange={vi.fn()} />);
      await screen.findByDisplayValue('Code Review');

      await user.type(fieldsOf('settings.reviewGates.additionalCriteriaLabel')[1], 'qa extra');
      await user.clear(fieldsOf('settings.reviewGates.criteriaLabel')[1]);
      await user.click(saveButton());

      const gates = await waitFor(savedGates);
      expect(gates.qa_review).toEqual({ max_iterations: 4, additional_criteria: 'qa extra' });
    });

    it('moves an existing override of a custom gate to its new ID when the ID is changed', async () => {
      mockedFetchCatalog.mockResolvedValue(WITH_CUSTOM_GATE);
      const user = userEvent.setup();
      render(<ReviewGatesEditor onDirtyChange={vi.fn()} />);
      await screen.findByDisplayValue('Custom Review');

      const id = fieldsOf('settings.reviewGates.idLabel')[2];
      expect(id).toHaveValue('custom_review');
      expect(id).not.toBeDisabled();
      expect(id).not.toHaveAttribute('title');
      await user.clear(id);
      await user.type(id, 'custom_review_v2');
      await user.click(saveButton());

      const gates = await waitFor(savedGates);
      expect(gates).not.toHaveProperty('custom_review');
      expect(gates.custom_review_v2).toEqual({ name: 'Custom Review', criteria: 'custom criteria' });
    });

    it('writes a newly added gate with the fields that were filled in', async () => {
      const user = userEvent.setup();
      render(<ReviewGatesEditor onDirtyChange={vi.fn()} />);
      await screen.findByDisplayValue('Code Review');

      await user.click(screen.getByRole('button', { name: i18n.t('settings.reviewGates.addGate') }));
      await user.type(fieldsOf('settings.reviewGates.idLabel')[2], 'security_review');
      await user.type(fieldsOf('settings.reviewGates.criteriaLabel')[2], 'no secrets');
      await user.click(saveButton());

      const gates = await waitFor(savedGates);
      expect(gates.security_review).toEqual({ criteria: 'no secrets' });
    });
  });

  it('keeps the ID field of a not-yet-overridden gate disabled after another field is edited', async () => {
    const user = userEvent.setup();
    render(<ReviewGatesEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('Code Review');

    await user.type(fieldsOf('settings.reviewGates.criteriaLabel')[0], ' extra');

    expect(fieldsOf('settings.reviewGates.idLabel')[0]).toBeDisabled();
    // The name field opens up once the row is being overridden, as before.
    expect(fieldsOf('settings.reviewGates.nameLabel')[0]).not.toBeDisabled();
  });

  describe('the ID of a gate with a plugin default', () => {
    it('stays disabled, with the reason as its title, once the gate has a partial override', async () => {
      mockedFetchCatalog.mockResolvedValue(WITH_PARTIAL_DEFAULT_OVERRIDE);
      render(<ReviewGatesEditor onDirtyChange={vi.fn()} />);
      await screen.findByDisplayValue('QA Review (overridden)');

      const ids = fieldsOf('settings.reviewGates.idLabel');
      expect(ids[0]).toHaveValue('code_review');
      expect(ids[0]).toBeDisabled();
      expect(ids[0]).toHaveAttribute('title', i18n.t('settings.reviewGates.cannotChangeDefaultIdHint'));
      // A full override of a default gate is locked as well.
      expect(ids[1]).toBeDisabled();
    });

    it('stays disabled after saving a single changed field of a default gate', async () => {
      const user = userEvent.setup();
      render(<ReviewGatesEditor onDirtyChange={vi.fn()} />);
      await screen.findByDisplayValue('Code Review');

      mockedFetchCatalog.mockResolvedValue(WITH_PARTIAL_DEFAULT_OVERRIDE);
      const max = fieldsOf('settings.reviewGates.maxIterationsLabel')[0];
      await user.clear(max);
      await user.type(max, '5');
      await user.click(saveButton());
      await screen.findByText(i18n.t('settings.common.saveSuccess'));

      expect(fieldsOf('settings.reviewGates.idLabel')[0]).toBeDisabled();
    });

    it('is disabled with the reason for a not-yet-overridden gate too', async () => {
      render(<ReviewGatesEditor onDirtyChange={vi.fn()} />);
      await screen.findByDisplayValue('Code Review');

      const id = fieldsOf('settings.reviewGates.idLabel')[0];
      expect(id).toBeDisabled();
      expect(id).toHaveAttribute('title', i18n.t('settings.reviewGates.cannotChangeDefaultIdHint'));
    });
  });

  it("shows the inherited default as the placeholder of an override's empty fields", async () => {
    mockedFetchCatalog.mockResolvedValue(WITH_PARTIAL_DEFAULT_OVERRIDE);
    render(<ReviewGatesEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('QA Review (overridden)');

    const placeholder = (value: string | number) => i18n.t('settings.reviewGates.inheritedPlaceholder', { value });
    const name = fieldsOf('settings.reviewGates.nameLabel')[0];
    expect(name).toHaveValue('');
    expect(name).toHaveAttribute('placeholder', placeholder('Code Review'));
    const criteria = fieldsOf('settings.reviewGates.criteriaLabel')[0];
    expect(criteria).toHaveValue('');
    expect(criteria).toHaveAttribute('placeholder', placeholder('code criteria'));
    // A field the override sets shows its own value; its placeholder still
    // names the default it replaces.
    const max = fieldsOf('settings.reviewGates.maxIterationsLabel')[0];
    expect(max).toHaveValue(5);
    expect(max).toHaveAttribute('placeholder', placeholder(3));
  });

  it('keeps the plain label placeholders on a newly added gate', async () => {
    const user = userEvent.setup();
    render(<ReviewGatesEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('Code Review');
    await user.click(screen.getByRole('button', { name: i18n.t('settings.reviewGates.addGate') }));

    expect(fieldsOf('settings.reviewGates.nameLabel')[2]).toHaveAttribute('placeholder', i18n.t('settings.reviewGates.nameLabel'));
    expect(fieldsOf('settings.reviewGates.criteriaLabel')[2]).not.toHaveAttribute('placeholder');
  });
});
