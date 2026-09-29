import { useEffect, useState } from 'react';

function matches(query: string): boolean {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return false;
  return window.matchMedia(query).matches;
}

// Whether a media query matches, kept up to date through the query list's
// `change` event (DFLT-00293). The first render already has the right value
// (read synchronously), so a layout that depends on it does not flash the
// other state. Without matchMedia (an environment that lacks it) the query
// never matches.
//
// Use the same query string as the CSS it pairs with -- e.g. App's
// `(max-width: 200px)` next to the `upto-200px:` variant, which is
// `@media (max-width: 200px)` (index.css) -- so the script and the styles
// switch at the same width, the boundary included.
export function useMediaQuery(query: string): boolean {
  const [value, setValue] = useState(() => matches(query));

  useEffect(() => {
    if (typeof window.matchMedia !== 'function') {
      setValue(false);
      return;
    }
    const mql = window.matchMedia(query);
    const update = () => setValue(mql.matches);
    // The query may have changed, or the window may have been resized
    // between the first render and this effect.
    update();
    mql.addEventListener('change', update);
    return () => mql.removeEventListener('change', update);
  }, [query]);

  return value;
}
