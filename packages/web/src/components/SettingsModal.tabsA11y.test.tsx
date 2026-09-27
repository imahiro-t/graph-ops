// DFLT-00254: the settings modal's seven tabs follow the WAI-ARIA tabs
// pattern, like the artifact tabs in TicketItem (DFLT-00243): a named
// tablist, tabs with aria-selected and a roving tabIndex, one tabpanel
// labelled by the selected tab, and arrow / Home / End keys that go through
// the same unsaved-changes confirmation as a click.
import { act, render, screen, waitFor, within } from '@testing-library/react';
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
    fetchSettingsCatalog: vi.fn(),
    fetchAppSettings: vi.fn(),
    fetchSettingsSkills: vi.fn(),
    fetchSettingsSkill: vi.fn(),
    fetchSettingsPlanTemplate: vi.fn(),
    fetchSettingsReviewTemplate: vi.fn(),
    fetchSettingsReportTemplate: vi.fn()
  };
});

import {
  fetchAppSettings,
  fetchSettingsCatalog,
  fetchSettingsNodeType,
  fetchSettingsNodeTypes,
  fetchSettingsPlanTemplate,
  fetchSettingsReportTemplate,
  fetchSettingsReviewTemplate,
  fetchSettingsSkill,
  fetchSettingsSkills
} from '../lib/settingsApi';

type Mock = ReturnType<typeof vi.fn>;

const TAB_KEYS = ['nodeTypes', 'reviewGates', 'skills', 'templates', 'labels', 'autopilot', 'appSettings'] as const;
type TabKey = (typeof TAB_KEYS)[number];

const modal = (
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

const tabName = (key: TabKey) => i18n.t(`settings.tabs.${key}`);
const getTab = (key: TabKey) => screen.getByRole('tab', { name: tabName(key) });

// Selects `key` by clicking it (no unsaved edit exists yet) and leaves focus
// on it, as a keyboard user would have after reaching it.
async function selectAndFocus(user: ReturnType<typeof userEvent.setup>, key: TabKey) {
  await user.click(getTab(key));
  await waitFor(() => expect(getTab(key)).toHaveAttribute('aria-selected', 'true'));
  act(() => getTab(key).focus());
  expect(getTab(key)).toHaveFocus();
}

// Makes an unsaved edit in the plan template of the Templates tab, as
// SettingsModal.test.tsx does, and leaves focus on the Templates tab.
async function makeUnsavedTemplateEdit(user: ReturnType<typeof userEvent.setup>) {
  await user.click(getTab('templates'));
  const textarea = await screen.findByDisplayValue('plan-tier');
  await user.type(textarea, ' edited');
  act(() => getTab('templates').focus());
  expect(getTab('templates')).toHaveFocus();
  return textarea;
}

// Each tab's content is identified by the request only that tab's editor
// makes, plus the panel's label.
async function expectPanelShows(key: TabKey) {
  const panel = screen.getByRole('tabpanel');
  switch (key) {
    case 'nodeTypes':
      await waitFor(() => expect(fetchSettingsNodeTypes).toHaveBeenCalled());
      break;
    case 'reviewGates':
      await waitFor(() => expect(fetchSettingsCatalog).toHaveBeenCalled());
      break;
    case 'appSettings':
      await waitFor(() => expect(fetchAppSettings).toHaveBeenCalled());
      break;
    default:
      break;
  }
  expect(panel).toHaveAttribute('aria-labelledby', getTab(key).id);
}

describe('SettingsModal tabs (WAI-ARIA tabs pattern)', () => {
  beforeEach(() => {
    (fetchSettingsNodeTypes as unknown as Mock).mockResolvedValue([]);
    (fetchSettingsNodeType as unknown as Mock).mockResolvedValue({ type: '', tier_text: '', merged_text: '' });
    // The review-gate and app-settings editors are only checked for having
    // started loading; their requests never answer.
    (fetchSettingsCatalog as unknown as Mock).mockReturnValue(new Promise(() => {}));
    (fetchAppSettings as unknown as Mock).mockReturnValue(new Promise(() => {}));
    (fetchSettingsSkills as unknown as Mock).mockResolvedValue([]);
    (fetchSettingsSkill as unknown as Mock).mockResolvedValue({ name: '', tier_text: '', merged_text: '' });
    (fetchSettingsPlanTemplate as unknown as Mock).mockResolvedValue({ tier_text: 'plan-tier', merged_text: 'plan-merged' });
    (fetchSettingsReviewTemplate as unknown as Mock).mockResolvedValue({ tier_text: 'review-tier', merged_text: 'review-merged' });
    (fetchSettingsReportTemplate as unknown as Mock).mockResolvedValue({ tier_text: '', merged_text: '' });
  });

  afterEach(async () => {
    vi.clearAllMocks();
    await i18n.changeLanguage('ja');
  });

  it('has one named tablist with the seven tabs in order', async () => {
    render(modal);

    const tablists = screen.getAllByRole('tablist');
    expect(tablists).toHaveLength(1);
    expect(tablists[0]).toHaveAccessibleName('設定の項目');
    const tabs = within(tablists[0]).getAllByRole('tab');
    expect(tabs.map(tab => tab.textContent)).toEqual([
      'ノード種別',
      'レビューゲート',
      'スキル',
      'テンプレート',
      'ラベル',
      'オートパイロット',
      'アプリ設定'
    ]);
    for (const tab of tabs) {
      expect(tab).toHaveAttribute('type', 'button');
    }
    await waitFor(() => expect(fetchSettingsNodeTypes).toHaveBeenCalled());
  });

  it('marks only the selected tab with aria-selected and tabIndex 0 (roving tabIndex)', async () => {
    const user = userEvent.setup();
    render(modal);

    const expectSelected = (selected: TabKey) => {
      for (const key of TAB_KEYS) {
        expect(getTab(key)).toHaveAttribute('aria-selected', key === selected ? 'true' : 'false');
        expect(getTab(key).tabIndex).toBe(key === selected ? 0 : -1);
      }
    };
    expectSelected('nodeTypes');

    await user.click(getTab('skills'));
    await waitFor(() => expectSelected('skills'));
  });

  it('ties the tabs and the tabpanel together by id', async () => {
    const user = userEvent.setup();
    render(modal);

    const panels = screen.getAllByRole('tabpanel');
    expect(panels).toHaveLength(1);
    const panel = panels[0];
    const ids = TAB_KEYS.map(key => getTab(key).id);
    for (const id of ids) expect(id).not.toBe('');
    expect(new Set(ids).size).toBe(ids.length);
    expect(panel.id).not.toBe('');
    for (const key of TAB_KEYS) {
      expect(getTab(key)).toHaveAttribute('aria-controls', panel.id);
    }
    expect(panel).toHaveAttribute('aria-labelledby', getTab('nodeTypes').id);
    expect(panel).toHaveAccessibleName(tabName('nodeTypes'));
    expect(panel.tabIndex).toBe(0);
    expect(panel).toHaveClass('focus-visible:ring-2', 'focus-visible:ring-inset');

    await user.click(getTab('templates'));
    await waitFor(() => expect(panel).toHaveAttribute('aria-labelledby', getTab('templates').id));
    await screen.findByDisplayValue('plan-tier');
  });

  it('keeps ids unique when two modals are rendered at once', async () => {
    render(
      <>
        {modal}
        {modal}
      </>
    );

    const ids = [...screen.getAllByRole('tab'), ...screen.getAllByRole('tabpanel')].map(el => el.id);
    expect(ids).toHaveLength(16);
    expect(new Set(ids).size).toBe(16);
    await waitFor(() => expect(fetchSettingsNodeTypes).toHaveBeenCalled());
  });

  it.each<[TabKey, string, TabKey]>([
    ['nodeTypes', 'ArrowRight', 'reviewGates'],
    ['reviewGates', 'ArrowLeft', 'nodeTypes'],
    ['appSettings', 'ArrowRight', 'nodeTypes'],
    ['nodeTypes', 'ArrowLeft', 'appSettings'],
    ['skills', 'Home', 'nodeTypes'],
    ['skills', 'End', 'appSettings']
  ])('moves selection and focus from %s with %s to %s', async (start, key, target) => {
    const user = userEvent.setup();
    render(modal);
    await selectAndFocus(user, start);

    await user.keyboard(`{${key}}`);

    await waitFor(() => expect(getTab(target)).toHaveAttribute('aria-selected', 'true'));
    await waitFor(() => expect(getTab(target)).toHaveFocus());
    await expectPanelShows(target);
  });

  it.each(['Alt', 'Control', 'Meta', 'Shift'])('ignores ArrowRight pressed with %s', async modifier => {
    const user = userEvent.setup();
    render(modal);
    await selectAndFocus(user, 'nodeTypes');

    await user.keyboard(`{${modifier}>}{ArrowRight}{/${modifier}}`);

    expect(getTab('nodeTypes')).toHaveAttribute('aria-selected', 'true');
    expect(getTab('reviewGates')).toHaveAttribute('aria-selected', 'false');
    expect(getTab('nodeTypes')).toHaveFocus();
  });

  it('asks before a key moves away from an unsaved edit, and stays put when cancelled', async () => {
    const user = userEvent.setup();
    render(modal);
    const textarea = await makeUnsavedTemplateEdit(user);

    await user.keyboard('{ArrowRight}');
    expect(screen.getByTestId('settings-discard-confirm')).toBeInTheDocument();

    await user.click(screen.getByTestId('settings-discard-confirm-cancel'));

    expect(screen.queryByTestId('settings-discard-confirm')).not.toBeInTheDocument();
    expect(getTab('templates')).toHaveAttribute('aria-selected', 'true');
    expect(getTab('labels')).toHaveAttribute('aria-selected', 'false');
    await waitFor(() => expect(getTab('templates')).toHaveFocus());
    expect(textarea).toHaveValue('plan-tier edited');
  });

  it('moves to the key-selected tab, with focus, once the discard is confirmed', async () => {
    const user = userEvent.setup();
    render(modal);
    await makeUnsavedTemplateEdit(user);

    await user.keyboard('{End}');
    expect(screen.getByTestId('settings-discard-confirm')).toBeInTheDocument();

    await user.click(screen.getByTestId('settings-discard-confirm-confirm'));

    expect(screen.queryByTestId('settings-discard-confirm')).not.toBeInTheDocument();
    await waitFor(() => expect(getTab('appSettings')).toHaveAttribute('aria-selected', 'true'));
    await waitFor(() => expect(getTab('appSettings')).toHaveFocus());
    await expectPanelShows('appSettings');
  });

  it('still asks before a click moves away from an unsaved edit', async () => {
    const user = userEvent.setup();
    render(modal);
    await makeUnsavedTemplateEdit(user);

    await user.click(getTab('reviewGates'));

    expect(screen.getByTestId('settings-discard-confirm')).toBeInTheDocument();
  });

  it('tabs from the close button to the selected tab, then to the tabpanel, skipping the other tabs', async () => {
    const user = userEvent.setup();
    render(modal);
    const focused: Element[] = [];
    const recordFocus = (e: FocusEvent) => focused.push(e.target as Element);
    document.addEventListener('focusin', recordFocus);

    expect(screen.getByRole('button', { name: i18n.t('common.closeDialog') })).toHaveFocus();
    await user.tab();
    expect(getTab('nodeTypes')).toHaveFocus();
    await user.tab();
    expect(screen.getByRole('tabpanel')).toHaveFocus();

    for (const key of TAB_KEYS.filter(k => k !== 'nodeTypes')) {
      expect(focused).not.toContain(getTab(key));
    }
    document.removeEventListener('focusin', recordFocus);
    await waitFor(() => expect(fetchSettingsNodeTypes).toHaveBeenCalled());
  });

  it('names the tablist in English when the UI language is English', async () => {
    await act(async () => {
      await i18n.changeLanguage('en');
    });
    render(modal);

    expect(screen.getByRole('tablist')).toHaveAccessibleName('Settings sections');
    await waitFor(() => expect(fetchSettingsNodeTypes).toHaveBeenCalled());
  });
});
