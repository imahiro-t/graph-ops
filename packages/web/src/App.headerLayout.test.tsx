// DFLT-00220: on a narrow screen (375px, 320px) the app header used to be
// wider than the window (a horizontal scrollbar on every page) and, being
// sticky and several lines tall once it wraps, covered the expanded ticket's
// Action Footer while scrolling. The header now wraps within the window, is
// sticky only from the lg breakpoint up, and the other elements that widened
// the page on the ticket detail (the summary's figures, the family links)
// wrap or shrink too.
//
// jsdom computes no layout and applies no media queries, so these tests pin
// the classes that produce that behaviour. The behaviour itself --
// document.documentElement.scrollWidth no wider than the window at 375px and
// 320px, the header's controls and the Action Footer's buttons hit by
// elementFromPoint, and an unchanged header at 1024px and 1280px -- was
// measured in a real browser (see the ticket's implementation notes).
//
// fetch is served by test/fakeBackend.ts, like App.iconA11y.test.tsx.
import { render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from './i18n';
import App from './App';
import { Project } from './types';
import { TicketFamily } from './components/TicketFamily';
import { createFakeBackend, installFakeBackend } from './test/fakeBackend';

const alpha: Project = { id: 'p-alpha', name: 'Alpha', prefix: 'ALP', local_path: '/work/alpha', created_at: '', updated_at: '' };

function seed() {
  const backend = createFakeBackend({
    projects: [alpha],
    currentProjectId: alpha.id,
    labels: [],
    tickets: [{ id: 'ALP-00001', project_id: alpha.id, title: 'チケット 1', status: 'TODO', priority: 'MEDIUM', labelIds: [] }]
  });
  installFakeBackend(backend);
}

async function renderHeader() {
  render(<App />);
  await screen.findByText('ALP-00001');
  return screen.getByRole('banner');
}

const classesOf = (el: Element) => el.className.split(/\s+/).filter(Boolean);

describe('App header layout on narrow screens', () => {
  beforeEach(() => {
    seed();
  });

  afterEach(async () => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    await i18n.changeLanguage('ja');
  });

  it('is sticky only from lg up, keeping its stacking order and shadow', async () => {
    const header = await renderHeader();
    const classes = classesOf(header);
    expect(classes).toEqual(expect.arrayContaining(['lg:sticky', 'lg:top-0', 'z-30', 'shadow-xs']));
    // Below lg it scrolls away with the page instead of covering the
    // Action Footer. `relative` keeps z-30 in effect there, so the popups
    // inside the header still open in front of <main>.
    expect(classes).toContain('relative');
    expect(classes).not.toContain('sticky');
    expect(classes).not.toContain('top-0');
  });

  it('uses the narrower side padding below lg and the old one from lg up', async () => {
    const classes = classesOf(await renderHeader());
    expect(classes).toEqual(expect.arrayContaining(['px-4', 'lg:px-6']));
    expect(classes).not.toContain('px-6');
  });

  it('lets the first row, the logo group and the button group wrap', async () => {
    const header = await renderHeader();
    const firstRow = header.firstElementChild as HTMLElement;
    expect(classesOf(firstRow)).toEqual(expect.arrayContaining(['flex', 'flex-wrap']));

    const logoGroup = within(firstRow).getByText('GraphOps').parentElement as HTMLElement;
    expect(classesOf(logoGroup)).toEqual(expect.arrayContaining(['flex-wrap', 'min-w-0']));

    const newTicket = within(header).getByRole('button', { name: i18n.t('header.newTicket') });
    const buttonGroup = newTicket.parentElement as HTMLElement;
    expect(buttonGroup.parentElement).toBe(firstRow);
    expect(classesOf(buttonGroup)).toEqual(expect.arrayContaining(['flex', 'flex-wrap', 'min-w-0']));
  });

  it('still renders every header control, found by role and name', async () => {
    const header = await renderHeader();
    const byName = (name: string | RegExp) => within(header).getByRole('button', { name });
    expect(byName('Alpha')).toBeInTheDocument(); // the project switcher shows the current project
    expect(byName(i18n.t('header.launchClaude'))).toBeInTheDocument();
    expect(byName(i18n.t('header.language.toggleTitle', { lang: i18n.t('header.language.ja') }))).toBeInTheDocument();
    expect(byName(/^テーマ: /)).toBeInTheDocument();
    expect(byName(i18n.t('header.settings'))).toBeInTheDocument();
    expect(byName(i18n.t('header.newTicket'))).toBeInTheDocument();
    expect(byName(i18n.t('toolbar.refreshTitle'))).toBeInTheDocument();

    // The toolbar row: the search box and the four filters.
    expect(within(header).getByRole('textbox', { name: i18n.t('toolbar.searchLabel') })).toBeInTheDocument();
    for (const filter of ['status', 'assignee', 'priority', 'label']) {
      expect(within(header).getByTestId(`toolbar-${filter}-filter-panel-trigger`)).toBeInTheDocument();
    }
  });

  it('lets the search box shrink to the toolbar instead of widening it', async () => {
    const header = await renderHeader();
    const search = within(header).getByRole('textbox', { name: i18n.t('toolbar.searchLabel') });
    // w-56 stays the width wherever it fits (sm and up it always does).
    expect(classesOf(search)).toEqual(expect.arrayContaining(['w-56', 'max-w-full']));
    expect(classesOf(search.parentElement as HTMLElement)).toContain('max-w-full');
    expect(classesOf(search.parentElement!.parentElement as HTMLElement)).toEqual(
      expect.arrayContaining(['flex-wrap', 'min-w-0'])
    );
  });

  it('wraps the summary figures and draws their dividers only from sm up', async () => {
    await renderHeader();
    const main = screen.getByRole('main');
    const figures = within(main).getByText(i18n.t('summary.total')).parentElement!.parentElement as HTMLElement;
    const classes = classesOf(figures);
    expect(classes).toEqual(expect.arrayContaining(['flex', 'flex-wrap', 'sm:divide-x']));
    // An unprefixed divide-x would draw a stray line at the start of a
    // wrapped row.
    expect(classes).not.toContain('divide-x');
  });
});

describe('TicketFamily links on narrow screens', () => {
  it('keeps a family link within its row, truncating the title', () => {
    render(
      <TicketFamily
        parent={{ id: 'ALP-00002', title: 'とても長い親チケットのタイトル'.repeat(4), status: 'TODO' }}
        onOpenTicket={() => undefined}
      />
    );
    const link = screen.getByTestId('ticket-family-parent');
    expect(classesOf(link)).toContain('max-w-full');
    const title = within(link).getByText(/とても長い/);
    expect(classesOf(title)).toEqual(expect.arrayContaining(['truncate', 'min-w-0']));
  });
});
