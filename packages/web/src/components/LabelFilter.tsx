import React, { useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ChevronDown } from 'lucide-react';
import { Label } from '../types';
import { LabelChip } from './LabelChip';

interface Props {
  // The current project's labels (the filter's options).
  labels: Label[];
  // Selected label ids; [] means "don't filter by label".
  selectedIds: string[];
  onChange: (next: string[]) => void;
}

// Toolbar label filter (DFLT-00084). Same trigger button + checkbox panel as
// App.tsx's status/priority filters (outside click and Escape close it,
// Escape returning focus to the trigger), but it starts with nothing
// selected: an empty selection means no label filtering, so unlabeled
// tickets stay visible, and a selection matches tickets carrying ANY of the
// selected labels (OR, see labelMeta.ts's matchesLabelFilter).
export const LabelFilter: React.FC<Props> = ({ labels, selectedIds, onChange }) => {
  const { t } = useTranslation();
  const [isOpen, setIsOpen] = useState(false);
  const buttonRef = useRef<HTMLButtonElement>(null);

  const toggle = (id: string) => {
    // Re-derive from `labels` when adding so the selection stays in list order.
    onChange(
      selectedIds.includes(id) ? selectedIds.filter(x => x !== id) : labels.map(l => l.id).filter(x => x === id || selectedIds.includes(x))
    );
  };

  return (
    <div
      className="relative"
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
        aria-controls="toolbar-label-filter-panel"
        className="px-3 py-1.5 rounded-lg bg-slate-50 dark:bg-slate-800 border border-slate-300 dark:border-slate-700 text-xs font-medium text-slate-700 dark:text-slate-300 flex items-center gap-1.5 whitespace-nowrap"
      >
        {selectedIds.length === 0 ? t('toolbar.labelAll') : t('toolbar.labelSelected', { count: selectedIds.length })}
        <ChevronDown className="w-3.5 h-3.5 text-slate-400 shrink-0" aria-hidden="true" />
      </button>

      {isOpen && (
        <>
          <div className="fixed inset-0 z-40" data-testid="label-filter-overlay" onClick={() => setIsOpen(false)} />
          <div
            id="toolbar-label-filter-panel"
            role="group"
            aria-label={t('toolbar.labelGroupLabel')}
            className="absolute left-0 mt-1.5 w-56 max-h-80 overflow-y-auto bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-lg shadow-lg z-50 py-1 text-xs"
          >
            {labels.length === 0 && (
              <div className="px-3 py-1.5 text-slate-400 dark:text-slate-500">{t('toolbar.labelEmpty')}</div>
            )}
            {labels.map(l => (
              <label
                key={l.id}
                className="flex items-center gap-2 px-3 py-1.5 hover:bg-slate-50 dark:hover:bg-slate-800 cursor-pointer text-slate-700 dark:text-slate-300 font-medium whitespace-nowrap"
              >
                <input
                  type="checkbox"
                  checked={selectedIds.includes(l.id)}
                  onChange={() => toggle(l.id)}
                  aria-label={l.name}
                  className="rounded border-slate-300 dark:border-slate-600 text-blue-600 focus:ring-0"
                />
                <LabelChip name={l.name} color={l.color} />
              </label>
            ))}
            {labels.length > 0 && (
              <div className="border-t border-slate-100 dark:border-slate-800 mt-1 pt-1 px-1">
                <button
                  type="button"
                  onClick={() => onChange([])}
                  disabled={selectedIds.length === 0}
                  className="w-full text-left px-2 py-1.5 rounded hover:bg-slate-50 dark:hover:bg-slate-800 text-blue-700 dark:text-blue-400 font-medium disabled:opacity-50 disabled:hover:bg-transparent"
                >
                  {t('toolbar.labelClear')}
                </button>
              </div>
            )}
          </div>
        </>
      )}
    </div>
  );
};
