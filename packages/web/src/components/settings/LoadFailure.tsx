// What a settings editor shows in place of its form when its settings (or
// the selected item's text) could not be loaded (DFLT-00343 for
// AppSettingsEditor, shared with the other editors in DFLT-00350).
//
// The form is not drawn at all while the load has failed: it would hold the
// defaults (or an empty textarea), not the stored values, and saving it
// would overwrite what is stored with them. The error and a retry button
// take its place.
//
// The rules every editor follows around it:
//
// - What is drawn is decided in this order: (1) the current target's load
//   has failed -> LoadFailure, (2) it is loading -> LoadingLine, (3) the
//   form. The failure comes first because a failed load never marks the
//   target as loaded, so the "loading" test would stay true forever and the
//   failure would never be shown -- and because a retry keeps this view (and
//   the focused retry button, showing that it is busy) until its result is
//   in.
// - A retry does not clear the failure when it starts: it sets `retrying`
//   and leaves this view up until its own result is in. On success the
//   failure is cleared and useFocusAfterRetry moves focus into the form; on
//   failure the message is updated and `failureKey` goes up by one, which
//   remounts the alert so that a failure with the same wording is announced
//   again.
// - An editor that switches between targets (an item, a project) keeps the
//   failure together with the target it belongs to and shows it only while
//   that target is the current one, so a switch never shows the previous
//   target's error under the new one.
import { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { RotateCw } from 'lucide-react';
import { SubmittingText, submittingProps } from '../Submitting';
import { focusIfLost } from '../../lib/focusAfterRemoval';
import { ErrorBox } from './ErrorBox';
import { Spinner } from '../Spinner';

interface Props {
  /** The error text to show, already worded by the caller. */
  message: string;
  /** A retry is running: the button says so and ignores clicks. */
  retrying: boolean;
  onRetry: () => void;
  /** The number of failed loads so far; remounts the alert on each one. */
  failureKey: number;
}

export function LoadFailure({ message, retrying, onRetry, failureKey }: Props) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col items-start gap-3 py-4">
      <ErrorBox key={failureKey} role="alert" className="p-2.5 text-[0.6875rem] whitespace-pre-wrap self-stretch">
        {message}
      </ErrorBox>
      {/* Styled like App.tsx's retry button for a project that failed to
          load (DFLT-00164 contrast, DFLT-00167 focus ring). aria-disabled,
          not disabled, while a retry is running, so the button keeps focus;
          hence the guard against a second request in onClick. */}
      <button
        type="button"
        onClick={() => {
          if (!retrying) onRetry();
        }}
        aria-disabled={retrying}
        {...submittingProps(retrying)}
        className="px-3.5 py-1.5 rounded-lg border border-slate-300 dark:border-slate-700 text-slate-600 dark:text-slate-300 hover:bg-slate-100 dark:hover:bg-slate-800 text-xs font-semibold inline-flex items-center gap-1.5 transition focus:outline-hidden focus-visible:ring-2 focus-visible:ring-blue-500 dark:focus-visible:ring-blue-400 aria-disabled:cursor-wait"
      >
        {retrying ? <Spinner className="w-4 h-4" /> : <RotateCw aria-hidden="true" className="w-4 h-4" />}
        {t('settings.common.retry')}
        <SubmittingText busy={retrying} />
      </button>
    </div>
  );
}

// A successful retry unmounts the focused retry button, dropping focus to
// <body>. Call the returned function once the retry has succeeded: after the
// renders that follow, focus moves to the element getTarget returns -- unless
// the user has moved it somewhere else meanwhile (focusIfLost). While the
// element is missing (the form is still loading) or disabled, the move waits
// for a later render, the same rule as LabelsEditor's pendingFocus.
//
// getTarget may be an inline arrow or a memoized function: the hook does
// not depend on its identity (see the effect below).
export function useFocusAfterRetry(getTarget: () => HTMLElement | null | undefined): () => void {
  const [pending, setPending] = useState(false);
  // The target may only appear (or be enabled) a few renders after the
  // retry, and those renders are caused by the caller's own state, not by
  // anything this hook can list. So the effect has no dependency array: it
  // runs after every render, calls the getTarget of that render, and does
  // nothing while no move is pending. It cannot loop: it sets state only
  // once, when the move is done, and then stops at `!pending`.
  // eslint-disable-next-line react-hooks/exhaustive-deps -- deliberately runs after every render, see above
  useEffect(() => {
    if (!pending) return;
    const el = getTarget();
    if (!el || (el as HTMLButtonElement | HTMLInputElement).disabled) return;
    focusIfLost(el);
    setPending(false);
  });
  return useCallback(() => setPending(true), []);
}
