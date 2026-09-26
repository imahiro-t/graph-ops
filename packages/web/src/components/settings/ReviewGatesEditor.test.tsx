// DFLT-00058: the name input for a not-yet-overridden default gate
// (isOverridden === false) must be disabled with a tooltip explaining why
// (see cannotRenameDefaultHint), while a gate already overridden in this
// scope (isOverridden === true, whether newly added or an existing
// override) must keep working exactly as before. See this ticket's plan --
// the guard/tooltip pattern mirrored the delete button of the time
// (disabled + cannotDeleteDefaultHint). Since DFLT-00203 that delete button
// is aria-disabled={!g.isOverridden} instead, so it keeps keyboard focus
// and shows its reason there too.
import { act, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../../i18n';
import { ReviewGatesEditor } from './ReviewGatesEditor';
import { SETTINGS_CATALOG_WARNINGS, SettingsCatalogResponse, SettingsCatalogWarning } from '../../types';
import { openIconButtonTooltip } from '../../test/iconButtonTooltip';

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
      qa_review: { name: 'QA Review (overridden)', criteria: 'qa criteria', enabled: true }
    }
  },
  merged_catalog: {
    review_gates: {
      code_review: { name: 'Code Review', criteria: 'code criteria', enabled: true },
      qa_review: { name: 'QA Review (overridden)', criteria: 'qa criteria', enabled: true }
    },
    nodes: [],
    max_iterations: 3
  },
  inherited_catalog: {
    review_gates: {
      code_review: { name: 'Code Review', criteria: 'code criteria', enabled: true },
      qa_review: { name: 'QA Review', criteria: 'default qa criteria', enabled: true }
    },
    nodes: [],
    max_iterations: 3
  },
  warnings: []
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

// code_review with a partial override (only additional_criteria) in this
// scope -- what saving a single changed field of the default gate produces.
const WITH_PARTIAL_DEFAULT_OVERRIDE: SettingsCatalogResponse = {
  ...CATALOG_RESPONSE,
  tier_document: {
    version: 1,
    review_gates: { ...CATALOG_RESPONSE.tier_document.review_gates, code_review: { additional_criteria: 'also docs' } }
  },
  merged_catalog: {
    ...CATALOG_RESPONSE.merged_catalog,
    review_gates: {
      ...CATALOG_RESPONSE.merged_catalog.review_gates,
      code_review: { name: 'Code Review', criteria: 'code criteria\nalso docs', enabled: true }
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
  it('labels the id, name and criteria fields of each row', async () => {
    render(<ReviewGatesEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('Code Review');

    const ids = screen.getAllByLabelText(i18n.t('settings.reviewGates.idLabel'));
    expect(ids.map(el => (el as HTMLInputElement).value)).toEqual(['code_review', 'qa_review']);
    const names = screen.getAllByLabelText(i18n.t('settings.reviewGates.nameLabel'));
    expect(names.map(el => (el as HTMLInputElement).value)).toEqual(['Code Review', 'QA Review (overridden)']);
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
    it('writes just additional_criteria for a not-yet-overridden gate', async () => {
      const user = userEvent.setup();
      render(<ReviewGatesEditor onDirtyChange={vi.fn()} />);
      await screen.findByDisplayValue('Code Review');

      await user.type(fieldsOf('settings.reviewGates.additionalCriteriaLabel')[0], 'also docs');
      await user.click(saveButton());

      const gates = await waitFor(savedGates);
      expect(gates.code_review).toEqual({ additional_criteria: 'also docs' });
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
      expect(gates.qa_review).toEqual({ name: 'QA Review (overridden)', criteria: 'new qa criteria', enabled: true });
      expect(gates).not.toHaveProperty('code_review');
    });

    it('keeps a partial override partial and drops a field that is emptied', async () => {
      mockedFetchCatalog.mockResolvedValue({
        ...CATALOG_RESPONSE,
        tier_document: { version: 1, review_gates: { qa_review: { enabled: true, criteria: 'own qa criteria' } } },
        merged_catalog: {
          ...CATALOG_RESPONSE.merged_catalog,
          review_gates: {
            ...CATALOG_RESPONSE.merged_catalog.review_gates,
            qa_review: { name: 'QA Review', criteria: 'own qa criteria', enabled: true }
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
      expect(gates.qa_review).toEqual({ enabled: true, additional_criteria: 'qa extra' });
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
      await user.type(fieldsOf('settings.reviewGates.additionalCriteriaLabel')[0], 'also docs');
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
    // A field the override sets shows its own value.
    expect(fieldsOf('settings.reviewGates.additionalCriteriaLabel')[0]).toHaveValue('also docs');
  });

  it('keeps the plain label placeholders on a newly added gate', async () => {
    const user = userEvent.setup();
    render(<ReviewGatesEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('Code Review');
    await user.click(screen.getByRole('button', { name: i18n.t('settings.reviewGates.addGate') }));

    expect(fieldsOf('settings.reviewGates.nameLabel')[2]).toHaveAttribute('placeholder', i18n.t('settings.reviewGates.nameLabel'));
    expect(fieldsOf('settings.reviewGates.criteriaLabel')[2]).not.toHaveAttribute('placeholder');
  });

  // DFLT-00140 -----------------------------------------------------------

  describe('the workflow-wide review iteration limit', () => {
    const limitSelect = () =>
      screen.getByLabelText(i18n.t('settings.reviewGates.workflowMaxIterationsLabel')) as HTMLSelectElement;
    const inheritLabel = (value: number) => i18n.t('settings.reviewGates.workflowMaxIterationsInherit', { value });
    const savedDocument = () => {
      expect(mockedSaveCatalog).toHaveBeenCalledTimes(1);
      return mockedSaveCatalog.mock.calls[0][1];
    };

    it('has no per-gate iteration field, and one labelled select offering inherit/3/4/5', async () => {
      render(<ReviewGatesEditor onDirtyChange={vi.fn()} />);
      await screen.findByDisplayValue('Code Review');

      expect(screen.queryAllByRole('spinbutton')).toHaveLength(0);
      const select = limitSelect();
      expect(select.tagName).toBe('SELECT');
      expect(Array.from(select.options).map(o => o.textContent)).toEqual([inheritLabel(3), '3', '4', '5']);
      expect(select).toHaveAccessibleDescription(i18n.t('settings.reviewGates.workflowMaxIterationsHelp'));
    });

    it('selects "inherit (3)" when the user tier sets nothing', async () => {
      render(<ReviewGatesEditor onDirtyChange={vi.fn()} />);
      await screen.findByDisplayValue('Code Review');
      expect(limitSelect()).toHaveValue('');
      expect(limitSelect().selectedOptions[0].textContent).toBe(inheritLabel(3));
    });

    it("selects the user tier's own value, keeping inherit available", async () => {
      mockedFetchCatalog.mockResolvedValue({
        ...CATALOG_RESPONSE,
        tier_document: { ...CATALOG_RESPONSE.tier_document, max_iterations: 4 },
        merged_catalog: { ...CATALOG_RESPONSE.merged_catalog, max_iterations: 4 }
      });
      render(<ReviewGatesEditor onDirtyChange={vi.fn()} />);
      await screen.findByDisplayValue('Code Review');
      expect(limitSelect()).toHaveValue('4');
      expect(screen.getByRole('option', { name: inheritLabel(3) })).toBeInTheDocument();
    });

    it('marks the tab dirty and saves the chosen value at the top level', async () => {
      const onDirtyChange = vi.fn();
      const user = userEvent.setup();
      render(<ReviewGatesEditor onDirtyChange={onDirtyChange} />);
      await screen.findByDisplayValue('Code Review');
      expect(saveButton()).toBeDisabled();

      await user.selectOptions(limitSelect(), '5');
      expect(onDirtyChange).toHaveBeenLastCalledWith(true);
      expect(saveButton()).not.toBeDisabled();

      mockedFetchCatalog.mockResolvedValue({
        ...CATALOG_RESPONSE,
        tier_document: { ...CATALOG_RESPONSE.tier_document, max_iterations: 5 },
        merged_catalog: { ...CATALOG_RESPONSE.merged_catalog, max_iterations: 5 }
      });
      await user.click(saveButton());

      const doc = await waitFor(savedDocument);
      expect(doc.max_iterations).toBe(5);
      expect(doc.review_gates).toEqual({ qa_review: CATALOG_RESPONSE.tier_document.review_gates!.qa_review });
      await screen.findByText(i18n.t('settings.common.saveSuccess'));
      expect(limitSelect()).toHaveValue('5');
      expect(onDirtyChange).toHaveBeenLastCalledWith(false);
    });

    it('drops the top-level value when switched back to inherit', async () => {
      const withFour = {
        ...CATALOG_RESPONSE,
        tier_document: { ...CATALOG_RESPONSE.tier_document, max_iterations: 4 },
        merged_catalog: { ...CATALOG_RESPONSE.merged_catalog, max_iterations: 4 }
      };
      mockedFetchCatalog.mockResolvedValue(withFour);
      const onDirtyChange = vi.fn();
      const user = userEvent.setup();
      render(<ReviewGatesEditor onDirtyChange={onDirtyChange} />);
      await screen.findByDisplayValue('Code Review');

      await user.selectOptions(limitSelect(), '');
      expect(onDirtyChange).toHaveBeenLastCalledWith(true);

      // The first fetch inside save still sees the stored 4; after the save
      // the server reports it gone.
      mockedFetchCatalog.mockResolvedValueOnce(withFour).mockResolvedValue(CATALOG_RESPONSE);
      await user.click(saveButton());

      const doc = await waitFor(savedDocument);
      expect(doc.max_iterations ?? null).toBeNull();
      await screen.findByText(i18n.t('settings.common.saveSuccess'));
      expect(limitSelect()).toHaveValue('');
      expect(limitSelect().selectedOptions[0].textContent).toBe(inheritLabel(3));
      expect(onDirtyChange).toHaveBeenLastCalledWith(false);
    });

    // The server sends codes, never sentences: the note must show this
    // screen's own translated wording in the UI's language (DFLT-00140
    // accessibility review -- an English server string inside the Japanese
    // UI was read out with the Japanese voice).
    it.each(['ja', 'en'])('shows the legacy per-gate warning translated into %s, above the gate list', async lang => {
      const previous = i18n.language;
      await i18n.changeLanguage(lang);
      try {
        mockedFetchCatalog.mockResolvedValue({
          ...CATALOG_RESPONSE,
          warnings: [{ code: SETTINGS_CATALOG_WARNINGS.legacyGateMaxIterations, gate_id: 'code_review' }]
        });
        render(<ReviewGatesEditor onDirtyChange={vi.fn()} />);
        await screen.findByDisplayValue('Code Review');

        const note = screen.getByRole('note', { name: i18n.t('settings.reviewGates.warningsTitle') });
        const expected = i18n.t('settings.reviewGates.warningLegacyGateMaxIterations', { id: 'code_review' });
        expect(expected).toContain('code_review');
        expect(within(note).getByRole('listitem')).toHaveTextContent(expected);
        expect(note).not.toHaveTextContent('is no longer supported per gate');
        // Above the list: it precedes the first gate row in document order.
        const firstId = fieldsOf('settings.reviewGates.idLabel')[0];
        expect(note.compareDocumentPosition(firstId) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
      } finally {
        await i18n.changeLanguage(previous);
      }
    });

    it('shows nothing for a warning code it has no message for', async () => {
      mockedFetchCatalog.mockResolvedValue({
        ...CATALOG_RESPONSE,
        warnings: [{ code: 'SOMETHING_NEW' }, 'an old server sentence' as unknown as SettingsCatalogWarning]
      });
      render(<ReviewGatesEditor onDirtyChange={vi.fn()} />);
      await screen.findByDisplayValue('Code Review');
      expect(screen.queryByRole('note')).not.toBeInTheDocument();
      expect(screen.queryByText('an old server sentence')).not.toBeInTheDocument();
    });

    // A hand-edited out-of-range value (QA review of DFLT-00140): the select
    // must not pretend to be on "inherit" while holding 7.
    it('shows an out-of-range value as an invalid choice, explains it, and saves the value picked instead', async () => {
      const withSeven = {
        ...CATALOG_RESPONSE,
        tier_document: { ...CATALOG_RESPONSE.tier_document, max_iterations: 7 },
        merged_catalog: { ...CATALOG_RESPONSE.merged_catalog, max_iterations: 7 },
        warnings: [{ code: SETTINGS_CATALOG_WARNINGS.maxIterationsOutOfRange, value: 7 }]
      };
      mockedFetchCatalog.mockResolvedValue(withSeven);
      const onDirtyChange = vi.fn();
      const user = userEvent.setup();
      render(<ReviewGatesEditor onDirtyChange={onDirtyChange} />);
      await screen.findByDisplayValue('Code Review');

      const message = i18n.t('settings.reviewGates.warningMaxIterationsOutOfRange', { value: 7 });
      const note = screen.getByRole('note', { name: i18n.t('settings.reviewGates.warningsTitle') });
      expect(note).toHaveTextContent(message);

      const select = limitSelect();
      const invalidLabel = i18n.t('settings.reviewGates.workflowMaxIterationsInvalid', { value: 7 });
      expect(select).toHaveValue('7');
      expect(select.selectedOptions[0].textContent).toBe(invalidLabel);
      expect(Array.from(select.options).map(o => o.textContent)).toEqual([inheritLabel(3), '3', '4', '5', invalidLabel]);
      expect(select).toHaveAttribute('aria-invalid', 'true');
      expect(select).toHaveAccessibleDescription(`${i18n.t('settings.reviewGates.workflowMaxIterationsHelp')} ${message}`);

      await user.selectOptions(select, '4');
      expect(onDirtyChange).toHaveBeenLastCalledWith(true);
      expect(select).not.toHaveAttribute('aria-invalid');
      expect(screen.queryByRole('option', { name: invalidLabel })).not.toBeInTheDocument();

      mockedFetchCatalog.mockResolvedValueOnce(withSeven).mockResolvedValue({
        ...CATALOG_RESPONSE,
        tier_document: { ...CATALOG_RESPONSE.tier_document, max_iterations: 4 },
        merged_catalog: { ...CATALOG_RESPONSE.merged_catalog, max_iterations: 4 }
      });
      await user.click(saveButton());
      const doc = await waitFor(savedDocument);
      expect(doc.max_iterations).toBe(4);
      await screen.findByText(i18n.t('settings.common.saveSuccess'));
      expect(screen.queryByRole('note')).not.toBeInTheDocument();
    });

    it('shows no warning note when there are none', async () => {
      render(<ReviewGatesEditor onDirtyChange={vi.fn()} />);
      await screen.findByDisplayValue('Code Review');
      expect(screen.queryByRole('note')).not.toBeInTheDocument();
    });
  });
});

// DFLT-00165: the icon-only delete button rests at text-slate-500 /
// dark:text-slate-400 for WCAG 1.4.11's 3:1 on the gate row's white /
// slate-900 (4.76:1 / 6.96:1; the old text-slate-400 / dark:text-slate-500
// was 2.56:1 / 3.75:1). The red hover and the dimmed look of an unavailable
// button (a disabled control is exempt from 1.4.11) are kept. DFLT-00203: a
// default gate's button is aria-disabled rather than disabled, so its
// dimming is a plain opacity-40 (the disabled: variant no longer matches)
// and it gets no red hover.
describe('ReviewGatesEditor delete button contrast (WCAG 1.4.11)', () => {
  beforeEach(async () => {
    mockedFetchCatalog.mockReset();
    mockedFetchCatalog.mockResolvedValue(CATALOG_RESPONSE);
    await i18n.changeLanguage('ja');
  });

  it('rests the enabled and disabled delete buttons at slate-500 / dark:slate-400', async () => {
    render(<ReviewGatesEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('Code Review');

    const enabled = screen.getByRole('button', { name: i18n.t('settings.reviewGates.deleteGateAriaLabel', { name: 'qa_review' }) });
    const disabled = screen.getByRole('button', { name: i18n.t('settings.reviewGates.deleteGateAriaLabel', { name: 'code_review' }) });
    expect(enabled).toBeEnabled();
    expect(enabled).not.toHaveAttribute('aria-disabled');
    expect(disabled).toBeEnabled();
    expect(disabled).toHaveAttribute('aria-disabled', 'true');
    for (const b of [enabled, disabled]) {
      expect(b).toHaveClass('text-slate-500', 'dark:text-slate-400');
      expect(b).not.toHaveClass('text-slate-400');
      expect(b).not.toHaveClass('dark:text-slate-500');
    }
    expect(enabled).toHaveClass('hover:text-red-600', 'dark:hover:text-red-400');
    expect(enabled).not.toHaveClass('opacity-40');
    expect(disabled).toHaveClass('opacity-40');
    expect(disabled).not.toHaveClass('hover:text-red-600');
    expect(disabled).not.toHaveClass('dark:hover:text-red-400');
  });
});

// DFLT-00195: each row's icon-only delete button is named "<action>: <gate>"
// like the labels / node types / projects / tickets delete buttons, so rows
// can be told apart by a screen reader. The target is the gate ID, or the
// name on a new row that has no ID yet; with neither, the plain "delete" text
// is the name (never a name ending in an empty target). DFLT-00171: there is
// no title any more -- IconButton shows the tooltip ("delete", or why a
// default gate cannot be deleted) and always sets aria-label; only the
// "cannot delete" reason is a description, since "delete" is already part of
// the name.
describe('ReviewGatesEditor delete button accessible name', () => {
  beforeEach(async () => {
    mockedFetchCatalog.mockReset();
    mockedFetchCatalog.mockResolvedValue(WITH_CUSTOM_GATE);
    await i18n.changeLanguage('ja');
  });

  const deleteName = (name: string) => i18n.t('settings.reviewGates.deleteGateAriaLabel', { name });

  it.each(['ja', 'en'])('names each delete button after its gate in %s and describes only a default gate', async lang => {
    await i18n.changeLanguage(lang);
    render(<ReviewGatesEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('Code Review');

    const ids = ['code_review', 'qa_review', 'custom_review'];
    const buttons = ids.map(id => screen.getByRole('button', { name: deleteName(id) }));
    expect(new Set(buttons).size).toBe(ids.length);
    expect(new Set(ids.map(deleteName)).size).toBe(ids.length);
    expect(deleteName('code_review')).toBe(
      lang === 'ja' ? 'ゲートを削除: code_review' : 'Delete review gate: code_review'
    );

    const [defaultGate, overridden, custom] = buttons;
    expect(defaultGate).toHaveAttribute('aria-disabled', 'true');
    expect(defaultGate).not.toHaveAttribute('title');
    expect(defaultGate).toHaveAccessibleDescription(i18n.t('settings.reviewGates.cannotDeleteDefaultHint'));
    for (const b of [overridden, custom]) {
      expect(b).toBeEnabled();
      expect(b).not.toHaveAttribute('title');
      expect(b).toHaveAccessibleDescription('');
    }
  });

  it('shows the delete tooltip on keyboard focus, and why a default gate cannot be deleted on hover', async () => {
    const user = userEvent.setup();
    render(<ReviewGatesEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('Code Review');

    const custom = screen.getByRole('button', { name: deleteName('custom_review') });
    act(() => custom.focus());
    expect(openIconButtonTooltip()).toHaveTextContent(new RegExp(`^${i18n.t('settings.reviewGates.deleteGate')}$`));
    act(() => custom.blur());

    const defaultGate = screen.getByRole('button', { name: deleteName('code_review') });
    await user.hover(defaultGate.parentElement as HTMLElement);
    expect(openIconButtonTooltip()).toHaveTextContent(i18n.t('settings.reviewGates.cannotDeleteDefaultHint'));
  });

  // DFLT-00203: a default gate's delete button is aria-disabled rather than
  // disabled, so Tab reaches it and keyboard focus shows the same reason.
  it('shows why a default gate cannot be deleted on keyboard focus', async () => {
    const user = userEvent.setup();
    render(<ReviewGatesEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('Code Review');

    const defaultGate = screen.getByRole('button', { name: deleteName('code_review') });
    // Tab to it from the row's "enabled" checkbox, the control just before it.
    const enabledBox = within(defaultGate.closest('div')!).getByRole('checkbox');
    act(() => enabledBox.focus());
    await user.tab();
    expect(defaultGate).toHaveFocus();
    expect(openIconButtonTooltip()).toHaveTextContent(i18n.t('settings.reviewGates.cannotDeleteDefaultHint'));
    expect(defaultGate).toHaveAccessibleDescription(i18n.t('settings.reviewGates.cannotDeleteDefaultHint'));
  });

  // DFLT-00203: neither a click nor Enter nor Space removes a default gate.
  it('keeps a default gate when its delete button is clicked or activated from the keyboard', async () => {
    const onDirtyChange = vi.fn();
    const user = userEvent.setup();
    render(<ReviewGatesEditor onDirtyChange={onDirtyChange} />);
    await screen.findByDisplayValue('Code Review');
    const idCount = () => screen.getAllByLabelText(i18n.t('settings.reviewGates.idLabel')).length;
    const before = idCount();

    const defaultGate = screen.getByRole('button', { name: deleteName('code_review') });
    await user.click(defaultGate);
    expect(defaultGate).toHaveFocus();
    await user.keyboard('{Enter}');
    await user.keyboard(' ');

    expect(idCount()).toBe(before);
    expect(screen.getByDisplayValue('Code Review')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: deleteName('code_review') })).toBe(defaultGate);
    expect(onDirtyChange).not.toHaveBeenCalledWith(true);
  });

  it.each(['ja', 'en'])('falls back from the ID to the name, then to the plain delete text, on a new row in %s', async lang => {
    await i18n.changeLanguage(lang);
    const user = userEvent.setup();
    render(<ReviewGatesEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('Code Review');
    await user.click(screen.getByRole('button', { name: i18n.t('settings.reviewGates.addGate') }));

    const plain = i18n.t('settings.reviewGates.deleteGate');
    const newRowButton = () => screen.getByRole('button', { name: plain });
    const button = newRowButton();
    expect(button).toHaveAttribute('aria-label', plain);
    expect(button).not.toHaveAttribute('title');
    expect(button).toHaveAccessibleDescription('');
    expect(button).toHaveAccessibleName(plain);
    expect(button).not.toHaveAccessibleName(/:\s*$/);

    const lastField = (label: string) => {
      const fields = screen.getAllByLabelText(i18n.t(label));
      return fields[fields.length - 1];
    };
    // Whitespace alone is not a target.
    await user.type(lastField('settings.reviewGates.nameLabel'), '   ');
    expect(newRowButton()).toHaveAttribute('aria-label', plain);

    await user.clear(lastField('settings.reviewGates.nameLabel'));
    await user.type(lastField('settings.reviewGates.nameLabel'), ' New Gate ');
    const byName = screen.getByRole('button', { name: deleteName('New Gate') });
    expect(byName).toBe(button);
    expect(byName).toHaveAccessibleDescription('');

    await user.type(lastField('settings.reviewGates.idLabel'), 'new_gate');
    expect(screen.getByRole('button', { name: deleteName('new_gate') })).toBe(button);
    expect(screen.queryByRole('button', { name: deleteName('New Gate') })).not.toBeInTheDocument();
  });
});
