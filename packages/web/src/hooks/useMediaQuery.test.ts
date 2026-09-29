// DFLT-00293: useMediaQuery follows a media query through its `change`
// event, and never matches where matchMedia does not exist.
import { act, renderHook } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useMediaQuery } from './useMediaQuery';

type Listener = () => void;

// A matchMedia stub whose result per query the test controls.
function stubMatchMedia(initial: Record<string, boolean>) {
  const state = { ...initial };
  const listeners = new Map<string, Set<Listener>>();
  const lists: Record<string, { addEventListener: ReturnType<typeof vi.fn>; removeEventListener: ReturnType<typeof vi.fn> }> = {};
  const matchMedia = vi.fn((query: string) => {
    const set = listeners.get(query) ?? new Set<Listener>();
    listeners.set(query, set);
    const list = {
      media: query,
      get matches() {
        return state[query] ?? false;
      },
      addEventListener: vi.fn((_type: string, fn: Listener) => set.add(fn)),
      removeEventListener: vi.fn((_type: string, fn: Listener) => set.delete(fn))
    };
    lists[query] = list;
    return list;
  });
  vi.stubGlobal('matchMedia', matchMedia);
  window.matchMedia = globalThis.matchMedia;
  return {
    matchMedia,
    lists,
    listenerCount: (query: string) => listeners.get(query)?.size ?? 0,
    set(query: string, value: boolean) {
      state[query] = value;
      for (const fn of listeners.get(query) ?? []) fn();
    }
  };
}

const QUERY = '(max-width: 200px)';

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('useMediaQuery', () => {
  it('starts from the query result on the first render', () => {
    stubMatchMedia({ [QUERY]: true });
    const { result } = renderHook(() => useMediaQuery(QUERY));
    expect(result.current).toBe(true);
  });

  it('starts false when the query does not match', () => {
    stubMatchMedia({ [QUERY]: false });
    const { result } = renderHook(() => useMediaQuery(QUERY));
    expect(result.current).toBe(false);
  });

  it('follows the change event both ways', () => {
    const mm = stubMatchMedia({ [QUERY]: false });
    const { result } = renderHook(() => useMediaQuery(QUERY));
    act(() => mm.set(QUERY, true));
    expect(result.current).toBe(true);
    act(() => mm.set(QUERY, false));
    expect(result.current).toBe(false);
  });

  it('asks matchMedia for exactly the query it is given', () => {
    const mm = stubMatchMedia({});
    renderHook(() => useMediaQuery(QUERY));
    expect(mm.matchMedia).toHaveBeenCalledWith(QUERY);
    expect(mm.matchMedia.mock.calls.every(([q]) => q === QUERY)).toBe(true);
  });

  it('is false where matchMedia does not exist', () => {
    vi.stubGlobal('matchMedia', undefined);
    window.matchMedia = undefined as unknown as typeof window.matchMedia;
    const { result } = renderHook(() => useMediaQuery(QUERY));
    expect(result.current).toBe(false);
  });

  it('removes its listener on unmount', () => {
    const mm = stubMatchMedia({ [QUERY]: false });
    const { unmount } = renderHook(() => useMediaQuery(QUERY));
    expect(mm.listenerCount(QUERY)).toBe(1);
    unmount();
    expect(mm.listenerCount(QUERY)).toBe(0);
    expect(mm.lists[QUERY].removeEventListener).toHaveBeenCalledWith('change', expect.any(Function));
  });

  it('switches to a new query, leaving the old one', () => {
    const other = '(min-width: 1000px)';
    const mm = stubMatchMedia({ [QUERY]: false, [other]: true });
    const { result, rerender } = renderHook(({ q }) => useMediaQuery(q), { initialProps: { q: QUERY } });
    expect(result.current).toBe(false);
    rerender({ q: other });
    expect(result.current).toBe(true);
    expect(mm.listenerCount(QUERY)).toBe(0);
    expect(mm.listenerCount(other)).toBe(1);
  });
});
