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
export function useTransientAnnouncement(durationMs: number = TRANSIENT_ANNOUNCEMENT_DURATION_MS) {
  const [message, setMessage] = useState('');
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

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
      setMessage(text);
      timerRef.current = setTimeout(() => {
        timerRef.current = null;
        setMessage('');
      }, durationMs);
    },
    [cancelPendingClear, durationMs]
  );

  return { message, announce };
}
