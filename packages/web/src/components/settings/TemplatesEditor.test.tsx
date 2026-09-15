// Covers the テンプレート tab (DFLT-00071): switching between the plan /
// review / report templates in the left-hand list, saving and clearing an
// override, the unsaved-changes confirmation on a list switch, the report
// template's unchanged error path, and the accessibility wiring (selected
// item, labelled preview and textarea, text contrast, focus after saving).
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../../i18n';
import { TemplatesEditor } from './TemplatesEditor';

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

  it('lists plan, review and report in order and opens the plan template first', async () => {
    render(<TemplatesEditor scope="global" projectId="" canEdit onDirtyChange={vi.fn()} />);

    const items = screen.getAllByRole('listitem').map(li => li.textContent);
    expect(items).toEqual([
      i18n.t('settings.templates.list.plan'),
      i18n.t('settings.templates.list.review'),
      i18n.t('settings.templates.list.report')
    ]);
    expect(listButton('plan')).toHaveAttribute('aria-current', 'true');
    expect(listButton('review')).not.toHaveAttribute('aria-current');

    await waitFor(() => expect(fetchPlan).toHaveBeenCalledWith(expect.anything(), 'global', ''));
    expect(await previewOf('settings.planTemplate')).toHaveTextContent('plan-merged');
    expect(await textareaOf('settings.planTemplate')).toHaveValue('plan-tier');
    expect(screen.getByText(i18n.t('settings.planTemplate.intro'))).toBeInTheDocument();
    expect(fetchReview).not.toHaveBeenCalled();
    expect(fetchReport).not.toHaveBeenCalled();
  });

  it('shows the selected template on the right when another list item is chosen', async () => {
    const user = userEvent.setup();
    render(<TemplatesEditor scope="project" projectId="proj-A" canEdit onDirtyChange={vi.fn()} />);
    await textareaOf('settings.planTemplate');

    await user.click(listButton('review'));
    expect(listButton('review')).toHaveAttribute('aria-current', 'true');
    expect(listButton('plan')).not.toHaveAttribute('aria-current');
    expect(fetchReview).toHaveBeenCalledWith(expect.anything(), 'project', 'proj-A');
    expect(await previewOf('settings.reviewTemplate')).toHaveTextContent('review-merged');
    expect(await textareaOf('settings.reviewTemplate')).toHaveValue('review-tier');

    await user.click(listButton('report'));
    expect(fetchReport).toHaveBeenCalledWith(expect.anything(), 'project', 'proj-A');
    expect(await previewOf('settings.reportTemplate')).toHaveTextContent('report-merged');
    expect(await textareaOf('settings.reportTemplate')).toHaveValue('report-tier');
    expect(screen.getByText(i18n.t('settings.reportTemplate.intro'))).toBeInTheDocument();
  });

  it('can be operated with the keyboard alone', async () => {
    const user = userEvent.setup();
    render(<TemplatesEditor scope="global" projectId="" canEdit onDirtyChange={vi.fn()} />);
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
    render(<TemplatesEditor scope="global" projectId="" canEdit onDirtyChange={onDirtyChange} />);

    const textarea = await textareaOf('settings.planTemplate');
    expect(saveButton()).toBeDisabled();
    await user.clear(textarea);
    await user.type(textarea, '# 目的{Enter}本文');
    expect(saveButton()).toBeEnabled();
    expect(onDirtyChange).toHaveBeenLastCalledWith(true);

    await user.click(saveButton());

    expect(savePlan).toHaveBeenCalledWith(expect.anything(), 'global', '', '# 目的\n本文');
    expect(await screen.findByText(i18n.t('settings.common.saveSuccess'))).toBeInTheDocument();
    expect(await previewOf('settings.planTemplate')).toHaveTextContent('# 目的 本文');
    expect(saveButton()).toBeDisabled();
    expect(onDirtyChange).toHaveBeenLastCalledWith(false);
  });

  it('clears a review override by saving it empty', async () => {
    const user = userEvent.setup();
    saveReview.mockResolvedValue({ tier_text: '', merged_text: '# Verdict' });
    render(<TemplatesEditor scope="global" projectId="" canEdit onDirtyChange={vi.fn()} />);
    await textareaOf('settings.planTemplate');
    await user.click(listButton('review'));

    const textarea = await textareaOf('settings.reviewTemplate');
    await user.clear(textarea);
    await user.click(saveButton());

    expect(saveReview).toHaveBeenCalledWith(expect.anything(), 'global', '', '');
    await waitFor(() => expect(textarea).toHaveValue(''));
    expect(await previewOf('settings.reviewTemplate')).toHaveTextContent('# Verdict');
    expect(screen.getByText(i18n.t('settings.reviewTemplate.emptyOverrideHint'))).toBeInTheDocument();
  });

  it('keeps the selection and the unsaved edit when the switch is cancelled', async () => {
    const user = userEvent.setup();
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false);
    render(<TemplatesEditor scope="global" projectId="" canEdit onDirtyChange={vi.fn()} />);

    const textarea = await textareaOf('settings.planTemplate');
    await user.clear(textarea);
    await user.type(textarea, '# 編集途中');
    await user.click(listButton('review'));

    expect(confirm).toHaveBeenCalledWith(i18n.t('settings.unsavedChanges.confirmMessage'));
    expect(listButton('plan')).toHaveAttribute('aria-current', 'true');
    expect(textarea).toHaveValue('# 編集途中');
    expect(fetchReview).not.toHaveBeenCalled();
  });

  it('discards the edit on confirm and does not ask again on the next clean switch', async () => {
    const user = userEvent.setup();
    const onDirtyChange = vi.fn();
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true);
    render(<TemplatesEditor scope="global" projectId="" canEdit onDirtyChange={onDirtyChange} />);

    const textarea = await textareaOf('settings.planTemplate');
    await user.type(textarea, ' edited');
    await user.click(listButton('review'));

    expect(confirm).toHaveBeenCalledTimes(1);
    expect(await textareaOf('settings.reviewTemplate')).toHaveValue('review-tier');
    expect(onDirtyChange).toHaveBeenLastCalledWith(false);

    await user.click(listButton('report'));
    expect(confirm).toHaveBeenCalledTimes(1);
    expect(listButton('report')).toHaveAttribute('aria-current', 'true');
  });

  it('does not ask for confirmation when nothing was edited', async () => {
    const user = userEvent.setup();
    const confirm = vi.spyOn(window, 'confirm');
    render(<TemplatesEditor scope="global" projectId="" canEdit onDirtyChange={vi.fn()} />);
    await textareaOf('settings.planTemplate');

    await user.click(listButton('review'));

    expect(confirm).not.toHaveBeenCalled();
    expect(listButton('review')).toHaveAttribute('aria-current', 'true');
  });

  it('disables editing for every template when editing is not allowed', async () => {
    const user = userEvent.setup();
    render(<TemplatesEditor scope="project" projectId="" canEdit={false} onDirtyChange={vi.fn()} />);

    for (const [key, prefix] of [
      ['plan', 'settings.planTemplate'],
      ['review', 'settings.reviewTemplate'],
      ['report', 'settings.reportTemplate']
    ] as const) {
      await user.click(listButton(key));
      expect(await textareaOf(prefix)).toBeDisabled();
      expect(saveButton()).toBeDisabled();
    }
  });

  it('shows the error when loading a template fails', async () => {
    fetchPlan.mockRejectedValue(new Error('load failed'));
    render(<TemplatesEditor scope="global" projectId="" canEdit onDirtyChange={vi.fn()} />);

    expect(await screen.findByRole('alert')).toHaveTextContent('load failed');
  });

  it('keeps the input unsaved when saving a review override fails', async () => {
    const user = userEvent.setup();
    saveReview.mockRejectedValue(new Error('save failed'));
    render(<TemplatesEditor scope="global" projectId="" canEdit onDirtyChange={vi.fn()} />);
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
    render(<TemplatesEditor scope="global" projectId="" canEdit onDirtyChange={vi.fn()} />);
    await textareaOf('settings.planTemplate');
    await user.click(listButton('report'));

    const textarea = await textareaOf('settings.reportTemplate');
    await user.clear(textarea);
    await user.type(textarea, '<p>no markers</p>');
    await user.click(saveButton());

    expect(saveReport).toHaveBeenCalledWith(expect.anything(), 'global', '', '<p>no markers</p>');
    expect(savePlan).not.toHaveBeenCalled();
    expect(await screen.findByRole('alert')).toHaveTextContent('missing required marker data-report-template');
    expect(await previewOf('settings.reportTemplate')).toHaveTextContent('report-merged');
    expect(textarea).toHaveValue('<p>no markers</p>');
    expect(saveButton()).toBeEnabled();
  });

  describe('accessibility review (a11y F-1 / F-2)', () => {
    const deferred = <T,>() => {
      let resolve!: (v: T) => void;
      const promise = new Promise<T>(r => { resolve = r; });
      return { promise, resolve };
    };

    // Class pairs chosen for WCAG 1.4.3 (>= 4.5:1): slate-500 on white 4.76:1,
    // slate-400 on slate-900 6.96:1, emerald-700 on white 5.48:1,
    // emerald-400 on slate-900 well above 4.5:1.
    it('F-1: the loading text uses colours that meet 4.5:1 in light and dark', async () => {
      fetchPlan.mockReturnValue(new Promise(() => {}));
      render(<TemplatesEditor scope="global" projectId="" canEdit onDirtyChange={vi.fn()} />);

      const loading = (await screen.findByText(i18n.t('settings.common.loading'))).closest('div');
      expect(loading).toHaveClass('text-slate-500', 'dark:text-slate-400');
      expect(loading).not.toHaveClass('text-slate-400');
      expect(loading).not.toHaveClass('dark:text-slate-500');
    });

    it('F-1: the empty-save hint and the saved notice use colours that meet 4.5:1', async () => {
      const user = userEvent.setup();
      savePlan.mockResolvedValue({ tier_text: 'x', merged_text: 'x' });
      render(<TemplatesEditor scope="global" projectId="" canEdit onDirtyChange={vi.fn()} />);

      const textarea = await textareaOf('settings.planTemplate');
      const hint = screen.getByText(i18n.t('settings.planTemplate.emptyOverrideHint'));
      expect(hint).toHaveClass('text-slate-500', 'dark:text-slate-400');
      expect(hint).not.toHaveClass('text-slate-400');
      expect(hint).not.toHaveClass('dark:text-slate-500');

      await user.clear(textarea);
      await user.type(textarea, 'x');
      await user.click(saveButton());

      const status = await screen.findByRole('status');
      expect(status).toHaveTextContent(i18n.t('settings.common.saveSuccess'));
      expect(status).toHaveClass('text-emerald-700', 'dark:text-emerald-400');
      expect(status).not.toHaveClass('text-emerald-600');
    });

    it('F-2: saving with the keyboard moves focus to the textarea instead of losing it', async () => {
      const user = userEvent.setup();
      const pending = deferred<{ tier_text: string; merged_text: string }>();
      savePlan.mockReturnValue(pending.promise);
      render(<TemplatesEditor scope="global" projectId="" canEdit onDirtyChange={vi.fn()} />);

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

      expect(await screen.findByRole('status')).toBeInTheDocument();
      expect(saveButton()).toBeDisabled();
      expect(textarea).toHaveFocus();
    });

    it('F-2: focus also lands on the textarea when a keyboard save fails', async () => {
      const user = userEvent.setup();
      savePlan.mockRejectedValue(new Error('save failed'));
      render(<TemplatesEditor scope="global" projectId="" canEdit onDirtyChange={vi.fn()} />);

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
      render(<TemplatesEditor scope="global" projectId="" canEdit onDirtyChange={vi.fn()} />);

      const textarea = await textareaOf('settings.planTemplate');
      await user.type(textarea, ' edited');
      saveButton().focus();
      await user.keyboard('{Enter}');

      const reviewItem = listButton('review');
      act(() => reviewItem.focus());

      await act(async () => {
        pending.resolve({ tier_text: 'plan-tier edited', merged_text: 'plan-tier edited' });
      });

      expect(await screen.findByRole('status')).toBeInTheDocument();
      expect(reviewItem).toHaveFocus();
      expect(textarea).not.toHaveFocus();
    });
  });

  it('F-1 non-regression: switching language does not re-fetch or discard an unsaved edit', async () => {
    const user = userEvent.setup();
    render(<TemplatesEditor scope="global" projectId="" canEdit onDirtyChange={vi.fn()} />);

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
