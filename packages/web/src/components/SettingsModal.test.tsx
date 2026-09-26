// Covers the settings modal's tab list after DFLT-00071: a single
// テンプレート tab replaces the old standalone レポートテンプレート tab, and an
// unsaved edit inside it is still protected by the modal's own tab-switch
// confirmation.
//
// There is no scope switcher to protect any more (DFLT-00124): every tab
// edits the one user tier, and the labels tab -- the only per-project one
// left -- carries its own project selector.
import { useState } from 'react';
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { getFocusableElements } from '../hooks/useModalDialog';
import { SettingsModal } from './SettingsModal';
import { openIconButtonTooltip, openIconButtonTooltips } from '../test/iconButtonTooltip';
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
    await waitFor(() => expect(fetchSettingsPlanTemplate).toHaveBeenCalledWith(expect.anything()));
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

  // Since DFLT-00148 the question is the in-app ConfirmDialog, opened on top
  // of this modal.
  it('asks before leaving the Templates tab with an unsaved edit', async () => {
    const user = userEvent.setup();
    renderModal();

    await user.click(screen.getByRole('button', { name: 'テンプレート' }));
    await user.click(screen.getByRole('button', { name: 'レビュー' }));
    const textarea = await screen.findByDisplayValue('review-tier');
    await user.clear(textarea);
    await user.type(textarea, '# 編集途中');

    const nodeTypesTab = screen.getByRole('button', { name: i18n.t('settings.tabs.nodeTypes') });
    await user.click(nodeTypesTab);
    const dialog = screen.getByRole('alertdialog', { name: i18n.t('settings.unsavedChanges.confirmTitle') });
    expect(dialog).toHaveAttribute('data-testid', 'settings-discard-confirm');
    expect(dialog).toHaveAccessibleDescription(i18n.t('settings.unsavedChanges.confirmMessage'));
    expect(screen.getByTestId('settings-discard-confirm-confirm')).toHaveTextContent(i18n.t('settings.unsavedChanges.discardButton'));

    await user.click(screen.getByTestId('settings-discard-confirm-cancel'));
    expect(screen.queryByTestId('settings-discard-confirm')).not.toBeInTheDocument();
    expect(nodeTypesTab).toHaveFocus();
    expect(screen.getByRole('button', { name: 'レビュー' })).toHaveAttribute('aria-current', 'true');
    expect(textarea).toHaveValue('# 編集途中');
  });

  it('switches the tab once the discard is confirmed', async () => {
    const user = userEvent.setup();
    renderModal();

    await user.click(screen.getByRole('button', { name: 'テンプレート' }));
    const textarea = await screen.findByDisplayValue('plan-tier');
    await user.type(textarea, ' edited');

    await user.click(screen.getByRole('button', { name: i18n.t('settings.tabs.skills') }));
    await user.click(screen.getByTestId('settings-discard-confirm-confirm'));

    expect(screen.queryByTestId('settings-discard-confirm')).not.toBeInTheDocument();
    expect(screen.queryByDisplayValue('plan-tier edited')).not.toBeInTheDocument();
    await waitFor(() => expect(fetchSettingsSkills).toHaveBeenCalled());
    expect(screen.getByRole('dialog', { name: i18n.t('settings.modalTitle') })).toBeInTheDocument();
  });

  // DFLT-00124, completion criterion 8: the "全体設定 / プロジェクト単位設定"
  // switcher and the project selector that belonged to it are gone, along
  // with the two notices that only appeared under the project scope.
  it('has no scope switcher and no scope-level project selector', async () => {
    renderModal();

    for (const key of [
      'settings.scope.label',
      'settings.scope.global',
      'settings.scope.project',
      'settings.scope.projectLabel',
      'settings.scope.noProjectSelected',
      'settings.scope.localPathNotSet'
    ]) {
      // i18n.t returns the key itself once the key is gone, so this asserts
      // both that nothing renders the string and that the key is unused.
      expect(i18n.t(key)).toBe(key);
      expect(screen.queryByText(key)).not.toBeInTheDocument();
    }
  });

  it('does not ask when switching to another tab after discarding a template edit', async () => {
    const user = userEvent.setup();
    renderModal();

    await user.click(screen.getByRole('button', { name: 'テンプレート' }));
    const textarea = await screen.findByDisplayValue('plan-tier');
    await user.type(textarea, ' edited');
    await user.click(screen.getByRole('button', { name: 'レビュー' }));
    await user.click(screen.getByTestId('template-discard-confirm-confirm'));
    await screen.findByDisplayValue('review-tier');

    await user.click(screen.getByRole('button', { name: 'レポート' }));
    await user.click(screen.getByRole('button', { name: i18n.t('settings.tabs.skills') }));
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
    await waitFor(() => expect(fetchSettingsSkills).toHaveBeenCalled());
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

    const discardDialog = () => screen.queryByTestId('settings-discard-confirm');

    it('closes on Escape without asking when nothing is unsaved', async () => {
      const user = userEvent.setup();
      const onClose = vi.fn();
      renderModal(onClose);
      await waitFor(() => expect(fetchSettingsNodeTypes).toHaveBeenCalled());

      await user.keyboard('{Escape}');
      expect(discardDialog()).not.toBeInTheDocument();
      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it.each([
      ['the cancel button', (user: ReturnType<typeof userEvent.setup>) => user.click(screen.getByTestId('settings-discard-confirm-cancel'))],
      ['a click on the overlay', (user: ReturnType<typeof userEvent.setup>) => user.click(screen.getByTestId('settings-discard-confirm-overlay'))]
    ])('asks before closing on Escape with an unsaved edit, and stays open with the edit when cancelled with %s', async (_how, dismiss) => {
      const user = userEvent.setup();
      const onClose = vi.fn();
      renderModal(onClose);

      const textarea = await makeTemplateEditDirty(user);
      await user.keyboard('{Escape}');

      expect(screen.getByRole('alertdialog', { name: i18n.t('settings.unsavedChanges.confirmTitle') })).toHaveAccessibleDescription(
        i18n.t('settings.unsavedChanges.confirmMessage')
      );
      await dismiss(user);

      expect(discardDialog()).not.toBeInTheDocument();
      expect(onClose).not.toHaveBeenCalled();
      expect(screen.getByRole('dialog', { name: i18n.t('settings.modalTitle') })).toBeInTheDocument();
      expect(textarea).toHaveValue('plan-tier edited');
      expect(textarea).toHaveFocus();
    });

    it('closes on Escape with an unsaved edit once the discard is confirmed', async () => {
      const user = userEvent.setup();
      const onClose = vi.fn();
      renderModal(onClose);

      await makeTemplateEditDirty(user);
      await user.keyboard('{Escape}');
      expect(onClose).not.toHaveBeenCalled();
      await user.click(screen.getByTestId('settings-discard-confirm-confirm'));

      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it('lets Escape in the "add node type" input cancel only the add, and closes on the next Escape', async () => {
      const user = userEvent.setup();
      const onClose = vi.fn();
      renderModal(onClose);

      await user.click(await screen.findByRole('button', { name: i18n.t('settings.nodeTypes.addType') }));
      const input = screen.getByLabelText(i18n.t('settings.nodeTypes.newTypeLabel'));
      expect(input).toHaveFocus();

      await user.keyboard('{Escape}');
      expect(screen.queryByLabelText(i18n.t('settings.nodeTypes.newTypeLabel'))).not.toBeInTheDocument();
      expect(screen.getByRole('button', { name: i18n.t('settings.nodeTypes.addType') })).toBeInTheDocument();
      expect(onClose).not.toHaveBeenCalled();
      expect(discardDialog()).not.toBeInTheDocument();

      await user.keyboard('{Escape}');
      expect(onClose).toHaveBeenCalledTimes(1);
    });

    // DFLT-00171: an icon button's tooltip is dismissed by Escape first; the
    // modal only closes on the next Escape, once no tooltip is open.
    it('lets Escape close an open icon button tooltip only, and closes on the next Escape', async () => {
      const user = userEvent.setup();
      const onClose = vi.fn();
      renderModal(onClose);

      await user.click(await screen.findByRole('button', { name: i18n.t('settings.nodeTypes.addType') }));
      await user.tab();
      const confirm = screen.getByRole('button', { name: i18n.t('settings.nodeTypes.confirmAddType') });
      expect(confirm).toHaveFocus();
      expect(openIconButtonTooltip()).toHaveTextContent(i18n.t('settings.nodeTypes.confirmAddType'));

      await user.keyboard('{Escape}');
      expect(openIconButtonTooltips()).toHaveLength(0);
      expect(onClose).not.toHaveBeenCalled();
      expect(screen.getByRole('dialog')).toBeInTheDocument();
      expect(confirm).toHaveFocus();

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

    // DFLT-00148: a ConfirmDialog opened on top of this modal (a nested
    // modal). Only the topmost dialog handles Escape and Tab, and closing it
    // puts focus back on the element inside this modal that opened it.
    describe('with a confirmation open on top of it', () => {
      const settingsDialog = () => screen.getByRole('dialog', { name: i18n.t('settings.modalTitle') });

      const openDiscardFromTab = async (user: ReturnType<typeof userEvent.setup>, onClose = vi.fn()) => {
        renderModal(onClose);
        await makeTemplateEditDirty(user);
        const skillsTab = screen.getByRole('button', { name: i18n.t('settings.tabs.skills') });
        await user.click(skillsTab);
        expect(screen.getByTestId('settings-discard-confirm')).toBeInTheDocument();
        return skillsTab;
      };

      it('closes only the confirmation on Escape, keeping the settings modal and the edit, and returns focus to the tab', async () => {
        const user = userEvent.setup();
        const onClose = vi.fn();
        const skillsTab = await openDiscardFromTab(user, onClose);
        expect(screen.getByTestId('settings-discard-confirm-cancel')).toHaveFocus();

        await user.keyboard('{Escape}');

        expect(discardDialog()).not.toBeInTheDocument();
        expect(onClose).not.toHaveBeenCalled();
        expect(settingsDialog()).toBeInTheDocument();
        expect(screen.getByDisplayValue('plan-tier edited')).toBeInTheDocument();
        expect(skillsTab).toHaveFocus();

        // The settings modal handles Escape again once the confirmation is
        // gone (and asks again, since the edit is still unsaved).
        await user.keyboard('{Escape}');
        expect(discardDialog()).toBeInTheDocument();
        expect(onClose).not.toHaveBeenCalled();
      });

      it('keeps Tab and Shift+Tab inside the confirmation', async () => {
        const user = userEvent.setup();
        await openDiscardFromTab(user);
        const cancel = screen.getByTestId('settings-discard-confirm-cancel');
        const confirm = screen.getByTestId('settings-discard-confirm-confirm');
        expect(cancel).toHaveFocus();

        await user.tab();
        expect(confirm).toHaveFocus();
        await user.tab();
        expect(cancel).toHaveFocus();
        await user.tab({ shift: true });
        expect(confirm).toHaveFocus();
        await user.tab({ shift: true });
        expect(cancel).toHaveFocus();
        expect(settingsDialog()).toBeInTheDocument();
      });

      it('handles an editor confirmation (the template list) the same way', async () => {
        const user = userEvent.setup();
        const onClose = vi.fn();
        renderModal(onClose);
        await makeTemplateEditDirty(user);
        const reviewItem = screen.getByRole('button', { name: 'レビュー' });
        await user.click(reviewItem);
        expect(screen.getByTestId('template-discard-confirm-cancel')).toHaveFocus();

        await user.tab({ shift: true });
        expect(screen.getByTestId('template-discard-confirm-confirm')).toHaveFocus();
        await user.keyboard('{Escape}');

        expect(screen.queryByTestId('template-discard-confirm')).not.toBeInTheDocument();
        expect(onClose).not.toHaveBeenCalled();
        expect(settingsDialog()).toBeInTheDocument();
        expect(reviewItem).toHaveFocus();
        expect(screen.getByRole('button', { name: '実行計画' })).toHaveAttribute('aria-current', 'true');
      });

      it('handles a delete confirmation (a node type override) the same way', async () => {
        (fetchSettingsNodeTypes as unknown as Mock).mockResolvedValue([
          { type: 'custom_lint', has_default: false, has_user_override: true }
        ]);
        const user = userEvent.setup();
        const onClose = vi.fn();
        renderModal(onClose);
        const deleteButton = await screen.findByRole('button', { name: i18n.t('settings.nodeTypes.deleteTypeAriaLabel', { name: 'custom_lint' }) });

        await user.click(deleteButton);
        expect(screen.getByRole('alertdialog', { name: i18n.t('settings.nodeTypes.confirmDeleteTypeTitle') })).toBeInTheDocument();
        await user.tab();
        await user.tab();
        expect(screen.getByTestId('node-type-delete-confirm-cancel')).toHaveFocus();
        await user.keyboard('{Escape}');

        expect(screen.queryByTestId('node-type-delete-confirm')).not.toBeInTheDocument();
        expect(onClose).not.toHaveBeenCalled();
        expect(settingsDialog()).toBeInTheDocument();
        expect(deleteButton).toHaveFocus();
      });

      it('closes the settings modal with the confirmation when the discard from the close button is confirmed, returning focus to its opener', async () => {
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
        await makeTemplateEditDirty(user);

        await user.click(closeButton());
        expect(discardDialog()).toBeInTheDocument();
        await user.click(screen.getByTestId('settings-discard-confirm-confirm'));

        expect(discardDialog()).not.toBeInTheDocument();
        expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
        expect(screen.getByRole('button', { name: 'open-settings' })).toHaveFocus();
      });

      it('returns focus to the close button when the discard from it is cancelled', async () => {
        const user = userEvent.setup();
        const onClose = vi.fn();
        renderModal(onClose);
        await makeTemplateEditDirty(user);

        await user.click(closeButton());
        await user.click(screen.getByTestId('settings-discard-confirm-cancel'));

        expect(onClose).not.toHaveBeenCalled();
        expect(closeButton()).toHaveFocus();
      });
    });
  });

  // DFLT-00077, carried over to the labels tab (DFLT-00124): App.tsx keeps
  // the modal mounted and resolves its current project asynchronously, so
  // the tab must pick up the header's project each time it opens rather than
  // only on its first mount. Labels are the one per-project thing left in
  // this modal, so this is now that tab's selector rather than a scope-level
  // one.
  describe('the labels tab picks its own project', () => {
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
    const projectSelect = () => screen.getByLabelText(i18n.t('settings.labels.projectLabel'));
    const openLabelsTab = (user: ReturnType<typeof userEvent.setup>) =>
      user.click(screen.getByRole('button', { name: i18n.t('settings.tabs.labels') }));

    beforeEach(() => {
      vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify([]), { status: 200 })));
    });

    afterEach(() => {
      vi.unstubAllGlobals();
    });

    it('starts on the header project when opened after being mounted closed with no project', async () => {
      const user = userEvent.setup();
      const { rerender } = render(modal(false, null));
      rerender(modal(false, alpha));
      rerender(modal(true, alpha));
      await waitFor(() => expect(fetchSettingsNodeTypes).toHaveBeenCalled());

      await openLabelsTab(user);

      expect(projectSelect()).toHaveValue(alpha.id);
      expect(screen.queryByText(i18n.t('settings.labels.selectProject'))).not.toBeInTheDocument();
    });

    it('goes back to the header project on reopen after another project was picked inside the tab', async () => {
      const user = userEvent.setup();
      const { rerender } = render(modal(true, alpha));

      await openLabelsTab(user);
      await user.selectOptions(projectSelect(), beta.id);
      expect(projectSelect()).toHaveValue(beta.id);

      rerender(modal(false, alpha));
      rerender(modal(true, alpha));
      await openLabelsTab(user);

      expect(projectSelect()).toHaveValue(alpha.id);
    });

    it('follows a header project change made while the modal was closed', async () => {
      const user = userEvent.setup();
      const { rerender } = render(modal(true, alpha));
      await openLabelsTab(user);
      expect(projectSelect()).toHaveValue(alpha.id);

      rerender(modal(false, alpha));
      rerender(modal(false, beta));
      rerender(modal(true, beta));
      await openLabelsTab(user);

      expect(projectSelect()).toHaveValue(beta.id);
    });

    // With nothing selected in the header the tab still has projects to
    // offer, so it falls back to the first rather than opening disabled.
    it('falls back to the first project when the header has none selected', async () => {
      const user = userEvent.setup();
      const { rerender } = render(modal(false, null));
      rerender(modal(true, null));
      await openLabelsTab(user);

      expect(projectSelect()).toHaveValue(alpha.id);
    });
  });
});
