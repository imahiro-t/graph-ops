import { useRef, useEffect } from 'react';

// Keeps a ref pointing at the latest value without ever changing identity,
// so a callback that reads valueRef.current stays referentially stable
// across renders even though the underlying value (here: the i18next `t`
// function, which react-i18next re-creates on every language change) does
// change. Used to keep exhaustive-deps satisfied for load-style callbacks
// whose *retry trigger* must stay scope/projectId only -- switching
// language must not itself re-run a network fetch and blow away unsaved
// form edits.
export function useLatest<T>(value: T) {
  const ref = useRef(value);
  useEffect(() => {
    ref.current = value;
  }, [value]);
  return ref;
}
