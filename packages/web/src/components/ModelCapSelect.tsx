import React, { useId } from 'react';
import { useTranslation } from 'react-i18next';
import type { ModelCap } from '../types';

// Highest first, the order the list shows them in.
export const MODEL_CAPS: readonly Exclude<ModelCap, ''>[] = ['opus', 'sonnet', 'haiku'];

interface Props {
  value: ModelCap;
  onChange: (value: ModelCap) => void;
  disabled?: boolean;
  // The id of the text that explains the cap (rendered by the caller, where
  // its layout fits), announced as the select's description.
  describedBy?: string;
  testId?: string;
}

// A labelled native <select> for the model cap: "not specified (the
// launching model)", Opus, Sonnet, Haiku. Native, so the keyboard and screen
// readers work as with any select; the visible label is tied to it with
// htmlFor. The label and the select wrap onto separate lines when the row is
// too narrow (320px at 200% text), and the select never grows past its row.
export const ModelCapSelect: React.FC<Props> = ({ value, onChange, disabled, describedBy, testId }) => {
  const { t } = useTranslation();
  const id = useId();
  return (
    <span className="inline-flex flex-wrap items-center gap-1.5 min-w-0 max-w-full">
      <label htmlFor={id} className="text-xs font-semibold text-slate-700 dark:text-slate-300 wrap-anywhere">
        {t('modelCap.label')}
      </label>
      <select
        id={id}
        data-testid={testId}
        value={value}
        disabled={disabled}
        aria-describedby={describedBy}
        onChange={e => onChange(e.target.value as ModelCap)}
        className="max-w-full min-w-0 bg-white dark:bg-slate-800 border border-slate-300 dark:border-slate-600 rounded-lg px-2 py-1.5 text-xs text-slate-900 dark:text-slate-100 focus:outline-hidden focus-visible:ring-2 focus-visible:ring-blue-500 dark:focus-visible:ring-blue-400 disabled:opacity-50 disabled:cursor-not-allowed"
      >
        <option value="">{t('modelCap.options.inherit')}</option>
        {MODEL_CAPS.map(m => (
          <option key={m} value={m}>
            {t(`modelCap.options.${m}`)}
          </option>
        ))}
      </select>
    </span>
  );
};
