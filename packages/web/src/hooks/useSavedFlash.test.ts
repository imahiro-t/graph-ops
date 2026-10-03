// DFLT-00094: the "saved" confirmation's timers must never outlive the
// component (a timer firing after jsdom is torn down surfaces as an
// unhandled "window is not defined" and fails the whole test run), and a
// repeated save must replace the pending timers instead of stacking more.
// DFLT-00359: a save repeated while the confirmation is still shown empties
// the live region's text (savedAnnounced) and puts it back after
// REANNOUNCE_GAP_MS, so the second save is announced too.
import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { SAVED_FLASH_DURATION_MS, useSavedFlash } from './useSavedFlash';
import { REANNOUNCE_GAP_MS } from './useTransientAnnouncement';

describe('useSavedFlash', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.useRealTimers();
  });

  it('(a) shows the flash and hides it again after the display duration', () => {
    const { result } = renderHook(() => useSavedFlash());
    expect(result.current.savedFlash).toBe(false);

    act(() => result.current.showSavedFlash());
    expect(result.current.savedFlash).toBe(true);

    act(() => vi.advanceTimersByTime(SAVED_FLASH_DURATION_MS - 1));
    expect(result.current.savedFlash).toBe(true);

    act(() => vi.advanceTimersByTime(1));
    expect(result.current.savedFlash).toBe(false);
    expect(vi.getTimerCount()).toBe(0);
  });

  it('(b) cancels the pending hide timer on unmount so it never fires afterwards', () => {
    const clearTimeoutSpy = vi.spyOn(globalThis, 'clearTimeout');
    const { result, unmount } = renderHook(() => useSavedFlash());

    act(() => result.current.showSavedFlash());
    // Only the hide timer: a first save is announced straight away.
    expect(vi.getTimerCount()).toBe(1);

    unmount();

    expect(clearTimeoutSpy).toHaveBeenCalled();
    expect(vi.getTimerCount()).toBe(0);
    expect(() => vi.advanceTimersByTime(SAVED_FLASH_DURATION_MS)).not.toThrow();
  });

  it('(c) a repeated save replaces the pending timer and restarts the display duration', () => {
    const { result } = renderHook(() => useSavedFlash());

    act(() => result.current.showSavedFlash());
    act(() => vi.advanceTimersByTime(1500));
    act(() => result.current.showSavedFlash());
    // The new hide timer plus the re-announce gap timer; the first save's
    // hide timer is gone.
    expect(vi.getTimerCount()).toBe(2);

    act(() => vi.advanceTimersByTime(REANNOUNCE_GAP_MS));
    // The gap timer has fired; only the hide timer is left.
    expect(vi.getTimerCount()).toBe(1);

    // 2500ms after the first save, 1000ms after the second: the first save's
    // timer must not have hidden the newer confirmation early.
    act(() => vi.advanceTimersByTime(1000 - REANNOUNCE_GAP_MS));
    expect(result.current.savedFlash).toBe(true);

    // SAVED_FLASH_DURATION_MS after the second save.
    act(() => vi.advanceTimersByTime(SAVED_FLASH_DURATION_MS - 1000));
    expect(result.current.savedFlash).toBe(false);
    expect(vi.getTimerCount()).toBe(0);
  });

  it('(d) a first save is announced at once and stops being announced with the flash', () => {
    const { result } = renderHook(() => useSavedFlash());
    expect(result.current.savedAnnounced).toBe(false);

    act(() => result.current.showSavedFlash());
    expect(result.current.savedAnnounced).toBe(true);

    act(() => vi.advanceTimersByTime(SAVED_FLASH_DURATION_MS - 1));
    expect(result.current.savedAnnounced).toBe(true);

    act(() => vi.advanceTimersByTime(1));
    expect(result.current.savedAnnounced).toBe(false);
    expect(result.current.savedFlash).toBe(false);
  });

  it('(e) a save while the flash is shown empties the announcement and refills it after the gap', () => {
    const { result } = renderHook(() => useSavedFlash());

    act(() => result.current.showSavedFlash());
    act(() => vi.advanceTimersByTime(500));
    act(() => result.current.showSavedFlash());
    expect(result.current.savedAnnounced).toBe(false);
    expect(result.current.savedFlash).toBe(true);

    act(() => vi.advanceTimersByTime(REANNOUNCE_GAP_MS - 1));
    expect(result.current.savedAnnounced).toBe(false);
    expect(result.current.savedFlash).toBe(true);

    act(() => vi.advanceTimersByTime(1));
    expect(result.current.savedAnnounced).toBe(true);
    expect(result.current.savedFlash).toBe(true);

    // SAVED_FLASH_DURATION_MS after the second save, both clear together.
    act(() => vi.advanceTimersByTime(SAVED_FLASH_DURATION_MS - REANNOUNCE_GAP_MS - 1));
    expect(result.current.savedAnnounced).toBe(true);
    act(() => vi.advanceTimersByTime(1));
    expect(result.current.savedAnnounced).toBe(false);
    expect(result.current.savedFlash).toBe(false);
    expect(vi.getTimerCount()).toBe(0);
  });

  it('(f) unmounting during the re-announce gap leaves no timer behind', () => {
    const { result, unmount } = renderHook(() => useSavedFlash());

    act(() => result.current.showSavedFlash());
    act(() => result.current.showSavedFlash());
    expect(vi.getTimerCount()).toBe(2);

    unmount();

    expect(vi.getTimerCount()).toBe(0);
    expect(() => vi.advanceTimersByTime(SAVED_FLASH_DURATION_MS)).not.toThrow();
  });

  it('(g) a save during the gap restarts the gap, so an older gap timer does not refill early', () => {
    const { result } = renderHook(() => useSavedFlash());

    act(() => result.current.showSavedFlash());
    act(() => result.current.showSavedFlash());
    act(() => vi.advanceTimersByTime(REANNOUNCE_GAP_MS / 2));
    act(() => result.current.showSavedFlash());
    expect(vi.getTimerCount()).toBe(2);

    // The second save's gap would have ended here.
    act(() => vi.advanceTimersByTime(REANNOUNCE_GAP_MS / 2));
    expect(result.current.savedAnnounced).toBe(false);

    // The third save's gap ends here.
    act(() => vi.advanceTimersByTime(REANNOUNCE_GAP_MS / 2));
    expect(result.current.savedAnnounced).toBe(true);
    expect(result.current.savedFlash).toBe(true);
  });

  it('(h) keeps showSavedFlash stable across renders', () => {
    const { result } = renderHook(() => useSavedFlash());
    const first = result.current.showSavedFlash;

    act(() => result.current.showSavedFlash());
    act(() => result.current.showSavedFlash());
    expect(result.current.showSavedFlash).toBe(first);
  });
});
