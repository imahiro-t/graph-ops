// Covers the settings modal's tab list after DFLT-00071: a single
// テンプレート tab replaces the old standalone レポートテンプレート tab, and an
// unsaved edit inside it is still protected by the modal's own tab/scope
// switch confirmation.
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { SettingsModal } from './SettingsModal';

vi.mock('../lib/settingsApi', async () => {
  const actual = await vi.importActual<typeof import('../lib/settingsApi')>('../lib/settingsApi');
  return {
    ...actual,
    fetchSettingsNodeTypes: vi.fn(),
    fetchSettingsNodeType: vi.fn(),
    fetchSettingsSkills: vi.fn(),
    fetchSettingsSkill: vi.fn(),
    fetchSettingsPlanTemplate: vi.fn(),
    fetchSettingsReviewTemplate: vi.fn(),
    fetchSettingsReportTemplate: vi.fn()
  };
});

import {
  fetchSettingsNodeType,
  fetchSettingsNodeTypes,
  fetchSettingsPlanTemplate,
  fetchSettingsReportTemplate,
  fetchSettingsReviewTemplate,
  fetchSettingsSkill,
  fetchSettingsSkills
} from '../lib/settingsApi';

type Mock = ReturnType<typeof vi.fn>;

function renderModal() {
  return render(
    <SettingsModal
      isOpen
      onClose={vi.fn()}
      projects={[]}
      currentProject={null}
      onProjectsChanged={vi.fn()}
      onPaginationPageSizeChanged={vi.fn()}
      onMyNameChanged={vi.fn()}
    />
  );
}

describe('SettingsModal', () => {
  beforeEach(() => {
    (fetchSettingsNodeTypes as unknown as Mock).mockResolvedValue([]);
    (fetchSettingsNodeType as unknown as Mock).mockResolvedValue({ type: '', tier_text: '', merged_text: '' });
    (fetchSettingsSkills as unknown as Mock).mockResolvedValue([]);
    (fetchSettingsSkill as unknown as Mock).mockResolvedValue({ name: '', tier_text: '', merged_text: '' });
    (fetchSettingsPlanTemplate as unknown as Mock).mockResolvedValue({ tier_text: 'plan-tier', merged_text: 'plan-merged' });
    (fetchSettingsReviewTemplate as unknown as Mock).mockResolvedValue({ tier_text: 'review-tier', merged_text: 'review-merged' });
    (fetchSettingsReportTemplate as unknown as Mock).mockResolvedValue({ tier_text: '', merged_text: '' });
  });

  afterEach(async () => {
    vi.restoreAllMocks();
    await i18n.changeLanguage('ja');
  });

  it('has a Templates tab and no standalone Report Template tab', async () => {
    renderModal();

    expect(screen.getByRole('button', { name: 'テンプレート' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'レポートテンプレート' })).not.toBeInTheDocument();
    for (const key of ['nodeTypes', 'reviewGates', 'skills', 'appSettings']) {
      expect(screen.getByRole('button', { name: i18n.t(`settings.tabs.${key}`) })).toBeInTheDocument();
    }
    await waitFor(() => expect(fetchSettingsNodeTypes).toHaveBeenCalled());
  });

  it('opens the template list with the plan template selected', async () => {
    const user = userEvent.setup();
    renderModal();

    await user.click(screen.getByRole('button', { name: 'テンプレート' }));

    const plan = screen.getByRole('button', { name: '実行計画' });
    expect(plan).toHaveAttribute('aria-current', 'true');
    expect(screen.getByRole('button', { name: 'レビュー' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'レポート' })).toBeInTheDocument();
    await waitFor(() => expect(fetchSettingsPlanTemplate).toHaveBeenCalledWith(expect.anything(), 'global', ''));
    expect(await screen.findByDisplayValue('plan-tier')).toBeInTheDocument();
  });

  it('shows English tab and list names when the UI language is English', async () => {
    const user = userEvent.setup();
    await act(async () => {
      await i18n.changeLanguage('en');
    });
    renderModal();

    expect(screen.queryByRole('button', { name: 'Report Template' })).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Templates' }));

    expect(screen.getByRole('button', { name: 'Execution Plan' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Review' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Report' })).toBeInTheDocument();
    await screen.findByDisplayValue('plan-tier');
  });

  it('asks before leaving the Templates tab or switching scope with an unsaved edit', async () => {
    const user = userEvent.setup();
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false);
    renderModal();

    await user.click(screen.getByRole('button', { name: 'テンプレート' }));
    await user.click(screen.getByRole('button', { name: 'レビュー' }));
    const textarea = await screen.findByDisplayValue('review-tier');
    await user.clear(textarea);
    await user.type(textarea, '# 編集途中');

    await user.click(screen.getByRole('button', { name: i18n.t('settings.tabs.nodeTypes') }));
    expect(confirm).toHaveBeenCalledTimes(1);
    expect(screen.getByRole('button', { name: 'レビュー' })).toHaveAttribute('aria-current', 'true');
    expect(textarea).toHaveValue('# 編集途中');

    await user.click(screen.getByRole('button', { name: i18n.t('settings.scope.project') }));
    expect(confirm).toHaveBeenCalledTimes(2);
  });

  it('does not ask when switching to another tab after discarding a template edit', async () => {
    const user = userEvent.setup();
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true);
    renderModal();

    await user.click(screen.getByRole('button', { name: 'テンプレート' }));
    const textarea = await screen.findByDisplayValue('plan-tier');
    await user.type(textarea, ' edited');
    await user.click(screen.getByRole('button', { name: 'レビュー' }));
    expect(confirm).toHaveBeenCalledTimes(1);
    await screen.findByDisplayValue('review-tier');

    await user.click(screen.getByRole('button', { name: 'レポート' }));
    await user.click(screen.getByRole('button', { name: i18n.t('settings.tabs.skills') }));
    expect(confirm).toHaveBeenCalledTimes(1);
  });
});
