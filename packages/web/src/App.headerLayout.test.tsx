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
import { act, render, screen, within } from '@testing-library/react';
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

  it('is sticky only from lg up, keeping its stacking order', async () => {
    const header = await renderHeader();
    const classes = classesOf(header);
    expect(classes).toEqual(expect.arrayContaining(['lg:sticky', 'lg:top-0', 'z-30']));
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
    // New Ticket is a plain <button> (DFLT-00319), a flex item of the group itself.
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

// DFLT-00258: from lg up the header is sticky only while it takes at most a
// quarter of the window's height or is at most 128px tall. With a 200% root
// font size set on the page it was about 532px tall in a 1024x768 window and,
// pinned, covered the ticket header rows' buttons (WCAG 2.4.11). jsdom
// computes no layout, so offsetHeight and innerHeight are stubbed; the real
// heights were measured in a browser (see the ticket's implementation notes).
describe('App header stickiness follows its height', () => {
  let headerHeight = 0;
  let observers: { callback: ResizeObserverCallback; targets: Element[] }[] = [];

  beforeEach(() => {
    seed();
    headerHeight = 0;
    observers = [];
    vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockImplementation(function (this: HTMLElement) {
      return this.tagName === 'HEADER' ? headerHeight : 0;
    });
  });

  afterEach(async () => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    await i18n.changeLanguage('ja');
  });

  function stubResizeObserver() {
    vi.stubGlobal(
      'ResizeObserver',
      class {
        private entry: { callback: ResizeObserverCallback; targets: Element[] };
        constructor(callback: ResizeObserverCallback) {
          this.entry = { callback, targets: [] };
          observers.push(this.entry);
        }
        observe(target: Element) {
          this.entry.targets.push(target);
        }
        disconnect() {
          this.entry.targets = [];
        }
      }
    );
  }

  const fireHeaderResize = (header: HTMLElement) =>
    act(() => {
      for (const o of observers) {
        if (o.targets.includes(header)) o.callback([], {} as ResizeObserver);
      }
    });

  const stickyClasses = ['lg:sticky', 'lg:top-0'];

  it('unpins when the header grows past a quarter of the window and pins again when the window grows', async () => {
    stubResizeObserver();
    vi.stubGlobal('innerHeight', 768);
    headerHeight = 114; // one or two rows at the default font: 15%
    const header = await renderHeader();
    expect(classesOf(header)).toEqual(expect.arrayContaining([...stickyClasses, 'relative', 'z-30']));

    headerHeight = 532; // 200% root font size in a 1024x768 window: 69%
    fireHeaderResize(header);
    let classes = classesOf(header);
    expect(classes).not.toContain('lg:sticky');
    expect(classes).not.toContain('lg:top-0');
    expect(classes).toEqual(expect.arrayContaining(['relative', 'z-30']));
    expect(classes).not.toContain('sticky');
    expect(classes).not.toContain('top-0');

    // A taller window: 532px is a quarter of 2128px.
    vi.stubGlobal('innerHeight', 2200);
    act(() => {
      window.dispatchEvent(new Event('resize'));
    });
    classes = classesOf(header);
    expect(classes).toEqual(expect.arrayContaining([...stickyClasses, 'relative', 'z-30']));
    expect(classes).not.toContain('sticky');
  });

  it('keeps the exact quarter pinned', async () => {
    stubResizeObserver();
    vi.stubGlobal('innerHeight', 800);
    headerHeight = 200;
    const header = await renderHeader();
    expect(classesOf(header)).toEqual(expect.arrayContaining(stickyClasses));
    headerHeight = 201;
    fireHeaderResize(header);
    expect(classesOf(header)).not.toContain('lg:sticky');
  });

  // The default-font header (114px from lg up) stays pinned in a low window,
  // where it takes more than a quarter: a laptop with the developer tools
  // docked below the page, say.
  it('keeps a header of up to 128px pinned however low the window is', async () => {
    stubResizeObserver();
    vi.stubGlobal('innerHeight', 300);
    headerHeight = 114; // 38% of the window
    const header = await renderHeader();
    expect(classesOf(header)).toEqual(expect.arrayContaining([...stickyClasses, 'relative', 'z-30']));

    headerHeight = 128;
    fireHeaderResize(header);
    expect(classesOf(header)).toEqual(expect.arrayContaining(stickyClasses));

    headerHeight = 129; // over the floor and over a quarter of 300px
    fireHeaderResize(header);
    expect(classesOf(header)).not.toContain('lg:sticky');
    expect(classesOf(header)).not.toContain('lg:top-0');

    // Back to the default-font height in an even lower window: pinned again.
    vi.stubGlobal('innerHeight', 200);
    headerHeight = 114;
    act(() => {
      window.dispatchEvent(new Event('resize'));
    });
    expect(classesOf(header)).toEqual(expect.arrayContaining([...stickyClasses, 'relative', 'z-30']));
  });

  it('unpins the 200% header in a low window too', async () => {
    stubResizeObserver();
    vi.stubGlobal('innerHeight', 400);
    headerHeight = 532;
    const header = await renderHeader();
    expect(classesOf(header)).not.toContain('lg:sticky');
    expect(classesOf(header)).toEqual(expect.arrayContaining(['relative', 'z-30']));
  });

  it('stays pinned without a ResizeObserver', async () => {
    vi.stubGlobal('ResizeObserver', undefined);
    const header = await renderHeader();
    expect(classesOf(header)).toEqual(expect.arrayContaining([...stickyClasses, 'relative', 'z-30']));
  });

  // Several App tests stub ResizeObserver with a class that has only
  // observe() and disconnect().
  it('works with a ResizeObserver stub that has no unobserve()', async () => {
    vi.stubGlobal(
      'ResizeObserver',
      class {
        observe() {}
        disconnect() {}
      }
    );
    const { unmount } = render(<App />);
    await screen.findByText('ALP-00001');
    expect(classesOf(screen.getByRole('banner'))).toEqual(expect.arrayContaining([...stickyClasses, 'relative', 'z-30']));
    expect(() => unmount()).not.toThrow();
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
