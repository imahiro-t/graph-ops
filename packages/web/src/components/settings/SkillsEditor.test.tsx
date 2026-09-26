// Covers the #7 dependency-array fix (loadSkills must not re-run just
// because `selected` changed) and F-1's structural non-regression. See this
// ticket's plan sections 3-2 (#7/#8) and 4-2.
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../../i18n';
import { SkillsEditor } from './SkillsEditor';
import { SettingsSkillInfo } from '../../types';

vi.mock('../../lib/settingsApi', async () => {
  const actual = await vi.importActual<typeof import('../../lib/settingsApi')>('../../lib/settingsApi');
  return {
    ...actual,
    fetchSettingsSkills: vi.fn(),
    fetchSettingsSkill: vi.fn(),
    saveSettingsSkill: vi.fn()
  };
});

import { fetchSettingsSkill, fetchSettingsSkills } from '../../lib/settingsApi';

const mockedFetchSkills = fetchSettingsSkills as unknown as ReturnType<typeof vi.fn>;
const mockedFetchSkill = fetchSettingsSkill as unknown as ReturnType<typeof vi.fn>;

const SKILLS: SettingsSkillInfo[] = [
  { name: 'create-ticket', has_user_override: false },
  { name: 'refine-ticket', has_user_override: false }
];

function stubFetchSkill() {
  mockedFetchSkill.mockImplementation(async (_t, name: string) => ({
    name,
    tier_text: `${name}-tier-text`,
    merged_text: `${name}-merged-text`
  }));
}

describe('SkillsEditor', () => {
  beforeEach(() => {
    mockedFetchSkills.mockReset();
    mockedFetchSkill.mockReset();
    mockedFetchSkills.mockResolvedValue(SKILLS);
    stubFetchSkill();
  });

  afterEach(async () => {
    await i18n.changeLanguage('ja');
  });

  it('selects the first skill on initial mount and fetches its detail', async () => {
    render(<SkillsEditor onDirtyChange={vi.fn()} />);

    await waitFor(() => expect(mockedFetchSkill).toHaveBeenCalledWith(expect.anything(), 'create-ticket'));
    expect(await screen.findByDisplayValue('create-ticket-tier-text')).toBeInTheDocument();
  });

  // DFLT-00162: the hint is secondary text on the white / slate-900 pane, so
  // it uses text-slate-500 / dark:text-slate-400 (4.76:1 / 6.96:1) to meet
  // WCAG 1.4.3's 4.5:1. jsdom computes no colors, so the classes are pinned.
  it('draws the empty-override hint in slate-500 / dark:slate-400', async () => {
    render(<SkillsEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('create-ticket-tier-text');

    const hint = screen.getByText(i18n.t('settings.skills.emptyOverrideHint'));
    expect(hint).toHaveClass('text-slate-500', 'dark:text-slate-400', 'text-[10px]');
    expect(hint).not.toHaveClass('text-slate-400');
    expect(hint).not.toHaveClass('dark:text-slate-500');
  });

  // DFLT-00074
  it('labels the tier text textarea', async () => {
    render(<SkillsEditor onDirtyChange={vi.fn()} />);

    const textarea = await screen.findByDisplayValue('create-ticket-tier-text');
    expect(screen.getByLabelText(i18n.t('settings.skills.tierTextLabel'))).toBe(textarea);
  });

  it('#7 non-regression: changing the selection does not re-fetch the skill list', async () => {
    const user = userEvent.setup();
    render(<SkillsEditor onDirtyChange={vi.fn()} />);

    await screen.findByDisplayValue('create-ticket-tier-text');
    expect(mockedFetchSkills).toHaveBeenCalledTimes(1);

    const refineButton = screen.getByRole('button', { name: new RegExp(i18n.t('settings.skills.names.refineTicket')) });
    await user.click(refineButton);

    await screen.findByDisplayValue('refine-ticket-tier-text');
    expect(mockedFetchSkills).toHaveBeenCalledTimes(1);
    expect(mockedFetchSkill).toHaveBeenCalledWith(expect.anything(), 'refine-ticket');
  });

  it('F-1 non-regression: switching language after editing does not re-fetch and does not discard the unsaved edit', async () => {
    const user = userEvent.setup();
    render(<SkillsEditor onDirtyChange={vi.fn()} />);

    const textarea = await screen.findByDisplayValue('create-ticket-tier-text');
    await user.clear(textarea);
    await user.type(textarea, 'unsaved edit');

    expect(mockedFetchSkills).toHaveBeenCalledTimes(1);
    expect(mockedFetchSkill).toHaveBeenCalledTimes(1);

    await i18n.changeLanguage('en');
    await waitFor(() => expect(screen.getByDisplayValue('unsaved edit')).toBeInTheDocument());

    expect(mockedFetchSkills).toHaveBeenCalledTimes(1);
    expect(mockedFetchSkill).toHaveBeenCalledTimes(1);
  });

  // DFLT-00137: switching the selected skill with unsaved edits asks first --
  // through the in-app ConfirmDialog since DFLT-00148.
  describe('switching the selection with unsaved edits', () => {
    const dialog = () => screen.queryByTestId('skill-discard-confirm');

    const refineButton = () => screen.getByRole('button', { name: i18n.t('settings.skills.names.refineTicket') });
    const createButton = () => screen.getByRole('button', { name: i18n.t('settings.skills.names.createTicket') });

    it.each([
      ['the cancel button', (user: ReturnType<typeof userEvent.setup>) => user.click(screen.getByTestId('skill-discard-confirm-cancel'))],
      ['Escape', (user: ReturnType<typeof userEvent.setup>) => user.keyboard('{Escape}')],
      ['a click on the overlay', (user: ReturnType<typeof userEvent.setup>) => user.click(screen.getByTestId('skill-discard-confirm-overlay'))]
    ])('keeps the selection and the edit when the user dismisses the dialog with %s', async (_how, dismiss) => {
      const user = userEvent.setup();
      render(<SkillsEditor onDirtyChange={vi.fn()} />);
      const textarea = await screen.findByDisplayValue('create-ticket-tier-text');
      await user.clear(textarea);
      await user.type(textarea, 'unsaved edit');

      await user.click(refineButton());

      const opened = screen.getByRole('alertdialog', { name: i18n.t('settings.unsavedChanges.confirmTitle') });
      expect(opened).toHaveAccessibleDescription(i18n.t('settings.unsavedChanges.confirmMessage'));
      await dismiss(user);

      expect(dialog()).not.toBeInTheDocument();
      expect(mockedFetchSkill).not.toHaveBeenCalledWith(expect.anything(), 'refine-ticket');
      expect(screen.getByDisplayValue('unsaved edit')).toBeInTheDocument();
      expect(refineButton()).toHaveFocus();
    });

    it('switches and clears the relayed dirty flag when the user confirms', async () => {
      const onDirtyChange = vi.fn();
      const user = userEvent.setup();
      render(<SkillsEditor onDirtyChange={onDirtyChange} />);
      const textarea = await screen.findByDisplayValue('create-ticket-tier-text');
      await user.clear(textarea);
      await user.type(textarea, 'unsaved edit');
      expect(onDirtyChange).toHaveBeenLastCalledWith(true);

      await user.click(refineButton());
      await user.click(screen.getByTestId('skill-discard-confirm-confirm'));

      expect(dialog()).not.toBeInTheDocument();
      expect(await screen.findByDisplayValue('refine-ticket-tier-text')).toBeInTheDocument();
      expect(onDirtyChange).toHaveBeenLastCalledWith(false);
      const values = onDirtyChange.mock.calls.map(c => c[0]);
      expect(values.lastIndexOf(false)).toBeGreaterThan(values.lastIndexOf(true));
    });

    it('switches without asking when nothing is unsaved, and re-picking the selected skill does nothing', async () => {
      const user = userEvent.setup();
      render(<SkillsEditor onDirtyChange={vi.fn()} />);
      await screen.findByDisplayValue('create-ticket-tier-text');

      await user.click(createButton());
      expect(mockedFetchSkill).toHaveBeenCalledTimes(1);

      await user.click(refineButton());
      const textarea = await screen.findByDisplayValue('refine-ticket-tier-text');

      await user.type(textarea, ' more');
      await user.click(refineButton());

      expect(dialog()).not.toBeInTheDocument();
      expect(screen.getByDisplayValue('refine-ticket-tier-text more')).toBeInTheDocument();
    });
  });

  // DFLT-00142: the three autopilot skills are listed with translated names.
  it.each(['ja', 'en'])('lists the autopilot skills with translated names (%s)', async (lang) => {
    await i18n.changeLanguage(lang);
    mockedFetchSkills.mockResolvedValue([
      { name: 'process-ticket', has_user_override: false },
      { name: 'autopilot-ticket', has_user_override: false },
      { name: 'autopilot-tree', has_user_override: false },
      { name: 'autopilot-worker', has_user_override: false }
    ]);
    render(<SkillsEditor onDirtyChange={vi.fn()} />);

    for (const key of ['autopilotTicket', 'autopilotTree', 'autopilotWorker']) {
      const label = i18n.t(`settings.skills.names.${key}`);
      expect(label).not.toBe(`settings.skills.names.${key}`);
      expect(await screen.findByRole('button', { name: label })).toBeInTheDocument();
    }
  });
});

// DFLT-00212: the merged preview's heading was a <label> with no form control
// to label, and the scrolling <pre> could not be reached by keyboard. The
// heading is now a paragraph that names the <pre> as a focusable region (the
// same structure as TemplateTextEditor / ReviewGatesEditor). Only the selected
// skill's preview is ever shown, so the heading alone keeps the name unique.
describe('SkillsEditor merged preview region', () => {
  beforeEach(() => {
    mockedFetchSkills.mockReset();
    mockedFetchSkill.mockReset();
    mockedFetchSkills.mockResolvedValue(SKILLS);
    stubFetchSkill();
  });

  afterEach(async () => {
    await i18n.changeLanguage('ja');
  });

  const region = () => screen.getByRole('region', { name: i18n.t('settings.skills.mergedPreviewLabel') });

  it('exposes the preview as a region named by a paragraph heading, holding the merged text', async () => {
    render(<SkillsEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('create-ticket-tier-text');

    const preview = region();
    expect(preview.tagName).toBe('PRE');
    expect(preview).toHaveTextContent('create-ticket-merged-text');

    // useId values contain colons, so resolve the reference with getElementById.
    const heading = document.getElementById(preview.getAttribute('aria-labelledby') ?? '');
    expect(heading).not.toBeNull();
    expect(heading!.tagName).toBe('P');
    expect(heading).toHaveTextContent(i18n.t('settings.skills.mergedPreviewLabel'));
    expect(preview.parentElement!.querySelector('label')).toBeNull();
  });

  it('shows the empty-merged hint inside the region when the merged text is empty', async () => {
    mockedFetchSkill.mockImplementation(async (_t, name: string) => ({ name, tier_text: `${name}-tier-text`, merged_text: '' }));
    render(<SkillsEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('create-ticket-tier-text');

    expect(region()).toHaveTextContent(i18n.t('settings.skills.emptyMergedHint'));
  });

  it('is keyboard focusable and draws a focus ring', async () => {
    const user = userEvent.setup();
    render(<SkillsEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('create-ticket-tier-text');

    const preview = region();
    expect(preview).toHaveAttribute('tabindex', '0');
    // jsdom computes no styles, so the focus ring classes are pinned.
    expect(preview).toHaveClass('focus:outline-none', 'focus-visible:ring-2', 'focus-visible:ring-blue-500', 'max-h-40');

    // The preview sits right before the tier text textarea in tab order.
    const textarea = screen.getByLabelText(i18n.t('settings.skills.tierTextLabel'));
    textarea.focus();
    await user.tab({ shift: true });
    expect(preview).toHaveFocus();
  });

  it('stays a single region whose content follows the selection', async () => {
    const user = userEvent.setup();
    render(<SkillsEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('create-ticket-tier-text');

    await user.click(screen.getByRole('button', { name: new RegExp(i18n.t('settings.skills.names.refineTicket')) }));
    await screen.findByDisplayValue('refine-ticket-tier-text');

    expect(screen.getAllByRole('region', { name: i18n.t('settings.skills.mergedPreviewLabel') })).toHaveLength(1);
    expect(region()).toHaveTextContent('refine-ticket-merged-text');
  });

  it('is named by the English heading after switching the language', async () => {
    render(<SkillsEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('create-ticket-tier-text');

    await act(async () => {
      await i18n.changeLanguage('en');
    });

    expect(region()).toHaveTextContent('create-ticket-merged-text');
  });
});
