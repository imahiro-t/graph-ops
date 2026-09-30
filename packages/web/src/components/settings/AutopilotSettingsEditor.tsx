// "オートパイロット" tab (DFLT-00142): the current project's autopilot
// settings, via GET/PUT /api/projects/{id}/autopilot-settings. See
// packages/core-go/internal/autopilot/settings.go for how a value is
// resolved -- per key, team projects.<id> > team defaults > local > built-in
// default.
//
// What this form edits is the LOCAL value only. A key the team settings fix
// (`locked`) is shown read-only with where it comes from, and is never put in
// the PUT body: the server refuses a body naming a locked key as a whole
// (AUTOPILOT_SETTING_LOCKED), so sending it would fail every save. Values are
// not validated here -- the server's single validation is the one that
// counts, and its 400 is shown translated.
import React, { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { AlertTriangle, CheckCircle2, Lock, RotateCcw, Save } from 'lucide-react';
import { StatusLiveRegion } from '../StatusLiveRegion';
import { IconButton } from '../IconButton';
import {
  AUTOPILOT_SETTING_KEYS,
  AutopilotSettingItem,
  AutopilotSettingKey,
  AutopilotSettingsPatch,
  AutopilotSettingsResponse,
  AutopilotSettingValue
} from '../../types';
import { fetchAutopilotSettings, saveAutopilotSettings } from '../../lib/settingsApi';
import { errorMessage } from '../../lib/apiError';
import { useLatest } from '../../hooks/useLatest';
import { useSavedFlash } from '../../hooks/useSavedFlash';
import { submittingProps } from '../Submitting';
import { ErrorBox } from './ErrorBox';
import { LoadFailure, useFocusAfterRetry } from './LoadFailure';
import { LoadingLine } from './LoadingLine';
import { Spinner } from '../Spinner';

interface Props {
  // The project whose settings are edited ('' when none is selected).
  projectId: string;
  projectName?: string;
  onDirtyChange: (dirty: boolean) => void;
}

type Control =
  | { kind: 'select'; options: string[] }
  | { kind: 'boolean' }
  | { kind: 'number'; min: number; max: number };

const CONTROLS: Record<AutopilotSettingKey, Control> = {
  mainReflection: { kind: 'select', options: ['branch', 'pull_request', 'merge'] },
  permissionMode: { kind: 'select', options: ['acceptEdits', 'auto', 'dontAsk', 'bypassPermissions'] },
  autoApproveGates: { kind: 'boolean' },
  autoCreateTickets: { kind: 'boolean' },
  maxTickets: { kind: 'number', min: 1, max: 100 },
  maxDepth: { kind: 'number', min: 0, max: 10 },
  onFailure: { kind: 'select', options: ['stop', 'continue'] },
  stallTimeoutMinutes: { kind: 'number', min: 15, max: 1440 }
};

// A row's controls. Select rows add upto-15rem:flex-wrap and
// upto-15rem:justify-end so the reset button drops under the select. Kept a
// complete string literal (and never glued to a `${`) so Tailwind finds it.
const CONTROLS_CLASS = 'flex items-center gap-2 shrink-0 narrow:shrink narrow:min-w-0 narrow:w-full';

// A draft entry is the local value being edited: a string for select and
// number fields (a number field keeps what was typed, so an invalid entry
// reaches the server's validation instead of being silently dropped), a
// boolean for checkboxes, or null for "no local value -- inherit".
type Draft = Partial<Record<AutopilotSettingKey, string | boolean | null>>;

function toDraftValue(v: AutopilotSettingValue | null): string | boolean | null {
  if (v === null) return null;
  return typeof v === 'boolean' ? v : String(v);
}

function draftFrom(items: AutopilotSettingItem[]): Draft {
  const d: Draft = {};
  for (const it of items) d[it.key] = toDraftValue(it.local);
  return d;
}

// Converts a draft value to what the PUT body carries. An integer-looking
// number field becomes a number; anything else typed there is sent as-is so
// the server rejects it with a translated VALIDATION_ERROR.
function toPatchValue(key: AutopilotSettingKey, v: string | boolean | null): AutopilotSettingValue | null {
  if (v === null || typeof v === 'boolean') return v;
  if (CONTROLS[key].kind === 'number' && /^-?\d+$/.test(v.trim())) return Number(v.trim());
  return v;
}

export const AutopilotSettingsEditor: React.FC<Props> = ({ projectId, projectName, onDirtyChange }) => {
  const { t } = useTranslation();
  const tRef = useLatest(t);
  const idPrefix = useId();
  const [data, setData] = useState<AutopilotSettingsResponse | null>(null);
  const [draft, setDraft] = useState<Draft>({});
  // The project whose settings are in `data`, '' while none is. Set only by
  // a successful load for the latest request, and cleared when a load
  // starts, so `loadedProjectId !== projectId` means "this project's
  // settings are not in yet": the first render, and the one right after
  // projectId changes (before the effect has started the next load), show
  // the loading line rather than the defaults or the previous project's
  // rows, warnings and team file (DFLT-00323, DFLT-00343, DFLT-00350).
  const [loadedProjectId, setLoadedProjectId] = useState('');
  // The project the latest load was started for: an answer (success or
  // failure) for any other project is dropped, and so is a save's answer
  // once the project has changed under it.
  const requestedProjectIdRef = useRef('');
  // Why the settings could not be loaded, with the project it belongs to:
  // shown (in place of the form, with a retry button) only while that
  // project is the current one. A retry leaves it until its result is in.
  const [loadError, setLoadError] = useState<{ projectId: string; message: string } | null>(null);
  const [loadFailures, setLoadFailures] = useState(0);
  // The project a retry is running for (see loadError).
  const [retryingFor, setRetryingFor] = useState<string | null>(null);
  // The project a save is running for, or null. Like retryingFor, a save
  // for another project leaves the current one's form and buttons alone.
  const [savingFor, setSavingFor] = useState<string | null>(null);
  const saving = savingFor !== null && savingFor === projectId;
  // A failed save, shown above the form.
  const [error, setError] = useState('');
  const { savedFlash, showSavedFlash } = useSavedFlash();

  const items = useMemo(() => {
    const byKey = new Map((data?.items ?? []).map(it => [it.key, it]));
    return AUTOPILOT_SETTING_KEYS.map(k => byKey.get(k)).filter((it): it is AutopilotSettingItem => !!it);
  }, [data]);
  const saved = useMemo(() => draftFrom(items), [items]);

  // The keys whose local value would change -- never a locked one.
  const patch = useMemo(() => {
    const p: AutopilotSettingsPatch = {};
    for (const it of items) {
      if (it.locked) continue;
      const cur = draft[it.key] ?? null;
      if (cur !== (saved[it.key] ?? null)) p[it.key] = toPatchValue(it.key, cur);
    }
    return p;
  }, [draft, items, saved]);
  const isDirty = Object.keys(patch).length > 0;
  useEffect(() => onDirtyChange(isDirty), [isDirty, onDirtyChange]);

  const apply = useCallback((res: AutopilotSettingsResponse) => {
    setData(res);
    setDraft(draftFrom(res.items));
  }, []);

  // Resolves to whether the settings were loaded (for the latest request).
  // retry: the same project's load again after a failure, which keeps the
  // failure on screen until its result is in.
  const load = useCallback(async (retry = false): Promise<boolean> => {
    requestedProjectIdRef.current = projectId;
    if (!projectId) return false;
    setLoadedProjectId('');
    if (!retry) setLoadError(null);
    setError('');
    try {
      const res = await fetchAutopilotSettings(tRef.current, projectId);
      if (requestedProjectIdRef.current !== projectId) return false;
      apply(res);
      setLoadedProjectId(projectId);
      setLoadError(null);
      return true;
    } catch (e) {
      if (requestedProjectIdRef.current !== projectId) return false;
      setLoadError({ projectId, message: errorMessage(e, tRef.current('errors.UNKNOWN')) });
      setLoadFailures(n => n + 1);
      return false;
    }
  }, [projectId, tRef, apply]);

  useEffect(() => { load(); }, [load]);

  const rowsRef = useRef<HTMLDivElement>(null);
  const focusRowsAfterRetry = useFocusAfterRetry(() => rowsRef.current);
  const retryLoad = async () => {
    const retryProject = projectId;
    setRetryingFor(retryProject);
    const ok = await load(true);
    setRetryingFor(prev => (prev === retryProject ? null : prev));
    if (ok) focusRowsAfterRetry();
  };

  const handleSave = async () => {
    // The answer is only used while the same project is shown: once the
    // project has changed, applying it would put the old project's settings
    // (or its error) in the new one's form.
    const saveProject = projectId;
    setSavingFor(saveProject);
    setError('');
    try {
      const res = await saveAutopilotSettings(t, saveProject, patch);
      if (requestedProjectIdRef.current !== saveProject) return;
      apply(res);
      showSavedFlash();
    } catch (e) {
      if (requestedProjectIdRef.current !== saveProject) return;
      setError(errorMessage(e, t('errors.UNKNOWN')));
    } finally {
      // Only this save's own busy state: a save started since (for the
      // project switched to) stays busy until its own answer is in.
      setSavingFor(prev => (prev === saveProject ? null : prev));
    }
  };

  if (!projectId) {
    return <p className="text-xs text-slate-500 dark:text-slate-400">{t('settings.autopilot.noProject')}</p>;
  }

  const valueLabel = (key: AutopilotSettingKey, v: AutopilotSettingValue | string | boolean | null): string => {
    if (v === null) return '';
    if (typeof v === 'boolean') return v ? t('settings.common.yes') : t('settings.common.no');
    if (CONTROLS[key].kind === 'select') return t(`settings.autopilot.keys.${key}.options.${v}`, { defaultValue: String(v) });
    return String(v);
  };

  const sourceText = (it: AutopilotSettingItem): string => {
    if (it.locked) {
      const base = t(`settings.autopilot.source.${it.source}`);
      return it.local !== null
        ? t('settings.autopilot.lockedWithLocal', { source: base, value: valueLabel(it.key, it.local) })
        : base;
    }
    return t(`settings.autopilot.source.${it.source}`);
  };

  const warningText = (w: AutopilotSettingsResponse['warnings'][number]): string => {
    const params = {
      key: w.key ? t(`settings.autopilot.keys.${w.key}.label`, { defaultValue: w.key }) : '',
      value: w.value ?? '',
      source: w.source ? t(`settings.autopilot.warningSource.${w.source}`, { defaultValue: w.source }) : ''
    };
    return t(`settings.autopilot.warnings.${w.code}`, { ...params, defaultValue: w.message });
  };

  const renderControl = (it: AutopilotSettingItem, inputId: string, describedBy: string) => {
    const control = CONTROLS[it.key];
    const cur = draft[it.key] ?? null;
    // Locked: the team value; otherwise the local draft, else the default.
    const shown = it.locked ? toDraftValue(it.value) : (cur ?? toDraftValue(it.default));
    const common = {
      id: inputId,
      disabled: it.locked || saving,
      'aria-describedby': describedBy
    };
    const inputClass =
      'bg-white dark:bg-slate-900 border border-slate-300 dark:border-slate-700 rounded-lg px-2 py-1 text-xs text-slate-900 dark:text-slate-100 focus:outline-hidden focus:border-blue-600 dark:focus:border-blue-400 disabled:opacity-60 disabled:bg-slate-50 dark:disabled:bg-slate-800 narrow:min-w-0 narrow:max-w-full';
    const set = (v: string | boolean) => setDraft(d => ({ ...d, [it.key]: v }));
    if (control.kind === 'boolean') {
      return (
        <input
          {...common}
          type="checkbox"
          checked={shown === true}
          onChange={e => set(e.target.checked)}
          className="w-4 h-4"
        />
      );
    }
    if (control.kind === 'select') {
      return (
        <select {...common} value={String(shown)} onChange={e => set(e.target.value)} className={`${inputClass} narrow:flex-1 upto-15rem:basis-full upto-15rem:w-full`}>
          {control.options.map(o => (
            <option key={o} value={o}>
              {valueLabel(it.key, o)}
            </option>
          ))}
        </select>
      );
    }
    return (
      <input
        {...common}
        type="number"
        min={control.min}
        max={control.max}
        step={1}
        value={String(shown)}
        onChange={e => set(e.target.value)}
        className={`${inputClass} w-24`}
      />
    );
  };

  const permissionShown = (() => {
    const it = items.find(i => i.key === 'permissionMode');
    if (!it) return null;
    return it.locked ? it.value : (draft.permissionMode ?? it.default);
  })();

  // In this order (see LoadFailure): the current project's load failure,
  // then loading, then the form. A failed load never sets loadedProjectId,
  // so the loading test would otherwise hide the failure for good.
  const failed = loadError !== null && loadError.projectId === projectId;
  const loaded = !failed && loadedProjectId === projectId;

  return (
    <div className="h-full min-h-0 overflow-y-auto flex flex-col gap-3 narrow:h-auto narrow:overflow-visible">
      <div>
        <h3 className="text-sm font-semibold text-slate-800 dark:text-slate-200">
          {t('settings.autopilot.title', { project: projectName || projectId })}
        </h3>
        <p className="text-[0.6875rem] text-slate-500 dark:text-slate-400 mt-1">{t('settings.autopilot.description')}</p>
        {loaded && data?.team_file && (
          <p className="text-[0.6875rem] text-slate-500 dark:text-slate-400 mt-1 wrap-anywhere">
            {t('settings.autopilot.teamFile', { path: data.team_file })}
          </p>
        )}
      </div>

      {failed ? (
        <LoadFailure
          message={t('settings.common.loadFailed', { message: loadError.message })}
          retrying={retryingFor === projectId}
          onRetry={() => void retryLoad()}
          failureKey={loadFailures}
        />
      ) : !loaded ? (
        <LoadingLine />
      ) : (
        <>
          {error && (
            <ErrorBox role="alert" className="p-2.5 text-[0.6875rem]">
              {error}
            </ErrorBox>
          )}

          {data && data.warnings.length > 0 && (
            <ul className="p-2.5 bg-amber-50 dark:bg-amber-950 text-amber-800 dark:text-amber-200 text-[0.6875rem] rounded-lg border border-amber-200 dark:border-amber-900 list-disc pl-6">
              {data.warnings.map((w, i) => (
                <li key={`${w.code}-${w.source ?? ''}-${w.key ?? ''}-${i}`}>{warningText(w)}</li>
              ))}
            </ul>
          )}

          {/* tabIndex -1 and the ref: where focus goes after a successful
              retry. No outline: it is not a control. */}
          <div
            ref={rowsRef}
            tabIndex={-1}
            className="divide-y divide-slate-100 dark:divide-slate-800 border border-slate-200 dark:border-slate-800 rounded-lg focus:outline-hidden"
          >
            {items.map(it => {
              const inputId = `${idPrefix}-${it.key}`;
              const hintId = `${inputId}-hint`;
              const sourceId = `${inputId}-source`;
              const canClear = !it.locked && (draft[it.key] ?? null) !== null;
              // DFLT-00287: at the narrowest size (upto-15rem: -- 480px and
              // below at 200% text) a select gets the row's full width and
              // its reset button moves to the next line, right-aligned, so
              // enough of the chosen option shows to tell the options apart.
              // Checkboxes and number inputs are short, so they stay as they
              // are. The DOM order (select, then reset) -- and so the tab
              // order -- does not change.
              const isSelect = CONTROLS[it.key].kind === 'select';
              return (
                <div key={it.key} className="flex items-start gap-3 px-3 py-2.5 narrow:flex-wrap upto-15rem:px-2">
                  <div className="flex-1 min-w-0 narrow:basis-full">
                    <label htmlFor={inputId} className="block text-xs font-semibold text-slate-700 dark:text-slate-300">
                      {t(`settings.autopilot.keys.${it.key}.label`)}
                    </label>
                    <p id={hintId} className="text-[0.625rem] text-slate-500 dark:text-slate-400 mt-0.5">
                      {t(`settings.autopilot.keys.${it.key}.hint`)}
                    </p>
                    {/* Where the value comes from -- for a locked key, the reason
                        it cannot be changed: required information, so it
                        keeps the hint's contrast and is part of the
                        control's description. */}
                    <p
                      id={sourceId}
                      className="text-[0.625rem] mt-0.5 flex items-center gap-1 text-slate-500 dark:text-slate-400"
                      data-testid={`autopilot-source-${it.key}`}
                    >
                      {it.locked && <Lock className="w-3 h-3" aria-hidden="true" />}
                      {sourceText(it)}
                    </p>
                  </div>
                  <div
                    className={
                      isSelect ? `${CONTROLS_CLASS} upto-15rem:flex-wrap upto-15rem:justify-end` : CONTROLS_CLASS
                    }
                  >
                    {renderControl(it, inputId, `${hintId} ${sourceId}`)}
                    {!it.locked && (
                      <IconButton
                        onClick={() => setDraft(d => ({ ...d, [it.key]: null }))}
                        disabled={!canClear || saving}
                        label={t('settings.autopilot.clearLocalFor', { key: t(`settings.autopilot.keys.${it.key}.label`) })}
                        tooltip={t('settings.autopilot.clearLocal')}
                        className="p-1 rounded-sm text-slate-500 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-200 disabled:opacity-30"
                      >
                        <RotateCcw className="w-3.5 h-3.5" aria-hidden="true" />
                      </IconButton>
                    )}
                  </div>
                </div>
              );
            })}
          </div>

          {permissionShown === 'bypassPermissions' && (
            <ErrorBox role="note" className="p-2.5 flex gap-2 text-[0.6875rem]">
              <AlertTriangle className="w-4 h-4 shrink-0" aria-hidden="true" />
              <span>{t('settings.autopilot.bypassWarning')}</span>
            </ErrorBox>
          )}

          <div className="flex justify-end items-center gap-2 narrow:flex-wrap">
            {/* The flash disappears after 2 seconds; the always-mounted live
                region is what announces it (SC 4.1.3), like
                AppSettingsEditor's. */}
            <StatusLiveRegion message={savedFlash ? t('settings.common.saveSuccess') : ''} />
            {savedFlash && (
              <span aria-hidden="true" className="text-emerald-700 dark:text-emerald-400 text-xs flex items-center gap-1">
                <CheckCircle2 aria-hidden="true" className="w-3.5 h-3.5" /> {t('settings.common.saveSuccess')}
              </span>
            )}
            <button
              type="button"
              onClick={handleSave}
              disabled={saving || !isDirty}
              {...submittingProps(saving)}
              className="px-4 py-1.5 bg-blue-600 hover:bg-blue-700 disabled:opacity-50 rounded-lg text-xs font-semibold text-white flex items-center gap-1.5 transition"
            >
              {saving ? (
                <Spinner className="w-3.5 h-3.5" />
              ) : (
                <Save className="w-3.5 h-3.5" aria-hidden="true" />
              )}
              {saving ? t('settings.common.saving') : t('settings.common.save')}
            </button>
          </div>
        </>
      )}
    </div>
  );
};
