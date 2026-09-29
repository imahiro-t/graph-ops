// DFLT-00293: in a window of 200 CSS px or less the top header shows icons
// only. The "GraphOps" name, the subtitle, the project name and the Launch
// Claude, language and New Ticket labels are visually hidden
// ([@media(max-width:200px)]:sr-only) but stay the buttons' accessible
// names, and Launch Claude and New Ticket become IconButtons whose visible
// tooltip (hover and keyboard focus) is on only at that width -- the script
// asks matchMedia('(max-width: 200px)'), the same query as the CSS. Wider
// than 200px nothing changes: no tooltip on those two, and New Ticket keeps
// its title while no project is selected.
//
// Without a project New Ticket is natively disabled; its description is the
// reason alone at every width (a hidden span referenced by
// aria-describedby), so the title never becomes the description and the name
// is not repeated in it. A disabled button cannot take keyboard focus, so
// its two-line tooltip (name, reason) is checked on hover only.
//
// jsdom computes no layout, so the sr-only classes are pinned here; the
// widths, the icon-only look and the tooltips' positions were measured in a
// real browser (see the ticket's implementation notes).
//
// fetch is served by test/fakeBackend.ts; matchMedia is replaced after
// installFakeBackend with one that answers per query.
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import i18n from './i18n';
import App from './App';
import { Project } from './types';
import { createFakeBackend, installFakeBackend } from './test/fakeBackend';
import { openIconButtonTooltip, openIconButtonTooltips } from './test/iconButtonTooltip';

const alpha: Project = { id: 'p-alpha', name: 'Alpha', prefix: 'ALP', local_path: '/work/alpha', created_at: '', updated_at: '' };

const TINY_QUERY = '(max-width: 200px)';
const SR_ONLY = '[@media(max-width:200px)]:sr-only';

// matches only for the tiny-window query, and only when `tiny`; every other
// query (the theme's prefers-color-scheme, prefers-reduced-motion) stays
// false, as installFakeBackend's stub has it.
function stubTinyWindow(tiny: boolean) {
  vi.stubGlobal(
    'matchMedia',
    vi.fn((query: string) => ({
      media: query,
      matches: tiny && query === TINY_QUERY,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn()
    }))
  );
  window.matchMedia = globalThis.matchMedia;
}

function seed({ tiny, withProject = true }: { tiny: boolean; withProject?: boolean }) {
  installFakeBackend(
    createFakeBackend({
      projects: [alpha],
      currentProjectId: withProject ? alpha.id : '',
      labels: [],
      tickets: withProject
        ? [{ id: 'ALP-00001', project_id: alpha.id, title: 'チケット', status: 'TODO' as const, priority: 'MEDIUM' as const, labelIds: [] }]
        : []
    })
  );
  stubTinyWindow(tiny);
}

async function renderApp({ withProject = true }: { withProject?: boolean } = {}) {
  render(<App />);
  const header = screen.getByRole('banner');
  if (withProject) await screen.findByText('ALP-00001');
  else await within(header).findByRole('button', { name: i18n.t('projectSwitcher.noProject') });
  return header;
}

function languageName(lng: 'ja' | 'en') {
  return i18n.t('header.language.toggleTitle', { lang: i18n.t(`header.language.${lng}`) });
}

afterEach(async () => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  vi.useRealTimers();
  await i18n.changeLanguage('ja');
});

describe.each(['ja', 'en'] as const)('top header in a window of 200px or less (%s)', lng => {
  it('hides the name, the subtitle and every button label visually only', async () => {
    seed({ tiny: true });
    await i18n.changeLanguage(lng);
    const header = await renderApp();
    const wordmark = within(header).getByText('GraphOps');
    expect(wordmark).toHaveClass(SR_ONLY);
    expect(within(header).getByText(i18n.t('header.subtitle'))).toHaveClass(SR_ONLY);
    // The logo stays (aria-hidden, decorative).
    const logo = header.querySelector('img') as HTMLImageElement;
    expect(logo).not.toHaveClass(SR_ONLY);
    expect(logo).toHaveAttribute('aria-hidden', 'true');

    const switcher = within(header).getByRole('button', { name: 'Alpha' });
    expect(within(switcher).getByText('Alpha')).toHaveClass('truncate', SR_ONLY);
    const launch = within(header).getByRole('button', { name: i18n.t('header.launchClaude') });
    expect(within(launch).getByText(i18n.t('header.launchClaude'))).toHaveClass(SR_ONLY);
    const language = within(header).getByRole('button', { name: languageName(lng) });
    expect(within(language).getByText(i18n.t(`header.language.${lng}`))).toHaveClass(SR_ONLY);
    const newTicket = within(header).getByRole('button', { name: i18n.t('header.newTicket') });
    expect(within(newTicket).getByText(i18n.t('header.newTicket'))).toHaveClass(SR_ONLY);
    // The icons stay visible.
    for (const button of [switcher, launch, language, newTicket]) {
      const icon = button.querySelector('svg') as SVGElement;
      expect(icon).toHaveAttribute('aria-hidden', 'true');
      expect(icon).not.toHaveClass(SR_ONLY);
    }
  });

  it('keeps every accessible name as before', async () => {
    seed({ tiny: true });
    await i18n.changeLanguage(lng);
    const header = await renderApp();
    const byName = (name: string) => within(header).getAllByRole('button', { name });
    expect(byName('Alpha')).toHaveLength(1);
    expect(byName(i18n.t('header.launchClaude'))).toHaveLength(1);
    expect(byName(languageName(lng))).toHaveLength(1);
    expect(byName(i18n.t('header.newTicket'))).toHaveLength(1);
    // No aria-label on the two that are named by their content.
    expect(byName(i18n.t('header.launchClaude'))[0]).not.toHaveAttribute('aria-label');
    expect(byName(i18n.t('header.newTicket'))[0]).not.toHaveAttribute('aria-label');
  });

  it.each([['header.launchClaude'], ['header.newTicket']])('shows the %s label as a tooltip on hover, and Escape closes it', async key => {
    seed({ tiny: true });
    await i18n.changeLanguage(lng);
    const user = userEvent.setup();
    const header = await renderApp();
    const button = within(header).getByRole('button', { name: i18n.t(key) });
    expect(openIconButtonTooltips()).toHaveLength(0);
    await user.hover(button);
    const tooltip = openIconButtonTooltip();
    expect(tooltip).toHaveTextContent(i18n.t(key), { normalizeWhitespace: true });
    expect(tooltip.textContent).toBe(i18n.t(key));
    expect(tooltip).toHaveAttribute('aria-hidden', 'true');
    await user.keyboard('{Escape}');
    expect(openIconButtonTooltips()).toHaveLength(0);
  });

  it.each([['header.launchClaude'], ['header.newTicket']])('shows the %s label as a tooltip on keyboard focus, and Escape closes it', async key => {
    seed({ tiny: true });
    await i18n.changeLanguage(lng);
    const user = userEvent.setup();
    const header = await renderApp();
    const button = within(header).getByRole('button', { name: i18n.t(key) });
    // Tab until the button has focus (keyboard focus, so :focus-visible).
    for (let i = 0; i < 20 && document.activeElement !== button; i++) await user.tab();
    expect(button).toHaveFocus();
    const tooltip = openIconButtonTooltip();
    expect(tooltip.textContent).toBe(i18n.t(key));
    await user.keyboard('{Escape}');
    expect(openIconButtonTooltips()).toHaveLength(0);
    expect(button).toHaveFocus();
  });

  it('shows the language tooltip as before, its text being the accessible name', async () => {
    seed({ tiny: true });
    await i18n.changeLanguage(lng);
    const user = userEvent.setup();
    const header = await renderApp();
    const button = within(header).getByRole('button', { name: languageName(lng) });
    await user.hover(button);
    const tooltip = openIconButtonTooltip();
    expect(tooltip.textContent).toBe(languageName(lng));
    expect(tooltip.textContent).toContain(i18n.t(`header.language.${lng}`));
  });
});

// DFLT-00293: the page pads 0.25rem at the sides in a window of 200px or
// less (was upto-15rem's 0.75rem), so the expanded ticket's sections keep
// three characters a line at 160px / 200% (see
// components/TicketItem.tinySectionsReflow.test.tsx).
it('pads the page less only in a window of 200px or less', async () => {
  seed({ tiny: true });
  await renderApp();
  expect(screen.getByRole('main')).toHaveClass('px-6', 'max-sm:px-3', 'upto-15rem:px-3', '[@media(max-width:200px)]:px-1');
});

describe.each(['ja', 'en'] as const)('top header wider than 200px (%s)', lng => {
  it('opens no tooltip on Launch Claude or New Ticket, by hover or keyboard focus', async () => {
    seed({ tiny: false });
    await i18n.changeLanguage(lng);
    const user = userEvent.setup();
    const header = await renderApp();
    for (const key of ['header.launchClaude', 'header.newTicket']) {
      const button = within(header).getByRole('button', { name: i18n.t(key) });
      await user.hover(button);
      expect(openIconButtonTooltips()).toHaveLength(0);
      await user.unhover(button);
      for (let i = 0; i < 20 && document.activeElement !== button; i++) await user.tab();
      expect(button).toHaveFocus();
      expect(openIconButtonTooltips()).toHaveLength(0);
    }
  });

  it('keeps the Tab order of the header controls', async () => {
    seed({ tiny: false });
    await i18n.changeLanguage(lng);
    const user = userEvent.setup();
    const header = await renderApp();
    const expected = [
      'Alpha',
      i18n.t('header.launchClaude'),
      languageName(lng),
      new RegExp(`^${i18n.t('header.theme.toggleTitle', { mode: '' }).split(':')[0]}`),
      i18n.t('header.settings'),
      i18n.t('header.newTicket')
    ];
    const buttons = expected.map(name => within(header).getByRole('button', { name }));
    await user.tab();
    for (const button of buttons) {
      expect(button).toHaveFocus();
      await user.tab();
    }
  });
});

describe.each([
  ['wider than 200px', false],
  ['200px or less', true]
] as const)('New Ticket without a project, %s', (_label, tiny) => {
  it('is disabled and described by the reason alone, through a hidden element', async () => {
    seed({ tiny, withProject: false });
    const header = await renderApp({ withProject: false });
    const button = within(header).getByRole('button', { name: i18n.t('header.newTicket') });
    expect(button).toBeDisabled();
    const reason = i18n.t('projectSwitcher.selectFirst');
    expect(button).toHaveAccessibleDescription(reason);
    expect(button).not.toHaveAccessibleDescription(new RegExp(i18n.t('header.newTicket')));
    const describedBy = button.getAttribute('aria-describedby');
    expect(describedBy).toBeTruthy();
    const description = document.getElementById(describedBy as string) as HTMLElement;
    expect(description).toHaveAttribute('hidden');
    expect(description.textContent).toBe(reason);
  });

  it(tiny ? 'has no title, and hover (only: disabled means no keyboard focus) opens a two-line tooltip: the name and the reason' : 'keeps the title with the reason and opens no visible tooltip on hover', async () => {
    seed({ tiny, withProject: false });
    const user = userEvent.setup();
    const header = await renderApp({ withProject: false });
    const button = within(header).getByRole('button', { name: i18n.t('header.newTicket') });
    const reason = i18n.t('projectSwitcher.selectFirst');
    // Hover is tracked on IconButton's wrapper, so it works on a natively
    // disabled button too.
    await user.hover(button.parentElement as HTMLElement);
    if (tiny) {
      expect(button).not.toHaveAttribute('title');
      const tooltip = openIconButtonTooltip();
      const lines = Array.from(tooltip.children).map(el => el.textContent);
      expect(lines).toEqual([i18n.t('header.newTicket'), reason]);
    } else {
      expect(button).toHaveAttribute('title', reason);
      expect(openIconButtonTooltips()).toHaveLength(0);
    }
  });
});

describe.each([
  ['wider than 200px', false],
  ['200px or less', true]
] as const)('New Ticket with a project, %s', (_label, tiny) => {
  it('has no description and no title', async () => {
    seed({ tiny });
    const header = await renderApp();
    const button = within(header).getByRole('button', { name: i18n.t('header.newTicket') });
    await waitFor(() => expect(button).toBeEnabled());
    expect(button).not.toHaveAttribute('aria-describedby');
    expect(button).not.toHaveAttribute('title');
    expect(button).toHaveAccessibleDescription('');
  });
});
