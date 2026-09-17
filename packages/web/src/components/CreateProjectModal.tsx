import React, { useId, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { Loader2 } from 'lucide-react';
import { useModalDialog } from '../hooks/useModalDialog';

interface Props {
  // The form state and submit handler stay in App.tsx (a controlled
  // component): App.tsx pre-fills workDir from the `?newProject=1&workDir=`
  // query, keeps the typed values across a cancel, and resets them and
  // switches to the new project after a successful create.
  name: string;
  prefix: string;
  workDir: string;
  onNameChange: (value: string) => void;
  onPrefixChange: (value: string) => void;
  onWorkDirChange: (value: string) => void;
  onSubmit: (e: React.FormEvent) => void;
  onClose: () => void;
  isSaving: boolean;
  error: string;
  // Where focus goes on close when the element that opened the modal no
  // longer exists: the project menu's "new project" item (the menu closes as
  // the modal opens), the empty state's button (gone once a project is
  // created), or nothing at all when opened from the URL query.
  returnFocusFallbackRef?: React.RefObject<HTMLElement>;
}

const INPUT_CLASS =
  'w-full bg-slate-50 dark:bg-slate-800 border border-slate-300 dark:border-slate-700 rounded-lg px-3 py-2 text-sm text-slate-900 dark:text-slate-100 focus:outline-none focus:border-blue-600 dark:focus:border-blue-400 disabled:opacity-60';
const LABEL_CLASS = 'block text-xs font-semibold text-slate-700 dark:text-slate-300 mb-1';

// The "Create Project" modal (moved out of App.tsx in DFLT-00074 so its
// dialog behaviour can be tested on its own). Mounted only while open.
export const CreateProjectModal: React.FC<Props> = ({
  name,
  prefix,
  workDir,
  onNameChange,
  onPrefixChange,
  onWorkDirChange,
  onSubmit,
  onClose,
  isSaving,
  error,
  returnFocusFallbackRef
}) => {
  const { t } = useTranslation();
  const idBase = useId();
  const titleId = `${idBase}-title`;
  const nameId = `${idBase}-name`;
  const prefixId = `${idBase}-prefix`;
  const workDirId = `${idBase}-workdir`;
  const nameInputRef = useRef<HTMLInputElement>(null);
  const dialogRef = useModalDialog({ onEscape: onClose, initialFocusRef: nameInputRef, returnFocusFallbackRef });

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
        <h2 id={titleId} className="text-lg font-bold text-slate-900 dark:text-slate-100 mb-4">{t('createProjectModal.title')}</h2>
        <p className="text-xs text-slate-500 dark:text-slate-400 mb-4">{t('createProjectModal.description')}</p>
        {/* Single-line inputs submit on plain Enter (native form behaviour);
            there is no Cmd/Ctrl+Enter shortcut here, so no shortcut hint. */}
        <form onSubmit={onSubmit} className="space-y-4">
          <div>
            <label htmlFor={nameId} className={LABEL_CLASS}>{t('createProjectModal.nameLabel')}</label>
            <input
              ref={nameInputRef}
              id={nameId}
              type="text"
              required
              disabled={isSaving}
              className={INPUT_CLASS}
              value={name}
              onChange={e => onNameChange(e.target.value)}
              placeholder={t('createProjectModal.namePlaceholder')}
            />
          </div>
          <div>
            <label htmlFor={prefixId} className={LABEL_CLASS}>{t('createProjectModal.prefixLabel')}</label>
            <input
              id={prefixId}
              type="text"
              maxLength={5}
              disabled={isSaving}
              className={`${INPUT_CLASS} font-mono uppercase`}
              value={prefix}
              onChange={e => onPrefixChange(e.target.value.toUpperCase())}
              placeholder={t('createProjectModal.prefixPlaceholder')}
            />
          </div>
          <div>
            <label htmlFor={workDirId} className={LABEL_CLASS}>{t('createProjectModal.workDirLabel')}</label>
            <input
              id={workDirId}
              type="text"
              required
              disabled={isSaving}
              className={`${INPUT_CLASS} font-mono`}
              value={workDir}
              onChange={e => onWorkDirChange(e.target.value)}
              placeholder={t('createProjectModal.workDirPlaceholder')}
            />
          </div>

          {error && (
            <div className="p-2.5 bg-red-50 dark:bg-red-950 text-red-700 dark:text-red-300 text-[11px] rounded-lg border border-red-200 dark:border-red-900">
              {error}
            </div>
          )}

          <div className="flex justify-end gap-2 pt-2">
            <button
              type="button"
              onClick={onClose}
              className="px-4 py-2 bg-slate-100 dark:bg-slate-800 hover:bg-slate-200 dark:hover:bg-slate-700 rounded-lg text-xs font-medium text-slate-700 dark:text-slate-300 transition"
            >
              {t('createModal.cancel')}
            </button>
            <button
              type="submit"
              disabled={isSaving || !name.trim() || !workDir.trim()}
              className="px-4 py-2 bg-blue-600 hover:bg-blue-700 disabled:opacity-50 rounded-lg text-xs font-semibold text-white shadow-xs transition flex items-center gap-1.5"
            >
              {isSaving && <Loader2 className="w-3.5 h-3.5 animate-spin" />}
              {t('createModal.submit')}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
};
