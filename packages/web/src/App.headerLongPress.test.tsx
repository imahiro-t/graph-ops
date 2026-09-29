// DFLT-00307: in a window of 200 CSS px or less the header's icon-only
// buttons (the project switcher, Launch Claude, language, New Ticket; see
// App.headerIconOnly.test.tsx for DFLT-00293) also open their visible
// tooltip on a touch long press, as a touch neither hovers nor moves
// keyboard focus. The long press does not run the button; a tap still does
// at once. Wider than 200px the option is off and nothing changes. Without
// a project New Ticket is natively disabled and, at 200px or less, takes
// pointer-events-none so the long press lands on IconButton's wrapper.
//
// jsdom has no PointerEvent: test/touchLongPress.ts dispatches pointer
// events carrying pointerType. matchMedia is replaced after
// installFakeBackend with one that answers the tiny-window query and can
// change it (a window growing past 200px).
import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import i18n from './i18n';
import App from './App';
import { Project } from './types';
import { createFakeBackend, installFakeBackend } from './test/fakeBackend';
import { openIconButtonTooltip, openIconButtonTooltips } from './test/iconButtonTooltip';
import { advance, firePointer, touchHold, touchLongPress, touchTap } from './test/touchLongPress';
import { LONG_PRESS_MS } from './components/IconButton';

const alpha: Project = { id: 'p-alpha', name: 'Alpha', prefix: 'ALP', local_path: '/work/alpha', created_at: '', updated_at: '' };

const TINY_QUERY = '(max-width: 200px)';
const LONG_PRESS_CLASSES = ['select-none', '[-webkit-touch-callout:none]'];

// A matchMedia whose tiny-window query can change; every other query stays
// false, as installFakeBackend's stub has it.
function stubTinyWindow(initial: boolean) {
  let tiny = initial;
  const listeners = new Set<() => void>();
  vi.stubGlobal(
    'matchMedia',
    vi.fn((query: string) => ({
      media: query,
      get matches() {
        return tiny && query === TINY_QUERY;
      },
      addEventListener: (_type: string, fn: () => void) => {
        if (query === TINY_QUERY) listeners.add(fn);
      },
      removeEventListener: (_type: string, fn: () => void) => {
        listeners.delete(fn);
      },
      addListener: vi.fn(),
      removeListener: vi.fn()
    }))
  );
  window.matchMedia = globalThis.matchMedia;
  return (next: boolean) => {
    tiny = next;
    act(() => {
      for (const fn of listeners) fn();
    });
  };
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
  return stubTinyWindow(tiny);
}

async function renderApp({ withProject = true }: { withProject?: boolean } = {}) {
  render(<App />);
  const header = screen.getByRole('banner');
  if (withProject) await screen.findByText('ALP-00001');
  else await within(header).findByRole('button', { name: i18n.t('projectSwitcher.noProject') });
  // From here on, the long-press timers are driven by hand.
  vi.useFakeTimers();
  return header;
}

function languageName(lng: 'ja' | 'en') {
  return i18n.t('header.language.toggleTitle', { lang: i18n.t(`header.language.${lng}`) });
}

type Key = 'switcher' | 'launch' | 'language' | 'newTicket';

function headerButton(header: HTMLElement, key: Key): HTMLElement {
  switch (key) {
    case 'switcher':
      return within(header).getByRole('button', { name: 'Alpha' });
    case 'launch':
      return within(header).getByRole('button', { name: i18n.t('header.launchClaude') });
    case 'language':
      return within(header).getByRole('button', { name: languageName('ja') });
    case 'newTicket':
      return within(header).getByRole('button', { name: i18n.t('header.newTicket') });
  }
}

// What each button does, checked after the press.
function actionHappened(key: Key, button: HTMLElement): boolean {
  switch (key) {
    case 'switcher':
      return button.getAttribute('aria-expanded') === 'true';
    case 'launch':
    case 'newTicket':
      return screen.queryAllByRole('dialog').length > 0;
    case 'language':
      return i18n.language === 'en';
  }
}

function expectedTooltip(key: Key): string[] {
  switch (key) {
    case 'switcher':
      return ['Alpha', '/work/alpha'];
    case 'launch':
      return [i18n.t('header.launchClaude')];
    case 'language':
      return [languageName('ja')];
    case 'newTicket':
      return [i18n.t('header.newTicket')];
  }
}

const KEYS: Key[] = ['switcher', 'launch', 'language', 'newTicket'];

afterEach(async () => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  await i18n.changeLanguage('ja');
});

describe('header in a window of 200px or less: a touch long press', () => {
  it.each(KEYS)('on %s opens its tooltip and does not run it', async key => {
    seed({ tiny: true });
    const header = await renderApp();
    const button = headerButton(header, key);
    expect(button).toHaveClass(...LONG_PRESS_CLASSES);
    touchLongPress(button);
    const tooltip = openIconButtonTooltip();
    for (const text of expectedTooltip(key)) expect(tooltip).toHaveTextContent(text);
    expect(actionHappened(key, button)).toBe(false);
  });

  it('on the switcher without a project shows "Select a project"', async () => {
    seed({ tiny: true, withProject: false });
    const header = await renderApp({ withProject: false });
    const switcher = within(header).getByRole('button', { name: i18n.t('projectSwitcher.noProject') });
    touchLongPress(switcher);
    expect(openIconButtonTooltip().textContent).toBe(i18n.t('projectSwitcher.noProject'));
    expect(switcher).toHaveAttribute('aria-expanded', 'false');
  });

  it('on the disabled New Ticket without a project (pointer-events-none, so on its wrapper) shows the name and the reason', async () => {
    seed({ tiny: true, withProject: false });
    const header = await renderApp({ withProject: false });
    const newTicket = within(header).getByRole('button', { name: i18n.t('header.newTicket') });
    expect(newTicket).toBeDisabled();
    expect(newTicket).toHaveClass('pointer-events-none');
    const wrapper = newTicket.parentElement!;
    touchHold(wrapper);
    firePointer(wrapper, 'pointerup');
    fireEvent.click(wrapper);
    const tooltip = openIconButtonTooltip();
    const lines = Array.from(tooltip.querySelectorAll('span.block')).map(el => el.textContent);
    expect(lines).toEqual([i18n.t('header.newTicket'), i18n.t('projectSwitcher.selectFirst')]);
    expect(screen.queryAllByRole('dialog')).toHaveLength(0);
  });

  it('on the switcher while its popup is open opens no tooltip', async () => {
    seed({ tiny: true });
    const header = await renderApp();
    const switcher = headerButton(header, 'switcher');
    touchTap(switcher);
    expect(switcher).toHaveAttribute('aria-expanded', 'true');
    touchHold(switcher);
    expect(openIconButtonTooltips()).toHaveLength(0);
  });

  it('is not applied to the theme and settings buttons', async () => {
    seed({ tiny: true });
    const header = await renderApp();
    const theme = within(header).getByRole('button', { name: new RegExp(`^${i18n.t('header.theme.toggleTitle', { mode: '' }).split(':')[0]}`) });
    const settings = within(header).getByRole('button', { name: i18n.t('header.settings') });
    for (const cls of LONG_PRESS_CLASSES) {
      expect(theme).not.toHaveClass(cls);
      expect(settings).not.toHaveClass(cls);
    }
    touchLongPress(settings);
    expect(screen.queryAllByRole('dialog').length).toBeGreaterThan(0);
  });

  it('leaves no tooltip when the window grows past 200px, and a tap then runs the button', async () => {
    const setTiny = seed({ tiny: true });
    const header = await renderApp();
    const launch = headerButton(header, 'launch');
    touchLongPress(launch, { click: false });
    expect(openIconButtonTooltip()).toHaveTextContent(i18n.t('header.launchClaude'));
    setTiny(false);
    expect(openIconButtonTooltips()).toHaveLength(0);
    touchTap(launch);
    expect(screen.queryAllByRole('dialog').length).toBeGreaterThan(0);
  });
});

describe('header in a window of 200px or less: a tap', () => {
  it.each(KEYS)('on %s runs it at once', async key => {
    seed({ tiny: true });
    const header = await renderApp();
    const button = headerButton(header, key);
    touchTap(button);
    expect(actionHappened(key, button)).toBe(true);
  });

  it('New Ticket with a project has no pointer-events-none', async () => {
    seed({ tiny: true });
    const header = await renderApp();
    expect(headerButton(header, 'newTicket')).not.toHaveClass('pointer-events-none');
  });
});

describe('header wider than 200px: no long press', () => {
  it.each(KEYS)('a touch held on %s opens no tooltip, and its click runs it as before', async key => {
    seed({ tiny: false });
    const header = await renderApp();
    const button = headerButton(header, key);
    firePointer(button, 'pointerdown');
    advance(LONG_PRESS_MS);
    expect(openIconButtonTooltips()).toHaveLength(0);
    firePointer(button, 'pointerup');
    fireEvent.click(button);
    expect(actionHappened(key, button)).toBe(true);
  });

  it('adds none of the long-press classes, and New Ticket without a project keeps its title and no pointer-events-none', async () => {
    seed({ tiny: false, withProject: false });
    const header = await renderApp({ withProject: false });
    const buttons = [
      within(header).getByRole('button', { name: i18n.t('projectSwitcher.noProject') }),
      within(header).getByRole('button', { name: i18n.t('header.launchClaude') }),
      within(header).getByRole('button', { name: languageName('ja') }),
      within(header).getByRole('button', { name: i18n.t('header.newTicket') })
    ];
    for (const button of buttons) for (const cls of LONG_PRESS_CLASSES) expect(button).not.toHaveClass(cls);
    const newTicket = buttons[3];
    expect(newTicket).not.toHaveClass('pointer-events-none');
    expect(newTicket).toHaveAttribute('title', i18n.t('projectSwitcher.selectFirst'));
  });
});
