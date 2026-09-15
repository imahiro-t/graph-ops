// Pins the wire shape of the template endpoints (DFLT-00071): plan/review
// overrides go out as `text`, while the report override keeps its `html`
// field -- sending the report HTML as `text` would reach the server as an
// empty `html` and silently clear the override.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';

vi.mock('./apiFetch', () => ({ apiFetch: vi.fn() }));

import { apiFetch } from './apiFetch';
import {
  fetchSettingsPlanTemplate,
  fetchSettingsReviewTemplate,
  saveSettingsPlanTemplate,
  saveSettingsReportTemplate,
  saveSettingsReviewTemplate
} from './settingsApi';

const mockedApiFetch = apiFetch as unknown as ReturnType<typeof vi.fn>;

function lastCall(): { path: string; init: RequestInit | undefined } {
  const [path, init] = mockedApiFetch.mock.calls[mockedApiFetch.mock.calls.length - 1];
  return { path: String(path), init };
}

describe('settingsApi template endpoints', () => {
  beforeEach(() => {
    mockedApiFetch.mockReset();
    mockedApiFetch.mockImplementation(async () =>
      new Response(JSON.stringify({ tier_text: 't', merged_text: 'm' }), { status: 200 })
    );
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('fetches the plan and review templates with the scope query', async () => {
    await expect(fetchSettingsPlanTemplate(i18n.t, 'global', '')).resolves.toEqual({ tier_text: 't', merged_text: 'm' });
    expect(lastCall().path).toBe('/api/settings/plan-template?scope=global');

    await fetchSettingsReviewTemplate(i18n.t, 'project', 'proj-A');
    expect(lastCall().path).toBe('/api/settings/review-template?scope=project&project_id=proj-A');
  });

  it('sends plan and review overrides in the text field', async () => {
    await saveSettingsPlanTemplate(i18n.t, 'global', '', '# 目的');
    expect(lastCall().path).toBe('/api/settings/plan-template');
    expect(lastCall().init?.method).toBe('PUT');
    expect(JSON.parse(String(lastCall().init?.body))).toEqual({ scope: 'global', project_id: '', text: '# 目的' });

    await saveSettingsReviewTemplate(i18n.t, 'project', 'proj-A', '');
    expect(lastCall().path).toBe('/api/settings/review-template');
    expect(JSON.parse(String(lastCall().init?.body))).toEqual({ scope: 'project', project_id: 'proj-A', text: '' });
  });

  it('keeps sending the report override in the html field', async () => {
    await saveSettingsReportTemplate(i18n.t, 'global', '', '<html></html>');
    const body = JSON.parse(String(lastCall().init?.body));
    expect(lastCall().path).toBe('/api/settings/report-template');
    expect(body).toEqual({ scope: 'global', project_id: '', html: '<html></html>' });
    expect(body).not.toHaveProperty('text');
  });
});
