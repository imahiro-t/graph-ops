// Covers the #7 dependency-array fix (loadSkills must not re-run just
// because `selected` changed) and F-1's structural non-regression. See this
// ticket's plan sections 3-2 (#7/#8) and 4-2.
import { render, screen, waitFor } from '@testing-library/react';
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
  { name: 'create-ticket', has_user_override: false, has_team_override: false },
  { name: 'refine-ticket', has_user_override: false, has_team_override: false }
];

function stubFetchSkill() {
  mockedFetchSkill.mockImplementation(async (_t, _scope, _projectId, name: string) => ({
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
    render(<SkillsEditor scope="global" projectId="" canEdit onDirtyChange={vi.fn()} />);

    await waitFor(() => expect(mockedFetchSkill).toHaveBeenCalledWith(expect.anything(), 'global', '', 'create-ticket'));
    expect(await screen.findByDisplayValue('create-ticket-tier-text')).toBeInTheDocument();
  });

  // DFLT-00074
  it('labels the tier text textarea', async () => {
    render(<SkillsEditor scope="global" projectId="" canEdit onDirtyChange={vi.fn()} />);

    const textarea = await screen.findByDisplayValue('create-ticket-tier-text');
    expect(screen.getByLabelText(i18n.t('settings.skills.tierTextLabel'))).toBe(textarea);
  });

  it('#7 non-regression: changing the selection does not re-fetch the skill list', async () => {
    const user = userEvent.setup();
    render(<SkillsEditor scope="global" projectId="" canEdit onDirtyChange={vi.fn()} />);

    await screen.findByDisplayValue('create-ticket-tier-text');
    expect(mockedFetchSkills).toHaveBeenCalledTimes(1);

    const refineButton = screen.getByRole('button', { name: new RegExp(i18n.t('settings.skills.names.refineTicket')) });
    await user.click(refineButton);

    await screen.findByDisplayValue('refine-ticket-tier-text');
    expect(mockedFetchSkills).toHaveBeenCalledTimes(1);
    expect(mockedFetchSkill).toHaveBeenCalledWith(expect.anything(), 'global', '', 'refine-ticket');
  });

  it('F-1 non-regression: switching language after editing does not re-fetch and does not discard the unsaved edit', async () => {
    const user = userEvent.setup();
    render(<SkillsEditor scope="global" projectId="" canEdit onDirtyChange={vi.fn()} />);

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
});
