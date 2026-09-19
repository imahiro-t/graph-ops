// Covers the settings modal's tab list after DFLT-00071: a single
// テンプレート tab replaces the old standalone レポートテンプレート tab, and an
// unsaved edit inside it is still protected by the modal's own tab/scope
// switch confirmation.
import { useState } from 'react';
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { getFocusableElements } from '../hooks/useModalDialog';
import { SettingsModal } from './SettingsModal';
import { Project } from '../types';

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

function renderModal(onClose: () => void = vi.fn()) {
  return render(
    <SettingsModal
      isOpen
      onClose={onClose}
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

  // DFLT-00074: dialog semantics, focus management and Escape handling.
  describe('as a modal dialog', () => {
    const closeButton = () => screen.getByRole('button', { name: i18n.t('common.closeDialog') });

    // Makes the plan template dirty the same way the tests above do.
    const makeTemplateEditDirty = async (user: ReturnType<typeof userEvent.setup>) => {
      await user.click(screen.getByRole('button', { name: 'テンプレート' }));
      const textarea = await screen.findByDisplayValue('plan-tier');
      await user.type(textarea, ' edited');
      return textarea;
    };

    it('is a dialog named by its title with a labelled close button that gets the initial focus', async () => {
      renderModal();

      const dialog = screen.getByRole('dialog', { name: i18n.t('settings.modalTitle') });
      expect(dialog).toHaveAttribute('aria-modal', 'true');
      expect(closeButton()).toHaveFocus();
      await waitFor(() => expect(fetchSettingsNodeTypes).toHaveBeenCalled());
    });

    it('wraps Tab and Shift+Tab inside the dialog', async () => {
      const user = userEvent.setup();
      renderModal();
      await waitFor(() => expect(fetchSettingsNodeTypes).toHaveBeenCalled());

      const focusable = getFocusableElements(screen.getByRole('dialog'));
      const last = focusable[focusable.length - 1];
      expect(focusable[0]).toBe(closeButton());

      await user.tab({ shift: true });
      expect(last).toHaveFocus();
      await user.tab();
      expect(closeButton()).toHaveFocus();
    });

    it('closes on Escape without asking when nothing is unsaved', async () => {
      const user = userEvent.setup();
      const confirm = vi.spyOn(window, 'confirm');
      const onClose = vi.fn();
      renderModal(onClose);
      await waitFor(() => expect(fetchSettingsNodeTypes).toHaveBeenCalled());

      await user.keyboard('{Escape}');
      expect(confirm).not.toHaveBeenCalled();
      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it('asks before closing on Escape with an unsaved edit, and stays open with the edit when cancelled', async () => {
      const user = userEvent.setup();
      const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false);
      const onClose = vi.fn();
      renderModal(onClose);

      const textarea = await makeTemplateEditDirty(user);
      await user.keyboard('{Escape}');

      expect(confirm).toHaveBeenCalledTimes(1);
      expect(confirm).toHaveBeenCalledWith(i18n.t('settings.unsavedChanges.confirmMessage'));
      expect(onClose).not.toHaveBeenCalled();
      expect(screen.getByRole('dialog')).toBeInTheDocument();
      expect(textarea).toHaveValue('plan-tier edited');
    });

    it('closes on Escape with an unsaved edit once the discard is confirmed', async () => {
      const user = userEvent.setup();
      const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true);
      const onClose = vi.fn();
      renderModal(onClose);

      await makeTemplateEditDirty(user);
      await user.keyboard('{Escape}');

      expect(confirm).toHaveBeenCalledTimes(1);
      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it('lets Escape in the "add node type" input cancel only the add, and closes on the next Escape', async () => {
      const user = userEvent.setup();
      const confirm = vi.spyOn(window, 'confirm');
      const onClose = vi.fn();
      renderModal(onClose);

      await user.click(await screen.findByRole('button', { name: i18n.t('settings.nodeTypes.addType') }));
      const input = screen.getByLabelText(i18n.t('settings.nodeTypes.newTypeLabel'));
      expect(input).toHaveFocus();

      await user.keyboard('{Escape}');
      expect(screen.queryByLabelText(i18n.t('settings.nodeTypes.newTypeLabel'))).not.toBeInTheDocument();
      expect(screen.getByRole('button', { name: i18n.t('settings.nodeTypes.addType') })).toBeInTheDocument();
      expect(onClose).not.toHaveBeenCalled();
      expect(confirm).not.toHaveBeenCalled();

      await user.keyboard('{Escape}');
      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it('returns focus to the button that opened it when closed', async () => {
      const user = userEvent.setup();
      const Harness = () => {
        const [isOpen, setIsOpen] = useState(false);
        return (
          <>
            <button type="button" onClick={() => setIsOpen(true)}>
              open-settings
            </button>
            <SettingsModal
              isOpen={isOpen}
              onClose={() => setIsOpen(false)}
              projects={[]}
              currentProject={null}
              onProjectsChanged={vi.fn()}
              onPaginationPageSizeChanged={vi.fn()}
              onMyNameChanged={vi.fn()}
            />
          </>
        );
      };
      render(<Harness />);

      await user.click(screen.getByRole('button', { name: 'open-settings' }));
      expect(closeButton()).toHaveFocus();
      await waitFor(() => expect(fetchSettingsNodeTypes).toHaveBeenCalled());

      await user.keyboard('{Escape}');
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'open-settings' })).toHaveFocus();
    });

    it('labels the project select shown for the project scope', async () => {
      const user = userEvent.setup();
      renderModal();

      await user.click(screen.getByRole('button', { name: i18n.t('settings.scope.project') }));
      const select = screen.getByLabelText(i18n.t('settings.scope.projectLabel'));
      expect(select.tagName).toBe('SELECT');
    });
  });

  // DFLT-00077: App.tsx keeps the modal mounted and resolves its current
  // project asynchronously, so the modal must pick up the header's project
  // each time it opens rather than only on its first mount.
  describe('project selection carried over from the header', () => {
    const alpha: Project = { id: 'p-alpha', name: 'Alpha', prefix: 'ALPHA', local_path: '/work/alpha', created_at: '', updated_at: '' };
    const beta: Project = { id: 'p-beta', name: 'Beta', prefix: 'BETA', local_path: '/work/beta', created_at: '', updated_at: '' };

    const modal = (isOpen: boolean, currentProject: Project | null) => (
      <SettingsModal
        isOpen={isOpen}
        onClose={vi.fn()}
        projects={[alpha, beta]}
        currentProject={currentProject}
        onProjectsChanged={vi.fn()}
        onPaginationPageSizeChanged={vi.fn()}
        onMyNameChanged={vi.fn()}
      />
    );
    const projectSelect = () => screen.getByLabelText(i18n.t('settings.scope.projectLabel'));
    const switchToProjectScope = (user: ReturnType<typeof userEvent.setup>) =>
      user.click(screen.getByRole('button', { name: i18n.t('settings.scope.project') }));

    it('selects the header project when opened after being mounted closed with no project', async () => {
      const user = userEvent.setup();
      const { rerender } = render(modal(false, null));
      rerender(modal(false, alpha));
      rerender(modal(true, alpha));
      await waitFor(() => expect(fetchSettingsNodeTypes).toHaveBeenCalled());
      (fetchSettingsNodeTypes as unknown as Mock).mockClear();

      await switchToProjectScope(user);

      expect(projectSelect()).toHaveValue(alpha.id);
      expect(screen.queryByText(i18n.t('settings.scope.noProjectSelected'))).not.toBeInTheDocument();
      await waitFor(() => expect(fetchSettingsNodeTypes).toHaveBeenCalledWith(expect.anything(), alpha.id));
      expect(fetchSettingsNodeTypes).not.toHaveBeenCalledWith(expect.anything(), '');
    });

    it('goes back to the header project on reopen after another project was picked inside the modal', async () => {
      const user = userEvent.setup();
      const { rerender } = render(modal(true, alpha));

      await switchToProjectScope(user);
      await user.selectOptions(projectSelect(), beta.id);
      expect(projectSelect()).toHaveValue(beta.id);

      rerender(modal(false, alpha));
      rerender(modal(true, alpha));

      expect(projectSelect()).toHaveValue(alpha.id);
      await waitFor(() => expect(fetchSettingsNodeTypes).toHaveBeenLastCalledWith(expect.anything(), alpha.id));
    });

    it('follows a header project change made while the modal was closed', async () => {
      const user = userEvent.setup();
      const { rerender } = render(modal(true, alpha));
      await switchToProjectScope(user);
      expect(projectSelect()).toHaveValue(alpha.id);

      rerender(modal(false, alpha));
      rerender(modal(false, beta));
      rerender(modal(true, beta));

      expect(projectSelect()).toHaveValue(beta.id);
      await waitFor(() => expect(fetchSettingsNodeTypes).toHaveBeenLastCalledWith(expect.anything(), beta.id));
    });

    it('still warns when no project is selected in the header', async () => {
      const user = userEvent.setup();
      const { rerender } = render(modal(false, null));
      rerender(modal(true, null));

      await switchToProjectScope(user);

      expect(projectSelect()).toHaveValue('');
      expect(screen.getByText(i18n.t('settings.scope.noProjectSelected'))).toBeInTheDocument();
    });
  });
});
