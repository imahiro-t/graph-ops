import React, { useEffect, useId, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Bot, GitFork, Loader2 } from 'lucide-react';
import { AutopilotMode, TicketStatus } from '../types';
import { startAutopilot, TicketAutopilotView } from '../lib/autopilotApi';
import { errorMessage } from '../lib/apiError';
import { StatusLiveRegion } from './StatusLiveRegion';

interface Props {
  ticketId: string;
  status: TicketStatus;
  view: TicketAutopilotView;
  // Called once a start request settles (success or failure), so the caller
  // can refresh the runs (and the badges) right away rather than at the
  // next poll.
  onSettled?: () => void;
}

// How long the result of a start stays on screen.
const MESSAGE_CLEAR_MS = 8000;

const MODES: AutopilotMode[] = ['ticket', 'tree'];

// The "autopilot (single)" / "autopilot (tree)" buttons of a ticket's action
// footer (DFLT-00142 phase 5). A click asks for confirmation, then POSTs
// /api/tickets/{id}/autopilot, which reserves the run and opens the
// orchestrator's terminal.
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
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const reasonIdBase = useId();

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

  const show = (text: string, error: boolean) => {
    if (timer.current !== null) clearTimeout(timer.current);
    setMessage({ text, error });
    timer.current = setTimeout(() => {
      timer.current = null;
      setMessage(null);
    }, MESSAGE_CLEAR_MS);
  };

  const handleStart = async (mode: AutopilotMode) => {
    const confirmText = view.resumable[mode]
      ? t('autopilot.confirm.resume', { id: ticketId, mode: t(`autopilot.modes.${mode}`) })
      : t(`autopilot.confirm.${mode}`, { id: ticketId });
    if (!window.confirm(confirmText)) return;
    setStarting(mode);
    try {
      const res = await startAutopilot(t, ticketId, mode);
      show(t(res.resumed ? 'autopilot.resumed' : 'autopilot.started', { runId: res.run_id }), false);
    } catch (e) {
      show(t('autopilot.failed', { message: errorMessage(e, t('errors.UNKNOWN')) }), true);
    } finally {
      setStarting(null);
      onSettled?.();
    }
  };

  return (
    <div className="flex flex-col gap-1.5" data-testid="autopilot-controls">
      <div className="flex flex-wrap items-center gap-2">
        {MODES.map((mode, i) => {
          const reason = reasons[i];
          const reasonId = reason ? `${reasonIdBase}-reason-${distinctReasons.indexOf(reason)}` : undefined;
          return (
            <button
              key={mode}
              type="button"
              data-testid={`autopilot-start-${mode}`}
              onClick={() => handleStart(mode)}
              disabled={starting !== null || reason !== ''}
              title={reason || undefined}
              aria-describedby={reasonId}
              className="px-3 py-1.5 bg-white dark:bg-slate-800 hover:bg-violet-50 dark:hover:bg-slate-700 text-violet-800 dark:text-violet-200 border border-violet-300 dark:border-violet-700 rounded-lg text-xs font-semibold flex items-center gap-1.5 shadow-xs transition disabled:opacity-50 disabled:cursor-not-allowed disabled:hover:bg-white dark:disabled:hover:bg-slate-800"
            >
              {starting === mode ? (
                <Loader2 className="w-3.5 h-3.5 animate-spin" aria-hidden="true" />
              ) : mode === 'tree' ? (
                <GitFork className="w-3.5 h-3.5" aria-hidden="true" />
              ) : (
                <Bot className="w-3.5 h-3.5" aria-hidden="true" />
              )}
              {t(`autopilot.buttons.${mode}`)}
            </button>
          );
        })}
      </div>
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
    </div>
  );
};
