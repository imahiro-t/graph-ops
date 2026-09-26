import { useCallback, useEffect, useRef, useState } from 'react';

// How long an announcement stays in its live region before it is cleared.
export const TRANSIENT_ANNOUNCEMENT_DURATION_MS = 5000;

// How long the live region stays empty before the same wording is put back
// when an announcement repeats the text still shown (DFLT-00204): emptying and
// refilling it in one commit would leave the DOM unchanged, so nothing would be
// read out again.
export const REANNOUNCE_GAP_MS = 100;

// Drives the text of an always-mounted StatusLiveRegion for one-off status
// messages (a deletion that succeeded, filters cleared on the person's behalf).
// The text is cleared again after a while so the next announcement -- even one
// with the same wording -- is a change the screen reader picks up. An
// announcement that repeats the text still on screen (before that clear) first
// empties the region and puts the text back REANNOUNCE_GAP_MS later, so it is
// read out again too (DFLT-00204). clear() empties the region at once, for a
// caller whose later actions make the last announcement stale (e.g. an edit
// after a local, unsaved delete).
//
// Only one timer is ever pending (timerRef): the gap before a repeated text
// or the clear after the display duration. It is owned here so it never
// outlives the component (a timer firing after the test environment is torn
// down surfaces as an unhandled "window is not defined"), and a new
// announcement or clear() replaces it instead of letting a stale one clear the
// newer text early or bring back an older one.
export function useTransientAnnouncement(durationMs: number = TRANSIENT_ANNOUNCEMENT_DURATION_MS) {
  const [message, setMessageState] = useState('');
  // The text last set, readable from the stable callbacks below.
  const messageRef = useRef('');
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const setMessage = useCallback((text: string) => {
    messageRef.current = text;
    setMessageState(text);
  }, []);

  const cancelPendingTimer = useCallback(() => {
    if (timerRef.current !== null) {
      clearTimeout(timerRef.current);
      timerRef.current = null;
    }
  }, []);

  // Make sure no timer outlives the component using this hook.
  useEffect(() => cancelPendingTimer, [cancelPendingTimer]);

  const showFor = useCallback(
    (text: string) => {
      setMessage(text);
      timerRef.current = setTimeout(() => {
        timerRef.current = null;
        setMessage('');
      }, durationMs);
    },
    [setMessage, durationMs]
  );

  const announce = useCallback(
    (text: string) => {
      cancelPendingTimer();
      if (text !== '' && text === messageRef.current) {
        setMessage('');
        timerRef.current = setTimeout(() => {
          timerRef.current = null;
          showFor(text);
        }, REANNOUNCE_GAP_MS);
        return;
      }
      showFor(text);
    },
    [cancelPendingTimer, setMessage, showFor]
  );

  const clear = useCallback(() => {
    cancelPendingTimer();
    setMessage('');
  }, [cancelPendingTimer, setMessage]);

  return { message, announce, clear };
}
