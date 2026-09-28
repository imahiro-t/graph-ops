import React, { useEffect, useRef, useState } from 'react';
import { Trans, useTranslation } from 'react-i18next';
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
    // DFLT-00292: the metadata bar's labels item no longer passes
    // wrap-anywhere down, so the button and the save error each carry their
    // own wrapping. flex-wrap sends the error to the next line when it does
    // not fit beside the button (rows are laid out from max-content widths),
    // so the button is never squeezed into mid-word breaks by the error;
    // without an error the button is the only child and nothing changes.
    // min-w-0 max-w-full keep the wrapper itself inside the card.
    <div
      className="relative inline-flex flex-wrap items-center gap-x-2 gap-y-1 min-w-0 max-w-full"
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
        // DFLT-00292: the name wraps between words only ("Edit / labels",
        // "ラベルを / 編集"). wrap-break-word breaks inside a word only when
        // that one word is wider than the button can be (160px / 200%);
        // unlike wrap-anywhere it leaves the min-content width alone, so the
        // flex row cannot squeeze the name into "Edit / labe / ls". The icons
        // are shrink-0 so they keep their size.
        className="px-2 py-0.5 rounded-full border border-slate-300 dark:border-slate-700 text-slate-600 dark:text-slate-300 hover:text-indigo-600 dark:hover:text-indigo-400 hover:border-indigo-300 dark:hover:border-indigo-700 text-[11px] font-semibold flex items-center gap-1 transition min-w-0 max-w-full wrap-break-word text-left"
      >
        {saving ? <Loader2 className="w-3 h-3 shrink-0 animate-spin" aria-hidden="true" /> : <Tag className="w-3 h-3 shrink-0" aria-hidden="true" />}
        {/* ticket.labels.editVisible is ticket.labels.edit with its break
            opportunities marked: "ラベルを<wbr/>編集" in Japanese, where
            break-keep (word-break: keep-all) otherwise allows no break
            between the characters, so it wraps at "ラベルを / 編集" only.
            It contains markup, so it is only ever rendered through Trans,
            never with t() (escapeValue is off). The aria-label above still
            comes from ticket.labels.edit. */}
        <span className="min-w-0 break-keep">
          <Trans i18nKey="ticket.labels.editVisible" components={{ wbr: <wbr /> }} />
        </span>
      </button>

      {/* DFLT-00292: the error is a value -- its message comes from the
          server and may hold one long word (a label or ticket id) -- so it
          breaks inside a word when it cannot fit, instead of running past the
          card. It used to inherit this from the metadata bar's labels item. */}
      {error && (
        <span role="alert" className="text-red-600 dark:text-red-400 font-medium text-[11px] min-w-0 max-w-full wrap-anywhere">
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
                    className="rounded-sm border-slate-300 dark:border-slate-600 text-blue-600 focus:ring-0 aria-disabled:opacity-50"
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
