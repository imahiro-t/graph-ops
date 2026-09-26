// "レビューゲート" tab: edits this scope's own Document.review_gates
// (criteria/additional_criteria/enabled per gate id) plus the workflow-wide
// review iteration limit (Document.max_iterations: 3/4/5, DFLT-00140), and
// shows -- per gate -- the merged preview (what an agent actually sees once
// this scope's overrides are folded into the inherited defaults), mirroring
// the ノード tab's mergedPreviewLabel. Per mergeReviewGates
// (internal/config/merge.go), additional_criteria is already folded into
// criteria by the time it reaches merged_catalog, so the preview only needs
// to show the resulting criteria/enabled -- not a separate
// additional_criteria field. There is no per-gate iteration limit any more.
import React, { useCallback, useEffect, useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Loader2, Save, Plus, Trash2, CheckCircle2, ChevronDown, ChevronRight, AlertTriangle } from 'lucide-react';
import { ReviewGateDef, SETTINGS_CATALOG_WARNINGS, SettingsCatalog, SettingsCatalogWarning, SettingsDocument } from '../../types';
import { fetchSettingsCatalog, saveSettingsCatalog } from '../../lib/settingsApi';
import { errorMessage } from '../../lib/apiError';
import { useLatest } from '../../hooks/useLatest';
import { useSavedFlash } from '../../hooks/useSavedFlash';
import { IconButton } from '../IconButton';

interface Props {
  onDirtyChange: (dirty: boolean) => void;
}

// isOverridden marks whether this scope's own tier_document actually has an
// entry for this gate id: false means the row is showing the plugin
// default/inherited gate as-is (not yet overridden here), which is why it
// can't be deleted (there is no local override to remove) and why editing
// any field on it flips isOverridden to true (see updateGate) -- a save only
// ever writes isOverridden rows into review_gates, so an untouched default
// row is never turned into a needless duplicate override just by being
// displayed.
//
// origin/baseline record where the row came from when it was loaded and the
// values it showed then, so a save can write only the fields the user
// actually changed (see buildGateOverride) instead of freezing the whole row
// -- a whole-row copy would stop later plugin-default updates (criteria,
// enabled, ...) from ever reaching that gate. 'inherited' rows show
// the merged values with no override at this scope, 'override' rows show
// this scope's own override, 'new' rows were added with "Add Review Gate".
//
// hasDefault marks a row whose ID (as loaded) has a gate in
// inherited_catalog behind it. Such a row's ID can't be edited: its override
// -- partial, now that only changed fields are saved -- relies on that
// default for every field it leaves out, so moving it to another ID would
// leave a gate with no criteria/name behind it. A custom gate's override
// (no default) stays renamable, and a 'new' row never has a default.
type GateOrigin = 'inherited' | 'override' | 'new';
type GateRow = ReviewGateDef & {
  id: string;
  isOverridden: boolean;
  origin: GateOrigin;
  baseline: ReviewGateDef;
  hasDefault: boolean;
};

type GateField = keyof ReviewGateDef;
const GATE_FIELDS: GateField[] = ['name', 'criteria', 'additional_criteria', 'enabled'];

// The values the workflow-wide review iteration limit may take (the server
// rejects anything else with INVALID_MAX_ITERATIONS).
const MAX_ITERATIONS_CHOICES: readonly number[] = [3, 4, 5];
const DEFAULT_MAX_ITERATIONS = 3;

// The server's warnings are codes plus details, never sentences: keep only
// well-formed entries with a code this screen has a message for, so an
// unknown code (or an older server's plain string) shows nothing rather than
// untranslated text.
const KNOWN_WARNING_CODES: readonly string[] = Object.values(SETTINGS_CATALOG_WARNINGS);
function knownWarnings(raw: unknown): SettingsCatalogWarning[] {
  if (!Array.isArray(raw)) return [];
  return raw.filter(
    (w): w is SettingsCatalogWarning =>
      typeof w === 'object' && w !== null && KNOWN_WARNING_CODES.includes((w as SettingsCatalogWarning).code)
  );
}

// Normalizes a field's value the way the form displays it, so an untouched
// field, or one changed and then changed back, compares equal to its
// baseline: strings treat undefined/null as '', and enabled treats anything
// but false as enabled (matching the checkbox's `g.enabled !== false`).
function normalizeGateField(field: GateField, value: ReviewGateDef[GateField]): string | boolean {
  if (field === 'enabled') return value !== false;
  return (value as string | null | undefined) ?? '';
}

// "Empty" means inherit from the layer below (mergeReviewGates treats an
// empty string or a missing field that way): only '' for the text fields.
// enabled always normalizes to a boolean, so it is never "empty" -- a
// changed checkbox is always written as true/false.
function isEmptyGateValue(value: string | boolean): boolean {
  return value === '';
}

// A retired per-gate max_iterations may still come back from an older
// server or file; it is never shown, compared or sent back.
function withoutLegacyMaxIterations(gate: ReviewGateDef): ReviewGateDef {
  const { max_iterations: _legacy, ...rest } = gate as ReviewGateDef & { max_iterations?: unknown };
  return rest;
}

// Builds the override this scope saves for one row, or null when nothing
// should be written for it. Starts from the existing override (so fields it
// already had are kept) or from nothing, then applies each field whose value
// differs from what the row showed when loaded: an emptied field is dropped
// (inherit again), any other value is written. An 'inherited' row whose
// changes all ended up reverted writes nothing; an 'override' row keeps its
// entry even if it ends up empty (removing an override is the delete
// button's job), and a 'new' row is always written.
function buildGateOverride(row: GateRow): ReviewGateDef | null {
  const result: ReviewGateDef = row.origin === 'override' ? { ...row.baseline } : {};
  for (const field of GATE_FIELDS) {
    const current = normalizeGateField(field, row[field]);
    if (current === normalizeGateField(field, row.baseline[field])) continue;
    if (isEmptyGateValue(current)) {
      delete result[field];
    } else {
      (result as Record<GateField, unknown>)[field] = current;
    }
  }
  if (row.origin === 'inherited' && Object.keys(result).length === 0) return null;
  return result;
}

type FetchedRows = {
  rows: GateRow[];
  merged: SettingsCatalog['review_gates'];
  inherited: SettingsCatalog['review_gates'];
  // This scope's own workflow-wide limit (null = inherit), the value it
  // would inherit, and the server's warnings about this scope's file.
  maxIterations: number | null;
  inheritedMaxIterations: number;
  warnings: SettingsCatalogWarning[];
};

const SMALL_LABEL_CLASS = 'block text-[10px] font-semibold text-slate-500 dark:text-slate-400 mb-0.5';

export const ReviewGatesEditor: React.FC<Props> = ({ onDirtyChange }) => {
  const { t } = useTranslation();
  // See src/hooks/useLatest.ts -- keeps fetchRows/load below insensitive to
  // language changes (F-1).
  const tRef = useLatest(t);
  // Row inputs are repeated, so each id is this prefix plus the row index.
  const idPrefix = useId();
  const [gates, setGates] = useState<GateRow[]>([]);
  const [savedGates, setSavedGates] = useState<GateRow[]>([]);
  const [mergedGates, setMergedGates] = useState<SettingsCatalog['review_gates']>({});
  // The defaults this scope inherits, per gate ID: shown as placeholders in
  // an override's empty fields, which inherit these values.
  const [inheritedGates, setInheritedGates] = useState<SettingsCatalog['review_gates']>({});
  const [expandedPreview, setExpandedPreview] = useState<Record<number, boolean>>({});
  // The workflow-wide review iteration limit this scope sets (null =
  // inherit), as edited and as last loaded/saved.
  const [maxIterations, setMaxIterations] = useState<number | null>(null);
  const [savedMaxIterations, setSavedMaxIterations] = useState<number | null>(null);
  const [inheritedMaxIterations, setInheritedMaxIterations] = useState<number>(DEFAULT_MAX_ITERATIONS);
  const [warnings, setWarnings] = useState<SettingsCatalogWarning[]>([]);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  // True while `error` is the empty-ID validation error, so the rows whose ID
  // is (still) empty can be marked invalid and point at the message.
  const [emptyIdErrorShown, setEmptyIdErrorShown] = useState(false);
  const errorId = `${idPrefix}-error`;
  const { savedFlash, showSavedFlash } = useSavedFlash();

  const isDirty = JSON.stringify(gates) !== JSON.stringify(savedGates) || maxIterations !== savedMaxIterations;
  useEffect(() => onDirtyChange(isDirty), [isDirty, onDirtyChange]);

  // Builds the row list from the union of merged_catalog.review_gates (every
  // gate actually in effect, including plugin defaults never touched at this
  // scope) and this scope's own tier_document.review_gates (its overrides)
  // -- so plugin-default gates show up even before anyone overrides them,
  // per completion criterion. A row's displayed values come from the tier
  // document when overridden here, otherwise from the merged/effective
  // values (a sensible starting point if the user decides to override it).
  const fetchRows = useCallback(async (): Promise<FetchedRows> => {
    const catalogRes = await fetchSettingsCatalog(tRef.current);
    const overrideMap = catalogRes.tier_document.review_gates || {};
    const mergedMap = catalogRes.merged_catalog.review_gates || {};
    const inheritedMap = catalogRes.inherited_catalog?.review_gates || {};
    const ids = Array.from(new Set([...Object.keys(mergedMap), ...Object.keys(overrideMap)]));
    const rows: GateRow[] = ids.map(id => {
      const isOverridden = id in overrideMap;
      const base = withoutLegacyMaxIterations(isOverridden ? overrideMap[id] : mergedMap[id]);
      return {
        ...base,
        id,
        isOverridden,
        origin: isOverridden ? 'override' : 'inherited',
        baseline: { ...base },
        // A row with no override here is showing an inherited gate, so it
        // has a default even if inherited_catalog were to omit it.
        hasDefault: !isOverridden || id in inheritedMap
      };
    });
    return {
      rows,
      merged: mergedMap,
      inherited: inheritedMap,
      maxIterations: catalogRes.tier_document.max_iterations ?? null,
      inheritedMaxIterations: catalogRes.inherited_catalog?.max_iterations ?? DEFAULT_MAX_ITERATIONS,
      warnings: knownWarnings(catalogRes.warnings)
    };
  }, [tRef]);

  const applyFetched = useCallback((fetched: FetchedRows) => {
    setGates(fetched.rows);
    setSavedGates(fetched.rows);
    setMergedGates(fetched.merged);
    setInheritedGates(fetched.inherited);
    setMaxIterations(fetched.maxIterations);
    setSavedMaxIterations(fetched.maxIterations);
    setInheritedMaxIterations(fetched.inheritedMaxIterations);
    setWarnings(fetched.warnings);
  }, []);

  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    setEmptyIdErrorShown(false);
    try {
      applyFetched(await fetchRows());
    } catch (e) {
      setError(errorMessage(e, tRef.current('errors.UNKNOWN')));
    } finally {
      setLoading(false);
    }
  }, [fetchRows, applyFetched, tRef]);

  useEffect(() => { load(); }, [load]);

  // Editing any field on a not-yet-overridden default row is what actually
  // creates its override -- see GateRow's doc comment.
  const updateGate = (idx: number, patch: Partial<GateRow>) => {
    setGates(prev => prev.map((g, i) => (i === idx ? { ...g, ...patch, isOverridden: true } : g)));
  };

  const addGate = () => {
    setGates(prev => [...prev, { id: '', name: '', criteria: '', enabled: true, isOverridden: true, origin: 'new', baseline: {}, hasDefault: false }]);
  };

  // Only ever called from a button that's disabled unless g.isOverridden
  // (see the JSX below); guarded here too so this can't remove a
  // not-yet-overridden default row even if triggered some other way.
  const removeGate = (idx: number) => {
    setGates(prev => (prev[idx]?.isOverridden ? prev.filter((_, i) => i !== idx) : prev));
  };

  const handleSave = async () => {
    // A row without an ID cannot be saved; refuse the whole save (and keep
    // every input as typed) instead of silently dropping that row and still
    // reporting success.
    if (gates.some(g => g.id.trim() === '')) {
      setError(t('settings.reviewGates.emptyIdError'));
      setEmptyIdErrorShown(true);
      return;
    }
    setSaving(true);
    setError('');
    setEmptyIdErrorShown(false);
    try {
      const catalogRes = await fetchSettingsCatalog(t);
      const review_gates: Record<string, ReviewGateDef> = {};
      for (const g of gates) {
        if (!g.isOverridden) continue;
        const override = buildGateOverride(g);
        if (override) review_gates[g.id] = override;
      }
      const document: SettingsDocument = {
        ...catalogRes.tier_document,
        version: catalogRes.tier_document.version || 1,
        review_gates
      };
      // "Inherit" is expressed by leaving the key out, which clears this
      // scope's own value on save.
      if (maxIterations === null) {
        delete document.max_iterations;
      } else {
        document.max_iterations = maxIterations;
      }
      await saveSettingsCatalog(t, document);
      // Re-derive rows from the server rather than patching local state --
      // a deleted override needs to reappear as a non-overridden default row
      // (if it's still part of merged_catalog), which a simple
      // setSavedGates(gates) wouldn't do.
      applyFetched(await fetchRows());
      showSavedFlash();
    } catch (e) {
      setError(errorMessage(e, t('errors.UNKNOWN')));
    } finally {
      setSaving(false);
    }
  };

  // A hand-edited out-of-range value (e.g. 7) matches none of the choices;
  // show it as its own, invalid option instead of letting the select fall
  // back to displaying "inherit" while still holding 7.
  const maxIterationsInvalid = maxIterations !== null && !MAX_ITERATIONS_CHOICES.includes(maxIterations);
  const warningItemId = (i: number) => `${idPrefix}-warning-${i}`;
  const outOfRangeWarningIndex = warnings.findIndex(w => w.code === SETTINGS_CATALOG_WARNINGS.maxIterationsOutOfRange);
  const warningText = (w: SettingsCatalogWarning) =>
    w.code === SETTINGS_CATALOG_WARNINGS.legacyGateMaxIterations
      ? t('settings.reviewGates.warningLegacyGateMaxIterations', { id: w.gate_id ?? '' })
      : t('settings.reviewGates.warningMaxIterationsOutOfRange', { value: w.value ?? '' });
  const maxIterationsDescribedBy = [
    `${idPrefix}-workflow-max-help`,
    maxIterationsInvalid && outOfRangeWarningIndex >= 0 ? warningItemId(outOfRangeWarningIndex) : null
  ].filter(Boolean).join(' ');

  if (loading) {
    return (
      <div className="flex items-center gap-2 text-slate-500 dark:text-slate-400 text-xs py-8 justify-center">
        <Loader2 aria-hidden="true" className="w-4 h-4 animate-spin" /> {t('settings.common.loading')}
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-3 h-full min-h-0">
      <p className="text-[11px] text-slate-500 dark:text-slate-400">{t('settings.reviewGates.intro')}</p>
      {error && <div id={errorId} role="alert" className="p-2.5 bg-red-50 dark:bg-red-950 text-red-700 dark:text-red-300 text-[11px] rounded-lg border border-red-200 dark:border-red-900 whitespace-pre-wrap">{error}</div>}

      {warnings.length > 0 && (
        <div
          role="note"
          aria-labelledby={`${idPrefix}-warnings-title`}
          className="p-2.5 bg-amber-50 dark:bg-amber-950 text-amber-800 dark:text-amber-200 text-[11px] rounded-lg border border-amber-200 dark:border-amber-900"
        >
          <p id={`${idPrefix}-warnings-title`} className="flex items-center gap-1 font-semibold">
            <AlertTriangle className="w-3.5 h-3.5 shrink-0" aria-hidden="true" />
            {t('settings.reviewGates.warningsTitle')}
          </p>
          <ul className="mt-1 list-disc pl-5 space-y-0.5">
            {warnings.map((w, i) => <li key={i} id={warningItemId(i)} className="break-words">{warningText(w)}</li>)}
          </ul>
        </div>
      )}

      <div className="border border-slate-200 dark:border-slate-800 rounded-lg p-3 bg-white dark:bg-slate-900">
        <label htmlFor={`${idPrefix}-workflow-max`} className="block text-[11px] font-semibold text-slate-700 dark:text-slate-300 mb-1">
          {t('settings.reviewGates.workflowMaxIterationsLabel')}
        </label>
        <select
          id={`${idPrefix}-workflow-max`}
          value={maxIterations === null ? '' : String(maxIterations)}
          aria-describedby={maxIterationsDescribedBy}
          aria-invalid={maxIterationsInvalid || undefined}
          onChange={e => setMaxIterations(e.target.value === '' ? null : Number(e.target.value))}
          className="bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded px-2 py-1 text-xs text-slate-900 dark:text-slate-100"
        >
          <option value="">{t('settings.reviewGates.workflowMaxIterationsInherit', { value: inheritedMaxIterations })}</option>
          {MAX_ITERATIONS_CHOICES.map(v => <option key={v} value={String(v)}>{v}</option>)}
          {maxIterationsInvalid && (
            <option value={String(maxIterations)}>{t('settings.reviewGates.workflowMaxIterationsInvalid', { value: maxIterations })}</option>
          )}
        </select>
        <p id={`${idPrefix}-workflow-max-help`} className="mt-1 text-[10px] text-slate-500 dark:text-slate-400">
          {t('settings.reviewGates.workflowMaxIterationsHelp')}
        </p>
      </div>

      <div className="flex-1 min-h-0 overflow-auto space-y-3">
        {gates.length === 0 && (
          <div className="text-center text-slate-500 dark:text-slate-400 text-xs py-8 border border-dashed border-slate-200 dark:border-slate-700 rounded-lg">
            {t('settings.reviewGates.tableEmpty')}
          </div>
        )}
        {gates.map((g, idx) => {
          const inherited = inheritedGates[g.id];
          // An override's empty field inherits the default, so show that
          // default as the field's placeholder instead of a blank box.
          const inheritedPlaceholder = (value: string | null | undefined, fallback?: string) =>
            value === undefined || value === null || value === ''
              ? fallback
              : t('settings.reviewGates.inheritedPlaceholder', { value });
          const idLocked = !g.isOverridden || g.hasDefault;
          // What the delete button names: the ID, or on a new row that has
          // none yet, the name typed so far. Whitespace-only counts as empty.
          const deleteTarget = (g.id ?? '').trim() || (g.name ?? '').trim();
          const deleteTooltip = g.isOverridden ? t('settings.reviewGates.deleteGate') : t('settings.reviewGates.cannotDeleteDefaultHint');
          const idInvalid = emptyIdErrorShown && g.id.trim() === '';
          return (
          <div key={idx} className="border border-slate-200 dark:border-slate-800 rounded-lg p-3 space-y-2 bg-white dark:bg-slate-900">
            {/* Each text field has a small visible label above it (the
                placeholders stay as a supplementary hint); items-end keeps
                the checkbox and delete button aligned with the inputs. */}
            <div className="flex items-end gap-2">
              <div className="w-40 flex flex-col">
                <label htmlFor={`${idPrefix}-${idx}-id`} className={SMALL_LABEL_CLASS}>{t('settings.reviewGates.idLabel')}</label>
                <input
                  id={`${idPrefix}-${idx}-id`}
                  value={g.id}
                  // A gate with a default behind it keeps its ID (see
                  // GateRow's hasDefault) -- use "Add Review Gate" for a new
                  // ID instead.
                  disabled={idLocked}
                  title={idLocked ? t('settings.reviewGates.cannotChangeDefaultIdHint') : undefined}
                  aria-invalid={idInvalid || undefined}
                  aria-describedby={idInvalid ? errorId : undefined}
                  placeholder={t('settings.reviewGates.idLabel')}
                  onChange={e => updateGate(idx, { id: e.target.value })}
                  className="w-full bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded px-2 py-1 text-xs font-mono text-slate-900 dark:text-slate-100 disabled:opacity-60"
                />
              </div>
              {!g.isOverridden && (
                <span
                  title={t('settings.reviewGates.defaultBadgeHint')}
                  className="shrink-0 mb-1 text-[10px] font-semibold px-1.5 py-0.5 rounded bg-slate-100 dark:bg-slate-800 text-slate-500 dark:text-slate-400 border border-slate-200 dark:border-slate-700"
                >
                  {t('settings.reviewGates.defaultBadge')}
                </span>
              )}
              <div className="flex-1 min-w-0 flex flex-col">
                <label htmlFor={`${idPrefix}-${idx}-name`} className={SMALL_LABEL_CLASS}>{t('settings.reviewGates.nameLabel')}</label>
                <input
                  id={`${idPrefix}-${idx}-name`}
                  value={g.name || ''}
                  disabled={!g.isOverridden}
                  placeholder={inheritedPlaceholder(inherited?.name, t('settings.reviewGates.nameLabel'))}
                  onChange={e => updateGate(idx, { name: e.target.value })}
                  title={g.isOverridden ? undefined : t('settings.reviewGates.cannotRenameDefaultHint')}
                  className="w-full bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded px-2 py-1 text-xs text-slate-900 dark:text-slate-100 disabled:opacity-60"
                />
              </div>
              <label className="flex items-center gap-1 mb-1 text-[11px] text-slate-600 dark:text-slate-400 shrink-0">
                <input
                  type="checkbox"
                  checked={g.enabled !== false}
                  onChange={e => updateGate(idx, { enabled: e.target.checked })}
                  className="rounded border-slate-300 dark:border-slate-600 text-blue-600 focus:ring-0"
                />
                {t('settings.reviewGates.enabledLabel')}
              </label>
              <IconButton
                onClick={() => removeGate(idx)}
                disabled={!g.isOverridden}
                // The name carries the gate (its ID, or its name on a new row
                // with no ID yet) so a screen reader can tell which row focus
                // is on. With neither an ID nor a name there is nothing to
                // add, so the tooltip text itself is the name, rather than a
                // name ending in an empty target. The tooltip says "delete"
                // -- already part of the name -- or, on a default gate, why
                // it cannot be deleted; only that reason is added as the
                // description, and only when it is not the name already.
                label={deleteTarget ? t('settings.reviewGates.deleteGateAriaLabel', { name: deleteTarget }) : deleteTooltip}
                tooltip={deleteTooltip}
                describeWithTooltip={Boolean(deleteTarget) && !g.isOverridden}
                wrapperClassName="mb-0.5 shrink-0"
                className="p-1 text-slate-500 dark:text-slate-400 hover:text-red-600 dark:hover:text-red-400 disabled:opacity-40"
              >
                <Trash2 aria-hidden="true" className="w-3.5 h-3.5" />
              </IconButton>
            </div>
            <div>
              <label htmlFor={`${idPrefix}-${idx}-criteria`} className={SMALL_LABEL_CLASS}>{t('settings.reviewGates.criteriaLabel')}</label>
              <textarea
                id={`${idPrefix}-${idx}-criteria`}
                value={g.criteria || ''}
                placeholder={inheritedPlaceholder(inherited?.criteria)}
                onChange={e => updateGate(idx, { criteria: e.target.value })}
                rows={2}
                className="w-full bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded px-2 py-1 text-xs text-slate-900 dark:text-slate-100 disabled:opacity-60"
              />
            </div>
            <div>
              <label htmlFor={`${idPrefix}-${idx}-additional`} className={SMALL_LABEL_CLASS}>{t('settings.reviewGates.additionalCriteriaLabel')}</label>
              <textarea
                id={`${idPrefix}-${idx}-additional`}
                value={g.additional_criteria || ''}
                placeholder={inheritedPlaceholder(inherited?.additional_criteria)}
                onChange={e => updateGate(idx, { additional_criteria: e.target.value })}
                rows={2}
                className="w-full bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded px-2 py-1 text-xs text-slate-900 dark:text-slate-100 disabled:opacity-60"
              />
            </div>
            <div className="pt-1 border-t border-slate-100 dark:border-slate-800">
              <button
                type="button"
                onClick={() => setExpandedPreview(prev => ({ ...prev, [idx]: !prev[idx] }))}
                className="flex items-center gap-1 text-[10px] font-semibold text-slate-500 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-300"
              >
                {expandedPreview[idx] ? <ChevronDown aria-hidden="true" className="w-3 h-3" /> : <ChevronRight aria-hidden="true" className="w-3 h-3" />}
                {t('settings.reviewGates.mergedPreviewLabel')}
              </button>
              {expandedPreview[idx] && (
                mergedGates[g.id] ? (
                  <div className="mt-1.5 space-y-1.5">
                    <div>
                      <label className="block text-[10px] font-semibold text-slate-500 dark:text-slate-400 mb-0.5">{t('settings.reviewGates.mergedCriteriaLabel')}</label>
                      <pre className="whitespace-pre-wrap text-[11px] leading-relaxed bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded-lg p-2 max-h-32 overflow-y-auto text-slate-600 dark:text-slate-400 font-mono">
                        {mergedGates[g.id].criteria || t('settings.common.inheritedFromDefault')}
                      </pre>
                    </div>
                    <div className="flex items-center gap-4 text-[11px] text-slate-600 dark:text-slate-400">
                      <span>{t('settings.reviewGates.mergedEnabledLabel')}: {mergedGates[g.id].enabled === false ? t('settings.common.no') : t('settings.common.yes')}</span>
                    </div>
                  </div>
                ) : (
                  <p className="mt-1 text-[10px] text-slate-500 dark:text-slate-400">{t('settings.reviewGates.previewUnavailableHint')}</p>
                )
              )}
            </div>
          </div>
          );
        })}
      </div>

      <div className="flex justify-between items-center">
        <button
          onClick={addGate}
          className="px-3 py-1.5 bg-slate-100 dark:bg-slate-800 hover:bg-slate-200 dark:hover:bg-slate-700 disabled:opacity-50 rounded-lg text-xs font-semibold text-slate-700 dark:text-slate-300 flex items-center gap-1.5 transition"
        >
          <Plus aria-hidden="true" className="w-3.5 h-3.5" /> {t('settings.reviewGates.addGate')}
        </button>
        <div className="flex items-center gap-2">
          {savedFlash && (
            <span className="text-emerald-600 text-xs flex items-center gap-1">
              <CheckCircle2 aria-hidden="true" className="w-3.5 h-3.5" /> {t('settings.common.saveSuccess')}
            </span>
          )}
          <button
            onClick={handleSave}
            disabled={saving || !isDirty}
            className="px-4 py-1.5 bg-blue-600 hover:bg-blue-700 disabled:opacity-50 rounded-lg text-xs font-semibold text-white flex items-center gap-1.5 transition"
          >
            {saving ? <Loader2 aria-hidden="true" className="w-3.5 h-3.5 animate-spin" /> : <Save aria-hidden="true" className="w-3.5 h-3.5" />}
            {saving ? t('settings.common.saving') : t('settings.common.save')}
          </button>
        </div>
      </div>
    </div>
  );
};
