import { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { errorMessage, localizedApiErrorMessage } from '../lib/apiError';
import { apiFetch } from '../lib/apiFetch';

// How long a launch result (success/failure) stays visible before it clears
// itself -- it's a one-off send confirmation, not a persistent log entry, so
// it shouldn't linger on screen indefinitely.
const AUTO_CLEAR_DELAY_MS = 5000;

// Fires a prompt at /api/claude/launch, which opens an external, interactive
// terminal running `claude "<prompt>"` and returns immediately -- there is no
// stream to read (that's the point: a human drives that session themselves).
// onDone is called once the launch request settles, success or failure, so
// callers can e.g. refresh a ticket list.
export function useClaudeLaunch(onDone?: () => void) {
  const { t } = useTranslation();
  const [isLaunching, setIsLaunching] = useState(false);
  const [lastMessage, setLastMessage] = useState('');
  // Holds the pending auto-clear timeout so a new launch() call or an
  // explicit reset() can cancel a stale one before it fires.
  const clearTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const cancelPendingClear = useCallback(() => {
    if (clearTimerRef.current !== null) {
      clearTimeout(clearTimerRef.current);
      clearTimerRef.current = null;
    }
  }, []);

  // Make sure no timer outlives the component using this hook.
  useEffect(() => cancelPendingClear, [cancelPendingClear]);

  // Lets callers (e.g. a modal reopening) immediately drop any leftover
  // message/timer from a previous session instead of waiting for the
  // auto-clear delay.
  const reset = useCallback(() => {
    cancelPendingClear();
    setLastMessage('');
  }, [cancelPendingClear]);

  const launch = useCallback(
    async (prompt: string, ticketId?: string, projectId?: string) => {
      cancelPendingClear();
      setIsLaunching(true);
      setLastMessage('');
      let succeeded = false;
      try {
        const response = await apiFetch('/api/claude/launch', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ prompt, ticketId, project_id: projectId })
        });
        if (!response.ok) {
          throw new Error(await localizedApiErrorMessage(t, response));
        }
        setLastMessage(t('claudeLaunch.started'));
        succeeded = true;
      } catch (err) {
        // errorMessage, not `err.message` off an `any`: a throw can produce
        // a value with no usable message (or none at all), and reading
        // .message off it would either interpolate "undefined" into the
        // sentence below or throw a second time inside this catch. The
        // fallback is claudeLaunch-specific rather than the errors.UNKNOWN
        // the other call sites use, because here the text is interpolated
        // into "Failed to launch the terminal: {{message}}" and has to read
        // as the tail of that sentence, not as a standalone message.
        setLastMessage(
          t('claudeLaunch.failed', { message: errorMessage(err, t('claudeLaunch.unknownReason')) })
        );
      } finally {
        setIsLaunching(false);
        clearTimerRef.current = setTimeout(() => {
          clearTimerRef.current = null;
          setLastMessage('');
        }, AUTO_CLEAR_DELAY_MS);
        onDone?.();
      }
      return succeeded;
    },
    [cancelPendingClear, onDone, t]
  );

  return { isLaunching, lastMessage, launch, reset };
}
