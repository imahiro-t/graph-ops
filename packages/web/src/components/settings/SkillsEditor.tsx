// "スキル" tab: lets a scope (global/project) add or edit the supplementary
// instructions appended for one plugin skill (create-ticket / refine-ticket /
// process-ticket / onboarding), via GET/PUT /api/settings/skills(/{name}).
// See internal/config.ResolveSkillContext for the append-by-default merge
// semantics this editor exposes -- structurally a copy of NodeTypesEditor,
// minus the plugin-default layer skills don't have.
import React, { useCallback, useEffect, useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Loader2, Save, CheckCircle2 } from 'lucide-react';
import { SettingsSkillInfo } from '../../types';
import { fetchSettingsSkill, fetchSettingsSkills, saveSettingsSkill } from '../../lib/settingsApi';
import { errorMessage } from '../../lib/apiError';
import { useLatest } from '../../hooks/useLatest';
import { useSavedFlash } from '../../hooks/useSavedFlash';

interface Props {
  onDirtyChange: (dirty: boolean) => void;
}

// Maps a skill name to its i18n label key -- settings.skills.names.* --
// since skill names are kebab-case identifiers, not display text.
const skillNameKeys: Record<string, string> = {
  'create-ticket': 'settings.skills.names.createTicket',
  'refine-ticket': 'settings.skills.names.refineTicket',
  'process-ticket': 'settings.skills.names.processTicket',
  onboarding: 'settings.skills.names.onboarding'
};

export const SkillsEditor: React.FC<Props> = ({ onDirtyChange }) => {
  const { t } = useTranslation();
  // See src/hooks/useLatest.ts -- keeps loadSkills/loadSelected below
  // insensitive to language changes (F-1).
  const tRef = useLatest(t);
  const tierTextId = useId();
  const [skills, setSkills] = useState<SettingsSkillInfo[]>([]);
  const [selected, setSelected] = useState<string>('');
  const [tierText, setTierText] = useState('');
  const [savedTierText, setSavedTierText] = useState('');
  const [mergedText, setMergedText] = useState('');
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const { savedFlash, showSavedFlash } = useSavedFlash();

  const isDirty = tierText !== savedTierText;
  useEffect(() => onDirtyChange(isDirty), [isDirty, onDirtyChange]);

  const loadSkills = useCallback(async () => {
    try {
      const list = await fetchSettingsSkills(tRef.current);
      setSkills(list);
      // Functional updater (reads `selected` via `prev`) so this callback
      // doesn't need `selected` in its own dependency array -- otherwise
      // picking a different skill in the left-hand list would change
      // loadSkills's identity and re-fetch the whole list for no reason
      // (#7, mirrors NodeTypesEditor's #3).
      setSelected(prev => (!prev && list.length > 0 ? list[0].name : prev));
    } catch (e) {
      setError(errorMessage(e, tRef.current('errors.UNKNOWN')));
    }
  }, [tRef]);

  const loadSelected = useCallback(async (name: string) => {
    if (!name) return;
    setLoading(true);
    setError('');
    try {
      const res = await fetchSettingsSkill(tRef.current, name);
      setTierText(res.tier_text);
      setSavedTierText(res.tier_text);
      setMergedText(res.merged_text);
    } catch (e) {
      setError(errorMessage(e, tRef.current('errors.UNKNOWN')));
    } finally {
      setLoading(false);
    }
  }, [tRef]);

  useEffect(() => { loadSkills(); }, [loadSkills]);
  useEffect(() => { if (selected) loadSelected(selected); }, [selected, loadSelected]);

  const handleSave = async () => {
    setSaving(true);
    setError('');
    try {
      const res = await saveSettingsSkill(t, selected, tierText);
      setTierText(res.tier_text);
      setSavedTierText(res.tier_text);
      setMergedText(res.merged_text);
      showSavedFlash();
      await loadSkills();
    } catch (e) {
      setError(errorMessage(e, t('errors.UNKNOWN')));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="flex h-full min-h-0 gap-4">
      {/* Left: skill list */}
      <div className="w-56 shrink-0 border border-slate-200 dark:border-slate-800 rounded-lg overflow-y-auto bg-slate-50 dark:bg-slate-800">
        <div className="px-3 py-2 text-[11px] font-semibold text-slate-500 dark:text-slate-400 border-b border-slate-200 dark:border-slate-700 sticky top-0 bg-slate-50 dark:bg-slate-800">
          {t('settings.skills.listTitle')}
        </div>
        {skills.map(info => {
          // Read the two override flags by name rather than indexing info
          // with a scope-derived key string: the computed-key form needed an
          // `as any` to typecheck (DFLT-00023 M-1), and that cast disabled
          // the one check that would flag a renamed or removed flag on
          // SettingsSkillInfo -- the exact breakage it was silencing.
          const hasOverride = info.has_user_override;
          const labelKey = skillNameKeys[info.name];
          return (
            <button
              key={info.name}
              onClick={() => setSelected(info.name)}
              className={`w-full text-left px-3 py-2 text-xs flex items-center gap-2 border-b border-slate-100 dark:border-slate-700 hover:bg-white dark:hover:bg-slate-900 transition ${
                selected === info.name ? 'bg-white dark:bg-slate-900 font-semibold text-slate-900 dark:text-slate-100' : 'text-slate-600 dark:text-slate-400'
              }`}
            >
              <span className="truncate flex-1">{labelKey ? t(labelKey) : info.name}</span>
              {hasOverride && (
                <span className="shrink-0 w-1.5 h-1.5 rounded-full bg-blue-500" title={t('settings.skills.overrideBadge')} />
              )}
            </button>
          );
        })}
      </div>

      {/* Right: editor */}
      <div className="flex-1 min-w-0 flex flex-col gap-3">
        {error && <div className="p-2.5 bg-red-50 dark:bg-red-950 text-red-700 dark:text-red-300 text-[11px] rounded-lg border border-red-200 dark:border-red-900">{error}</div>}
        {loading ? (
          <div className="flex items-center gap-2 text-slate-400 dark:text-slate-500 text-xs py-8 justify-center">
            <Loader2 className="w-4 h-4 animate-spin" /> {t('settings.common.loading')}
          </div>
        ) : (
          <>
            <div>
              <label className="block text-xs font-semibold text-slate-700 dark:text-slate-300 mb-1">{t('settings.skills.mergedPreviewLabel')}</label>
              <pre className="whitespace-pre-wrap text-[11px] leading-relaxed bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded-lg p-3 max-h-40 overflow-y-auto text-slate-600 dark:text-slate-400 font-mono">
                {mergedText || t('settings.skills.emptyMergedHint')}
              </pre>
            </div>
            <div className="flex-1 min-h-0 flex flex-col">
              <label htmlFor={tierTextId} className="block text-xs font-semibold text-slate-700 dark:text-slate-300 mb-1">{t('settings.skills.tierTextLabel')}</label>
              <textarea
                id={tierTextId}
                value={tierText}
                onChange={e => setTierText(e.target.value)}
                placeholder={t('settings.skills.tierTextPlaceholder')}
                className="flex-1 min-h-[10rem] w-full bg-white dark:bg-slate-900 border border-slate-300 dark:border-slate-700 rounded-lg px-3 py-2 text-xs font-mono text-slate-900 dark:text-slate-100 focus:outline-none focus:border-blue-600 dark:focus:border-blue-400 disabled:opacity-60 disabled:bg-slate-50 dark:disabled:bg-slate-800"
              />
              <p className="text-[10px] text-slate-400 dark:text-slate-500 mt-1">{t('settings.skills.emptyOverrideHint')}</p>
            </div>
            <div className="flex justify-end items-center gap-2">
              {savedFlash && (
                <span className="text-emerald-600 text-xs flex items-center gap-1">
                  <CheckCircle2 className="w-3.5 h-3.5" /> {t('settings.common.saveSuccess')}
                </span>
              )}
              <button
                onClick={handleSave}
                disabled={saving || !isDirty}
                className="px-4 py-1.5 bg-blue-600 hover:bg-blue-700 disabled:opacity-50 rounded-lg text-xs font-semibold text-white flex items-center gap-1.5 transition"
              >
                {saving ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <Save className="w-3.5 h-3.5" />}
                {saving ? t('settings.common.saving') : t('settings.common.save')}
              </button>
            </div>
          </>
        )}
      </div>
    </div>
  );
};
