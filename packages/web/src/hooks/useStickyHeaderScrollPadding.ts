import { RefObject, useLayoutEffect } from 'react';

// Tailwind's `lg` breakpoint, from which the app header can be sticky
// (`lg:sticky lg:top-0` in App.tsx). Keep it in step with --breakpoint-lg in
// index.css's @theme.
export const LG_MEDIA_QUERY = '(min-width: 1024px)';

// DFLT-00278: the gap left between the pinned header's bottom edge and what
// the browser scrolls into view. See the comment on the hook.
export const STICKY_HEADER_FOCUS_GAP = '0.5rem';

// The scroll-padding-top used while the header is pinned: its real height
// plus STICKY_HEADER_FOCUS_GAP.
export const pinnedScrollPadding = (height: number) => `calc(${height}px + ${STICKY_HEADER_FOCUS_GAP})`;

// DFLT-00268: while the app header is pinned to the top of the window, gives
// the page's scroll container (<html>) a scroll-padding-top of the header's
// real height, so what the browser scrolls into view -- the element focused
// with Tab, and the card scrollIntoView({ block: 'start' }) brings up when a
// ticket is opened -- stops below the header instead of under it (WCAG
// 2.4.11). With the default font the header is 114px from lg up and stays
// pinned in any window height, so in a low window (1280x400, say) it hid
// focused buttons and the opened card's top edge.
//
// The header is pinned when the lg query matches and `pinnable` (App's
// headerFitsSticky) is true. Otherwise -- below lg, or while a header grown
// by large text scrolls away with the page -- the padding is cleared, which
// is the old behaviour. The padding is also left off while the height reads
// 0 (nothing laid out, as in jsdom); a missing matchMedia counts as "not lg".
// A missing ResizeObserver, or a matchMedia result without addEventListener,
// only means fewer triggers to recompute on: with a measurable header and a
// matching query the padding is still the header's height plus the gap.
//
// Recomputed when the header resizes (ResizeObserver; only observe() and
// disconnect() are called, like useFitsSticky), when the window resizes,
// when the lg query starts or stops matching, and when `pinnable` changes.
// Written straight to the DOM (no state, no re-render); the header's height
// does not depend on it, so nothing feeds back. The previous value is put
// back on unmount.
//
// DFLT-00278: the padding is the header's height plus STICKY_HEADER_FOCUS_GAP
// (0.5rem), not the height alone. With the height alone, WebKit (Option+Tab)
// stopped a focused button's top edge exactly at the header's bottom edge
// (114px), so the part of the focus indicator drawn outside the button
// (ring-2, or the browser's default outline and outline-offset) could be
// hidden under the header by a few pixels. The gap is in rem so it grows with
// the root font size (16px at a 32px root). It only moves where things stop
// when scrolled into view; whether the header is pinned (useFitsSticky) is
// not affected. The value is written as calc(), which browsers and jsdom
// both keep as is.
export function useStickyHeaderScrollPadding(ref: RefObject<HTMLElement | null>, pinnable: boolean) {
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const root = document.documentElement;
    const previous = root.style.scrollPaddingTop;
    const mql = typeof window.matchMedia === 'function' ? window.matchMedia(LG_MEDIA_QUERY) : null;

    const update = () => {
      const height = el.offsetHeight;
      const pinned = pinnable && (mql?.matches ?? false) && height > 0;
      root.style.scrollPaddingTop = pinned ? pinnedScrollPadding(height) : '';
    };
    update();

    window.addEventListener('resize', update);
    const observer = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(update);
    observer?.observe(el);
    // Older Safari, and some test stubs, have only addListener() or neither.
    if (typeof mql?.addEventListener === 'function') mql.addEventListener('change', update);
    else if (typeof mql?.addListener === 'function') mql.addListener(update);

    return () => {
      window.removeEventListener('resize', update);
      observer?.disconnect();
      if (typeof mql?.removeEventListener === 'function') mql.removeEventListener('change', update);
      else if (typeof mql?.removeListener === 'function') mql.removeListener(update);
      root.style.scrollPaddingTop = previous;
    };
  }, [ref, pinnable]);
}
