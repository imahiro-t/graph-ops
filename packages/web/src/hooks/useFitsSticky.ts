import { RefObject, useLayoutEffect, useState } from 'react';

// DFLT-00258: whether an element is short enough to pin to the top of the
// window. A sticky header several lines tall covers the content it scrolls
// over -- with a 200% root font size set on the page, the app header is
// about 532px tall in a 1024x768 window and hid the ticket header rows'
// buttons (WCAG 2.4.11). A media query cannot tell that case apart (a rem
// query does not follow the root font size the page itself sets), so this
// looks at the element's real height instead.
//
// `maxShare` is the largest share of the window's height the element may
// take and still be pinned. `alwaysFitsPx` is a height up to which the
// element is pinned however short the window is, so a header that is short
// in absolute terms keeps the old behaviour in low windows (a laptop with the
// developer tools docked below the page) instead of unpinning there too.
// The header's height does not depend on whether it is pinned, so switching
// never feeds back into the measurement.
//
// Starts at true and stays true where there is nothing to measure (jsdom
// reports offsetHeight 0), so the first paint and tests behave as before.
// Recomputed when the element resizes (ResizeObserver, when there is one)
// and when the window resizes. Only observe() and disconnect() are called,
// so the empty ResizeObserver stubs other tests install are enough.
export function useFitsSticky(ref: RefObject<HTMLElement | null>, maxShare: number, alwaysFitsPx = 0) {
  const [fits, setFits] = useState(true);

  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const update = () => {
      const height = el.offsetHeight;
      setFits(height <= alwaysFitsPx || height <= window.innerHeight * maxShare);
    };
    update();
    window.addEventListener('resize', update);
    const observer = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(update);
    observer?.observe(el);
    return () => {
      window.removeEventListener('resize', update);
      observer?.disconnect();
    };
  }, [ref, maxShare, alwaysFitsPx]);

  return fits;
}
