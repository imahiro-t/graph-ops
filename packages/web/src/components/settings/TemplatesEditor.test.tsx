// Covers the テンプレート tab (DFLT-00071): switching between the plan /
// review / report templates in the left-hand list, saving and clearing an
// override, the unsaved-changes confirmation on a list switch, the report
// template's unchanged error path, and the accessibility wiring (selected
// item, labelled preview and textarea, text contrast, focus after saving).
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../../i18n';
import { TemplatesEditor } from './TemplatesEditor';
import { deferred } from '../../test/deferred';
import { SAVED_FLASH_DURATION_MS } from '../../hooks/useSavedFlash';
import { REANNOUNCE_GAP_MS } from '../../hooks/useTransientAnnouncement';

vi.mock('../../lib/settingsApi', async () => {
  const actual = await vi.importActual<typeof import('../../lib/settingsApi')>('../../lib/settingsApi');
  return {
    ...actual,
    fetchSettingsPlanTemplate: vi.fn(),
    saveSettingsPlanTemplate: vi.fn(),
    fetchSettingsReviewTemplate: vi.fn(),
    saveSettingsReviewTemplate: vi.fn(),
    fetchSettingsReportTemplate: vi.fn(),
    saveSettingsReportTemplate: vi.fn()
  };
});

import {
  fetchSettingsPlanTemplate,
  fetchSettingsReportTemplate,
  fetchSettingsReviewTemplate,
  saveSettingsPlanTemplate,
  saveSettingsReportTemplate,
  saveSettingsReviewTemplate
} from '../../lib/settingsApi';

type Mock = ReturnType<typeof vi.fn>;
const fetchPlan = fetchSettingsPlanTemplate as unknown as Mock;
const savePlan = saveSettingsPlanTemplate as unknown as Mock;
const fetchReview = fetchSettingsReviewTemplate as unknown as Mock;
const saveReview = saveSettingsReviewTemplate as unknown as Mock;
const fetchReport = fetchSettingsReportTemplate as unknown as Mock;
const saveReport = saveSettingsReportTemplate as unknown as Mock;

const listButton = (key: 'plan' | 'review' | 'report') =>
  screen.getByRole('button', { name: i18n.t(`settings.templates.list.${key}`) });

const previewOf = (prefix: string) =>
  screen.findByRole('region', { name: i18n.t(`${prefix}.mergedPreviewLabel`) });

const textareaOf = (prefix: string) =>
  screen.findByLabelText(i18n.t(`${prefix}.tierTextLabel`)) as Promise<HTMLTextAreaElement>;

const saveButton = () => screen.getByRole('button', { name: i18n.t('settings.common.save') });

// The saved notice, told apart from the loading line (also role="status",
// DFLT-00350) by its text.
const savedStatus = () =>
  screen.findByText(i18n.t('settings.common.saveSuccess'), { selector: '[role="status"]' });

// The always-mounted, sr-only live region that announces the save
// (DFLT-00359). It is the only status region that is sr-only.
const savedLiveRegion = () => {
  const regions = screen.getAllByRole('status').filter(el => el.classList.contains('sr-only'));
  expect(regions).toHaveLength(1);
  return regions[0];
};

// The visible "saved" notice next to the save button (aria-hidden).
const savedVisual = () =>
  screen.findByText(i18n.t('settings.common.saveSuccess'), { selector: 'span[aria-hidden="true"]' });

// Status regions that currently hold some text.
const nonEmptyStatuses = () => screen.queryAllByRole('status').filter(el => el.textContent !== '');

const retryButton = () => screen.getByRole('button', { name: i18n.t('settings.common.retry') });

describe('TemplatesEditor', () => {
  beforeEach(() => {
    for (const m of [fetchPlan, savePlan, fetchReview, saveReview, fetchReport, saveReport]) m.mockReset();
    fetchPlan.mockResolvedValue({ tier_text: 'plan-tier', merged_text: 'plan-merged' });
    fetchReview.mockResolvedValue({ tier_text: 'review-tier', merged_text: 'review-merged' });
    fetchReport.mockResolvedValue({ tier_text: 'report-tier', merged_text: 'report-merged' });
  });

  afterEach(async () => {
    vi.restoreAllMocks();
    await i18n.changeLanguage('ja');
  });

  // DFLT-00343: the first render must already be the loading line, not the
  // editor with its empty defaults. render() runs effects straight away, so
  // the DOM is already past the first frame; renderToStaticMarkup renders
  // once and runs no effect, which is exactly that first frame.
  it('shows the loading line, not an empty editor, before the template has loaded', () => {
    fetchPlan.mockReturnValue(new Promise(() => {}));
    const html = renderToStaticMarkup(<TemplatesEditor onDirtyChange={vi.fn()} />);
    expect(html).toContain(i18n.t('settings.common.loading'));
    expect(html).not.toContain('<textarea');
    expect(html).not.toContain(i18n.t('settings.planTemplate.mergedPreviewLabel'));
    // The button's own text, not the word inside the intro paragraph.
    expect(html).not.toContain(`${i18n.t('settings.common.save')}</button>`);
    expect(fetchPlan).not.toHaveBeenCalled();
  });

  it('lists plan, review and report in order and opens the plan template first', async () => {
    render(<TemplatesEditor onDirtyChange={vi.fn()} />);

    const items = screen.getAllByRole('listitem').map(li => li.textContent);
    expect(items).toEqual([
      i18n.t('settings.templates.list.plan'),
      i18n.t('settings.templates.list.review'),
      i18n.t('settings.templates.list.report')
    ]);
    expect(listButton('plan')).toHaveAttribute('aria-current', 'true');
    expect(listButton('review')).not.toHaveAttribute('aria-current');

    await waitFor(() => expect(fetchPlan).toHaveBeenCalledWith(expect.anything()));
    expect(await previewOf('settings.planTemplate')).toHaveTextContent('plan-merged');
    expect(await textareaOf('settings.planTemplate')).toHaveValue('plan-tier');
    expect(screen.getByText(i18n.t('settings.planTemplate.intro'))).toBeInTheDocument();
    expect(fetchReview).not.toHaveBeenCalled();
    expect(fetchReport).not.toHaveBeenCalled();
  });

  it('shows the selected template on the right when another list item is chosen', async () => {
    const user = userEvent.setup();
    render(<TemplatesEditor onDirtyChange={vi.fn()} />);
    await textareaOf('settings.planTemplate');

    await user.click(listButton('review'));
    expect(listButton('review')).toHaveAttribute('aria-current', 'true');
    expect(listButton('plan')).not.toHaveAttribute('aria-current');
    expect(fetchReview).toHaveBeenCalledWith(expect.anything());
    expect(await previewOf('settings.reviewTemplate')).toHaveTextContent('review-merged');
    expect(await textareaOf('settings.reviewTemplate')).toHaveValue('review-tier');

    await user.click(listButton('report'));
    expect(fetchReport).toHaveBeenCalledWith(expect.anything());
    expect(await previewOf('settings.reportTemplate')).toHaveTextContent('report-merged');
    expect(await textareaOf('settings.reportTemplate')).toHaveValue('report-tier');
    expect(screen.getByText(i18n.t('settings.reportTemplate.intro'))).toBeInTheDocument();
  });

  it('can be operated with the keyboard alone', async () => {
    const user = userEvent.setup();
    render(<TemplatesEditor onDirtyChange={vi.fn()} />);
    await textareaOf('settings.planTemplate');

    listButton('review').focus();
    await user.keyboard('{Enter}');

    expect(listButton('review')).toHaveAttribute('aria-current', 'true');
    expect(await previewOf('settings.reviewTemplate')).toHaveTextContent('review-merged');
  });

  it('saves an edited plan override as text and shows the returned state', async () => {
    const user = userEvent.setup();
    const onDirtyChange = vi.fn();
    savePlan.mockResolvedValue({ tier_text: '# 目的\n本文', merged_text: '# 目的\n本文' });
    render(<TemplatesEditor onDirtyChange={onDirtyChange} />);

    const textarea = await textareaOf('settings.planTemplate');
    expect(saveButton()).toBeDisabled();
    await user.clear(textarea);
    await user.type(textarea, '# 目的{Enter}本文');
    expect(saveButton()).toBeEnabled();
    expect(onDirtyChange).toHaveBeenLastCalledWith(true);

    await user.click(saveButton());

    expect(savePlan).toHaveBeenCalledWith(expect.anything(), '# 目的\n本文');
    expect(await savedStatus()).toBeInTheDocument();
    expect(await previewOf('settings.planTemplate')).toHaveTextContent('# 目的 本文');
    expect(saveButton()).toBeDisabled();
    expect(onDirtyChange).toHaveBeenLastCalledWith(false);
  });

  it('clears a review override by saving it empty', async () => {
    const user = userEvent.setup();
    saveReview.mockResolvedValue({ tier_text: '', merged_text: '# Verdict' });
    render(<TemplatesEditor onDirtyChange={vi.fn()} />);
    await textareaOf('settings.planTemplate');
    await user.click(listButton('review'));

    const textarea = await textareaOf('settings.reviewTemplate');
    await user.clear(textarea);
    await user.click(saveButton());

    expect(saveReview).toHaveBeenCalledWith(expect.anything(), '');
    await waitFor(() => expect(textarea).toHaveValue(''));
    expect(await previewOf('settings.reviewTemplate')).toHaveTextContent('# Verdict');
    expect(screen.getByText(i18n.t('settings.reviewTemplate.emptyOverrideHint'))).toBeInTheDocument();
  });

  // The question is the in-app ConfirmDialog since DFLT-00148.
  it.each([
    ['the cancel button', (user: ReturnType<typeof userEvent.setup>) => user.click(screen.getByTestId('template-discard-confirm-cancel'))],
    ['Escape', (user: ReturnType<typeof userEvent.setup>) => user.keyboard('{Escape}')],
    ['a click on the overlay', (user: ReturnType<typeof userEvent.setup>) => user.click(screen.getByTestId('template-discard-confirm-overlay'))]
  ])('keeps the selection and the unsaved edit when the switch is cancelled with %s', async (_how, dismiss) => {
    const user = userEvent.setup();
    render(<TemplatesEditor onDirtyChange={vi.fn()} />);

    const textarea = await textareaOf('settings.planTemplate');
    await user.clear(textarea);
    await user.type(textarea, '# 編集途中');
    await user.click(listButton('review'));

    const dialog = screen.getByRole('alertdialog', { name: i18n.t('settings.unsavedChanges.confirmTitle') });
    expect(dialog).toHaveAccessibleDescription(i18n.t('settings.unsavedChanges.confirmMessage'));
    await dismiss(user);

    expect(screen.queryByTestId('template-discard-confirm')).not.toBeInTheDocument();
    expect(listButton('review')).toHaveFocus();
    expect(listButton('plan')).toHaveAttribute('aria-current', 'true');
    expect(textarea).toHaveValue('# 編集途中');
    expect(fetchReview).not.toHaveBeenCalled();
  });

  it('discards the edit on confirm and does not ask again on the next clean switch', async () => {
    const user = userEvent.setup();
    const onDirtyChange = vi.fn();
    render(<TemplatesEditor onDirtyChange={onDirtyChange} />);

    const textarea = await textareaOf('settings.planTemplate');
    await user.type(textarea, ' edited');
    await user.click(listButton('review'));
    await user.click(screen.getByTestId('template-discard-confirm-confirm'));

    expect(screen.queryByTestId('template-discard-confirm')).not.toBeInTheDocument();
    expect(await textareaOf('settings.reviewTemplate')).toHaveValue('review-tier');
    expect(onDirtyChange).toHaveBeenLastCalledWith(false);

    await user.click(listButton('report'));
    expect(screen.queryByTestId('template-discard-confirm')).not.toBeInTheDocument();
    expect(listButton('report')).toHaveAttribute('aria-current', 'true');
  });

  it('does not ask for confirmation when nothing was edited', async () => {
    const user = userEvent.setup();
    render(<TemplatesEditor onDirtyChange={vi.fn()} />);
    await textareaOf('settings.planTemplate');

    await user.click(listButton('review'));

    expect(screen.queryByTestId('template-discard-confirm')).not.toBeInTheDocument();
    expect(listButton('review')).toHaveAttribute('aria-current', 'true');
  });

  // DFLT-00350: a failed load shows the error and a retry button in place of
  // the editor, so an empty textarea cannot be saved over the stored override.
  it('shows the error and a retry button, not an empty editor, when loading a template fails', async () => {
    fetchPlan.mockRejectedValue(new Error('load failed'));
    render(<TemplatesEditor onDirtyChange={vi.fn()} />);

    expect(await screen.findByRole('alert')).toHaveTextContent('load failed');
    expect(retryButton()).toBeInTheDocument();
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
    expect(screen.queryByRole('region', { name: i18n.t('settings.planTemplate.mergedPreviewLabel') })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: i18n.t('settings.common.save') })).not.toBeInTheDocument();
    // No loading line: only the (empty) always-mounted save live region.
    expect(nonEmptyStatuses()).toHaveLength(0);
    expect(savedLiveRegion()).toBeEmptyDOMElement();
  });

  it('loads the template on retry and moves focus to the textarea', async () => {
    const user = userEvent.setup();
    fetchPlan.mockRejectedValueOnce(new Error('load failed'));
    const pending = deferred<{ tier_text: string; merged_text: string }>();
    fetchPlan.mockReturnValueOnce(pending.promise);
    render(<TemplatesEditor onDirtyChange={vi.fn()} />);
    await screen.findByRole('alert');

    const button = retryButton();
    button.focus();
    await user.keyboard('{Enter}');

    // While the retry runs, the failure and the focused retry button stay,
    // and a second press sends nothing.
    expect(fetchPlan).toHaveBeenCalledTimes(2);
    expect(button).toBeInTheDocument();
    expect(button).toHaveFocus();
    expect(button).toHaveAttribute('aria-disabled', 'true');
    expect(button).toHaveAttribute('aria-busy', 'true');
    expect(screen.getByRole('alert')).toBeInTheDocument();
    await user.keyboard('{Enter}');
    expect(fetchPlan).toHaveBeenCalledTimes(2);

    await act(async () => {
      pending.resolve({ tier_text: 'plan-tier', merged_text: 'plan-merged' });
    });

    const textarea = await textareaOf('settings.planTemplate');
    expect(textarea).toHaveValue('plan-tier');
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    await waitFor(() => expect(textarea).toHaveFocus());
  });

  it('keeps the error and the retry button when the retry fails again', async () => {
    const user = userEvent.setup();
    fetchPlan.mockRejectedValue(new Error('load failed'));
    render(<TemplatesEditor onDirtyChange={vi.fn()} />);
    const firstAlert = await screen.findByRole('alert');

    const button = retryButton();
    button.focus();
    await user.keyboard('{Enter}');

    await waitFor(() => expect(fetchPlan).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(retryButton()).not.toHaveAttribute('aria-busy'));
    // A new alert element, so the same wording is announced again.
    expect(screen.getByRole('alert')).not.toBe(firstAlert);
    expect(screen.getByRole('alert')).toHaveTextContent('load failed');
    expect(retryButton()).toHaveFocus();
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
  });

  it('report: shows no textarea when loading the report template fails', async () => {
    const user = userEvent.setup();
    fetchReport.mockRejectedValue(new Error('report load failed'));
    render(<TemplatesEditor onDirtyChange={vi.fn()} />);
    await textareaOf('settings.planTemplate');
    await user.click(listButton('report'));

    expect(await screen.findByRole('alert')).toHaveTextContent('report load failed');
    expect(retryButton()).toBeInTheDocument();
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: i18n.t('settings.common.save') })).not.toBeInTheDocument();
  });

  it('announces the loading line as a status', async () => {
    fetchPlan.mockReturnValue(new Promise(() => {}));
    render(<TemplatesEditor onDirtyChange={vi.fn()} />);

    const loadingLine = screen.getByText(i18n.t('settings.common.loading')).closest('[role="status"]');
    expect(loadingLine).toBeInTheDocument();
    expect(nonEmptyStatuses()).toEqual([loadingLine]);
  });

  it('keeps the input unsaved when saving a review override fails', async () => {
    const user = userEvent.setup();
    saveReview.mockRejectedValue(new Error('save failed'));
    render(<TemplatesEditor onDirtyChange={vi.fn()} />);
    await textareaOf('settings.planTemplate');
    await user.click(listButton('review'));

    const textarea = await textareaOf('settings.reviewTemplate');
    await user.type(textarea, ' more');
    await user.click(saveButton());

    expect(await screen.findByRole('alert')).toHaveTextContent('save failed');
    expect(textarea).toHaveValue('review-tier more');
    expect(saveButton()).toBeEnabled();
  });

  it('report: saves through the report API and shows INVALID_REPORT_TEMPLATE errors as before', async () => {
    const user = userEvent.setup();
    saveReport.mockRejectedValue(new Error('missing required marker data-report-template'));
    render(<TemplatesEditor onDirtyChange={vi.fn()} />);
    await textareaOf('settings.planTemplate');
    await user.click(listButton('report'));

    const textarea = await textareaOf('settings.reportTemplate');
    await user.clear(textarea);
    await user.type(textarea, '<p>no markers</p>');
    await user.click(saveButton());

    expect(saveReport).toHaveBeenCalledWith(expect.anything(), '<p>no markers</p>');
    expect(savePlan).not.toHaveBeenCalled();
    expect(await screen.findByRole('alert')).toHaveTextContent('missing required marker data-report-template');
    expect(await previewOf('settings.reportTemplate')).toHaveTextContent('report-merged');
    expect(textarea).toHaveValue('<p>no markers</p>');
    expect(saveButton()).toBeEnabled();
  });

  // DFLT-00359: the saved notice is announced by an always-mounted live
  // region (not a role="status" mounted together with its text), the visible
  // notice is aria-hidden so it is not read twice, and a second save within
  // the display time empties the region and refills it so it is announced.
  describe('saved announcement (DFLT-00359)', () => {
    afterEach(() => {
      vi.useRealTimers();
    });

    it('keeps the live region mounted while loading and after a load failure', async () => {
      const pending = deferred<{ tier_text: string; merged_text: string }>();
      fetchPlan.mockReturnValueOnce(pending.promise);
      const user = userEvent.setup();
      render(<TemplatesEditor onDirtyChange={vi.fn()} />);

      const region = savedLiveRegion();
      expect(region).toBeEmptyDOMElement();
      expect(region).toHaveAttribute('aria-live', 'polite');

      await act(async () => {
        pending.resolve({ tier_text: 'plan-tier', merged_text: 'plan-merged' });
      });
      await textareaOf('settings.planTemplate');
      expect(savedLiveRegion()).toBe(region);

      // Switching templates remounts the editor, so a fresh region appears
      // with it and stays through the failed load.
      fetchReview.mockRejectedValueOnce(new Error('load failed'));
      await user.click(listButton('review'));
      expect(await screen.findByRole('alert')).toHaveTextContent('load failed');
      expect(savedLiveRegion()).toBeEmptyDOMElement();
    });

    it('announces a save once, through the live region, with an aria-hidden visible notice', async () => {
      const user = userEvent.setup();
      savePlan.mockResolvedValue({ tier_text: 'plan-tier edited', merged_text: 'plan-tier edited' });
      render(<TemplatesEditor onDirtyChange={vi.fn()} />);

      const region = savedLiveRegion();
      await user.type(await textareaOf('settings.planTemplate'), ' edited');
      await user.click(saveButton());

      await waitFor(() => expect(region).toHaveTextContent(i18n.t('settings.common.saveSuccess')));
      expect(await savedVisual()).toBeInTheDocument();
      const announcing = screen
        .getAllByRole('status')
        .filter(el => el.textContent?.includes(i18n.t('settings.common.saveSuccess')));
      expect(announcing).toEqual([region]);
    });

    it('re-announces a second save made while the notice is still shown', async () => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
      savePlan.mockResolvedValueOnce({ tier_text: 'plan-tier a', merged_text: 'plan-tier a' });
      const second = deferred<{ tier_text: string; merged_text: string }>();
      savePlan.mockReturnValueOnce(second.promise);
      render(<TemplatesEditor onDirtyChange={vi.fn()} />);

      const region = savedLiveRegion();
      const textarea = await textareaOf('settings.planTemplate');
      await user.type(textarea, ' a');
      await user.click(saveButton());
      await waitFor(() => expect(region).toHaveTextContent(i18n.t('settings.common.saveSuccess')));

      await user.type(textarea, 'b');
      await user.click(saveButton());
      await act(async () => {
        second.resolve({ tier_text: 'plan-tier ab', merged_text: 'plan-tier ab' });
      });

      // Emptied first, while the visible notice stays up...
      expect(region).toBeEmptyDOMElement();
      expect(await savedVisual()).toBeInTheDocument();
      // ...and refilled after the gap, so the change is announced.
      act(() => {
        vi.advanceTimersByTime(REANNOUNCE_GAP_MS);
      });
      expect(region).toHaveTextContent(i18n.t('settings.common.saveSuccess'));

      // Both clear SAVED_FLASH_DURATION_MS after the second save.
      act(() => {
        vi.advanceTimersByTime(SAVED_FLASH_DURATION_MS);
      });
      expect(region).toBeEmptyDOMElement();
      expect(screen.queryByText(i18n.t('settings.common.saveSuccess'))).not.toBeInTheDocument();
    });
  });

  describe('accessibility review (a11y F-1 / F-2)', () => {
    // Class pairs chosen for WCAG 1.4.3 (>= 4.5:1): slate-500 on white 4.76:1,
    // slate-400 on slate-900 6.96:1, emerald-700 on white 5.48:1,
    // emerald-400 on slate-900 well above 4.5:1.
    it('F-1: the loading text uses colours that meet 4.5:1 in light and dark', async () => {
      fetchPlan.mockReturnValue(new Promise(() => {}));
      render(<TemplatesEditor onDirtyChange={vi.fn()} />);

      const loading = (await screen.findByText(i18n.t('settings.common.loading'))).closest('div');
      expect(loading).toHaveClass('text-slate-500', 'dark:text-slate-400');
      expect(loading).not.toHaveClass('text-slate-400');
      expect(loading).not.toHaveClass('dark:text-slate-500');
    });

    it('F-1: the empty-save hint and the saved notice use colours that meet 4.5:1', async () => {
      const user = userEvent.setup();
      savePlan.mockResolvedValue({ tier_text: 'x', merged_text: 'x' });
      render(<TemplatesEditor onDirtyChange={vi.fn()} />);

      const textarea = await textareaOf('settings.planTemplate');
      const hint = screen.getByText(i18n.t('settings.planTemplate.emptyOverrideHint'));
      expect(hint).toHaveClass('text-slate-500', 'dark:text-slate-400');
      expect(hint).not.toHaveClass('text-slate-400');
      expect(hint).not.toHaveClass('dark:text-slate-500');

      await user.clear(textarea);
      await user.type(textarea, 'x');
      await user.click(saveButton());

      const notice = await savedVisual();
      expect(notice).toHaveTextContent(i18n.t('settings.common.saveSuccess'));
      expect(notice).toHaveClass('text-emerald-700', 'dark:text-emerald-400');
      expect(notice).not.toHaveClass('text-emerald-600');
    });

    it('F-2: saving with the keyboard moves focus to the textarea instead of losing it', async () => {
      const user = userEvent.setup();
      const pending = deferred<{ tier_text: string; merged_text: string }>();
      savePlan.mockReturnValue(pending.promise);
      render(<TemplatesEditor onDirtyChange={vi.fn()} />);

      const textarea = await textareaOf('settings.planTemplate');
      await user.type(textarea, ' edited');
      const button = saveButton();
      button.focus();
      await user.keyboard('{Enter}');

      expect(savePlan).toHaveBeenCalledTimes(1);
      // The button is now disabled. A browser drops focus to <body> at this
      // point; jsdom leaves it on the disabled button. The editor treats both
      // as "focus was lost" and restores it to the textarea.
      expect(button).toBeDisabled();

      await act(async () => {
        pending.resolve({ tier_text: 'plan-tier edited', merged_text: 'plan-tier edited' });
      });

      expect(await savedStatus()).toBeInTheDocument();
      expect(saveButton()).toBeDisabled();
      expect(textarea).toHaveFocus();
    });

    it('F-2: focus also lands on the textarea when a keyboard save fails', async () => {
      const user = userEvent.setup();
      savePlan.mockRejectedValue(new Error('save failed'));
      render(<TemplatesEditor onDirtyChange={vi.fn()} />);

      const textarea = await textareaOf('settings.planTemplate');
      await user.type(textarea, ' edited');
      saveButton().focus();
      await user.keyboard('{Enter}');

      expect(await screen.findByRole('alert')).toHaveTextContent('save failed');
      expect(textarea).toHaveFocus();
    });

    it('F-2: does not take focus back if the user moved it elsewhere while saving', async () => {
      const user = userEvent.setup();
      const pending = deferred<{ tier_text: string; merged_text: string }>();
      savePlan.mockReturnValue(pending.promise);
      render(<TemplatesEditor onDirtyChange={vi.fn()} />);

      const textarea = await textareaOf('settings.planTemplate');
      await user.type(textarea, ' edited');
      saveButton().focus();
      await user.keyboard('{Enter}');

      const reviewItem = listButton('review');
      act(() => reviewItem.focus());

      await act(async () => {
        pending.resolve({ tier_text: 'plan-tier edited', merged_text: 'plan-tier edited' });
      });

      expect(await savedStatus()).toBeInTheDocument();
      expect(reviewItem).toHaveFocus();
      expect(textarea).not.toHaveFocus();
    });
  });

  it('F-1 non-regression: switching language does not re-fetch or discard an unsaved edit', async () => {
    const user = userEvent.setup();
    render(<TemplatesEditor onDirtyChange={vi.fn()} />);

    const textarea = await textareaOf('settings.planTemplate');
    await user.clear(textarea);
    await user.type(textarea, '# 編集途中');
    expect(fetchPlan).toHaveBeenCalledTimes(1);

    await act(async () => {
      await i18n.changeLanguage('en');
    });

    expect(fetchPlan).toHaveBeenCalledTimes(1);
    expect(screen.getByDisplayValue('# 編集途中')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Review' })).toBeInTheDocument();
  });
});

// DFLT-00261: below 48rem (`narrow:`, rem-based) the template list stacks
// above the editor at full width with a capped height and its names wrap;
// TemplateTextEditor (plan / review / report) drops its fill-the-panel height
// so the tab panel scrolls, and its save row wraps. The wide classes stay.
// jsdom does no layout, so the classes are pinned.
describe('TemplatesEditor narrow reflow (DFLT-00261)', () => {
  beforeEach(() => {
    for (const m of [fetchPlan, savePlan, fetchReview, saveReview, fetchReport, saveReport]) m.mockReset();
    fetchPlan.mockResolvedValue({ tier_text: 'plan-tier', merged_text: 'plan-merged' });
    fetchReport.mockResolvedValue({ tier_text: 'report-tier', merged_text: 'report-merged' });
  });

  it('stacks the list above the editor and wraps the editor rows below 48rem, for markdown and report templates', async () => {
    const user = userEvent.setup();
    const { container } = render(<TemplatesEditor onDirtyChange={vi.fn()} />);
    await textareaOf('settings.planTemplate');

    const root = container.firstElementChild as HTMLElement;
    expect(root).toHaveClass('flex', 'h-full', 'min-h-0', 'gap-4', 'narrow:flex-col', 'narrow:h-auto');
    const nav = screen.getByRole('navigation');
    expect(nav).toHaveClass('w-56', 'shrink-0', 'overflow-y-auto', 'narrow:w-full', 'narrow:max-h-40');
    for (const key of ['plan', 'review', 'report'] as const) {
      expect(listButton(key).querySelector('span.truncate')).toHaveClass('narrow:whitespace-normal', 'narrow:wrap-anywhere');
    }

    const checkEditor = () => {
      const saveRow = saveButton().parentElement as HTMLElement;
      expect(saveRow).toHaveClass('flex', 'justify-end', 'narrow:flex-wrap');
      const editorRoot = saveRow.parentElement as HTMLElement;
      expect(editorRoot).toHaveClass('flex', 'flex-col', 'h-full', 'min-h-0', 'narrow:h-auto');
    };
    checkEditor();
    await user.click(listButton('report'));
    await textareaOf('settings.reportTemplate');
    checkEditor();
  });

  // A-1: below 48rem the list is a max-h-40 scroll box under a sticky
  // heading, so an item focus scrolls into view must stop below the heading.
  it('keeps a focused list item from scrolling under the sticky heading', async () => {
    render(<TemplatesEditor onDirtyChange={vi.fn()} />);
    await textareaOf('settings.planTemplate');

    const nav = screen.getByRole('navigation');
    expect(nav).toHaveClass('overflow-y-auto', 'scroll-pt-12');
    expect(nav.firstElementChild).toHaveTextContent(i18n.t('settings.templates.listTitle'));
    expect(nav.firstElementChild).toHaveClass('sticky', 'top-0', 'z-10');
  });
});
