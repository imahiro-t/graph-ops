import React, { useId, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Loader2 } from 'lucide-react';
import { isSubmitShortcut } from '../lib/keyboardShortcuts';
import { useModalDialog } from '../hooks/useModalDialog';
import { StatusLiveRegion } from './StatusLiveRegion';

interface Props {
  // Called with the free-form request text once the user submits. The
  // parent (App.tsx) turns it into the claudePrompts.createTicket prompt,
  // launches the terminal, and closes this modal itself on success -- that's
  // why the launch state (isCreating/status) is passed in rather than owned
  // here: App.tsx also resets it right before reopening the modal.
  // Resolves to whether the launch succeeded, so a failed launch can keep
  // the request text for a retry.
  onSubmit: (request: string) => Promise<boolean>;
  onClose: () => void;
  isCreating: boolean;
  status: string;
}

const REQUEST_INPUT_ID = 'create-ticket-request';
const SHORTCUT_HINT_ID = 'create-ticket-shortcut-hint';

// The "New Ticket" modal. It deliberately asks for a single free-form
// request instead of separate title/description fields: working out a title
// and description from that request is the create-ticket skill's job,
// done together with the user in the launched terminal.
export const CreateTicketModal: React.FC<Props> = ({ onSubmit, onClose, isCreating, status }) => {
  const { t } = useTranslation();
  const [request, setRequest] = useState('');
  const titleId = useId();
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  // Mounted only while open, so isOpen stays at its default and the hook's
  // unmount cleanup returns focus to the "New Ticket" button.
  const dialogRef = useModalDialog({ onEscape: onClose, initialFocusRef: textareaRef });

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    // The button's disabled state doesn't cover the Cmd/Ctrl+Enter path, so
    // guard here too -- a whitespace/newline-only request is never sent.
    if (isCreating || !request.trim()) return;
    const succeeded = await onSubmit(request);
    // Only clear the input on success (same as ClaudeRunnerModal) -- a
    // failed launch keeps the modal open with the request intact so the user
    // can retry without retyping a possibly long, multi-line request.
    if (succeeded) setRequest('');
  };

  return (
    <div className="fixed inset-0 z-50 bg-black/40 backdrop-blur-xs flex items-center justify-center p-4">
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        tabIndex={-1}
        className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-xl w-full max-w-md p-6 shadow-2xl focus:outline-none"
      >
        <h2 id={titleId} className="text-lg font-bold text-slate-900 dark:text-slate-100 mb-4">{t('createModal.title')}</h2>
        <p className="text-xs text-slate-500 dark:text-slate-400 mb-4">
          {t('createModal.descriptionPrefix')} <span className="font-mono">/create-ticket</span> {t('createModal.descriptionSuffix')}
        </p>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label
              htmlFor={REQUEST_INPUT_ID}
              className="block text-xs font-semibold text-slate-700 dark:text-slate-300 mb-1"
            >
              {t('createModal.requestLabel')}
            </label>
            {/* Plain Enter inserts a newline (a request can span several
                lines); Cmd/Ctrl+Enter submits, same as the other prompt
                inputs. The value is passed on untouched so newlines survive. */}
            <textarea
              ref={textareaRef}
              id={REQUEST_INPUT_ID}
              aria-describedby={SHORTCUT_HINT_ID}
              rows={5}
              required
              disabled={isCreating}
              className="w-full bg-slate-50 dark:bg-slate-800 border border-slate-300 dark:border-slate-700 rounded-lg px-3 py-2 text-sm text-slate-900 dark:text-slate-100 focus:outline-none focus:border-blue-600 dark:focus:border-blue-400 disabled:opacity-60"
              value={request}
              onChange={e => setRequest(e.target.value)}
              onKeyDown={e => {
                if (isSubmitShortcut(e)) {
                  e.preventDefault();
                  e.currentTarget.form?.requestSubmit();
                }
              }}
              placeholder={t('createModal.requestPlaceholder')}
            />
            <p id={SHORTCUT_HINT_ID} className="mt-1 text-xs text-slate-500 dark:text-slate-400">
              {t('createModal.shortcutHint')}
            </p>
          </div>
          {/* 作成／起動の結果はフォーカス移動を伴わずに現れるため、読み上げは
              常時マウントの live region が担当する（SC 4.1.3）。 */}
          <StatusLiveRegion message={status || ''} />
          {status && (
            <div
              aria-hidden="true"
              className="p-2.5 bg-slate-50 dark:bg-slate-800 text-slate-600 dark:text-slate-300 text-[11px] rounded-lg border border-slate-200 dark:border-slate-700"
            >
              {status}
            </div>
          )}

          <div className="flex justify-end gap-2 pt-2">
            <button
              type="button"
              onClick={onClose}
              className="px-4 py-2 bg-slate-100 dark:bg-slate-800 hover:bg-slate-200 dark:hover:bg-slate-700 rounded-lg text-xs font-medium text-slate-700 dark:text-slate-300 transition"
            >
              {isCreating || status ? t('createModal.close') : t('createModal.cancel')}
            </button>
            <button
              type="submit"
              disabled={isCreating || !request.trim()}
              className="px-4 py-2 bg-blue-600 hover:bg-blue-700 disabled:opacity-50 rounded-lg text-xs font-semibold text-white shadow-xs transition flex items-center gap-1.5"
            >
              {isCreating && <Loader2 className="w-3.5 h-3.5 animate-spin" />}
              {t('createModal.submit')}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
};
