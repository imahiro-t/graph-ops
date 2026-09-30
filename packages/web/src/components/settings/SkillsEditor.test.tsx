// Covers the #7 dependency-array fix (loadSkills must not re-run just
// because `selected` changed) and F-1's structural non-regression. See this
// ticket's plan sections 3-2 (#7/#8) and 4-2.
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../../i18n';
import { SkillsEditor } from './SkillsEditor';
import { Deferred, deferred } from '../../test/deferred';
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

import { fetchSettingsSkill, fetchSettingsSkills, saveSettingsSkill } from '../../lib/settingsApi';

const mockedFetchSkills = fetchSettingsSkills as unknown as ReturnType<typeof vi.fn>;
const mockedFetchSkill = fetchSettingsSkill as unknown as ReturnType<typeof vi.fn>;
const mockedSaveSkill = saveSettingsSkill as unknown as ReturnType<typeof vi.fn>;

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

  // DFLT-00343: the first frame (renderToStaticMarkup renders once and runs
  // no effect) must show the loading line, not an empty editor, and the line
  // must go away when the first list load leaves nothing to load.
  describe('initial loading line', () => {
    it('shows the loading line, not an empty editor, before anything has loaded', () => {
      mockedFetchSkills.mockReturnValue(new Promise(() => {}));
      const html = renderToStaticMarkup(<SkillsEditor onDirtyChange={vi.fn()} />);
      expect(html).toContain(i18n.t('settings.common.loading'));
      expect(html).not.toContain('<textarea');
      expect(html).not.toContain(`${i18n.t('settings.common.save')}</button>`);
      expect(mockedFetchSkills).not.toHaveBeenCalled();
    });

    // DFLT-00350: a failed list load shows the error and a retry button in
    // place of the editor -- no empty textarea to save over stored text.
    it('drops the loading line and shows the error with a retry button, not the editor, when the list cannot be fetched', async () => {
      mockedFetchSkills.mockReset();
      mockedFetchSkills.mockRejectedValue(new Error('list failed'));
      render(<SkillsEditor onDirtyChange={vi.fn()} />);

      expect(screen.getByText(i18n.t('settings.common.loading'), { selector: '[role="status"]' })).toBeInTheDocument();
      expect(await screen.findByRole('alert')).toHaveTextContent('list failed');
      expect(screen.getByRole('button', { name: i18n.t('settings.common.retry') })).toBeInTheDocument();
      expect(screen.queryByText(i18n.t('settings.common.loading'))).not.toBeInTheDocument();
      expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
      expect(screen.queryByRole('button', { name: i18n.t('settings.common.save') })).not.toBeInTheDocument();
      expect(mockedFetchSkill).not.toHaveBeenCalled();
    });

    it('drops the loading line when the list is empty', async () => {
      mockedFetchSkills.mockReset();
      mockedFetchSkills.mockResolvedValue([]);
      render(<SkillsEditor onDirtyChange={vi.fn()} />);

      expect(screen.getByText(i18n.t('settings.common.loading'))).toBeInTheDocument();
      await waitFor(() => expect(screen.queryByText(i18n.t('settings.common.loading'))).not.toBeInTheDocument());
      expect(mockedFetchSkills).toHaveBeenCalledTimes(1);
      expect(mockedFetchSkill).not.toHaveBeenCalled();
    });
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
    expect(hint).toHaveClass('text-slate-500', 'dark:text-slate-400', 'text-[0.625rem]');
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
    expect(preview).toHaveClass('focus:outline-hidden', 'focus-visible:ring-2', 'focus-visible:ring-blue-500', 'max-h-40');

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

// DFLT-00261: as in NodeTypesEditor, below 48rem (`narrow:`, rem-based) the
// w-56 list stacks above the editor at full width with a capped height, the
// names wrap and the save row wraps; the wide classes stay. jsdom does no
// layout, so the classes are pinned.
describe('SkillsEditor narrow reflow (DFLT-00261)', () => {
  beforeEach(() => {
    mockedFetchSkills.mockReset();
    mockedFetchSkill.mockReset();
    mockedFetchSkills.mockResolvedValue(SKILLS);
    stubFetchSkill();
  });

  it('stacks the list above the editor below 48rem and keeps the side-by-side classes', async () => {
    const { container } = render(<SkillsEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('create-ticket-tier-text');

    const root = container.firstElementChild as HTMLElement;
    expect(root).toHaveClass('flex', 'h-full', 'min-h-0', 'gap-4', 'narrow:flex-col', 'narrow:h-auto');
    const list = root.querySelector('.w-56') as HTMLElement;
    expect(list).toHaveClass('w-56', 'shrink-0', 'overflow-y-auto', 'narrow:w-full', 'narrow:max-h-40');
    const names = list.querySelectorAll('span.truncate');
    expect(names.length).toBe(SKILLS.length);
    for (const name of names) expect(name).toHaveClass('narrow:whitespace-normal', 'narrow:wrap-anywhere');
    const saveRow = screen.getByRole('button', { name: i18n.t('settings.common.save') }).parentElement as HTMLElement;
    expect(saveRow).toHaveClass('flex', 'justify-end', 'narrow:flex-wrap');
  });

  // A-1: below 48rem the list is a max-h-40 scroll box under a sticky
  // heading, so an item focus scrolls into view must stop below the heading.
  it('keeps a focused list item from scrolling under the sticky heading', async () => {
    const { container } = render(<SkillsEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('create-ticket-tier-text');

    const list = (container.firstElementChild as HTMLElement).querySelector('.w-56') as HTMLElement;
    expect(list).toHaveClass('overflow-y-auto', 'scroll-pt-12');
    expect(list.firstElementChild).toHaveTextContent(i18n.t('settings.skills.listTitle'));
    expect(list.firstElementChild).toHaveClass('sticky', 'top-0', 'z-10');
  });

  // DFLT-00321: the selected skill's list button carries aria-current="true"
  // (the value TemplatesEditor uses), no other item carries it, and it
  // follows the selection.
  it('marks only the selected skill with aria-current, and moves it with the selection', async () => {
    const user = userEvent.setup();
    render(<SkillsEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('create-ticket-tier-text');

    const createButton = screen.getByRole('button', { name: new RegExp(i18n.t('settings.skills.names.createTicket')) });
    const refineButton = screen.getByRole('button', { name: new RegExp(i18n.t('settings.skills.names.refineTicket')) });
    expect(createButton).toHaveAttribute('aria-current', 'true');
    expect(refineButton).not.toHaveAttribute('aria-current');
    expect(document.querySelectorAll('[aria-current]')).toHaveLength(1);

    await user.click(refineButton);
    await screen.findByDisplayValue('refine-ticket-tier-text');
    expect(refineButton).toHaveAttribute('aria-current', 'true');
    expect(createButton).not.toHaveAttribute('aria-current');
    expect(document.querySelectorAll('[aria-current]')).toHaveLength(1);
  });
});

// DFLT-00350: load failures (the list, the selected skill's text), the retry
// that follows them, and what the right-hand pane shows right after a switch.
describe('SkillsEditor load failures and switching', () => {
  const retryButton = () => screen.getByRole('button', { name: i18n.t('settings.common.retry') });
  const refineItem = () => screen.getByRole('button', { name: i18n.t('settings.skills.names.refineTicket') });
  const loadingStatus = () => screen.queryByText(i18n.t('settings.common.loading'), { selector: '[role="status"]' });
  type Detail = { name: string; tier_text: string; merged_text: string };

  beforeEach(() => {
    mockedFetchSkills.mockReset();
    mockedFetchSkill.mockReset();
    mockedSaveSkill.mockReset();
    mockedFetchSkills.mockResolvedValue(SKILLS);
    stubFetchSkill();
  });

  it('shows the loading line as a status', () => {
    mockedFetchSkills.mockReturnValue(new Promise(() => {}));
    render(<SkillsEditor onDirtyChange={vi.fn()} />);
    expect(screen.getByRole('status')).toHaveTextContent(i18n.t('settings.common.loading'));
  });

  it('loads the list and the first skill on retry and moves focus to the editor pane', async () => {
    const user = userEvent.setup();
    const pending = deferred<SettingsSkillInfo[]>();
    mockedFetchSkills.mockRejectedValueOnce(new Error('list failed')).mockReturnValueOnce(pending.promise);
    render(<SkillsEditor onDirtyChange={vi.fn()} />);
    await screen.findByRole('alert');

    const button = retryButton();
    button.focus();
    await user.keyboard('{Enter}');
    expect(button).toHaveFocus();
    expect(button).toHaveAttribute('aria-busy', 'true');
    expect(screen.getByRole('alert')).toBeInTheDocument();

    await act(async () => { pending.resolve(SKILLS); });

    const textarea = await screen.findByDisplayValue('create-ticket-tier-text');
    await waitFor(() => expect(document.activeElement).toHaveAttribute('tabindex', '-1'));
    expect(document.activeElement?.contains(textarea)).toBe(true);
  });

  it('keeps the error and the retry button when the list retry fails again', async () => {
    const user = userEvent.setup();
    mockedFetchSkills.mockRejectedValue(new Error('list failed'));
    render(<SkillsEditor onDirtyChange={vi.fn()} />);
    const first = await screen.findByRole('alert');

    retryButton().focus();
    await user.keyboard('{Enter}');

    await waitFor(() => expect(screen.getByRole('alert')).not.toBe(first));
    expect(screen.getByRole('alert')).toHaveTextContent('list failed');
    expect(retryButton()).toHaveFocus();
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
  });

  it('leaves the loading line when the list retry returns an empty list', async () => {
    const user = userEvent.setup();
    mockedFetchSkills.mockRejectedValueOnce(new Error('list failed')).mockResolvedValueOnce([]);
    render(<SkillsEditor onDirtyChange={vi.fn()} />);
    await screen.findByRole('alert');

    await user.click(retryButton());

    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument());
    expect(loadingStatus()).not.toBeInTheDocument();
    expect(mockedFetchSkill).not.toHaveBeenCalled();
  });

  it('keeps the editor and shows a non-blocking error when the list re-fetch after a save fails', async () => {
    const user = userEvent.setup();
    mockedSaveSkill.mockResolvedValue({ name: 'create-ticket', tier_text: 'edited', merged_text: 'edited' });
    render(<SkillsEditor onDirtyChange={vi.fn()} />);
    const textarea = await screen.findByDisplayValue('create-ticket-tier-text');
    mockedFetchSkills.mockRejectedValueOnce(new Error('refresh failed'));

    await user.clear(textarea);
    await user.type(textarea, 'edited');
    await user.click(screen.getByRole('button', { name: i18n.t('settings.common.save') }));

    expect(await screen.findByText('refresh failed')).toBeInTheDocument();
    expect(screen.getByDisplayValue('edited')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: i18n.t('settings.common.retry') })).not.toBeInTheDocument();
  });

  it('shows the error and a retry button, not the editor, when the selected skill\'s text cannot be loaded, and loads it on retry', async () => {
    const user = userEvent.setup();
    const pending = deferred<Detail>();
    mockedFetchSkill.mockRejectedValueOnce(new Error('text failed')).mockReturnValueOnce(pending.promise);
    render(<SkillsEditor onDirtyChange={vi.fn()} />);

    expect(await screen.findByRole('alert')).toHaveTextContent('text failed');
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: i18n.t('settings.common.save') })).not.toBeInTheDocument();

    const button = retryButton();
    button.focus();
    await user.keyboard('{Enter}');
    expect(screen.getByRole('alert')).toBeInTheDocument();
    expect(button).toHaveFocus();
    expect(button).toHaveAttribute('aria-busy', 'true');
    expect(loadingStatus()).not.toBeInTheDocument();

    await act(async () => { pending.resolve({ name: 'create-ticket', tier_text: 'create-ticket-tier-text', merged_text: 'm' }); });

    const textarea = await screen.findByDisplayValue('create-ticket-tier-text');
    await waitFor(() => expect(textarea).toHaveFocus());
  });

  it('shows the loading line, not the previous skill\'s text, right after a switch', async () => {
    const user = userEvent.setup();
    render(<SkillsEditor onDirtyChange={vi.fn()} />);
    await screen.findByDisplayValue('create-ticket-tier-text');
    mockedFetchSkill.mockReturnValueOnce(new Promise(() => {}));

    await user.click(refineItem());

    expect(loadingStatus()).toBeInTheDocument();
    expect(screen.queryByDisplayValue('create-ticket-tier-text')).not.toBeInTheDocument();
    expect(screen.queryByText('create-ticket-merged-text')).not.toBeInTheDocument();
  });

  it('does not show the previous skill\'s load error after switching to another skill', async () => {
    const user = userEvent.setup();
    mockedFetchSkill.mockRejectedValueOnce(new Error('create failed'));
    render(<SkillsEditor onDirtyChange={vi.fn()} />);
    await screen.findByRole('alert');
    mockedFetchSkill.mockReturnValueOnce(new Promise(() => {}));

    await user.click(refineItem());

    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(loadingStatus()).toBeInTheDocument();
  });

  it.each([
    ['succeeds', (d: Deferred<Detail>) => d.resolve({ name: 'create-ticket', tier_text: 'late-create-text', merged_text: 'late' })],
    ['fails', (d: Deferred<Detail>) => d.reject(new Error('late create failure'))]
  ])('ignores the previous skill\'s answer that arrives after a switch (it %s)', async (_how, settle) => {
    const user = userEvent.setup();
    const first = deferred<Detail>();
    const second = deferred<Detail>();
    mockedFetchSkill.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
    render(<SkillsEditor onDirtyChange={vi.fn()} />);
    await waitFor(() => expect(mockedFetchSkill).toHaveBeenCalledTimes(1));

    await user.click(refineItem());
    await waitFor(() => expect(mockedFetchSkill).toHaveBeenCalledTimes(2));
    await act(async () => { second.resolve({ name: 'refine-ticket', tier_text: 'refine-ticket-tier-text', merged_text: 'r' }); });
    await screen.findByDisplayValue('refine-ticket-tier-text');

    await act(async () => { settle(first); });

    expect(screen.getByDisplayValue('refine-ticket-tier-text')).toBeInTheDocument();
    expect(screen.queryByDisplayValue('late-create-text')).not.toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });
});
