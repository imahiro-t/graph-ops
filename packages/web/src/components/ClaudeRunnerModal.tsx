import React, { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Terminal, X, ExternalLink, Loader2 } from 'lucide-react';
import { useClaudeLaunch } from '../hooks/useClaudeLaunch';
import { isSubmitShortcut } from '../lib/keyboardShortcuts';
import { StatusLiveRegion } from './StatusLiveRegion';

interface Props {
  isOpen: boolean;
  onClose: () => void;
  ticketId?: string;
  // The currently-selected project (App.tsx's currentProject.id), so the
  // launched terminal opens in *that* project's work_dir rather than
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
    const succeeded = await launch(prompt, ticketId, projectId);
    // Only clear the input and close on success -- a failed send should
    // keep the modal open with the text intact so the user can retry
    // without retyping it.
    if (succeeded) {
      setPrompt(buildDefaultPrompt());
      onClose();
    }
  };

  return (
    <div className="fixed inset-0 z-50 bg-black/40 backdrop-blur-xs flex items-center justify-center p-4">
      <div className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-xl w-full max-w-xl shadow-2xl overflow-hidden">
        {/* Header */}
        <div className="flex items-center justify-between px-6 py-4 border-b border-slate-200 dark:border-slate-800 bg-slate-50 dark:bg-slate-800">
          <div className="flex items-center gap-2 font-bold text-base text-slate-800 dark:text-slate-200">
            <Terminal className="w-5 h-5 text-indigo-600 dark:text-indigo-400" />
            {t('claudeRunnerModal.title')}
          </div>
          <button onClick={onClose} className="p-1 hover:bg-slate-200 dark:hover:bg-slate-700 rounded-lg text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200 transition">
            <X className="w-5 h-5" />
          </button>
        </div>

        <div className="p-6 space-y-4">
          <p className="text-xs text-slate-500 dark:text-slate-400">
            {t('claudeRunnerModal.descriptionPrefix')} <span className="font-mono">claude</span> {t('claudeRunnerModal.descriptionSuffix')}
          </p>

          <div className="flex gap-2">
            <input
              type="text"
              className="flex-1 bg-slate-50 dark:bg-slate-800 border border-slate-300 dark:border-slate-700 rounded-lg px-4 py-2 text-sm text-slate-900 dark:text-slate-100 focus:outline-none focus:border-indigo-500 focus:bg-white dark:focus:bg-slate-800 transition"
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
            <button
              onClick={handleLaunch}
              disabled={isLaunching || !prompt.trim()}
              className="px-4 py-2 bg-indigo-600 hover:bg-indigo-500 disabled:opacity-50 text-white text-sm font-semibold rounded-lg flex items-center gap-2 shadow-sm transition"
            >
              {isLaunching ? <Loader2 className="w-4 h-4 animate-spin" /> : <ExternalLink className="w-4 h-4" />}
              {t('claudeRunnerModal.launch')}
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
