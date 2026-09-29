// DFLT-00285: the project switcher's popup is never wider than the window
// less 0.5rem on each side and moves left when it would end past the
// window's right edge (lib/popupPlacement.ts). App works the placement out
// when the popup opens and again on resize and on a ResizeObserver callback
// (a change of the root font size fires no resize), and stops listening when
// it closes.
//
// jsdom computes no layout, so the window's width
// (document.documentElement.clientWidth), the wrapper's left edge
// (getBoundingClientRect) and the root font size (getComputedStyle) are
// stubbed here and the popup's style is checked. The layout itself was
// measured in a real browser (see the ticket's implementation notes).
//
// fetch is served by test/fakeBackend.ts.
import { act, fireEvent, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from './i18n';
import App from './App';
import { Project } from './types';
import { createFakeBackend, installFakeBackend } from './test/fakeBackend';

const alpha: Project = { id: 'p-alpha', name: 'Alpha', prefix: 'ALP', local_path: '/work/alpha', created_at: '', updated_at: '' };
const beta: Project = { id: 'p-beta', name: 'Beta', prefix: 'BETA', local_path: '', created_at: '', updated_at: '' };

let viewportWidth = 1024;
let anchorLeft = 0;
let rootFontSize = 16;
let observers: { callback: ResizeObserverCallback; targets: Element[]; disconnected: boolean }[] = [];

function stubResizeObserver() {
  vi.stubGlobal(
    'ResizeObserver',
    class {
      private entry: { callback: ResizeObserverCallback; targets: Element[]; disconnected: boolean };
      constructor(callback: ResizeObserverCallback) {
        this.entry = { callback, targets: [], disconnected: false };
        observers.push(this.entry);
      }
      observe(target: Element) {
        this.entry.targets.push(target);
      }
      unobserve() {}
      disconnect() {
        this.entry.disconnected = true;
        this.entry.targets = [];
      }
    }
  );
}

function switcherButton(): HTMLElement {
  const found = within(screen.getByRole('banner'))
    .getAllByRole('button')
    .filter(b => b.getAttribute('aria-haspopup') === 'dialog');
  expect(found).toHaveLength(1);
  return found[0];
}

// The relative wrapper the popup is positioned against.
function anchor(): HTMLElement {
  return switcherButton().closest('div') as HTMLElement;
}

async function renderAndOpen() {
  installFakeBackend(
    createFakeBackend({
      projects: [alpha, beta],
      currentProjectId: alpha.id,
      labels: [],
      tickets: [{ id: 'ALP-00001', project_id: alpha.id, title: 'チケット', status: 'TODO', priority: 'MEDIUM', labelIds: [] }]
    })
  );
  const user = userEvent.setup();
  render(<App />);
  await screen.findByText('ALP-00001');
  await user.click(switcherButton());
  return { user, popup: screen.getByRole('dialog', { name: i18n.t('projectSwitcher.menuLabel') }) };
}

function styleOf(popup: HTMLElement) {
  return { left: popup.style.left, maxWidth: popup.style.maxWidth };
}

beforeEach(async () => {
  viewportWidth = 1024;
  anchorLeft = 0;
  rootFontSize = 16;
  observers = [];
  await i18n.changeLanguage('ja');
  vi.spyOn(document.documentElement, 'clientWidth', 'get').mockImplementation(() => viewportWidth);
  const realGetBoundingClientRect = HTMLElement.prototype.getBoundingClientRect;
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
    if (this.tagName === 'DIV' && this.querySelector('button[aria-haspopup="dialog"]') && this.classList.contains('max-w-56')) {
      return DOMRect.fromRect({ x: anchorLeft, y: 14, width: 100, height: 30 });
    }
    return realGetBoundingClientRect.call(this);
  });
  const realGetComputedStyle = window.getComputedStyle.bind(window);
  vi.spyOn(window, 'getComputedStyle').mockImplementation((el: Element, pseudo?: string | null) => {
    const style = realGetComputedStyle(el, pseudo);
    if (el !== document.documentElement) return style;
    return new Proxy(style, {
      get(target, prop) {
        if (prop === 'fontSize') return `${rootFontSize}px`;
        const value = Reflect.get(target, prop, target);
        return typeof value === 'function' ? value.bind(target) : value;
      }
    });
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('project switcher popup placement', () => {
  it('lines up with the button as before, capped at the window less 2 x 0.5rem', async () => {
    anchorLeft = 444;
    const { popup } = await renderAndOpen();
    expect(styleOf(popup)).toEqual({ left: '0px', maxWidth: '1008px' });
  });

  it('moves left to stay inside the window when the button is near its right edge', async () => {
    viewportWidth = 400;
    anchorLeft = 300;
    const { popup } = await renderAndOpen();
    expect(styleOf(popup)).toEqual({ left: '-164px', maxWidth: '384px' });
    const left = parseFloat(popup.style.left);
    const width = Math.min(256, parseFloat(popup.style.maxWidth));
    expect(anchorLeft + left + width).toBeLessThanOrEqual(400 - 8);
    expect(anchorLeft + left).toBeGreaterThanOrEqual(8);
  });

  // A 320px window with a 32px root font: the 16rem (512px) popup is capped
  // at 320 - 2 x 16 = 288px (9rem), and gets its width back when the window
  // is widened.
  it('gives the width back when the window is widened while it is open', async () => {
    viewportWidth = 320;
    rootFontSize = 32;
    anchorLeft = 16;
    const { popup } = await renderAndOpen();
    expect(styleOf(popup)).toEqual({ left: '0px', maxWidth: '288px' });
    viewportWidth = 1024;
    act(() => {
      fireEvent(window, new Event('resize'));
    });
    expect(styleOf(popup)).toEqual({ left: '0px', maxWidth: '992px' });
  });

  it('follows a root font size change reported by ResizeObserver while it is open', async () => {
    stubResizeObserver();
    viewportWidth = 400;
    anchorLeft = 100;
    const { popup } = await renderAndOpen();
    expect(styleOf(popup)).toEqual({ left: '0px', maxWidth: '384px' });
    // Other parts of the app (the header's stickiness) observe too; the
    // popup's observer is the one watching the wrapper.
    const watching = observers.filter(o => o.targets.includes(anchor()));
    expect(watching).toHaveLength(1);
    expect(watching[0].targets).toEqual([anchor(), document.documentElement]);
    rootFontSize = 32;
    act(() => {
      watching[0].callback([], {} as ResizeObserver);
    });
    expect(styleOf(popup)).toEqual({ left: '-84px', maxWidth: '368px' });
  });

  it('stops listening once the popup closes', async () => {
    stubResizeObserver();
    const removeSpy = vi.spyOn(window, 'removeEventListener');
    const { user } = await renderAndOpen();
    const observer = observers.find(o => o.targets.includes(anchor()));
    if (!observer) throw new Error('the popup has no ResizeObserver');
    expect(observer.disconnected).toBe(false);
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('dialog', { name: i18n.t('projectSwitcher.menuLabel') })).not.toBeInTheDocument();
    expect(observer.disconnected).toBe(true);
    expect(removeSpy.mock.calls.some(([type]) => type === 'resize')).toBe(true);
  });

  it('works without ResizeObserver, following resize alone', async () => {
    vi.stubGlobal('ResizeObserver', undefined);
    viewportWidth = 400;
    anchorLeft = 300;
    const { popup } = await renderAndOpen();
    expect(styleOf(popup)).toEqual({ left: '-164px', maxWidth: '384px' });
    anchorLeft = 100;
    act(() => {
      fireEvent(window, new Event('resize'));
    });
    expect(styleOf(popup)).toEqual({ left: '0px', maxWidth: '384px' });
  });
});
