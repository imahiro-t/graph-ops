// "スキル" tab: lets a scope (global/project) add or edit the supplementary
// instructions appended for one plugin skill (create-ticket / refine-ticket /
// process-ticket / onboarding), via GET/PUT /api/settings/skills(/{name}).
// See internal/config.ResolveSkillContext for the append-by-default merge
// semantics this editor exposes -- structurally a copy of NodeTypesEditor,
// minus the plugin-default layer skills don't have.
import React, { useEffect, useId, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Save, CheckCircle2 } from 'lucide-react';
import { SettingsSkillInfo } from '../../types';
import { fetchSettingsSkill, fetchSettingsSkills, saveSettingsSkill } from '../../lib/settingsApi';
import { errorMessage } from '../../lib/apiError';
import { useSavedFlash } from '../../hooks/useSavedFlash';
import { useConfirmDialog } from '../../hooks/useConfirmDialog';
import { unsavedChangesConfirmOptions } from './unsavedChangesConfirm';
import { submittingProps } from '../Submitting';
import { ErrorBox } from './ErrorBox';
import { LoadFailure } from './LoadFailure';
import { LoadingLine } from './LoadingLine';
import { useSelectedTextLoader } from './useSelectedTextLoader';
import { useListLoader } from './useListLoader';
import { LIST_HEADING_CLASS, LIST_ITEM_FOCUS_CLASS, LIST_LAYOUT_CLASS, LIST_PANE_CLASS } from './listPane';
import { StatusLiveRegion } from '../StatusLiveRegion';
import { Spinner } from '../Spinner';

interface Props {
  onDirtyChange: (dirty: boolean) => void;
}

// Maps a skill name to its i18n label key -- settings.skills.names.* --
// since skill names are kebab-case identifiers, not display text.
const skillNameKeys: Record<string, string> = {
  'create-ticket': 'settings.skills.names.createTicket',
  'refine-ticket': 'settings.skills.names.refineTicket',
  'process-ticket': 'settings.skills.names.processTicket',
  onboarding: 'settings.skills.names.onboarding',
  'autopilot-ticket': 'settings.skills.names.autopilotTicket',
  'autopilot-tree': 'settings.skills.names.autopilotTree',
  'autopilot-worker': 'settings.skills.names.autopilotWorker'
};

export const SkillsEditor: React.FC<Props> = ({ onDirtyChange }) => {
  const { t } = useTranslation();
  // In-app confirmation (DFLT-00148), on top of the settings modal.
  const { confirm, confirmDialog } = useConfirmDialog();
  const tierTextId = useId();
  const mergedPreviewLabelId = useId();
  const [skills, setSkills] = useState<SettingsSkillInfo[]>([]);
  const [selected, setSelected] = useState<string>('');
  const [tierText, setTierText] = useState('');
  const [savedTierText, setSavedTierText] = useState('');
  const [mergedText, setMergedText] = useState('');
  // What the right-hand pane shows is derived at render time from the state
  // below, useListLoader's and useSelectedTextLoader's, the same way as
  // NodeTypesEditor's (DFLT-00343, DFLT-00350, DFLT-00356): no frame shows
  // an empty editor, or the previous skill's text or error.
  const [saving, setSaving] = useState(false);
  // A failed save, and a failed list re-fetch once the list has been
  // loaded: shown above the editor without hiding it.
  const [error, setError] = useState('');
  const { savedFlash, savedAnnounced, showSavedFlash } = useSavedFlash();

  const editorPaneRef = useRef<HTMLDivElement>(null);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  // The skill list, loaded the same way as NodeTypesEditor's (see
  // useListLoader): a failed re-fetch after a save goes to the non-blocking
  // `error`, and a successful retry moves focus to the right-hand pane.
  const skillList = useListLoader({
    fetchList: fetchSettingsSkills,
    onLoaded: list => {
      setSkills(list);
      // Functional updater (reads `selected` via `prev`) so a stale
      // `selected` from an earlier render is never used (#7, mirrors
      // NodeTypesEditor's #3). An empty list resets it to '' (DFLT-00357),
      // the same as NodeTypesEditor's, so the pane shows the empty-list note
      // rather than a skill that is no longer listed.
      setSelected(prev => (list.length === 0 ? '' : !prev ? list[0].name : prev));
    },
    onReloadError: setError,
    getFocusTarget: () => editorPaneRef.current
  });
  // The selected skill's text, loaded the same way as NodeTypesEditor's
  // (see useSelectedTextLoader).
  const selectedText = useSelectedTextLoader({
    selected,
    fetchText: fetchSettingsSkill,
    onLoaded: res => {
      setTierText(res.tier_text);
      setSavedTierText(res.tier_text);
      setMergedText(res.merged_text);
    },
    onLoadStart: () => setError(''),
    getFocusTarget: () => textareaRef.current
  });

  const isDirty = tierText !== savedTierText;
  useEffect(() => onDirtyChange(isDirty), [isDirty, onDirtyChange]);

  // Switches the selected skill, asking first when the current one has
  // unsaved edits -- mirrors NodeTypesEditor's / TemplatesEditor's select.
  // Returns whether the switch happened, like NodeTypesEditor's, and is
  // asynchronous like it too (the question is the in-app ConfirmDialog).
  const select = async (next: string): Promise<boolean> => {
    if (next === selected) return true;
    if (isDirty) {
      const discard = await confirm(unsavedChangesConfirmOptions(t, 'skill-discard-confirm'));
      if (!discard) return false;
    }
    // Discarding: reset the text first so isDirty is already false while the
    // next skill loads (see NodeTypesEditor's select for why).
    setTierText(savedTierText);
    onDirtyChange(false);
    selectedText.clearFailure();
    setSelected(next);
    return true;
  };

  const handleSave = async () => {
    setSaving(true);
    setError('');
    try {
      const res = await saveSettingsSkill(t, selected, tierText);
      setTierText(res.tier_text);
      setSavedTierText(res.tier_text);
      setMergedText(res.merged_text);
      showSavedFlash();
      await skillList.reload();
    } catch (e) {
      setError(errorMessage(e, t('errors.UNKNOWN')));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className={LIST_LAYOUT_CLASS}>
      {confirmDialog}
      {/* Announces the save flash (SC 4.1.3). It sits here rather than in the
          button row because that row is only rendered while a skill is
          selected; the visible flash there is aria-hidden so it is not read
          twice, like AutopilotSettingsEditor's. */}
      <StatusLiveRegion message={savedAnnounced ? t('settings.common.saveSuccess') : ''} />
      {/* Left: skill list. See listPane.ts for why the heading is sticky
          and the list has scroll padding. */}
      <div className={LIST_PANE_CLASS}>
        <div className={LIST_HEADING_CLASS}>
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
              onClick={() => void select(info.name)}
              // DFLT-00321: aria-current marks the one skill whose editor is
              // shown on the right, with the same value TemplatesEditor uses.
              // undefined (not false) keeps the attribute off the other
              // items -- React would render false as "false".
              aria-current={selected === info.name ? 'true' : undefined}
              className={`w-full text-left px-3 py-2 text-xs flex items-center gap-2 border-b border-slate-100 dark:border-slate-700 hover:bg-white dark:hover:bg-slate-900 transition ${LIST_ITEM_FOCUS_CLASS} ${
                selected === info.name ? 'bg-white dark:bg-slate-900 font-semibold text-slate-900 dark:text-slate-100' : 'text-slate-600 dark:text-slate-400'
              }`}
            >
              <span className="truncate flex-1 narrow:whitespace-normal narrow:wrap-anywhere">{labelKey ? t(labelKey) : info.name}</span>
              {hasOverride && (
                <span className="shrink-0 w-1.5 h-1.5 rounded-full bg-blue-500" title={t('settings.skills.overrideBadge')} />
              )}
            </button>
          );
        })}
      </div>

      {/* Right: editor. tabIndex -1 and the ref: where focus goes after a
          successful list retry. No outline: it is not a control. */}
      <div ref={editorPaneRef} tabIndex={-1} className="flex-1 min-w-0 flex flex-col gap-3 focus:outline-hidden">
        {error && <ErrorBox className="p-2.5 text-[0.6875rem]">{error}</ErrorBox>}
        {/* The same order as NodeTypesEditor's (see LoadFailure): a failure
            before loading, then -- once the list is loaded and empty -- a
            note in place of the editor (DFLT-00357). The check is the list's
            length, not selected === '', and comes before the selected
            skill's failure / loading lines, which never show for ''. */}
        {skillList.failure ? (
          <LoadFailure
            message={t('settings.common.loadFailed', { message: skillList.failure.message })}
            retrying={skillList.failure.retrying}
            onRetry={skillList.failure.onRetry}
            failureKey={skillList.failure.failureKey}
          />
        ) : !skillList.loaded ? (
          <LoadingLine />
        ) : skills.length === 0 ? (
          <p className="text-xs text-slate-500 dark:text-slate-400">{t('settings.skills.emptyList')}</p>
        ) : selectedText.failure ? (
          <LoadFailure
            message={t('settings.common.loadFailed', { message: selectedText.failure.message })}
            retrying={selectedText.failure.retrying}
            onRetry={selectedText.failure.onRetry}
            failureKey={selectedText.failure.failureKey}
          />
        ) : selectedText.pane === 'loading' ? (
          <LoadingLine />
        ) : (
          <>
            <div>
              {/* DFLT-00212: a paragraph, not a <label> -- there is no form control
                  to label. It names the preview region below instead. */}
              <p id={mergedPreviewLabelId} className="block text-xs font-semibold text-slate-700 dark:text-slate-300 mb-1">{t('settings.skills.mergedPreviewLabel')}</p>
              {/* A named, focusable region so keyboard users can scroll the
                  preview and screen readers announce what it contains. */}
              <pre
                role="region"
                aria-labelledby={mergedPreviewLabelId}
                tabIndex={0}
                className="whitespace-pre-wrap text-[0.6875rem] leading-relaxed bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded-lg p-3 max-h-40 overflow-y-auto text-slate-600 dark:text-slate-400 font-mono focus:outline-hidden focus-visible:ring-2 focus-visible:ring-blue-500"
              >
                {mergedText || t('settings.skills.emptyMergedHint')}
              </pre>
            </div>
            <div className="flex-1 min-h-0 flex flex-col">
              <label htmlFor={tierTextId} className="block text-xs font-semibold text-slate-700 dark:text-slate-300 mb-1">{t('settings.skills.tierTextLabel')}</label>
              <textarea
                ref={textareaRef}
                id={tierTextId}
                value={tierText}
                onChange={e => setTierText(e.target.value)}
                placeholder={t('settings.skills.tierTextPlaceholder')}
                className="flex-1 min-h-40 w-full bg-white dark:bg-slate-900 border border-slate-300 dark:border-slate-700 rounded-lg px-3 py-2 text-xs font-mono text-slate-900 dark:text-slate-100 focus:outline-hidden focus:border-blue-600 dark:focus:border-blue-400 disabled:opacity-60 disabled:bg-slate-50 dark:disabled:bg-slate-800"
              />
              <p className="text-[0.625rem] text-slate-500 dark:text-slate-400 mt-1">{t('settings.skills.emptyOverrideHint')}</p>
            </div>
            <div className="flex justify-end items-center gap-2 narrow:flex-wrap">
              {savedFlash && (
                <span aria-hidden="true" className="text-emerald-700 dark:text-emerald-400 text-xs flex items-center gap-1">
                  <CheckCircle2 aria-hidden="true" className="w-3.5 h-3.5" /> {t('settings.common.saveSuccess')}
                </span>
              )}
              <button
                onClick={handleSave}
                disabled={saving || !isDirty}
                {...submittingProps(saving)}
                className="px-4 py-1.5 bg-blue-600 hover:bg-blue-700 disabled:opacity-50 rounded-lg text-xs font-semibold text-white flex items-center gap-1.5 transition"
              >
                {saving ? <Spinner className="w-3.5 h-3.5" /> : <Save aria-hidden="true" className="w-3.5 h-3.5" />}
                {saving ? t('settings.common.saving') : t('settings.common.save')}
              </button>
            </div>
          </>
        )}
      </div>
    </div>
  );
};
