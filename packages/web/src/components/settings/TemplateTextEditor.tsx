// Editor pane for one fixed template in the settings UI's "テンプレート" tab
// (実行計画 / レビュー / レポート). Loads this scope's own override (tier_text)
// and the resolved template (merged_text), shows the resolved template as a
// read-only preview, and saves the override as a full replace, not an
// append. Saving empty clears the override so resolution falls back to the
// layer below (team -> user -> plugin default). Each template supplies its
// own fetch/save pair -- the report template's save sends `html` and is
// validated server-side, the plan/review saves send `text` unchecked -- plus
// the i18n key prefix for its labels. TemplatesEditor decides which one is
// shown.
import React, { useCallback, useEffect, useId, useRef, useState } from 'react';
import { TFunction } from 'i18next';
import { useTranslation } from 'react-i18next';
import { Loader2, Save, CheckCircle2 } from 'lucide-react';
import { SettingsTemplateTextResponse } from '../../types';
import { errorMessage } from '../../lib/apiError';
import { useLatest } from '../../hooks/useLatest';
import { useSavedFlash } from '../../hooks/useSavedFlash';
import { submittingProps } from '../Submitting';

export type TemplateFetcher = (
  t: TFunction
) => Promise<SettingsTemplateTextResponse>;

export type TemplateSaver = (
  t: TFunction,
  text: string
) => Promise<SettingsTemplateTextResponse>;

interface Props {
  onDirtyChange: (dirty: boolean) => void;
  fetchTemplate: TemplateFetcher;
  saveTemplate: TemplateSaver;
  // Prefix of this template's label keys (intro, mergedPreviewLabel,
  // tierTextLabel, tierTextPlaceholder, emptyOverrideHint), e.g.
  // 'settings.planTemplate'.
  i18nPrefix: string;
}

export const TemplateTextEditor: React.FC<Props> = ({
  onDirtyChange,
  fetchTemplate,
  saveTemplate,
  i18nPrefix
}) => {
  const { t } = useTranslation();
  // See src/hooks/useLatest.ts -- keeps `load` below insensitive to
  // language changes (F-1).
  const tRef = useLatest(t);
  const previewLabelId = useId();
  const textareaId = useId();
  const hintId = useId();
  const [tierText, setTierText] = useState('');
  const [savedTierText, setSavedTierText] = useState('');
  const [mergedText, setMergedText] = useState('');
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const { savedFlash, showSavedFlash } = useSavedFlash();
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const saveButtonRef = useRef<HTMLButtonElement>(null);

  const isDirty = tierText !== savedTierText;
  useEffect(() => onDirtyChange(isDirty), [isDirty, onDirtyChange]);

  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const res = await fetchTemplate(tRef.current);
      setTierText(res.tier_text);
      setSavedTierText(res.tier_text);
      setMergedText(res.merged_text);
    } catch (e) {
      setError(errorMessage(e, tRef.current('errors.UNKNOWN')));
    } finally {
      setLoading(false);
    }
  }, [tRef, fetchTemplate]);

  useEffect(() => { load(); }, [load]);

  const handleSave = async () => {
    // The save button is disabled while saving and stays disabled after a
    // successful save (nothing is dirty any more), and a disabled element
    // loses focus. So if the save was started from the button, move focus to
    // the textarea when the save settles, unless the user has already moved
    // it somewhere else in the meantime (a11y F-2).
    const startedFromButton = document.activeElement === saveButtonRef.current;
    setSaving(true);
    setError('');
    try {
      const res = await saveTemplate(t, tierText);
      setTierText(res.tier_text);
      setSavedTierText(res.tier_text);
      setMergedText(res.merged_text);
      showSavedFlash();
    } catch (e) {
      setError(errorMessage(e, t('errors.UNKNOWN')));
    } finally {
      setSaving(false);
      const active = document.activeElement;
      if (
        startedFromButton &&
        (active === null || active === document.body || active === saveButtonRef.current)
      ) {
        textareaRef.current?.focus();
      }
    }
  };

  return (
    <div className="flex flex-col gap-3 h-full min-h-0">
      <p className="text-[11px] text-slate-500 dark:text-slate-400">{t(`${i18nPrefix}.intro`)}</p>
      {error && (
        <div role="alert" className="p-2.5 bg-red-50 dark:bg-red-950 text-red-700 dark:text-red-300 text-[11px] rounded-lg border border-red-200 dark:border-red-900 whitespace-pre-wrap">
          {error}
        </div>
      )}
      {loading ? (
        <div className="flex items-center gap-2 text-slate-500 dark:text-slate-400 text-xs py-8 justify-center">
          <Loader2 aria-hidden="true" className="w-4 h-4 animate-spin" /> {t('settings.common.loading')}
        </div>
      ) : (
        <>
          <div>
            <p id={previewLabelId} className="block text-xs font-semibold text-slate-700 dark:text-slate-300 mb-1">
              {t(`${i18nPrefix}.mergedPreviewLabel`)}
            </p>
            {/* A named, focusable region so keyboard users can scroll the
                preview and screen readers announce what it contains. */}
            <pre
              role="region"
              aria-labelledby={previewLabelId}
              tabIndex={0}
              className="whitespace-pre-wrap text-[11px] leading-relaxed bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded-lg p-3 max-h-56 overflow-y-auto text-slate-600 dark:text-slate-400 font-mono focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500"
            >
              {mergedText}
            </pre>
          </div>
          <div className="flex-1 min-h-0 flex flex-col">
            <label htmlFor={textareaId} className="block text-xs font-semibold text-slate-700 dark:text-slate-300 mb-1">
              {t(`${i18nPrefix}.tierTextLabel`)}
            </label>
            <textarea
              ref={textareaRef}
              id={textareaId}
              aria-describedby={hintId}
              value={tierText}
              onChange={e => setTierText(e.target.value)}
              placeholder={t(`${i18nPrefix}.tierTextPlaceholder`)}
              className="flex-1 min-h-[12rem] w-full bg-white dark:bg-slate-900 border border-slate-300 dark:border-slate-700 rounded-lg px-3 py-2 text-xs font-mono text-slate-900 dark:text-slate-100 focus:outline-none focus:border-blue-600 dark:focus:border-blue-400 disabled:opacity-60 disabled:bg-slate-50 dark:disabled:bg-slate-800"
            />
            <p id={hintId} className="text-[10px] text-slate-500 dark:text-slate-400 mt-1">{t(`${i18nPrefix}.emptyOverrideHint`)}</p>
          </div>
          <div className="flex justify-end items-center gap-2">
            {savedFlash && (
              <span role="status" className="text-emerald-700 dark:text-emerald-400 text-xs flex items-center gap-1">
                <CheckCircle2 aria-hidden="true" className="w-3.5 h-3.5" /> {t('settings.common.saveSuccess')}
              </span>
            )}
            <button
              ref={saveButtonRef}
              type="button"
              onClick={handleSave}
              disabled={saving || !isDirty}
              {...submittingProps(saving)}
              className="px-4 py-1.5 bg-blue-600 hover:bg-blue-700 disabled:opacity-50 rounded-lg text-xs font-semibold text-white flex items-center gap-1.5 transition"
            >
              {saving ? <Loader2 aria-hidden="true" className="w-3.5 h-3.5 animate-spin" /> : <Save aria-hidden="true" className="w-3.5 h-3.5" />}
              {saving ? t('settings.common.saving') : t('settings.common.save')}
            </button>
          </div>
        </>
      )}
    </div>
  );
};
