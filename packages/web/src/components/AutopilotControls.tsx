import React, { useEffect, useId, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Bot } from 'lucide-react';
import { AutopilotMode, TicketStatus } from '../types';
import { startAutopilot, TicketAutopilotView } from '../lib/autopilotApi';
import { errorMessage } from '../lib/apiError';
import { StatusLiveRegion } from './StatusLiveRegion';
import { ConfirmDialog } from './ConfirmDialog';
import { SubmittingText, submittingProps } from './Submitting';
import { Spinner } from './Spinner';

interface Props {
  ticketId: string;
  status: TicketStatus;
  view: TicketAutopilotView;
  // Called once a start request settles (success or failure), so the caller
  // can refresh the runs (and the badges) right away rather than at the
  // next poll. A returned promise is awaited -- for at most
  // SETTLE_TIMEOUT_MS -- before the button leaves its "starting" state, so
  // it comes back with the refreshed view (and focus goes back to it only if
  // that view still leaves it enabled).
  onSettled?: () => void | Promise<void>;
  // The action row's regular actions (refine, run), laid out on its left;
  // the autopilot button sits at its right end (DFLT-00181). They stay
  // outside the focus fallback (DFLT-00218).
  actions?: React.ReactNode;
}

// How long the result of a start stays on screen.
const MESSAGE_CLEAR_MS = 8000;

// DFLT-00149: the longest the button waits on onSettled after a start. A
// refresh that hangs (a stalled request) or fails must not leave it in its
// "starting" state until the page is reloaded: past this, it follows the
// view it has, and the next poll brings the refreshed runs. Shorter
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

// The dialog lists the tree first: it is the usual choice, and the initial
// selection whenever it can start.
const DIALOG_MODES: AutopilotMode[] = ['tree', 'ticket'];

// The "autopilot" button of a ticket's action footer (DFLT-00142 phase 5;
// one button since DFLT-00181). A click asks for confirmation -- where the
// run's scope is chosen: the tree (this ticket and its descendants, the
// initial choice) or this ticket only -- then POSTs
// /api/tickets/{id}/autopilot with that mode, which reserves the run and
// opens the orchestrator's terminal.
//
// Layout (DFLT-00181, DFLT-00218, DFLT-00219): this component lays out the
// whole action row -- the caller's regular actions (`actions`: refine, run)
// on the left, and at the right end (ml-auto, in a layout-only slot) the
// autopilot column: the button, behind a divider on sm+ screens, and the
// lines under it (what a person is awaited for, the disabled reasons, the
// result, the untrusted-folder notice). Below sm the row wraps and the
// column moves to its own line with the button still right-aligned, set
// apart by that and by its violet colours -- no divider there.
// On sm+ the divider only makes sense right next to the regular actions: a
// column moved to the next line would leave it at the start of an otherwise
// empty line. That did happen at 640-700px (in English and Japanese alike)
// once the column had lines under the button, which widen it up to its
// max-w-xs. So on sm+ the row only wraps when not even the narrowest
// layout fits (DFLT-00219):
// - Both the regular actions and the slot start from a zero basis
//   (sm:basis-0) with their min-content as the floor (sm:min-w-min), so the
//   row wraps only when those floors do not fit side by side -- the slot's
//   floor being the whole button, whose label does not wrap on sm+
//   (sm:whitespace-nowrap).
// - The free space goes to the regular actions first (sm:grow-[999]) up to
//   their one-line width (sm:max-w-max), then to the slot, up to its
//   max-w-xs (the column's lines wrap inside it; the column hugs the slot's
//   right end, so its focus ring still fits the autopilot). What is left is
//   the ml-auto gap.
// - So with larger text (a 150% default font size, say) the regular actions
//   wrap inside their own box, instead of the button being squeezed or
//   drawn over them (as it was with a non-wrapping row).
// - Only at an extreme (a 200% default font size at 640px, say) do the
//   floors not fit and the row wraps. Sizes are in rem, so the divider is
//   also tied to the row's width in rem, not to the viewport: it shows only
//   while the row (a size container) is at least 16rem wide. The narrowest
//   layout needs about 14rem, so the row never wraps with the divider on.
//   Below that the autopilot is set apart by its place and colours alone,
//   as below sm.
// Below sm, with large text (a 200% default font size at 375px, say), the
// row can be narrower than a button's icon, label and padding side by side
// (DFLT-00224). The buttons -- the regular actions (see TicketItem) and the
// autopilot -- then put the icon on a line of its own and break the label
// anywhere (max-sm:flex-wrap, max-sm:wrap-anywhere), so they stay
// inside the row instead of running past the card; the untrusted-folder
// notice moves its dismiss button under the text the same way (at any
// width since DFLT-00225, see below). Nothing
// wraps while everything fits on a line, so the default size looks as
// before. Only below sm: on sm+ the labels do not wrap, being the floors
// the layout above is built on.
// That alone was not enough at 320px with a 200% default font size, where
// the rem padding of the nested cards left the row about 60px -- narrower
// than one button or the "Actions:" label. There TicketItem pads the
// expanded details and the Action Footer less (a rem media query, so only
// with large text on a narrow screen), which leaves the row about 124px
// (DFLT-00227).
// The lines under the button (and the mode reasons in the dialog) are sized
// in rem too (text-[0.6875rem], 11px at the default 16px), so they grow with
// the browser's default font size like the rest of the row (DFLT-00225).
// They break anywhere (wrap-anywhere), as the notice already did: a run ID
// such as "(run-20260927-012345-" has no break opportunity Chrome takes, and
// at 22px it is wider than the 124px row at 320px with a 200% default font
// size. overflow-wrap:anywhere also shrinks their min-content, so they never
// widen the column the divider relies on. wrap-anywhere alone, without
// wrap-break-word: Tailwind v4 emits wrap-break-word after wrap-anywhere, so
// with both on an element break-word would win and undo this (DFLT-00270).
// With larger text the column can then be as narrow as its button on sm+
// as well, so the untrusted-folder notice moves its dismiss button under
// the text at any width, not only below sm.
// The outer element only lays out the row and never takes focus; the column
// is the focus fallback below, so it holds the button and the text about it
// but not the regular actions -- its focus ring (and what a screen reader
// reads out there) covers the autopilot alone.
//
// The confirmation is an in-app ConfirmDialog, not window.confirm
// (DFLT-00147), so browser automation and tests can drive it:
//
// - The scope is a pair of native radio buttons in a fieldset. A mode whose
//   start would be refused is a disabled choice with its reason under it
//   (aria-describedby); that follows the live view while the dialog is open.
//   The initial choice is the tree, or this ticket alone when the tree
//   cannot start. The choice is never switched by a poll.
// - Its title, text and confirm button label follow the chosen mode. Whether
//   they speak of a resume or a fresh start comes from `resumable` as it was
//   when the dialog opened (for both modes), so a poll changing it while it
//   is open does not swap the text under the reader. The server decides
//   whether the run is actually resumed, and the result message follows its
//   answer.
// - If a poll shows the chosen mode's start would now be refused, the dialog
//   closes by itself without starting; if only the other mode is refused, its
//   choice is disabled and the dialog stays. A start confirmed before the poll
//   caught up still gets the server's 409, shown translated.
// - The button stays enabled while the dialog is open (its overlay and Tab
//   wrap keep it out of reach; handleClick ignores a click anyway). A button
//   disabled while the dialog is open would, under React.StrictMode in
//   development, make useModalDialog remember the fallback instead of the
//   button (see its notes).
// - Focus: cancelling returns it to the button. Confirming disables the
//   button while the request runs, so it goes to the autopilot column
//   (tabIndex={-1}, the focus fallback) instead of falling to <body>, and back
//   to the button once the request settles -- only if focus is still on the
//   fallback and the button is enabled in the refreshed view (a successful
//   start usually disables it: the new run owns the ticket). The refresh is
//   waited on for at most SETTLE_TIMEOUT_MS.
//
// A mode cannot start -- the start would be refused anyway -- when an active
// run owns the ticket (or, for a tree start, roots somewhere below it), or
// the ticket is DONE/CLOSED and there is no stopped or interrupted run of it
// in that mode to resume. When neither mode can start, the button is
// disabled, with the reasons shown as text under the button, tied to it
// with aria-describedby, and as its tooltip. The server stays the authority:
// a stale view that lets a duplicate through gets its 409, shown translated.
export const AutopilotControls: React.FC<Props> = ({ ticketId, status, view, onSettled, actions }) => {
  const { t } = useTranslation();
  const [starting, setStarting] = useState<AutopilotMode | null>(null);
  const [message, setMessage] = useState<{ text: string; error: boolean } | null>(null);
  // DFLT-00182: the folder the server judged untrusted in its start
  // response. Unlike `message` it is not cleared by a timer -- the terminal
  // stays stuck at the trust prompt until someone acts -- only by its
  // dismiss button or the next start.
  const [untrustedFolder, setUntrustedFolder] = useState('');
  const untrustedId = useId();
  // The open confirmation, with `resumable` as it was when it opened.
  const [pending, setPending] = useState<{ resumable: Record<AutopilotMode, boolean> } | null>(null);
  const [selectedMode, setSelectedMode] = useState<AutopilotMode>('tree');
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const reasonIdBase = useId();
  const radioName = useId();
  // The focus fallback: the autopilot column (see the layout notes above).
  const fallbackRef = useRef<HTMLDivElement>(null);
  const buttonRef = useRef<HTMLButtonElement>(null);
  // Whether a start is in flight, to put focus back on the button once
  // `starting` returns to null.
  const lastStarted = useRef(false);

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
  const reasons: Record<AutopilotMode, string> = { ticket: reasonFor('ticket'), tree: reasonFor('tree') };
  // Neither mode can start: the button is disabled, with one line per
  // distinct reason (the two modes usually share it) under the button.
  const noneStartable = reasons.ticket !== '' && reasons.tree !== '';
  const distinctReasons = noneStartable
    ? [reasons.ticket, reasons.tree].filter((r, i, all) => all.indexOf(r) === i)
    : [];
  const reasonIds = distinctReasons.map((_, i) => `${reasonIdBase}-reason-${i}`);

  // Close the dialog, without starting, once the view says the chosen mode's
  // start would be refused. Checked only while it is open, so the confirm
  // path (which sets pending to null itself) never runs into it.
  const pendingReason = pending ? reasons[selectedMode] : '';
  useEffect(() => {
    if (pending && pendingReason) setPending(null);
  }, [pending, pendingReason]);

  // Once a start settles: back to the button, if focus is still where the
  // dialog left it (the fallback) and the button is enabled. Otherwise focus
  // stays where it is -- never taken from wherever the user moved it.
  useEffect(() => {
    if (starting !== null) return;
    if (!lastStarted.current) return;
    lastStarted.current = false;
    const button = buttonRef.current;
    if (button && !button.disabled && document.activeElement === fallbackRef.current) button.focus();
  }, [starting]);

  const show = (text: string, error: boolean) => {
    if (timer.current !== null) clearTimeout(timer.current);
    setMessage({ text, error });
    timer.current = setTimeout(() => {
      timer.current = null;
      setMessage(null);
    }, MESSAGE_CLEAR_MS);
  };

  // Opens the confirmation on the tree, or on this ticket alone when the tree
  // cannot start, with `resumable` fixed from the view as it is now.
  const handleClick = () => {
    if (pending !== null || starting !== null) return;
    setSelectedMode(reasons.tree ? 'ticket' : 'tree');
    setPending({ resumable: { ...view.resumable } });
  };

  const runStart = async (mode: AutopilotMode) => {
    lastStarted.current = true;
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

  // The dismiss button unmounts with the notice: focus goes to the fallback
  // rather than falling to <body>.
  const dismissUntrusted = () => {
    setUntrustedFolder('');
    fallbackRef.current?.focus();
  };

  const handleConfirm = () => {
    if (!pending || reasons[selectedMode]) return;
    const mode = selectedMode;
    // Closed in the same render that disables the button: the dialog's
    // focus return then finds the button disabled and uses fallbackRef.
    setPending(null);
    void runStart(mode);
  };

  // The dialog's text, for the chosen mode; resume or fresh start from
  // `resumable` as it was when the dialog opened.
  const resume = pending !== null && pending.resumable[selectedMode];
  const modeName = t(`autopilot.modes.${selectedMode}`);
  const dialogTitle = t(resume ? 'autopilot.confirm.resumeTitle' : 'autopilot.confirm.title', { mode: modeName });
  const dialogMessage = resume
    ? t('autopilot.confirm.resume', { id: ticketId, mode: modeName })
    : t(`autopilot.confirm.${selectedMode}`, { id: ticketId });
  const dialogConfirmLabel = t(resume ? 'autopilot.confirm.resumeStart' : 'autopilot.confirm.start');

  return (
    // The action row: lays it out only, never takes focus (DFLT-00218).
    // On sm+ it wraps only when not even the narrowest layout fits, and is
    // the size container the divider is tied to (DFLT-00219, see above).
    <div data-testid="autopilot-controls" className="flex flex-wrap items-start gap-2 @container">
      {actions != null && (
        <div data-testid="autopilot-row-actions" className="min-w-0 sm:min-w-min sm:basis-0 sm:grow-999 sm:max-w-max">
          {actions}
        </div>
      )}
      {/* The slot for the autopilot column: pushed to the right end (on its
          own line when the row wraps); on sm+ it takes the width the regular
          actions leave, never less than the column's min-content
          (DFLT-00219, see above). Layout only. */}
      <div
        data-testid="autopilot-slot"
        className="ml-auto flex justify-end min-w-0 max-w-full sm:grow sm:basis-0 sm:min-w-min sm:max-w-xs"
      >
        {/* The autopilot column, set apart from the regular actions: at the
            right end of its slot, the button right-aligned in it behind a
            divider on sm+ (while the row is 16rem wide or more), the lines
            about it under the button. tabIndex={-1}:
            the focus fallback while a confirmed start runs (see above), so its
            ring surrounds the autopilot alone. Not a Tab stop; the ring shows
            when it gets focus that way after keyboard use. */}
        <div
          ref={fallbackRef}
          tabIndex={-1}
          data-testid="autopilot-focus-fallback"
          className="flex flex-col items-end gap-1.5 min-w-0 max-w-full rounded-lg focus:outline-hidden focus-visible:ring-2 focus-visible:ring-violet-500 focus-visible:ring-offset-2 dark:focus-visible:ring-offset-slate-900"
        >
          <div
            data-testid="autopilot-group"
            className="flex items-center sm:cq-from-16rem:border-l sm:cq-from-16rem:pl-3 border-slate-200 dark:border-slate-700"
          >
            <button
              ref={buttonRef}
              type="button"
              data-testid="autopilot-start"
              onClick={handleClick}
              disabled={starting !== null || noneStartable}
              {...submittingProps(starting !== null)}
              title={distinctReasons.length > 0 ? distinctReasons.join('\n') : undefined}
              aria-describedby={reasonIds.length > 0 ? reasonIds.join(' ') : undefined}
              className="px-3 py-1.5 bg-white dark:bg-slate-800 hover:bg-violet-50 dark:hover:bg-slate-700 text-violet-800 dark:text-violet-200 border border-violet-300 dark:border-violet-700 rounded-lg text-xs font-semibold sm:whitespace-nowrap flex max-sm:flex-wrap max-sm:wrap-anywhere items-center gap-1.5 transition disabled:opacity-50 disabled:cursor-not-allowed disabled:hover:bg-white dark:disabled:hover:bg-slate-800"
            >
              {starting !== null ? (
                <Spinner className="w-3.5 h-3.5 shrink-0" />
              ) : (
                <Bot className="w-3.5 h-3.5 shrink-0" aria-hidden="true" />
              )}
              {t('autopilot.button')}
              <SubmittingText busy={starting !== null} />
            </button>
          </div>
          {view.awaiting && (
            // What the person is waited on for, as text a sighted keyboard user
            // can read too (the badge only carries it as a tooltip).
            <p data-testid="autopilot-awaiting" className="self-stretch text-[0.6875rem] wrap-anywhere text-amber-900 dark:text-amber-100">
              {t('autopilot.badges.awaitingTitle', { what: view.awaiting })}
            </p>
          )}
          {distinctReasons.map((r, i) => (
            <p
              key={r}
              id={reasonIds[i]}
              data-testid="autopilot-disabled-reason"
              className="self-stretch text-[0.6875rem] wrap-anywhere text-slate-600 dark:text-slate-400"
            >
              {r}
            </p>
          ))}
          <StatusLiveRegion message={message?.text ?? ''} />
          {message && (
            <div
              aria-hidden="true"
              data-testid="autopilot-message"
              className={`self-stretch p-2 rounded-lg border text-[0.6875rem] wrap-anywhere ${
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
            // The dismiss button moves under the text once both no longer
            // fit on one line (the text wants 6rem), and is never wider than
            // the notice (DFLT-00224, see above). At any width, not only below
            // sm (DFLT-00225): on sm+ with large text the column can be as
            // narrow as its button, and the dismiss button (which does not
            // shrink) squeezed the text to a letter a line. The dismiss
            // button is at least 1.5rem (24px at 100%) each way, a target
            // large enough to press, with its label centred, and shows its
            // focus ring in dark mode too (DFLT-00252, WCAG 2.5.8 / 2.4.7).
            // DFLT-00259: the minimum width is capped at the notice's
            // content width (min(1.5rem,100%): 1.5rem wherever that fits, so
            // nothing changes at normal widths), so the button's frame never
            // runs past a notice narrower than that.
            <div
              data-testid="autopilot-untrusted"
              className="self-stretch flex flex-wrap items-start gap-2 p-2 rounded-lg border text-[0.6875rem] bg-amber-50 dark:bg-amber-950 border-amber-300 dark:border-amber-800 text-amber-900 dark:text-amber-100"
            >
              <p aria-hidden="true" className="flex-1 basis-24 min-w-0 wrap-anywhere">
                {t('autopilot.untrustedFolder', { path: untrustedFolder })}
              </p>
              <button
                type="button"
                data-testid="autopilot-untrusted-dismiss"
                onClick={dismissUntrusted}
                aria-describedby={untrustedId}
                className="shrink-0 max-w-full max-sm:wrap-anywhere min-h-6 min-w-[min(1.5rem,100%)] inline-flex items-center justify-center px-2 py-0.5 rounded-sm border border-amber-400 dark:border-amber-700 bg-white dark:bg-slate-800 font-semibold hover:bg-amber-100 dark:hover:bg-slate-700 focus:outline-hidden focus-visible:ring-2 focus-visible:ring-violet-500 dark:focus-visible:ring-violet-400"
              >
                {t('autopilot.untrustedDismiss')}
              </button>
            </div>
          )}
          {pending && (
            <ConfirmDialog
              title={dialogTitle}
              message={dialogMessage}
              confirmLabel={dialogConfirmLabel}
              cancelLabel={t('autopilot.confirm.cancel')}
              onConfirm={handleConfirm}
              onCancel={() => setPending(null)}
              returnFocusFallbackRef={fallbackRef}
              testIdPrefix="autopilot-confirm"
            >
              <fieldset data-testid="autopilot-mode" className="mb-4">
                <legend className="text-xs font-semibold text-slate-700 dark:text-slate-300 mb-2">
                  {t('autopilot.confirm.modeLegend')}
                </legend>
                <div className="flex flex-col gap-2">
                  {DIALOG_MODES.map(mode => {
                    const reason = reasons[mode];
                    const reasonId = `${reasonIdBase}-mode-reason-${mode}`;
                    return (
                      <div key={mode}>
                        <label
                          className={`flex items-center gap-2 text-sm text-slate-800 dark:text-slate-200 ${
                            reason ? 'opacity-60 cursor-not-allowed' : 'cursor-pointer'
                          }`}
                        >
                          <input
                            type="radio"
                            name={radioName}
                            value={mode}
                            data-testid={`autopilot-mode-${mode}`}
                            checked={selectedMode === mode}
                            disabled={reason !== ''}
                            onChange={() => setSelectedMode(mode)}
                            aria-describedby={reason ? reasonId : undefined}
                            className="accent-violet-600"
                          />
                          {t(`autopilot.modeOptions.${mode}`)}
                        </label>
                        {reason && (
                          <p
                            id={reasonId}
                            data-testid={`autopilot-mode-reason-${mode}`}
                            className="ml-6 mt-0.5 text-[0.6875rem] wrap-anywhere text-slate-600 dark:text-slate-400"
                          >
                            {reason}
                          </p>
                        )}
                      </div>
                    );
                  })}
                </div>
              </fieldset>
            </ConfirmDialog>
          )}
        </div>
      </div>
    </div>
  );
};
