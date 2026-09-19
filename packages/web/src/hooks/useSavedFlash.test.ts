// DFLT-00094: the "saved" confirmation's auto-hide timer must never outlive
// the component (a timer firing after jsdom is torn down surfaces as an
// unhandled "window is not defined" and fails the whole test run), and a
// repeated save must replace the pending timer instead of stacking another.
import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { SAVED_FLASH_DURATION_MS, useSavedFlash } from './useSavedFlash';

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
    expect(vi.getTimerCount()).toBe(1);

    // 2500ms after the first save, 1000ms after the second: the first save's
    // timer must not have hidden the newer confirmation early.
    act(() => vi.advanceTimersByTime(1000));
    expect(result.current.savedFlash).toBe(true);

    // SAVED_FLASH_DURATION_MS after the second save.
    act(() => vi.advanceTimersByTime(SAVED_FLASH_DURATION_MS - 1000));
    expect(result.current.savedFlash).toBe(false);
    expect(vi.getTimerCount()).toBe(0);
  });
});
