// "レビューゲート" tab: edits this scope's own Document.review_gates
// (criteria/additional_criteria/max_iterations/enabled per gate id), and
// shows -- per gate -- the merged preview (what an agent actually sees once
// this scope's overrides are folded into the inherited defaults), mirroring
// the ノード tab's mergedPreviewLabel. Per mergeReviewGates
// (internal/config/merge.go), additional_criteria is already folded into
// criteria by the time it reaches merged_catalog, so the preview only needs
// to show the resulting criteria/max_iterations/enabled -- not a separate
// additional_criteria field.
import React, { useCallback, useEffect, useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Loader2, Save, Plus, Trash2, CheckCircle2, ChevronDown, ChevronRight } from 'lucide-react';
import { ReviewGateDef, SettingsCatalog } from '../../types';
import { fetchSettingsCatalog, saveSettingsCatalog } from '../../lib/settingsApi';
import { errorMessage } from '../../lib/apiError';
import { useLatest } from '../../hooks/useLatest';
import { useSavedFlash } from '../../hooks/useSavedFlash';

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
// max_iterations, ...) from ever reaching that gate. 'inherited' rows show
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
const GATE_FIELDS: GateField[] = ['name', 'criteria', 'additional_criteria', 'max_iterations', 'enabled'];

// Normalizes a field's value the way the form displays it, so an untouched
// field, or one changed and then changed back, compares equal to its
// baseline: strings treat undefined/null as '', max_iterations treats
// undefined as null, and enabled treats anything but false as enabled
// (matching the checkbox's `g.enabled !== false`).
function normalizeGateField(field: GateField, value: ReviewGateDef[GateField]): string | number | boolean | null {
  if (field === 'enabled') return value !== false;
  if (field === 'max_iterations') return value ?? null;
  return (value as string | null | undefined) ?? '';
}

// "Empty" means inherit from the layer below (mergeReviewGates treats an
// empty string or a missing field that way): only '' for the text fields
// and null for max_iterations. enabled always normalizes to a boolean, so
// it is never "empty" -- a changed checkbox is always written as true/false.
function isEmptyGateValue(value: string | number | boolean | null): boolean {
  return value === '' || value === null;
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
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  // True while `error` is the empty-ID validation error, so the rows whose ID
  // is (still) empty can be marked invalid and point at the message.
  const [emptyIdErrorShown, setEmptyIdErrorShown] = useState(false);
  const errorId = `${idPrefix}-error`;
  const { savedFlash, showSavedFlash } = useSavedFlash();

  const isDirty = JSON.stringify(gates) !== JSON.stringify(savedGates);
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
      const base = isOverridden ? overrideMap[id] : mergedMap[id];
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
    return { rows, merged: mergedMap, inherited: inheritedMap };
  }, [tRef]);

  const applyFetched = useCallback(({ rows, merged, inherited }: FetchedRows) => {
    setGates(rows);
    setSavedGates(rows);
    setMergedGates(merged);
    setInheritedGates(inherited);
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
    setGates(prev => [...prev, { id: '', name: '', criteria: '', max_iterations: undefined, enabled: true, isOverridden: true, origin: 'new', baseline: {}, hasDefault: false }]);
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
      const document = {
        ...catalogRes.tier_document,
        version: catalogRes.tier_document.version || 1,
        review_gates
      };
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

  if (loading) {
    return (
      <div className="flex items-center gap-2 text-slate-400 dark:text-slate-500 text-xs py-8 justify-center">
        <Loader2 className="w-4 h-4 animate-spin" /> {t('settings.common.loading')}
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-3 h-full min-h-0">
      <p className="text-[11px] text-slate-500 dark:text-slate-400">{t('settings.reviewGates.intro')}</p>
      {error && <div id={errorId} role="alert" className="p-2.5 bg-red-50 dark:bg-red-950 text-red-700 dark:text-red-300 text-[11px] rounded-lg border border-red-200 dark:border-red-900 whitespace-pre-wrap">{error}</div>}

      <div className="flex-1 min-h-0 overflow-auto space-y-3">
        {gates.length === 0 && (
          <div className="text-center text-slate-400 dark:text-slate-500 text-xs py-8 border border-dashed border-slate-200 dark:border-slate-700 rounded-lg">
            {t('settings.reviewGates.tableEmpty')}
          </div>
        )}
        {gates.map((g, idx) => {
          const inherited = inheritedGates[g.id];
          // An override's empty field inherits the default, so show that
          // default as the field's placeholder instead of a blank box.
          const inheritedPlaceholder = (value: string | number | null | undefined, fallback?: string) =>
            value === undefined || value === null || value === ''
              ? fallback
              : t('settings.reviewGates.inheritedPlaceholder', { value });
          const idLocked = !g.isOverridden || g.hasDefault;
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
              <div className="w-20 flex flex-col">
                <label htmlFor={`${idPrefix}-${idx}-max`} className={`${SMALL_LABEL_CLASS} truncate`} title={t('settings.reviewGates.maxIterationsLabel')}>
                  {t('settings.reviewGates.maxIterationsLabel')}
                </label>
                <input
                  id={`${idPrefix}-${idx}-max`}
                  type="number"
                  min={1}
                  value={g.max_iterations ?? ''}
                  placeholder={inheritedPlaceholder(inherited?.max_iterations, t('settings.reviewGates.maxIterationsLabel'))}
                  onChange={e => updateGate(idx, { max_iterations: e.target.value === '' ? undefined : Number(e.target.value) })}
                  className="w-full bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded px-2 py-1 text-xs text-slate-900 dark:text-slate-100 disabled:opacity-60"
                />
              </div>
              <button
                onClick={() => removeGate(idx)}
                disabled={!g.isOverridden}
                className="p-1 mb-0.5 text-slate-400 dark:text-slate-500 hover:text-red-600 dark:hover:text-red-400 disabled:opacity-40 shrink-0"
                title={g.isOverridden ? t('settings.reviewGates.deleteGate') : t('settings.reviewGates.cannotDeleteDefaultHint')}
              >
                <Trash2 className="w-3.5 h-3.5" />
              </button>
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
                {expandedPreview[idx] ? <ChevronDown className="w-3 h-3" /> : <ChevronRight className="w-3 h-3" />}
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
                      <span>{t('settings.reviewGates.mergedMaxIterationsLabel')}: {mergedGates[g.id].max_iterations ?? t('settings.common.none')}</span>
                      <span>{t('settings.reviewGates.mergedEnabledLabel')}: {mergedGates[g.id].enabled === false ? t('settings.common.no') : t('settings.common.yes')}</span>
                    </div>
                  </div>
                ) : (
                  <p className="mt-1 text-[10px] text-slate-400 dark:text-slate-500">{t('settings.reviewGates.previewUnavailableHint')}</p>
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
          <Plus className="w-3.5 h-3.5" /> {t('settings.reviewGates.addGate')}
        </button>
        <div className="flex items-center gap-2">
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
      </div>
    </div>
  );
};
