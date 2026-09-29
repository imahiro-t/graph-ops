// DFLT-00287: the node types, skills and templates editors share their
// two-column list classes (listPane.ts) instead of each spelling them out,
// and every list item button draws an inset focus ring so the scrolling list
// does not clip its left and right edges. jsdom does no layout, so the
// classes are pinned; the ring was measured in a real browser (see the
// ticket's implementation notes).
import { render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../../i18n';
import { NodeTypesEditor } from './NodeTypesEditor';
import { SkillsEditor } from './SkillsEditor';
import { TemplatesEditor } from './TemplatesEditor';
import { LIST_HEADING_CLASS, LIST_ITEM_FOCUS_CLASS, LIST_LAYOUT_CLASS, LIST_PANE_CLASS } from './listPane';

vi.mock('../../lib/settingsApi', async () => {
  const actual = await vi.importActual<typeof import('../../lib/settingsApi')>('../../lib/settingsApi');
  return {
    ...actual,
    fetchSettingsNodeTypes: vi.fn(),
    fetchSettingsNodeType: vi.fn(),
    saveSettingsNodeType: vi.fn(),
    fetchSettingsSkills: vi.fn(),
    fetchSettingsSkill: vi.fn(),
    saveSettingsSkill: vi.fn(),
    fetchSettingsPlanTemplate: vi.fn(),
    saveSettingsPlanTemplate: vi.fn(),
    fetchSettingsReviewTemplate: vi.fn(),
    saveSettingsReviewTemplate: vi.fn(),
    fetchSettingsReportTemplate: vi.fn(),
    saveSettingsReportTemplate: vi.fn()
  };
});

import {
  fetchSettingsNodeType,
  fetchSettingsNodeTypes,
  fetchSettingsPlanTemplate,
  fetchSettingsSkill,
  fetchSettingsSkills
} from '../../lib/settingsApi';

type Mock = ReturnType<typeof vi.fn>;

const classesOf = (value: string) => value.split(/\s+/).filter(Boolean);
// Assembled at run time so this file adds no px text rule to the app's CSS
// (Tailwind scans src/ for class candidates).
const pxText = (n: number) => ['text-[', String(n), 'px]'].join('');

interface Case {
  name: string;
  flexCol: boolean;
  render: () => Promise<{ root: HTMLElement; itemButtons: HTMLElement[] }>;
}

const CASES: Case[] = [
  {
    name: 'NodeTypesEditor',
    flexCol: true,
    render: async () => {
      const { container } = render(<NodeTypesEditor onDirtyChange={vi.fn()} />);
      await screen.findByDisplayValue('implementation-tier-text');
      const buttons = ['nodeType.implementation', 'nodeType.review'].map(key =>
        screen.getByRole('button', { name: new RegExp(`^${i18n.t(key)}`) })
      );
      return { root: container.firstElementChild as HTMLElement, itemButtons: buttons };
    }
  },
  {
    name: 'SkillsEditor',
    flexCol: false,
    render: async () => {
      const { container } = render(<SkillsEditor onDirtyChange={vi.fn()} />);
      await screen.findByDisplayValue('create-ticket-tier-text');
      const buttons = ['create-ticket', 'refine-ticket'].map(name => screen.getByRole('button', { name: new RegExp(name) }));
      return { root: container.firstElementChild as HTMLElement, itemButtons: buttons };
    }
  },
  {
    name: 'TemplatesEditor',
    flexCol: true,
    render: async () => {
      const { container } = render(<TemplatesEditor onDirtyChange={vi.fn()} />);
      await screen.findByLabelText(i18n.t('settings.planTemplate.tierTextLabel'));
      const buttons = (['plan', 'review', 'report'] as const).map(key =>
        screen.getByRole('button', { name: i18n.t(`settings.templates.list.${key}`) })
      );
      return { root: container.firstElementChild as HTMLElement, itemButtons: buttons };
    }
  }
];

describe('two-column list classes shared by the settings editors (DFLT-00287)', () => {
  beforeEach(async () => {
    await i18n.changeLanguage('ja');
    (fetchSettingsNodeTypes as unknown as Mock).mockResolvedValue([
      { type: 'implementation', has_default: true, has_user_override: false },
      { type: 'review', has_default: true, has_user_override: true }
    ]);
    (fetchSettingsNodeType as unknown as Mock).mockImplementation(async (_t, type: string) => ({
      type,
      tier_text: `${type}-tier-text`,
      merged_text: `${type}-merged-text`
    }));
    (fetchSettingsSkills as unknown as Mock).mockResolvedValue([
      { name: 'create-ticket', has_user_override: false },
      { name: 'refine-ticket', has_user_override: true }
    ]);
    (fetchSettingsSkill as unknown as Mock).mockImplementation(async (_t, name: string) => ({
      name,
      tier_text: `${name}-tier-text`,
      merged_text: `${name}-merged-text`
    }));
    (fetchSettingsPlanTemplate as unknown as Mock).mockResolvedValue({ tier_text: 'plan-tier', merged_text: 'plan-merged' });
  });

  afterEach(async () => {
    vi.clearAllMocks();
    await i18n.changeLanguage('ja');
  });

  it('keeps the shared class strings complete and rem-based', () => {
    for (const value of [LIST_LAYOUT_CLASS, LIST_PANE_CLASS, LIST_HEADING_CLASS, LIST_ITEM_FOCUS_CLASS]) {
      expect(value).not.toMatch(/\$\{|undefined/);
    }
    expect(classesOf(LIST_PANE_CLASS)).not.toContain('flex-col');
    expect(classesOf(LIST_HEADING_CLASS)).toContain('text-[0.6875rem]');
    expect(classesOf(LIST_HEADING_CLASS)).not.toContain(pxText(11));
  });

  it.each(CASES)('$name uses the shared layout, list, heading and item focus classes', async c => {
    const { root, itemButtons } = await c.render();

    expect(root).toHaveClass(...classesOf(LIST_LAYOUT_CLASS));
    const pane = root.querySelector('.w-56') as HTMLElement;
    expect(pane).toHaveClass(...classesOf(LIST_PANE_CLASS));
    if (c.flexCol) {
      expect(pane).toHaveClass('flex', 'flex-col');
    } else {
      expect(pane).not.toHaveClass('flex');
      expect(pane).not.toHaveClass('flex-col');
    }

    const heading = pane.firstElementChild as HTMLElement;
    expect(heading).toHaveClass(...classesOf(LIST_HEADING_CLASS));
    expect(heading).toHaveClass('sticky', 'top-0', 'z-10', 'text-[0.6875rem]');
    expect(heading).not.toHaveClass(pxText(11));

    expect(itemButtons.length).toBeGreaterThan(0);
    for (const button of itemButtons) {
      expect(pane).toContainElement(button);
      expect(button).toHaveClass('focus:outline-hidden', 'focus-visible:ring-2', 'focus-visible:ring-inset', 'focus-visible:ring-blue-500');
    }

    // The editor's hint line (below the text box) is rem-sized as well.
    const hints = root.querySelectorAll('p.text-\\[0\\.625rem\\]');
    expect(hints.length).toBeGreaterThan(0);
    for (const hint of hints) expect(hint).not.toHaveClass(pxText(10));
  });
});
