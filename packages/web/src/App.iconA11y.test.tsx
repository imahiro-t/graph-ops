// DFLT-00166: decorative lucide icons are hidden from assistive technology
// (aria-hidden="true"), and icon-only controls in the header and the pager
// still have an accessible name. The pager's previous/next buttons had no
// name at all before this ticket; they now carry an i18n aria-label.
//
// fetch is served by test/fakeBackend.ts, like App.labels.test.tsx.
import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from './i18n';
import ja from './i18n/locales/ja/translation.json';
import en from './i18n/locales/en/translation.json';
import App from './App';
import { Project } from './types';
import { FakeBackend, createFakeBackend, installFakeBackend } from './test/fakeBackend';
import { openIconButtonTooltip, openIconButtonTooltips } from './test/iconButtonTooltip';

const alpha: Project = { id: 'p-alpha', name: 'Alpha', prefix: 'ALP', local_path: '/work/alpha', created_at: '', updated_at: '' };

let backend: FakeBackend;

function seed(ticketCount: number) {
  const tickets = [];
  for (let i = 1; i <= ticketCount; i++) {
    tickets.push({
      id: `ALP-${String(i).padStart(5, '0')}`,
      project_id: alpha.id,
      title: `チケット ${i}`,
      status: 'TODO' as const,
      priority: 'MEDIUM' as const,
      labelIds: []
    });
  }
  backend = createFakeBackend({ projects: [alpha], currentProjectId: alpha.id, labels: [], tickets });
  installFakeBackend(backend);
}

async function renderApp() {
  const user = userEvent.setup();
  const utils = render(<App />);
  await screen.findByText('ALP-00001');
  return { ...utils, user };
}

const expectAllIconsHidden = (root: Element) => {
  const icons = root.querySelectorAll('svg.lucide');
  expect(icons.length).toBeGreaterThan(0);
  icons.forEach(icon => expect(icon).toHaveAttribute('aria-hidden', 'true'));
};

describe('App icon accessibility', () => {
  beforeEach(() => {
    seed(30);
  });

  afterEach(async () => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    await i18n.changeLanguage('ja');
  });

  it('hides every lucide icon in the header and the toolbar', async () => {
    const { container } = await renderApp();
    const header = container.querySelector('header')!;
    expectAllIconsHidden(header);
    // Representative decorative icons next to their own text.
    const newTicket = within(header).getByRole('button', { name: i18n.t('header.newTicket') });
    expectAllIconsHidden(newTicket);
    const launchClaude = within(header).getByRole('button', { name: i18n.t('header.launchClaude') });
    expectAllIconsHidden(launchClaude);
  });

  it('gives every header button, icon-only ones included, an accessible name', async () => {
    const { container } = await renderApp();
    const header = container.querySelector('header') as HTMLElement;
    const all = within(header).getAllByRole('button');
    const named = within(header).getAllByRole('button', { name: /\S/ });
    expect(named).toHaveLength(all.length);
    // The icon-only ones get theirs from aria-label (DFLT-00171).
    expect(within(header).getByRole('button', { name: i18n.t('header.settings') })).toBeInTheDocument();
    expect(within(header).getByRole('button', { name: i18n.t('toolbar.refreshTitle') })).toBeInTheDocument();
  });

  it('names the pager buttons and hides their chevrons', async () => {
    const { user } = await renderApp();
    const prev = screen.getByRole('button', { name: i18n.t('pagination.previous') });
    const next = screen.getByRole('button', { name: i18n.t('pagination.next') });
    expectAllIconsHidden(prev);
    expectAllIconsHidden(next);
    expect(prev).toBeDisabled();

    await user.click(next);
    expect(screen.getByText(i18n.t('pagination.pageOf', { page: 2, total: 3 }))).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: i18n.t('pagination.previous') }));
    expect(screen.getByText(i18n.t('pagination.pageOf', { page: 1, total: 3 }))).toBeInTheDocument();
  });

  it('has the pager labels in both languages', async () => {
    expect(ja.pagination.previous).toBe('前のページ');
    expect(ja.pagination.next).toBe('次のページ');
    expect(en.pagination.previous).toBe('Previous page');
    expect(en.pagination.next).toBe('Next page');

    await i18n.changeLanguage('en');
    await renderApp();
    expect(screen.getByRole('button', { name: 'Previous page' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Next page' })).toBeInTheDocument();
  });

  // DFLT-00171: the header's language / theme / settings buttons and the
  // toolbar's refresh button are named through aria-label, not title, and
  // show that name as a tooltip on keyboard focus too.
  it('names the language, theme, settings and refresh buttons through aria-label, with no title', async () => {
    const { container } = await renderApp();
    const header = container.querySelector('header') as HTMLElement;
    const names = [
      i18n.t('header.language.toggleTitle', { lang: i18n.t('header.language.ja') }),
      i18n.t('header.theme.toggleTitle', { mode: i18n.t('header.theme.system') }),
      i18n.t('header.settings'),
      i18n.t('toolbar.refreshTitle')
    ];
    for (const name of names) {
      const button = within(header).getByRole('button', { name });
      expect(button).toHaveAttribute('aria-label', name);
      expect(button).not.toHaveAttribute('title');
    }
    // The language button's name contains the text it shows (WCAG 2.5.3).
    const language = within(header).getByRole('button', { name: names[0] });
    expect(language).toHaveTextContent(i18n.t('header.language.ja'));
    expect(names[0]).toContain(i18n.t('header.language.ja'));
  });

  it('shows the settings button tooltip on keyboard focus and hides it on blur', async () => {
    const { user, container } = await renderApp();
    const header = container.querySelector('header') as HTMLElement;
    const settings = within(header).getByRole('button', { name: i18n.t('header.settings') });
    expect(openIconButtonTooltips()).toHaveLength(0);
    for (let i = 0; i < 30 && document.activeElement !== settings; i++) await user.tab();
    expect(settings).toHaveFocus();
    const tooltip = openIconButtonTooltip();
    expect(tooltip).toBeVisible();
    expect(tooltip).toHaveTextContent(i18n.t('header.settings'));
    await user.tab();
    expect(openIconButtonTooltips()).toHaveLength(0);
  });
});
