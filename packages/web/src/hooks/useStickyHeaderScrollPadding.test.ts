// DFLT-00268: useStickyHeaderScrollPadding where APIs are missing or only
// partly stubbed. App.headerScrollPadding.test.tsx covers the behaviour
// through App; App itself needs matchMedia (useTheme), so the "no
// matchMedia" case is checked on the hook alone. Missing APIs never throw.
// Only a missing matchMedia (read as "not lg") or an unmeasurable header
// (offsetHeight 0, as in jsdom) leaves the padding off: without a
// ResizeObserver, or with a matchMedia result lacking addEventListener, a
// measurable pinned header still gets its height as the padding.
// DFLT-00278: the pinned padding is the height plus STICKY_HEADER_FOCUS_GAP
// (0.5rem), written as calc(); jsdom keeps the calc() string as is.
import { renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { STICKY_HEADER_FOCUS_GAP, pinnedScrollPadding, useStickyHeaderScrollPadding } from './useStickyHeaderScrollPadding';

const padding = () => document.documentElement.style.scrollPaddingTop;

function mountWith(height: number, pinnable = true) {
  const header = document.createElement('header');
  document.body.appendChild(header);
  vi.spyOn(header, 'offsetHeight', 'get').mockReturnValue(height);
  const ref = { current: header };
  const hook = renderHook(({ p }) => useStickyHeaderScrollPadding(ref, p), { initialProps: { p: pinnable } });
  return { header, ...hook };
}

// The expected pinned value for a 114px header, spelled out so a change to
// the formula shows up here.
const PINNED_114 = 'calc(114px + 0.5rem)';

const lgMatching = (extra: Record<string, unknown> = {}) => vi.fn(() => ({ matches: true, ...extra }));

describe('useStickyHeaderScrollPadding with missing or partial APIs', () => {
  beforeEach(() => {
    vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    document.documentElement.style.scrollPaddingTop = '';
    document.body.innerHTML = '';
  });

  it('pins the padding at the header height plus a 0.5rem gap', () => {
    vi.stubGlobal('matchMedia', lgMatching({ addEventListener: vi.fn(), removeEventListener: vi.fn() }));
    mountWith(114);
    expect(STICKY_HEADER_FOCUS_GAP).toBe('0.5rem');
    expect(pinnedScrollPadding(114)).toBe(PINNED_114);
    expect(padding()).toBe(`calc(114px + ${STICKY_HEADER_FOCUS_GAP})`);
  });

  it('leaves the padding off where the header reads 0 tall (jsdom default)', () => {
    vi.stubGlobal('matchMedia', lgMatching({ addEventListener: vi.fn(), removeEventListener: vi.fn() }));
    const { unmount } = mountWith(0);
    expect(padding()).toBe('');
    expect(() => unmount()).not.toThrow();
  });

  it('treats a missing matchMedia as "not lg" and does not throw', () => {
    vi.stubGlobal('matchMedia', undefined);
    const { unmount } = mountWith(114);
    expect(padding()).toBe('');
    expect(() => unmount()).not.toThrow();
  });

  it('still uses the header height plus the gap without a ResizeObserver', () => {
    vi.stubGlobal('ResizeObserver', undefined);
    vi.stubGlobal('matchMedia', lgMatching({ addEventListener: vi.fn(), removeEventListener: vi.fn() }));
    const { unmount } = mountWith(114);
    expect(padding()).toBe(PINNED_114);
    unmount();
    expect(padding()).toBe('');
  });

  it('works with a ResizeObserver stub that has only observe() and disconnect()', () => {
    vi.stubGlobal('matchMedia', lgMatching({ addEventListener: vi.fn(), removeEventListener: vi.fn() }));
    const { unmount } = mountWith(114);
    expect(padding()).toBe(PINNED_114);
    expect(() => unmount()).not.toThrow();
    expect(padding()).toBe('');
  });

  it('works with a matchMedia result that has no addEventListener, falling back to addListener', () => {
    const addListener = vi.fn();
    const removeListener = vi.fn();
    vi.stubGlobal('matchMedia', lgMatching({ addListener, removeListener }));
    const { unmount } = mountWith(114);
    expect(padding()).toBe(PINNED_114);
    expect(addListener).toHaveBeenCalledTimes(1);
    unmount();
    expect(removeListener).toHaveBeenCalledWith(addListener.mock.calls[0][0]);
    expect(padding()).toBe('');
  });

  it('works with a matchMedia result that has neither listener API', () => {
    vi.stubGlobal('matchMedia', lgMatching());
    const { unmount } = mountWith(114);
    expect(padding()).toBe(PINNED_114);
    expect(() => unmount()).not.toThrow();
    expect(padding()).toBe('');
  });

  it('clears when no longer pinnable and restores a pre-existing value on unmount', () => {
    vi.stubGlobal('matchMedia', lgMatching({ addEventListener: vi.fn(), removeEventListener: vi.fn() }));
    document.documentElement.style.scrollPaddingTop = '8px';
    const { rerender, unmount } = mountWith(114);
    expect(padding()).toBe(PINNED_114);
    rerender({ p: false });
    expect(padding()).toBe('');
    rerender({ p: true });
    expect(padding()).toBe(PINNED_114);
    unmount();
    expect(padding()).toBe('8px');
  });
});
