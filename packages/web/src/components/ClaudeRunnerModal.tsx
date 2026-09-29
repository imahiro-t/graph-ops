import React, { useEffect, useId, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Terminal, X, ExternalLink } from 'lucide-react';
import { useClaudeLaunch } from '../hooks/useClaudeLaunch';
import { useModalDialog } from '../hooks/useModalDialog';
import { isSubmitShortcut } from '../lib/keyboardShortcuts';
import { StatusLiveRegion } from './StatusLiveRegion';
import { SubmittingText, submittingProps } from './Submitting';
import { Spinner } from './Spinner';

interface Props {
  isOpen: boolean;
  onClose: () => void;
  ticketId?: string;
  // The currently-selected project (App.tsx's currentProject.id), so the
  // launched terminal opens in *that* project's local path rather than
  // silently falling back to the server's own launch directory when neither
  // this nor ticketId is set (see resolveLaunchWorkDir's priority order).
  projectId?: string;
  defaultPrompt?: string;
}

export const ClaudeRunnerModal: React.FC<Props> = ({ isOpen, onClose, ticketId, projectId, defaultPrompt }) => {
  const { t } = useTranslation();
  const buildDefaultPrompt = () => defaultPrompt || (ticketId ? t('claudePrompts.executeIncompleteNodes', { ticketId }) : '');
  const [prompt, setPrompt] = useState(buildDefaultPrompt);
  const { isLaunching, lastMessage, launch, reset } = useClaudeLaunch();
  const titleId = useId();
  const promptId = useId();
  const hintId = useId();
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  // Must be called before the `if (!isOpen) return null` below.
  const dialogRef = useModalDialog({ isOpen, onEscape: onClose, initialFocusRef: textareaRef });

  // The modal component stays mounted (see App.tsx) even while hidden, so a
  // previous session's prompt text and status message must be wiped out each
  // time it's reopened -- otherwise they'd flash back on screen.
  useEffect(() => {
    if (isOpen) {
      setPrompt(buildDefaultPrompt());
      reset();
    }
    // buildDefaultPrompt is deliberately NOT a dependency: it's a plain
    // function re-created every render (it closes over defaultPrompt/
    // ticketId/t), and depending on it would make this effect run on every
    // render instead of only isOpen's transition -- overwriting whatever the
    // user has typed while the modal is still open. reset() is stable
    // (useClaudeLaunch wraps it in useCallback), so it's safe to depend on.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isOpen, reset]);

  if (!isOpen) return null;

  const handleLaunch = async () => {
    // The button's disabled state doesn't cover the Cmd/Ctrl+Enter path, so
    // guard here too (same as TicketItem's handleSendPrompt) -- otherwise a
    // whitespace/newline-only prompt could still be launched from the keyboard.
    if (isLaunching || !prompt.trim()) return;
    const succeeded = await launch(prompt, ticketId, projectId);
    // Only clear the input and close on success -- a failed send should
    // keep the modal open with the text intact so the user can retry
    // without retyping it.
    if (succeeded) {
      setPrompt(buildDefaultPrompt());
      onClose();
    }
  };

  // Overlay and panel follow ConfirmDialog (DFLT-00233 / DFLT-00254): the
  // overlay scrolls vertically and the panel is centred with `m-auto`
  // instead of `items-center` / `justify-center`, so at a 200% default font
  // on a short 320px screen the panel starts at the top of the scroll area
  // and the title, close button and launch button stay reachable. `min-w-0`
  // lets it shrink to the overlay's width; it stays the overlay's direct
  // child. The panel's `overflow-hidden` only clips the rounded corners: it
  // has no height cap, so nothing inside is cut off.
  return (
    <div className="fixed inset-0 z-50 bg-black/40 flex p-4 overflow-y-auto overscroll-contain">
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        tabIndex={-1}
        className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-xl w-full max-w-xl m-auto min-w-0 shadow-2xl overflow-hidden focus:outline-hidden"
      >
        {/* Header */}
        <div className="flex items-center justify-between gap-2 px-6 upto-15rem:px-3 py-4 border-b border-slate-200 dark:border-slate-800 bg-slate-50 dark:bg-slate-800">
          {/* The title text sits in its own span: as a bare text node it would
              be an anonymous flex item that cannot shrink below its longest
              word, so at a 200% font on a 320px screen it ran under the
              shrink-0 close button (DFLT-00254). `min-w-0` plus
              `overflow-wrap:anywhere` let it wrap inside the h2 instead. */}
          <h2 id={titleId} className="flex items-center gap-2 min-w-0 font-bold text-base text-slate-800 dark:text-slate-200">
            <Terminal className="w-5 h-5 shrink-0 upto-15rem:hidden text-indigo-600 dark:text-indigo-400" aria-hidden="true" />
            <span className="min-w-0 wrap-anywhere">{t('claudeRunnerModal.title')}</span>
          </h2>
          <button
            type="button"
            onClick={onClose}
            aria-label={t('common.closeDialog')}
            className="shrink-0 p-1 hover:bg-slate-200 dark:hover:bg-slate-700 rounded-lg text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200 transition"
          >
            <X className="w-5 h-5" aria-hidden="true" />
          </button>
        </div>

        <div className="p-6 upto-15rem:p-3 space-y-4">
          <p className="text-xs text-slate-500 dark:text-slate-400">
            {t('claudeRunnerModal.descriptionPrefix')} <span className="font-mono">claude</span> {t('claudeRunnerModal.descriptionSuffix')}
          </p>

          {/* Stacked rather than side-by-side: next to a 4-row textarea the
              launch button would stretch to the textarea's full height. */}
          <div className="flex flex-col gap-2">
            {/* Plain Enter is left to the textarea's native newline insertion;
                only Cmd/Ctrl+Enter launches (same as TicketItem's free-form
                input). The value is passed to launch() untouched so newlines
                survive. */}
            <label htmlFor={promptId} className="block text-xs font-semibold text-slate-700 dark:text-slate-300">
              {t('claudeRunnerModal.promptLabel')}
            </label>
            <textarea
              ref={textareaRef}
              id={promptId}
              aria-describedby={hintId}
              rows={4}
              className="w-full resize-y font-sans bg-slate-50 dark:bg-slate-800 border border-slate-300 dark:border-slate-700 rounded-lg px-4 py-2 text-sm text-slate-900 dark:text-slate-100 focus:outline-hidden focus:border-indigo-500 dark:focus:border-indigo-400 focus:bg-white dark:focus:bg-slate-800 transition"
              placeholder={t('claudeRunnerModal.placeholder')}
              value={prompt}
              onChange={e => setPrompt(e.target.value)}
              onKeyDown={e => {
                if (isSubmitShortcut(e)) {
                  e.preventDefault();
                  handleLaunch();
                }
              }}
              disabled={isLaunching}
            />
            <p id={hintId} className="text-xs text-slate-500 dark:text-slate-400">
              {t('claudeRunnerModal.shortcutHint')}
            </p>
            <button
              type="button"
              onClick={handleLaunch}
              disabled={isLaunching || !prompt.trim()}
              {...submittingProps(isLaunching)}
              className="self-end px-4 py-2 bg-indigo-600 hover:bg-indigo-700 disabled:opacity-50 text-white text-sm font-semibold rounded-lg flex items-center gap-2 shadow-xs transition max-w-full wrap-break-word"
            >
              {isLaunching ? <Spinner className="w-4 h-4 shrink-0" /> : <ExternalLink aria-hidden="true" className="w-4 h-4 shrink-0" />}
              {t('claudeRunnerModal.launch')}
              <SubmittingText busy={isLaunching} />
            </button>
          </div>

          {/* 起動の成否はフォーカス移動を伴わずにここへ現れるため、読み上げは
              常時マウントの live region が担当する（SC 4.1.3）。見た目側は
              aria-hidden にして二重読み上げを避ける。 */}
          <StatusLiveRegion message={lastMessage || ''} />
          {lastMessage && (
            <div
              aria-hidden="true"
              className="text-xs text-slate-600 dark:text-slate-300 bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded-lg px-3 py-2"
            >
              {lastMessage}
            </div>
          )}
        </div>
      </div>
    </div>
  );
};
