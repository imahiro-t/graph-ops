// "レポートテンプレート" tab: edits this scope's fixed report HTML template
// override (GET/PUT /api/settings/report-template). Unlike the
// node-type/skill tabs this is a single target (no left-hand list) and a
// full replace, not an append -- saving with content replaces the whole
// template; saving empty clears the override and falls back to the layer
// below (team -> user -> plugin default, see config.ResolveReportTemplate).
// The preview is a plain text/`<pre>` dump of the HTML source, not a
// rendered iframe (see the ticket's "スコープ外" note).
import React, { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Loader2, Save, CheckCircle2 } from 'lucide-react';
import { SettingsScope } from '../../types';
import { fetchSettingsReportTemplate, saveSettingsReportTemplate } from '../../lib/settingsApi';
import { errorMessage } from '../../lib/apiError';
import { useLatest } from '../../hooks/useLatest';

interface Props {
  scope: SettingsScope;
  projectId: string;
  canEdit: boolean;
  onDirtyChange: (dirty: boolean) => void;
}

export const ReportTemplateEditor: React.FC<Props> = ({ scope, projectId, canEdit, onDirtyChange }) => {
  const { t } = useTranslation();
  // See src/hooks/useLatest.ts -- keeps `load` below insensitive to
  // language changes (F-1).
  const tRef = useLatest(t);
  const [tierText, setTierText] = useState('');
  const [savedTierText, setSavedTierText] = useState('');
  const [mergedText, setMergedText] = useState('');
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [savedFlash, setSavedFlash] = useState(false);

  const isDirty = canEdit && tierText !== savedTierText;
  useEffect(() => onDirtyChange(isDirty), [isDirty, onDirtyChange]);

  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const res = await fetchSettingsReportTemplate(tRef.current, scope, projectId);
      setTierText(res.tier_text);
      setSavedTierText(res.tier_text);
      setMergedText(res.merged_text);
    } catch (e) {
      setError(errorMessage(e, tRef.current('errors.UNKNOWN')));
    } finally {
      setLoading(false);
    }
  }, [scope, projectId, tRef]);

  useEffect(() => { load(); }, [load]);

  const handleSave = async () => {
    setSaving(true);
    setError('');
    try {
      const res = await saveSettingsReportTemplate(t, scope, projectId, tierText);
      setTierText(res.tier_text);
      setSavedTierText(res.tier_text);
      setMergedText(res.merged_text);
      setSavedFlash(true);
      setTimeout(() => setSavedFlash(false), 2000);
    } catch (e) {
      setError(errorMessage(e, t('errors.UNKNOWN')));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="flex flex-col gap-3 h-full min-h-0">
      <p className="text-[11px] text-slate-500 dark:text-slate-400">{t('settings.reportTemplate.intro')}</p>
      {error && <div className="p-2.5 bg-red-50 dark:bg-red-950 text-red-700 dark:text-red-300 text-[11px] rounded-lg border border-red-200 dark:border-red-900 whitespace-pre-wrap">{error}</div>}
      {loading ? (
        <div className="flex items-center gap-2 text-slate-400 dark:text-slate-500 text-xs py-8 justify-center">
          <Loader2 className="w-4 h-4 animate-spin" /> {t('settings.common.loading')}
        </div>
      ) : (
        <>
          <div>
            <label className="block text-xs font-semibold text-slate-700 dark:text-slate-300 mb-1">{t('settings.reportTemplate.mergedPreviewLabel')}</label>
            <pre className="whitespace-pre-wrap text-[11px] leading-relaxed bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded-lg p-3 max-h-56 overflow-y-auto text-slate-600 dark:text-slate-400 font-mono">
              {mergedText}
            </pre>
          </div>
          <div className="flex-1 min-h-0 flex flex-col">
            <label className="block text-xs font-semibold text-slate-700 dark:text-slate-300 mb-1">{t('settings.reportTemplate.tierTextLabel')}</label>
            <textarea
              value={tierText}
              onChange={e => setTierText(e.target.value)}
              disabled={!canEdit}
              placeholder={t('settings.reportTemplate.tierTextPlaceholder')}
              className="flex-1 min-h-[12rem] w-full bg-white dark:bg-slate-900 border border-slate-300 dark:border-slate-700 rounded-lg px-3 py-2 text-xs font-mono text-slate-900 dark:text-slate-100 focus:outline-none focus:border-blue-500 disabled:opacity-60 disabled:bg-slate-50 dark:disabled:bg-slate-800"
            />
            <p className="text-[10px] text-slate-400 dark:text-slate-500 mt-1">{t('settings.reportTemplate.emptyOverrideHint')}</p>
          </div>
          <div className="flex justify-end items-center gap-2">
            {savedFlash && (
              <span className="text-emerald-600 text-xs flex items-center gap-1">
                <CheckCircle2 className="w-3.5 h-3.5" /> {t('settings.common.saveSuccess')}
              </span>
            )}
            <button
              onClick={handleSave}
              disabled={!canEdit || saving || !isDirty}
              className="px-4 py-1.5 bg-blue-600 hover:bg-blue-500 disabled:opacity-50 rounded-lg text-xs font-semibold text-white flex items-center gap-1.5 transition"
            >
              {saving ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <Save className="w-3.5 h-3.5" />}
              {saving ? t('settings.common.saving') : t('settings.common.save')}
            </button>
          </div>
        </>
      )}
    </div>
  );
};
