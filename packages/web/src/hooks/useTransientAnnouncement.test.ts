// DFLT-00194: the live-region text for one-off status messages is set, cleared
// again after a while, never cleared early by a stale timer, and never leaves a
// timer running after unmount.
import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { TRANSIENT_ANNOUNCEMENT_DURATION_MS, useTransientAnnouncement } from './useTransientAnnouncement';

describe('useTransientAnnouncement', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.useRealTimers();
  });

  it('sets the message and clears it after the display duration', () => {
    const { result } = renderHook(() => useTransientAnnouncement());
    expect(result.current.message).toBe('');

    act(() => result.current.announce('Deleted "a".'));
    expect(result.current.message).toBe('Deleted "a".');

    act(() => vi.advanceTimersByTime(TRANSIENT_ANNOUNCEMENT_DURATION_MS - 1));
    expect(result.current.message).toBe('Deleted "a".');

    act(() => vi.advanceTimersByTime(1));
    expect(result.current.message).toBe('');
    expect(vi.getTimerCount()).toBe(0);
  });

  it('a newer announcement restarts the display duration instead of being cleared by the older timer', () => {
    const { result } = renderHook(() => useTransientAnnouncement());

    act(() => result.current.announce('first'));
    act(() => vi.advanceTimersByTime(TRANSIENT_ANNOUNCEMENT_DURATION_MS - 1000));
    act(() => result.current.announce('second'));
    expect(vi.getTimerCount()).toBe(1);

    // The first announcement's timer would have fired here.
    act(() => vi.advanceTimersByTime(1000));
    expect(result.current.message).toBe('second');

    act(() => vi.advanceTimersByTime(TRANSIENT_ANNOUNCEMENT_DURATION_MS - 1000));
    expect(result.current.message).toBe('');
  });

  it('cancels the pending clear timer on unmount', () => {
    const { result, unmount } = renderHook(() => useTransientAnnouncement());

    act(() => result.current.announce('x'));
    expect(vi.getTimerCount()).toBe(1);

    unmount();

    expect(vi.getTimerCount()).toBe(0);
    expect(() => vi.advanceTimersByTime(TRANSIENT_ANNOUNCEMENT_DURATION_MS)).not.toThrow();
  });

  it('keeps announce stable across renders', () => {
    const { result, rerender } = renderHook(() => useTransientAnnouncement());
    const first = result.current.announce;
    act(() => result.current.announce('x'));
    rerender();
    expect(result.current.announce).toBe(first);
  });

  // DFLT-00210: clear() empties the region early; clear(expected) only while
  // that text is still the one shown.
  it('clear() empties the message at once and cancels the pending timer', () => {
    const { result } = renderHook(() => useTransientAnnouncement());

    act(() => result.current.announce('Saving "a"...'));
    act(() => result.current.clear());
    expect(result.current.message).toBe('');
    expect(vi.getTimerCount()).toBe(0);
  });

  it('clear(expected) empties the message when that text is still shown', () => {
    const { result } = renderHook(() => useTransientAnnouncement());

    act(() => result.current.announce('Saving "a"...'));
    act(() => result.current.clear('Saving "a"...'));
    expect(result.current.message).toBe('');
    expect(vi.getTimerCount()).toBe(0);
  });

  it('clear(expected) leaves a newer announcement and its timer alone', () => {
    const { result } = renderHook(() => useTransientAnnouncement());

    act(() => result.current.announce('Saving "a"...'));
    act(() => result.current.announce('Deleted "b".'));
    act(() => result.current.clear('Saving "a"...'));
    expect(result.current.message).toBe('Deleted "b".');
    expect(vi.getTimerCount()).toBe(1);

    act(() => vi.advanceTimersByTime(TRANSIENT_ANNOUNCEMENT_DURATION_MS));
    expect(result.current.message).toBe('');
  });

  it('clear(expected) does nothing once the text has already been cleared by its timer', () => {
    const { result } = renderHook(() => useTransientAnnouncement());

    act(() => result.current.announce('Saving "a"...'));
    act(() => vi.advanceTimersByTime(TRANSIENT_ANNOUNCEMENT_DURATION_MS));
    act(() => result.current.announce('Saved "c".'));
    act(() => vi.advanceTimersByTime(1000));
    act(() => result.current.clear('Saving "a"...'));
    expect(result.current.message).toBe('Saved "c".');
  });
});
