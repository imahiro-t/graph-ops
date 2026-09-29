// DFLT-00268: while the app header is pinned (from lg up, and while
// useFitsSticky lets it be), <html> gets a scroll-padding-top, so the element
// focused with Tab and the card opened by handleOpenTicket's
// scrollIntoView({ block: 'start' }) stop below the header instead of under it
// (WCAG 2.4.11). Unpinned, the padding stays empty (0).
// DFLT-00278: the padding is the header's real height plus a 0.5rem gap
// (calc(<height>px + 0.5rem)), so the focus ring drawn outside a focused
// button is not clipped by the header either. pinned() below spells the
// expected value out in one place.
//
// jsdom computes no layout and evaluates no media queries, so the header's
// offsetHeight, innerHeight, ResizeObserver and matchMedia are stubbed. That
// the browser honours the padding when it scrolls on focus and on
// scrollIntoView was checked in a real browser (see the ticket's
// implementation notes). fetch is served by test/fakeBackend.ts.
import { act, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from './i18n';
import App from './App';
import { Project } from './types';
import { createFakeBackend, installFakeBackend } from './test/fakeBackend';
import { LG_MEDIA_QUERY } from './hooks/useStickyHeaderScrollPadding';

const alpha: Project = { id: 'p-alpha', name: 'Alpha', prefix: 'ALP', local_path: '/work/alpha', created_at: '', updated_at: '' };

function seed() {
  installFakeBackend(
    createFakeBackend({
      projects: [alpha],
      currentProjectId: alpha.id,
      labels: [],
      tickets: [
        { id: 'ALP-00001', project_id: alpha.id, title: '親チケット P', status: 'IN PROGRESS', priority: 'HIGH', labelIds: [] },
        { id: 'ALP-00002', project_id: alpha.id, title: '子 C1', status: 'TODO', priority: 'LOW', labelIds: [], parentId: 'ALP-00001' }
      ]
    })
  );
}

const classesOf = (el: Element) => el.className.split(/\s+/).filter(Boolean);
const padding = () => document.documentElement.style.scrollPaddingTop;
const stickyClasses = ['lg:sticky', 'lg:top-0'];
// The expected scroll-padding-top while the header is pinned at `height`.
const pinned = (height: number) => `calc(${height}px + 0.5rem)`;

describe('App header scroll padding (DFLT-00268)', () => {
  let headerHeight = 0;
  let observers: { callback: ResizeObserverCallback; targets: Element[] }[] = [];
  // The lg query's stubbed MediaQueryList: whether it matches, and its
  // 'change' listeners.
  let lg: { matches: boolean; listeners: Set<() => void> };

  function stubMatchMedia() {
    lg = { matches: true, listeners: new Set() };
    vi.stubGlobal(
      'matchMedia',
      vi.fn((query: string) => {
        if (query !== LG_MEDIA_QUERY) {
          return { matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn(), addListener: vi.fn(), removeListener: vi.fn() };
        }
        return {
          get matches() {
            return lg.matches;
          },
          addEventListener: (_type: string, l: () => void) => lg.listeners.add(l),
          removeEventListener: (_type: string, l: () => void) => lg.listeners.delete(l)
        };
      })
    );
    window.matchMedia = globalThis.matchMedia;
  }

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

  const setLg = (matches: boolean) =>
    act(() => {
      lg.matches = matches;
      for (const l of [...lg.listeners]) l();
    });

  async function renderHeader() {
    const utils = render(<App />);
    await screen.findByText('ALP-00001');
    return { ...utils, header: screen.getByRole('banner') };
  }

  beforeEach(async () => {
    seed();
    stubMatchMedia();
    stubResizeObserver();
    headerHeight = 0;
    observers = [];
    vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockImplementation(function (this: HTMLElement) {
      return this.tagName === 'HEADER' ? headerHeight : 0;
    });
    await i18n.changeLanguage('ja');
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    document.documentElement.style.scrollPaddingTop = '';
  });

  // Default font: the header is 114px from lg up, pinned in any window
  // height (the 128px floor), including low ones where it hid focused
  // elements.
  it.each([
    [1280, 400],
    [1024, 300],
    [1440, 900]
  ])('is the header height plus 0.5rem while pinned at %ix%i', async (_width, height) => {
    vi.stubGlobal('innerHeight', height);
    headerHeight = 114;
    render(<App />);
    // Right after the first commit: set in a layout effect, before paint.
    const header = screen.getByRole('banner');
    expect(classesOf(header)).toEqual(expect.arrayContaining(stickyClasses));
    expect(padding()).toBe(pinned(114));
    await screen.findByText('ALP-00001');
    expect(padding()).toBe(pinned(114));
  });

  it('follows the header height while it stays pinned', async () => {
    vi.stubGlobal('innerHeight', 900);
    headerHeight = 114;
    const { header } = await renderHeader();
    expect(padding()).toBe(pinned(114));

    headerHeight = 150; // 150 <= 900 * 0.25
    fireHeaderResize(header);
    expect(classesOf(header)).toEqual(expect.arrayContaining(stickyClasses));
    expect(padding()).toBe(pinned(150));
  });

  it('clears when the header grows past the threshold and unpins', async () => {
    vi.stubGlobal('innerHeight', 400);
    headerHeight = 114;
    const { header } = await renderHeader();
    expect(padding()).toBe(pinned(114));

    headerHeight = 150; // > 128 and > 400 * 0.25
    fireHeaderResize(header);
    expect(classesOf(header)).not.toContain('lg:sticky');
    expect(padding()).toBe('');
  });

  it('stays empty below lg', async () => {
    lg.matches = false; // a 768px-wide window
    vi.stubGlobal('innerHeight', 1024);
    headerHeight = 114;
    await renderHeader();
    expect(padding()).toBe('');
  });

  it('is set and cleared as the window crosses the lg breakpoint', async () => {
    lg.matches = false;
    vi.stubGlobal('innerHeight', 1024);
    headerHeight = 114;
    await renderHeader();
    expect(padding()).toBe('');

    setLg(true);
    expect(padding()).toBe(pinned(114));
    setLg(false);
    expect(padding()).toBe('');
  });

  // 200% root font size in a 1024x768 window: the header is about 532px and
  // useFitsSticky unpins it.
  it('stays empty while useFitsSticky unpins a large-text header, and is set once it pins again', async () => {
    vi.stubGlobal('innerHeight', 768);
    headerHeight = 532; // > 128 and > 768 * 0.25
    const { header } = await renderHeader();
    expect(classesOf(header)).not.toContain('lg:sticky');
    expect(padding()).toBe('');

    vi.stubGlobal('innerHeight', 2200); // 532 <= 2200 * 0.25
    act(() => {
      window.dispatchEvent(new Event('resize'));
    });
    expect(classesOf(header)).toEqual(expect.arrayContaining(stickyClasses));
    expect(padding()).toBe(pinned(532));
  });

  it('stays empty where nothing is laid out (offsetHeight 0, jsdom)', async () => {
    headerHeight = 0;
    await renderHeader();
    expect(padding()).toBe('');
  });

  it('puts the old value back and unsubscribes on unmount', async () => {
    vi.stubGlobal('innerHeight', 400);
    headerHeight = 114;
    document.documentElement.style.scrollPaddingTop = '8px';
    const removeSpy = vi.spyOn(window, 'removeEventListener');
    const { header, unmount } = await renderHeader();
    expect(padding()).toBe(pinned(114));
    expect(lg.listeners.size).toBeGreaterThan(0);

    unmount();
    expect(padding()).toBe('8px');
    expect(lg.listeners.size).toBe(0);
    expect(observers.some(o => o.targets.includes(header))).toBe(false);
    expect(removeSpy).toHaveBeenCalledWith('resize', expect.any(Function));
  });

  // The card the family link opens is scrolled with block 'start', which
  // the browser lines up with <html>'s scroll padding: the header's height
  // plus 0.5rem while it is pinned.
  it('scrolls the opened ticket into view while the padding holds the pinned header height plus the gap', async () => {
    vi.stubGlobal('innerHeight', 400);
    headerHeight = 114;
    const calls: { id: string; options: unknown; padding: string }[] = [];
    const scrollIntoView = vi.fn(function (this: HTMLElement, options?: unknown) {
      calls.push({ id: this.id, options, padding: padding() });
    });
    const original = Object.getOwnPropertyDescriptor(Element.prototype, 'scrollIntoView');
    Element.prototype.scrollIntoView = scrollIntoView;
    try {
      const user = userEvent.setup();
      await renderHeader();
      const card = (id: string) => document.getElementById(`ticket-${id}`) as HTMLElement;
      await user.click(within(card('ALP-00001')).getByTestId('ticket-header-row'));
      const family = await within(card('ALP-00001')).findByTestId('ticket-family');
      await user.click(within(family).getByTestId('ticket-family-child'));
      await waitFor(() => expect(document.activeElement).toBe(card('ALP-00002')));

      const opened = calls.filter(c => c.id === 'ticket-ALP-00002');
      expect(opened).toHaveLength(1);
      expect(opened[0].options).toEqual(expect.objectContaining({ block: 'start' }));
      expect(opened[0].padding).toBe(pinned(114));
    } finally {
      if (original) Object.defineProperty(Element.prototype, 'scrollIntoView', original);
      else delete (Element.prototype as { scrollIntoView?: unknown }).scrollIntoView;
    }
  });
});
