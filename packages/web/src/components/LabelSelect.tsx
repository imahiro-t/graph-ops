import React, { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Loader2, Tag } from 'lucide-react';
import { Label } from '../types';
import { setTicketLabels } from '../lib/labelsApi';
import { errorMessage } from '../lib/apiError';
import { submittingProps, useSubmittingLabel } from './Submitting';

interface Props {
  ticketId: string;
  // The ticket's current labels.
  labels: Label[];
  // The project's registered labels (the choices).
  projectLabels: Label[];
  // Called after a successful save so the parent re-fetches the ticket.
  onSaved: () => void | Promise<void>;
}

// A ticket's label picker (DFLT-00084): an edit button opening a checkbox
// panel with the project's labels, structured like the toolbar filters
// (Escape closes it and returns focus to the button). Every check/uncheck
// immediately PATCHes the ticket's complete label set. While that request is
// in flight the checkboxes are only aria-disabled (and dimmed), and toggle
// ignores them -- never natively `disabled`, because disabling the focused
// checkbox drops keyboard focus to <body>: the next label could then only be
// reached by tabbing in from the top of the page, and Escape (handled on the
// container) would no longer close the panel. Clicks never reach the ticket
// row, so picking labels can't expand/collapse it.
export const LabelSelect: React.FC<Props> = ({ ticketId, labels, projectLabels, onSaved }) => {
  const { t } = useTranslation();
  // The trigger stays usable while labels save (it only toggles the panel),
  // but it shows the spinner, so it carries the submitting state (DFLT-00206).
  const submittingLabel = useSubmittingLabel();
  const [isOpen, setIsOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const buttonRef = useRef<HTMLButtonElement>(null);

  // Local copy of the selection so a check shows at once, before the parent's
  // re-fetch brings the new labels back in; re-synced whenever they change.
  const labelIdsKey = labels.map(l => l.id).join(',');
  const [selectedIds, setSelectedIds] = useState<string[]>(() => labels.map(l => l.id));
  useEffect(() => {
    setSelectedIds(labelIdsKey === '' ? [] : labelIdsKey.split(','));
  }, [labelIdsKey]);

  const toggle = async (id: string) => {
    if (saving) return;
    // Adding keeps every current id, including one not (yet) in a stale
    // projectLabels; the server decides the display order.
    const next = selectedIds.includes(id) ? selectedIds.filter(x => x !== id) : [...selectedIds, id];
    setSaving(true);
    setError('');
    try {
      await setTicketLabels(t, ticketId, next);
      setSelectedIds(next);
      await onSaved();
    } catch (err) {
      setError(t('ticket.labels.saveError', { message: errorMessage(err, t('errors.UNKNOWN')) }));
    } finally {
      setSaving(false);
    }
  };

  const panelId = `ticket-label-panel-${ticketId}`;

  return (
    <div
      className="relative inline-flex items-center gap-2"
      onClick={e => e.stopPropagation()}
      onKeyDown={e => {
        if (e.key === 'Escape' && isOpen) {
          e.stopPropagation();
          setIsOpen(false);
          buttonRef.current?.focus();
        }
      }}
    >
      <button
        ref={buttonRef}
        type="button"
        onClick={() => setIsOpen(v => !v)}
        aria-expanded={isOpen}
        aria-controls={panelId}
        aria-label={submittingLabel(`${t('ticket.labels.edit')}: ${ticketId}`, saving)}
        {...submittingProps(saving)}
        className="px-2 py-0.5 rounded-full border border-slate-300 dark:border-slate-700 text-slate-600 dark:text-slate-300 hover:text-indigo-600 dark:hover:text-indigo-400 hover:border-indigo-300 dark:hover:border-indigo-700 text-[11px] font-semibold flex items-center gap-1 transition"
      >
        {saving ? <Loader2 className="w-3 h-3 animate-spin" aria-hidden="true" /> : <Tag className="w-3 h-3" aria-hidden="true" />}
        {t('ticket.labels.edit')}
      </button>

      {error && (
        <span role="alert" className="text-red-600 dark:text-red-400 font-medium text-[11px]">
          {error}
        </span>
      )}

      {isOpen && (
        <>
          <div className="fixed inset-0 z-40" onClick={() => setIsOpen(false)} />
          <div
            id={panelId}
            role="group"
            aria-label={t('ticket.labels.groupLabel', { id: ticketId })}
            aria-busy={saving}
            className="absolute left-0 top-full mt-1.5 w-56 max-h-80 overflow-y-auto bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-lg shadow-lg z-50 py-1 text-xs"
          >
            {projectLabels.length === 0 ? (
              <div className="px-3 py-1.5 text-slate-500 dark:text-slate-400">{t('ticket.labels.noRegistered')}</div>
            ) : (
              projectLabels.map(l => (
                <label
                  key={l.id}
                  className="flex items-center gap-2 px-3 py-1.5 hover:bg-slate-50 dark:hover:bg-slate-800 cursor-pointer text-slate-700 dark:text-slate-300 font-medium whitespace-nowrap"
                >
                  <input
                    type="checkbox"
                    checked={selectedIds.includes(l.id)}
                    aria-disabled={saving}
                    onChange={() => toggle(l.id)}
                    className="rounded border-slate-300 dark:border-slate-600 text-blue-600 focus:ring-0 aria-disabled:opacity-50"
                  />
                  <span className="truncate">{l.name}</span>
                </label>
              ))
            )}
          </div>
        </>
      )}
    </div>
  );
};
