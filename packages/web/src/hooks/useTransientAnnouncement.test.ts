// DFLT-00194: the live-region text for one-off status messages is set, cleared
// again after a while, never cleared early by a stale timer, and never leaves a
// timer running after unmount.
// DFLT-00204: repeating the text still shown empties the region and puts the
// text back after a short gap, so it is read out again; clear() empties it at
// once.
import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { REANNOUNCE_GAP_MS, TRANSIENT_ANNOUNCEMENT_DURATION_MS, useTransientAnnouncement } from './useTransientAnnouncement';

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

  it('clear(expected) also cancels that text while it waits out the re-announce gap', () => {
    const { result } = renderHook(() => useTransientAnnouncement());

    act(() => result.current.announce('Saving "a"...'));
    act(() => result.current.announce('Saving "a"...'));
    expect(result.current.message).toBe('');
    act(() => result.current.clear('Saving "a"...'));
    expect(vi.getTimerCount()).toBe(0);
    act(() => vi.advanceTimersByTime(REANNOUNCE_GAP_MS + TRANSIENT_ANNOUNCEMENT_DURATION_MS));
    expect(result.current.message).toBe('');
  });

  it('re-announces the same text by emptying the region first and putting the text back after a short gap', () => {
    const { result } = renderHook(() => useTransientAnnouncement());

    act(() => result.current.announce('same'));
    act(() => vi.advanceTimersByTime(1000));
    act(() => result.current.announce('same'));
    expect(result.current.message).toBe('');
    expect(vi.getTimerCount()).toBe(1);

    act(() => vi.advanceTimersByTime(REANNOUNCE_GAP_MS - 1));
    expect(result.current.message).toBe('');

    act(() => vi.advanceTimersByTime(1));
    expect(result.current.message).toBe('same');
    expect(vi.getTimerCount()).toBe(1);

    act(() => vi.advanceTimersByTime(TRANSIENT_ANNOUNCEMENT_DURATION_MS - 1));
    expect(result.current.message).toBe('same');
    act(() => vi.advanceTimersByTime(1));
    expect(result.current.message).toBe('');
    expect(vi.getTimerCount()).toBe(0);
  });

  it('a different text announced during the gap shows at once and is not overwritten by the pending repeat', () => {
    const { result } = renderHook(() => useTransientAnnouncement());

    act(() => result.current.announce('same'));
    act(() => result.current.announce('same'));
    expect(result.current.message).toBe('');

    act(() => result.current.announce('other'));
    expect(result.current.message).toBe('other');
    expect(vi.getTimerCount()).toBe(1);

    act(() => vi.advanceTimersByTime(REANNOUNCE_GAP_MS));
    expect(result.current.message).toBe('other');

    act(() => vi.advanceTimersByTime(TRANSIENT_ANNOUNCEMENT_DURATION_MS - REANNOUNCE_GAP_MS));
    expect(result.current.message).toBe('');
    expect(vi.getTimerCount()).toBe(0);
  });

  it('clear() empties the region at once and leaves no timer, even during the gap', () => {
    const { result } = renderHook(() => useTransientAnnouncement());

    act(() => result.current.announce('x'));
    act(() => result.current.clear());
    expect(result.current.message).toBe('');
    expect(vi.getTimerCount()).toBe(0);

    act(() => result.current.announce('x'));
    act(() => result.current.announce('x'));
    act(() => result.current.clear());
    expect(vi.getTimerCount()).toBe(0);
    act(() => vi.advanceTimersByTime(REANNOUNCE_GAP_MS + TRANSIENT_ANNOUNCEMENT_DURATION_MS));
    expect(result.current.message).toBe('');
  });

  it('after clear(), announcing the same text again shows it at once', () => {
    const { result } = renderHook(() => useTransientAnnouncement());

    act(() => result.current.announce('x'));
    act(() => result.current.clear());
    act(() => result.current.announce('x'));
    expect(result.current.message).toBe('x');
  });

  it('cancels the pending gap timer on unmount', () => {
    const { result, unmount } = renderHook(() => useTransientAnnouncement());

    act(() => result.current.announce('x'));
    act(() => result.current.announce('x'));
    expect(vi.getTimerCount()).toBe(1);

    unmount();

    expect(vi.getTimerCount()).toBe(0);
  });

  it('keeps clear stable across renders', () => {
    const { result, rerender } = renderHook(() => useTransientAnnouncement());
    const first = result.current.clear;
    act(() => result.current.announce('x'));
    act(() => result.current.clear());
    rerender();
    expect(result.current.clear).toBe(first);
  });
});
