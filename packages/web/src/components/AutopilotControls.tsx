import React, { useEffect, useId, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Bot, GitFork, Loader2 } from 'lucide-react';
import { AutopilotMode, TicketStatus } from '../types';
import { startAutopilot, TicketAutopilotView } from '../lib/autopilotApi';
import { errorMessage } from '../lib/apiError';
import { StatusLiveRegion } from './StatusLiveRegion';
import { ConfirmDialog } from './ConfirmDialog';
import { SubmittingText, submittingProps } from './Submitting';

interface Props {
  ticketId: string;
  status: TicketStatus;
  view: TicketAutopilotView;
  // Called once a start request settles (success or failure), so the caller
  // can refresh the runs (and the badges) right away rather than at the
  // next poll. A returned promise is awaited -- for at most
  // SETTLE_TIMEOUT_MS -- before the buttons leave their "starting" state, so
  // they come back with the refreshed view (and focus goes back to a button
  // only if that view still leaves it enabled).
  onSettled?: () => void | Promise<void>;
}

// How long the result of a start stays on screen.
const MESSAGE_CLEAR_MS = 8000;

// DFLT-00149: the longest the buttons wait on onSettled after a start. A
// refresh that hangs (a stalled request) or fails must not leave them in
// their "starting" state until the page is reloaded: past this, they follow
// the view they have, and the next poll brings the refreshed runs. Shorter
// than the runs' poll interval, long enough for a normal refresh.
export const SETTLE_TIMEOUT_MS = 10000;

// Waits on onSettled, for at most SETTLE_TIMEOUT_MS. Never throws: a failure
// or the timeout is only logged, since the next poll refreshes the runs
// anyway.
async function awaitSettled(onSettled: (() => void | Promise<void>) | undefined): Promise<void> {
  let timeout: ReturnType<typeof setTimeout> | undefined;
  try {
    const settled = Promise.resolve(onSettled?.()).then(
      () => 'settled' as const,
      // Also catches a rejection that comes after the timeout won the race.
      (e: unknown) => {
        console.error('Failed to refresh after an autopilot start', e);
        return 'failed' as const;
      }
    );
    const timedOut = new Promise<'timeout'>(resolve => {
      timeout = setTimeout(() => resolve('timeout'), SETTLE_TIMEOUT_MS);
    });
    if ((await Promise.race([settled, timedOut])) === 'timeout') {
      console.warn(`The refresh after an autopilot start did not finish within ${SETTLE_TIMEOUT_MS} ms`);
    }
  } catch (e) {
    // onSettled threw synchronously.
    console.error('Failed to refresh after an autopilot start', e);
  } finally {
    if (timeout !== undefined) clearTimeout(timeout);
  }
}

const MODES: AutopilotMode[] = ['ticket', 'tree'];

// The "autopilot (single)" / "autopilot (tree)" buttons of a ticket's action
// footer (DFLT-00142 phase 5). A click asks for confirmation, then POSTs
// /api/tickets/{id}/autopilot, which reserves the run and opens the
// orchestrator's terminal.
//
// The confirmation is an in-app ConfirmDialog, not window.confirm
// (DFLT-00147), so browser automation and tests can drive it:
//
// - Its title, text and confirm button label are fixed when it opens (resume
//   or fresh start, from the view at that moment); a poll changing
//   `resumable` while it is open does not swap the text under the reader.
//   The server decides whether the run is actually resumed, and the result
//   message follows its answer.
// - If a poll shows the start would now be refused (the button gets a
//   disabled reason), the dialog closes by itself without starting; the
//   reason appears next to the buttons as usual. A start confirmed before the
//   poll caught up still gets the server's 409, shown translated.
// - The buttons stay enabled while the dialog is open (its overlay and Tab
//   wrap keep them out of reach; handleClick ignores a click anyway). A
//   button disabled while the dialog is open would, under React.StrictMode
//   in development, make useModalDialog remember the fallback instead of the
//   button (see its notes).
// - Focus: cancelling returns it to the clicked button. Confirming disables
//   the buttons while the request runs, so it goes to this component's root
//   (tabIndex={-1}) instead of falling to <body>, and back to the clicked
//   button once the request settles -- only if focus is still on the root
//   and the button is enabled in the refreshed view (a successful start
//   usually disables it: the new run owns the ticket). The refresh is waited
//   on for at most SETTLE_TIMEOUT_MS.
//
// A button is disabled -- with the reason shown as text next to the buttons
// and tied to it with aria-describedby, and as its tooltip -- when the start
// would be refused anyway: an active run owns the ticket (or, for a tree
// start, roots somewhere below it), or the ticket is DONE/CLOSED and there is
// no stopped or interrupted run of it to resume. The server stays the
// authority: a stale view that lets a duplicate through gets its 409, shown
// translated.
export const AutopilotControls: React.FC<Props> = ({ ticketId, status, view, onSettled }) => {
  const { t } = useTranslation();
  const [starting, setStarting] = useState<AutopilotMode | null>(null);
  const [message, setMessage] = useState<{ text: string; error: boolean } | null>(null);
  // DFLT-00182: the folder the server judged untrusted in its start
  // response. Unlike `message` it is not cleared by a timer -- the terminal
  // stays stuck at the trust prompt until someone acts -- only by its
  // dismiss button or the next start.
  const [untrustedFolder, setUntrustedFolder] = useState('');
  const untrustedId = useId();
  const [pending, setPending] = useState<{
    mode: AutopilotMode;
    title: string;
    message: string;
    confirmLabel: string;
  } | null>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const reasonIdBase = useId();
  const rootRef = useRef<HTMLDivElement>(null);
  const buttonRefs = useRef<Partial<Record<AutopilotMode, HTMLButtonElement | null>>>({});
  // The mode whose start is in flight, to put focus back on its button once
  // `starting` returns to null.
  const lastStarted = useRef<AutopilotMode | null>(null);

  useEffect(
    () => () => {
      if (timer.current !== null) clearTimeout(timer.current);
    },
    []
  );

  const finished = status === 'DONE' || status === 'CLOSED';
  const reasonFor = (mode: AutopilotMode): string => {
    const blocked = view.blockedBy[mode];
    if (blocked) {
      return mode === 'tree' && !view.blockedBy.ticket
        ? t('autopilot.blockedDescendant', { root: blocked })
        : t('autopilot.blocked', { root: blocked });
    }
    if (finished && !view.resumable[mode]) return t('autopilot.finished');
    return '';
  };
  const reasons = MODES.map(reasonFor);
  // One line per distinct reason (the two buttons usually share it).
  const distinctReasons = reasons.filter((r, i) => r && reasons.indexOf(r) === i);

  // Close the dialog, without starting, once the view says the start would
  // be refused. Checked only while it is open, so the confirm path (which
  // sets pending to null itself) never runs into it.
  const pendingReason = pending ? reasonFor(pending.mode) : '';
  useEffect(() => {
    if (pending && pendingReason) setPending(null);
  }, [pending, pendingReason]);

  // Once a start settles: back to the clicked button, if focus is still where
  // the dialog left it (the root) and the button is enabled. Otherwise focus
  // stays where it is -- never taken from wherever the user moved it.
  useEffect(() => {
    if (starting !== null) return;
    const mode = lastStarted.current;
    if (mode === null) return;
    lastStarted.current = null;
    const button = buttonRefs.current[mode];
    if (button && !button.disabled && document.activeElement === rootRef.current) button.focus();
  }, [starting]);

  const show = (text: string, error: boolean) => {
    if (timer.current !== null) clearTimeout(timer.current);
    setMessage({ text, error });
    timer.current = setTimeout(() => {
      timer.current = null;
      setMessage(null);
    }, MESSAGE_CLEAR_MS);
  };

  // Opens the confirmation, with its text fixed from the view as it is now.
  const handleClick = (mode: AutopilotMode) => {
    if (pending !== null || starting !== null) return;
    const resume = view.resumable[mode];
    setPending({
      mode,
      title: t(resume ? 'autopilot.confirm.resumeTitle' : 'autopilot.confirm.title'),
      message: resume
        ? t('autopilot.confirm.resume', { id: ticketId, mode: t(`autopilot.modes.${mode}`) })
        : t(`autopilot.confirm.${mode}`, { id: ticketId }),
      confirmLabel: t(resume ? 'autopilot.confirm.resumeStart' : 'autopilot.confirm.start')
    });
  };

  const runStart = async (mode: AutopilotMode) => {
    lastStarted.current = mode;
    setStarting(mode);
    setUntrustedFolder('');
    try {
      const res = await startAutopilot(t, ticketId, mode);
      show(t(res.resumed ? 'autopilot.resumed' : 'autopilot.started', { runId: res.run_id }), false);
      if (typeof res.untrusted_folder === 'string' && res.untrusted_folder !== '') setUntrustedFolder(res.untrusted_folder);
    } catch (e) {
      show(t('autopilot.failed', { message: errorMessage(e, t('errors.UNKNOWN')) }), true);
    } finally {
      await awaitSettled(onSettled);
      setStarting(null);
    }
  };

  // The dismiss button unmounts with the notice: focus goes to the root
  // rather than falling to <body>.
  const dismissUntrusted = () => {
    setUntrustedFolder('');
    rootRef.current?.focus();
  };

  const handleConfirm = () => {
    if (!pending) return;
    const { mode } = pending;
    // Closed in the same render that disables the buttons: the dialog's
    // focus return then finds the button disabled and uses rootRef.
    setPending(null);
    void runStart(mode);
  };

  return (
    // tabIndex={-1}: the focus fallback while a confirmed start runs (see
    // above). Not a Tab stop; the ring shows when it gets focus that way
    // after keyboard use.
    <div
      ref={rootRef}
      tabIndex={-1}
      data-testid="autopilot-controls"
      className="flex flex-col gap-1.5 rounded-lg focus:outline-none focus-visible:ring-2 focus-visible:ring-violet-500 focus-visible:ring-offset-2 dark:focus-visible:ring-offset-slate-900"
    >
      <div className="flex flex-wrap items-center gap-2">
        {MODES.map((mode, i) => {
          const reason = reasons[i];
          const reasonId = reason ? `${reasonIdBase}-reason-${distinctReasons.indexOf(reason)}` : undefined;
          return (
            <button
              key={mode}
              ref={el => {
                buttonRefs.current[mode] = el;
              }}
              type="button"
              data-testid={`autopilot-start-${mode}`}
              onClick={() => handleClick(mode)}
              disabled={starting !== null || reason !== ''}
              {...submittingProps(starting === mode)}
              title={reason || undefined}
              aria-describedby={reasonId}
              className="px-3 py-1.5 bg-white dark:bg-slate-800 hover:bg-violet-50 dark:hover:bg-slate-700 text-violet-800 dark:text-violet-200 border border-violet-300 dark:border-violet-700 rounded-lg text-xs font-semibold flex items-center gap-1.5 shadow-xs transition disabled:opacity-50 disabled:cursor-not-allowed disabled:hover:bg-white dark:disabled:hover:bg-slate-800"
            >
              {starting === mode ? (
                <Loader2 className="w-3.5 h-3.5 motion-safe:animate-spin" aria-hidden="true" />
              ) : mode === 'tree' ? (
                <GitFork className="w-3.5 h-3.5" aria-hidden="true" />
              ) : (
                <Bot className="w-3.5 h-3.5" aria-hidden="true" />
              )}
              {t(`autopilot.buttons.${mode}`)}
              {/* Only the mode being started shows the spinner, so only it is busy. */}
              <SubmittingText busy={starting === mode} />
            </button>
          );
        })}
      </div>
      {view.awaiting && (
        // What the person is waited on for, as text a sighted keyboard user
        // can read too (the badge only carries it as a tooltip).
        <p data-testid="autopilot-awaiting" className="text-[11px] text-amber-900 dark:text-amber-100">
          {t('autopilot.badges.awaitingTitle', { what: view.awaiting })}
        </p>
      )}
      {distinctReasons.map((r, i) => (
        <p
          key={r}
          id={`${reasonIdBase}-reason-${i}`}
          data-testid="autopilot-disabled-reason"
          className="text-[11px] text-slate-600 dark:text-slate-400"
        >
          {r}
        </p>
      ))}
      <StatusLiveRegion message={message?.text ?? ''} />
      {message && (
        <div
          aria-hidden="true"
          data-testid="autopilot-message"
          className={`p-2 rounded-lg border text-[11px] ${
            message.error
              ? 'bg-red-50 dark:bg-red-950 border-red-200 dark:border-red-800 text-red-800 dark:text-red-200'
              : 'bg-slate-50 dark:bg-slate-800 border-slate-200 dark:border-slate-700 text-slate-700 dark:text-slate-300'
          }`}
        >
          {message.text}
        </div>
      )}
      {/* Always mounted (StatusLiveRegion): the notice is announced when it
          appears, and the dismiss button is described by it. */}
      <StatusLiveRegion
        id={untrustedId}
        message={untrustedFolder ? t('autopilot.untrustedFolder', { path: untrustedFolder }) : ''}
      />
      {untrustedFolder && (
        <div
          data-testid="autopilot-untrusted"
          className="flex items-start gap-2 p-2 rounded-lg border text-[11px] bg-amber-50 dark:bg-amber-950 border-amber-300 dark:border-amber-800 text-amber-900 dark:text-amber-100"
        >
          <p aria-hidden="true" className="flex-1 min-w-0 break-words [overflow-wrap:anywhere]">
            {t('autopilot.untrustedFolder', { path: untrustedFolder })}
          </p>
          <button
            type="button"
            data-testid="autopilot-untrusted-dismiss"
            onClick={dismissUntrusted}
            aria-describedby={untrustedId}
            className="shrink-0 px-2 py-0.5 rounded border border-amber-400 dark:border-amber-700 bg-white dark:bg-slate-800 font-semibold hover:bg-amber-100 dark:hover:bg-slate-700 focus:outline-none focus-visible:ring-2 focus-visible:ring-violet-500"
          >
            {t('autopilot.untrustedDismiss')}
          </button>
        </div>
      )}
      {pending && (
        <ConfirmDialog
          title={pending.title}
          message={pending.message}
          confirmLabel={pending.confirmLabel}
          cancelLabel={t('autopilot.confirm.cancel')}
          onConfirm={handleConfirm}
          onCancel={() => setPending(null)}
          returnFocusFallbackRef={rootRef}
          testIdPrefix="autopilot-confirm"
        />
      )}
    </div>
  );
};
