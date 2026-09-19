import { useCallback, useEffect, useRef, useState } from 'react';

// How long the "saved" confirmation stays visible after a successful save.
export const SAVED_FLASH_DURATION_MS = 2000;

// Drives the short-lived "saved" confirmation the settings editors show after
// a successful save. The auto-hide timer is owned here so it can never outlive
// the component: it's cancelled on unmount (a timer firing after the test
// environment is torn down surfaces as an unhandled "window is not defined"),
// and a repeated save replaces the pending timer instead of leaving a stale one
// that would hide the newer confirmation early.
export function useSavedFlash() {
  const [savedFlash, setSavedFlash] = useState(false);
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const cancelPendingHide = useCallback(() => {
    if (timerRef.current !== null) {
      clearTimeout(timerRef.current);
      timerRef.current = null;
    }
  }, []);

  // Make sure no timer outlives the component using this hook.
  useEffect(() => cancelPendingHide, [cancelPendingHide]);

  const showSavedFlash = useCallback(() => {
    cancelPendingHide();
    setSavedFlash(true);
    timerRef.current = setTimeout(() => {
      timerRef.current = null;
      setSavedFlash(false);
    }, SAVED_FLASH_DURATION_MS);
  }, [cancelPendingHide]);

  return { savedFlash, showSavedFlash };
}
