import { useCallback, useEffect, useRef, useState } from 'react';
import { REANNOUNCE_GAP_MS } from './useTransientAnnouncement';

// How long the "saved" confirmation stays visible after a successful save.
export const SAVED_FLASH_DURATION_MS = 2000;

// Drives the short-lived "saved" confirmation the settings editors show after
// a successful save. It returns two flags:
//
//   - savedFlash: whether the visible (aria-hidden) confirmation is drawn.
//   - savedAnnounced: whether the always-mounted StatusLiveRegion holds the
//     success text. Callers pass `savedAnnounced ? t(...) : ''` to it.
//
// They differ only when a save succeeds again while the confirmation is still
// on screen (DFLT-00359). The live region already holds the success text then,
// and setting the same text again changes nothing in the DOM, so a screen
// reader would stay silent about the second save. Instead the region is
// emptied and the text put back REANNOUNCE_GAP_MS later -- emptying and
// refilling it within one commit would not change the DOM either (the same
// reason as DFLT-00204's useTransientAnnouncement). The visible confirmation
// stays up throughout, so nothing flickers.
//
// The timers are owned here so they can never outlive the component: they are
// cancelled on unmount (a timer firing after the test environment is torn down
// surfaces as an unhandled "window is not defined"), and a repeated save
// replaces the pending ones instead of leaving stale timers that would hide
// the newer confirmation early or refill the region out of order.
export function useSavedFlash() {
  const [savedFlash, setSavedFlash] = useState(false);
  const [savedAnnounced, setSavedAnnounced] = useState(false);
  const hideTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const gapTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  // Read inside showSavedFlash so its identity never depends on the state.
  const savedFlashRef = useRef(false);

  const cancelPendingTimers = useCallback(() => {
    if (hideTimerRef.current !== null) {
      clearTimeout(hideTimerRef.current);
      hideTimerRef.current = null;
    }
    if (gapTimerRef.current !== null) {
      clearTimeout(gapTimerRef.current);
      gapTimerRef.current = null;
    }
  }, []);

  // Make sure no timer outlives the component using this hook.
  useEffect(() => cancelPendingTimers, [cancelPendingTimers]);

  const showSavedFlash = useCallback(() => {
    const alreadyShown = savedFlashRef.current;
    cancelPendingTimers();
    savedFlashRef.current = true;
    setSavedFlash(true);
    if (alreadyShown) {
      // Empty the live region now and refill it after the gap, so the
      // second save is announced as a change.
      setSavedAnnounced(false);
      gapTimerRef.current = setTimeout(() => {
        gapTimerRef.current = null;
        setSavedAnnounced(true);
      }, REANNOUNCE_GAP_MS);
    } else {
      setSavedAnnounced(true);
    }
    hideTimerRef.current = setTimeout(() => {
      hideTimerRef.current = null;
      savedFlashRef.current = false;
      setSavedFlash(false);
      setSavedAnnounced(false);
    }, SAVED_FLASH_DURATION_MS);
  }, [cancelPendingTimers]);

  return { savedFlash, savedAnnounced, showSavedFlash };
}
