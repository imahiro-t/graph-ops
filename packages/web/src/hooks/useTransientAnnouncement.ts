import { useCallback, useEffect, useRef, useState } from 'react';

// How long an announcement stays in its live region before it is cleared.
export const TRANSIENT_ANNOUNCEMENT_DURATION_MS = 5000;

// Drives the text of an always-mounted StatusLiveRegion for one-off status
// messages (a deletion that succeeded, filters cleared on the person's behalf).
// The text is cleared again after a while so the next announcement -- even one
// with the same wording -- is a change the screen reader picks up. The clear
// timer is owned here so it never outlives the component (a timer firing after
// the test environment is torn down surfaces as an unhandled "window is not
// defined"), and a new announcement replaces the pending timer instead of
// letting a stale one clear the newer text early.
//
// clear() empties the region early -- for a status that no longer holds, such
// as "saving..." after the save failed (DFLT-00210). Given the text it expects,
// it clears only while that text is still shown, so a newer announcement made
// in the meantime (another row's save or delete) is left alone.
export function useTransientAnnouncement(durationMs: number = TRANSIENT_ANNOUNCEMENT_DURATION_MS) {
  const [message, setMessage] = useState('');
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  // The text currently shown, for clear(expected) to compare against without
  // putting side effects (clearing the timer) inside a state updater.
  const messageRef = useRef('');

  const cancelPendingClear = useCallback(() => {
    if (timerRef.current !== null) {
      clearTimeout(timerRef.current);
      timerRef.current = null;
    }
  }, []);

  // Make sure no timer outlives the component using this hook.
  useEffect(() => cancelPendingClear, [cancelPendingClear]);

  const announce = useCallback(
    (text: string) => {
      cancelPendingClear();
      messageRef.current = text;
      setMessage(text);
      timerRef.current = setTimeout(() => {
        timerRef.current = null;
        messageRef.current = '';
        setMessage('');
      }, durationMs);
    },
    [cancelPendingClear, durationMs]
  );

  const clear = useCallback(
    (expected?: string) => {
      if (expected !== undefined && messageRef.current !== expected) return;
      cancelPendingClear();
      messageRef.current = '';
      setMessage('');
    },
    [cancelPendingClear]
  );

  return { message, announce, clear };
}
