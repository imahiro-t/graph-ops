// DFLT-00170: the toolbar's search box has an accessible name of its own
// (an i18n aria-label), instead of borrowing the placeholder, which disappears
// as soon as the user types and is not treated as a name by every assistive
// technology (WCAG 1.3.1 / 3.3.2 / 4.1.2).
//
// fetch is served by test/fakeBackend.ts, like App.iconA11y.test.tsx.
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from './i18n';
import ja from './i18n/locales/ja/translation.json';
import en from './i18n/locales/en/translation.json';
import App from './App';
import { Project } from './types';
import { createFakeBackend, installFakeBackend } from './test/fakeBackend';

const alpha: Project = { id: 'p-alpha', name: 'Alpha', prefix: 'ALP', local_path: '/work/alpha', created_at: '', updated_at: '' };

async function renderApp() {
  const user = userEvent.setup();
  const utils = render(<App />);
  await screen.findByText('ALP-00001');
  return { ...utils, user };
}

describe('App toolbar search box name', () => {
  beforeEach(() => {
    installFakeBackend(
      createFakeBackend({
        projects: [alpha],
        currentProjectId: alpha.id,
        labels: [],
        tickets: [
          { id: 'ALP-00001', project_id: alpha.id, title: 'チケット 1', status: 'TODO', priority: 'MEDIUM', labelIds: [] }
        ]
      })
    );
  });

  afterEach(async () => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    await i18n.changeLanguage('ja');
  });

  it.each([
    ['ja', ja.toolbar.searchLabel, ja.toolbar.searchPlaceholder],
    ['en', en.toolbar.searchLabel, en.toolbar.searchPlaceholder]
  ])('names the search box in %s independently of its placeholder', async (lang, label, placeholder) => {
    await i18n.changeLanguage(lang);
    await renderApp();

    const byName = screen.getByRole('textbox', { name: label });
    // The same field the existing tests reach through its placeholder.
    expect(byName).toBe(screen.getByPlaceholderText(placeholder));
    // The name comes from aria-label, not from the placeholder text.
    expect(byName).toHaveAttribute('aria-label', label);
    expect(byName).toHaveAccessibleName(label);
    expect(label).not.toBe(placeholder);
  });

  it('keeps the same name after the user has typed and the placeholder is gone', async () => {
    const { user } = await renderApp();
    const label = i18n.t('toolbar.searchLabel');
    const input = screen.getByRole('textbox', { name: label });

    await user.type(input, 'ALP');

    expect(input).toHaveValue('ALP');
    const afterTyping = screen.getByRole('textbox', { name: label });
    expect(afterTyping).toBe(input);
    expect(afterTyping).toHaveAccessibleName(label);
  });
});
