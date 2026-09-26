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
import React, { useCallback, useEffect, useId, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { AlertTriangle, CheckCircle2, Loader2, Lock, RotateCcw, Save } from 'lucide-react';
import { StatusLiveRegion } from '../StatusLiveRegion';
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
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
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

  const load = useCallback(async () => {
    if (!projectId) return;
    setLoading(true);
    setError('');
    try {
      apply(await fetchAutopilotSettings(tRef.current, projectId));
    } catch (e) {
      setError(errorMessage(e, tRef.current('errors.UNKNOWN')));
    } finally {
      setLoading(false);
    }
  }, [projectId, tRef, apply]);

  useEffect(() => { load(); }, [load]);

  const handleSave = async () => {
    setSaving(true);
    setError('');
    try {
      apply(await saveAutopilotSettings(t, projectId, patch));
      showSavedFlash();
    } catch (e) {
      setError(errorMessage(e, t('errors.UNKNOWN')));
    } finally {
      setSaving(false);
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
      'bg-white dark:bg-slate-900 border border-slate-300 dark:border-slate-700 rounded-lg px-2 py-1 text-xs text-slate-900 dark:text-slate-100 focus:outline-none focus:border-blue-600 dark:focus:border-blue-400 disabled:opacity-60 disabled:bg-slate-50 dark:disabled:bg-slate-800';
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
        <select {...common} value={String(shown)} onChange={e => set(e.target.value)} className={inputClass}>
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

  return (
    <div className="h-full min-h-0 overflow-y-auto flex flex-col gap-3">
      <div>
        <h3 className="text-sm font-semibold text-slate-800 dark:text-slate-200">
          {t('settings.autopilot.title', { project: projectName || projectId })}
        </h3>
        <p className="text-[11px] text-slate-500 dark:text-slate-400 mt-1">{t('settings.autopilot.description')}</p>
        {data?.team_file && (
          <p className="text-[11px] text-slate-500 dark:text-slate-400 mt-1">
            {t('settings.autopilot.teamFile', { path: data.team_file })}
          </p>
        )}
      </div>

      {error && (
        <div role="alert" className="p-2.5 bg-red-50 dark:bg-red-950 text-red-700 dark:text-red-300 text-[11px] rounded-lg border border-red-200 dark:border-red-900">
          {error}
        </div>
      )}

      {data && data.warnings.length > 0 && (
        <ul className="p-2.5 bg-amber-50 dark:bg-amber-950 text-amber-800 dark:text-amber-200 text-[11px] rounded-lg border border-amber-200 dark:border-amber-900 list-disc pl-6">
          {data.warnings.map((w, i) => (
            <li key={`${w.code}-${w.source ?? ''}-${w.key ?? ''}-${i}`}>{warningText(w)}</li>
          ))}
        </ul>
      )}

      {loading && !data ? (
        <div className="flex items-center gap-2 text-slate-500 dark:text-slate-400 text-xs py-8 justify-center">
          <Loader2 className="w-4 h-4 motion-safe:animate-spin" aria-hidden="true" /> {t('settings.common.loading')}
        </div>
      ) : (
        <>
          <div className="divide-y divide-slate-100 dark:divide-slate-800 border border-slate-200 dark:border-slate-800 rounded-lg">
            {items.map(it => {
              const inputId = `${idPrefix}-${it.key}`;
              const hintId = `${inputId}-hint`;
              const sourceId = `${inputId}-source`;
              const canClear = !it.locked && (draft[it.key] ?? null) !== null;
              return (
                <div key={it.key} className="flex items-start gap-3 px-3 py-2.5">
                  <div className="flex-1 min-w-0">
                    <label htmlFor={inputId} className="block text-xs font-semibold text-slate-700 dark:text-slate-300">
                      {t(`settings.autopilot.keys.${it.key}.label`)}
                    </label>
                    <p id={hintId} className="text-[10px] text-slate-500 dark:text-slate-400 mt-0.5">
                      {t(`settings.autopilot.keys.${it.key}.hint`)}
                    </p>
                    {/* Where the value comes from -- for a locked key, the reason
                        it cannot be changed: required information, so it
                        keeps the hint's contrast and is part of the
                        control's description. */}
                    <p
                      id={sourceId}
                      className="text-[10px] mt-0.5 flex items-center gap-1 text-slate-500 dark:text-slate-400"
                      data-testid={`autopilot-source-${it.key}`}
                    >
                      {it.locked && <Lock className="w-3 h-3" aria-hidden="true" />}
                      {sourceText(it)}
                    </p>
                  </div>
                  <div className="flex items-center gap-2 shrink-0">
                    {renderControl(it, inputId, `${hintId} ${sourceId}`)}
                    {!it.locked && (
                      <button
                        type="button"
                        onClick={() => setDraft(d => ({ ...d, [it.key]: null }))}
                        disabled={!canClear || saving}
                        title={t('settings.autopilot.clearLocal')}
                        aria-label={t('settings.autopilot.clearLocalFor', { key: t(`settings.autopilot.keys.${it.key}.label`) })}
                        className="p-1 rounded text-slate-500 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-200 disabled:opacity-30"
                      >
                        <RotateCcw className="w-3.5 h-3.5" aria-hidden="true" />
                      </button>
                    )}
                  </div>
                </div>
              );
            })}
          </div>

          {permissionShown === 'bypassPermissions' && (
            <div role="note" className="p-2.5 flex gap-2 bg-red-50 dark:bg-red-950 text-red-700 dark:text-red-300 text-[11px] rounded-lg border border-red-200 dark:border-red-900">
              <AlertTriangle className="w-4 h-4 shrink-0" aria-hidden="true" />
              <span>{t('settings.autopilot.bypassWarning')}</span>
            </div>
          )}

          <div className="flex justify-end items-center gap-2">
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
              className="px-4 py-1.5 bg-blue-600 hover:bg-blue-700 disabled:opacity-50 rounded-lg text-xs font-semibold text-white flex items-center gap-1.5 transition"
            >
              {saving ? (
                <Loader2 className="w-3.5 h-3.5 motion-safe:animate-spin" aria-hidden="true" />
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
