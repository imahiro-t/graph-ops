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
import React, { useCallback, useEffect, useId, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Loader2, Save, Plus, Trash2, CheckCircle2, ChevronDown, ChevronRight, AlertTriangle } from 'lucide-react';
import { ReviewGateDef, SETTINGS_CATALOG_WARNINGS, SettingsCatalog, SettingsCatalogWarning, SettingsDocument } from '../../types';
import { fetchSettingsCatalog, saveSettingsCatalog } from '../../lib/settingsApi';
import { errorMessage } from '../../lib/apiError';
import { useLatest } from '../../hooks/useLatest';
import { useSavedFlash } from '../../hooks/useSavedFlash';
import { useTransientAnnouncement } from '../../hooks/useTransientAnnouncement';
import { StatusLiveRegion } from '../StatusLiveRegion';
import { focusIfLost, focusKeySelector, neighborAfterRemoval } from '../../lib/focusAfterRemoval';
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
//
// rowKey is a client-only key, handed out once when the row is created
// (loaded or added) and never reused: it identifies the row for its preview
// open state, its React key and its DOM ids, so deleting a row or editing an
// ID can't move any of those onto another row (DFLT-00199). It is never
// saved -- buildGateOverride only writes GATE_FIELDS.
type GateOrigin = 'inherited' | 'override' | 'new';
type GateRow = ReviewGateDef & {
  id: string;
  isOverridden: boolean;
  origin: GateOrigin;
  baseline: ReviewGateDef;
  hasDefault: boolean;
  rowKey: string;
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

// data-focus-key values of the controls removeGate moves keyboard focus to
// once the deleted row is gone (see pendingFocus).
const deleteButtonKey = (rowKey: string) => `delete-${rowKey}`;
const previewToggleKey = (rowKey: string) => `preview-${rowKey}`;
const ADD_GATE_FOCUS_KEY = 'add-gate';

// What a row is called when naming it to a screen reader (its delete button's
// name and the removal announcement): the ID, or on a new row that has none
// yet, the name typed so far. Whitespace-only counts as empty, so '' means
// the row has neither.
const gateDisplayName = (g: Pick<GateRow, 'id' | 'name'>) => (g.id ?? '').trim() || (g.name ?? '').trim();

const SMALL_LABEL_CLASS = 'block text-[10px] font-semibold text-slate-500 dark:text-slate-400 mb-0.5';

export const ReviewGatesEditor: React.FC<Props> = ({ onDirtyChange }) => {
  const { t } = useTranslation();
  // See src/hooks/useLatest.ts -- keeps fetchRows/load below insensitive to
  // language changes (F-1).
  const tRef = useLatest(t);
  // Row inputs are repeated, so each id is this prefix plus the row's rowKey.
  const idPrefix = useId();
  // Hands out each row's rowKey (see GateRow); letters and digits only, so
  // it is safe inside an id.
  const nextRowKey = useRef(0);
  const newRowKey = useCallback(() => `row${nextRowKey.current++}`, []);
  const [gates, setGates] = useState<GateRow[]>([]);
  const [savedGates, setSavedGates] = useState<GateRow[]>([]);
  const [mergedGates, setMergedGates] = useState<SettingsCatalog['review_gates']>({});
  // The defaults this scope inherits, per gate ID: shown as placeholders in
  // an override's empty fields, which inherit these values.
  const [inheritedGates, setInheritedGates] = useState<SettingsCatalog['review_gates']>({});
  // Which rows' merged preview is open, per rowKey.
  const [expandedPreview, setExpandedPreview] = useState<Record<string, boolean>>({});
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
  const containerRef = useRef<HTMLDivElement>(null);
  // Where keyboard focus goes once the next render has settled (DFLT-00199,
  // following DFLT-00191's rule): deleting a gate unmounts its row, focused
  // delete button included, which would otherwise drop focus to <body>. Same
  // pattern as NodeTypesEditor's.
  const [pendingFocus, setPendingFocus] = useState<string | null>(null);
  useEffect(() => {
    if (pendingFocus === null) return;
    focusIfLost(containerRef.current?.querySelector<HTMLElement>(focusKeySelector(pendingFocus)));
    setPendingFocus(null);
    // gates is what mounts and unmounts the targets; it is listed so the
    // effect runs once the removed row is gone.
  }, [pendingFocus, gates]);
  const { savedFlash, showSavedFlash } = useSavedFlash();
  // Announces a removed row (DFLT-00204) through the always-mounted
  // StatusLiveRegion at the end of the editor, like NodeTypesEditor's and
  // LabelsEditor's deletes. Removing a row only changes the list here -- it
  // is not saved yet -- so the wording says so. Removing two rows that read
  // the same (e.g. two empty new rows) is announced both times: the hook
  // re-sets identical text after a short gap. The notice is cleared as soon as
  // anything else changes the form (add, edit, iteration limit, save, reload),
  // so a stale "removed" never lingers next to later changes; opening or
  // closing a preview changes nothing and leaves it alone.
  const { message: deleteNotice, announce: announceDelete, clear: clearDeleteNotice } = useTransientAnnouncement();

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
        hasDefault: !isOverridden || id in inheritedMap,
        rowKey: newRowKey()
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
  }, [tRef, newRowKey]);

  // gates and savedGates get the very same rows (same rowKeys), so an
  // untouched form is not dirty. The rows are new, with new rowKeys, so every
  // preview starts closed again rather than carrying over an old row's state.
  const applyFetched = useCallback((fetched: FetchedRows) => {
    setGates(fetched.rows);
    setExpandedPreview({});
    setSavedGates(fetched.rows);
    setMergedGates(fetched.merged);
    setInheritedGates(fetched.inherited);
    setMaxIterations(fetched.maxIterations);
    setSavedMaxIterations(fetched.maxIterations);
    setInheritedMaxIterations(fetched.inheritedMaxIterations);
    setWarnings(fetched.warnings);
  }, []);

  const load = useCallback(async () => {
    clearDeleteNotice();
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
  }, [fetchRows, applyFetched, tRef, clearDeleteNotice]);

  useEffect(() => { load(); }, [load]);

  // Editing any field on a not-yet-overridden default row is what actually
  // creates its override -- see GateRow's doc comment.
  const updateGate = (rowKey: string, patch: Partial<GateRow>) => {
    clearDeleteNotice();
    setGates(prev => prev.map(g => (g.rowKey === rowKey ? { ...g, ...patch, isOverridden: true } : g)));
  };

  const addGate = () => {
    clearDeleteNotice();
    setGates(prev => [...prev, { id: '', name: '', criteria: '', enabled: true, isOverridden: true, origin: 'new', baseline: {}, hasDefault: false, rowKey: newRowKey() }]);
  };

  // Only ever called for an overridden row: a not-yet-overridden default
  // row's delete button is aria-disabled (see the JSX below), IconButton
  // swallows its clicks and its onClick checks g.isOverridden too. Guarded
  // here as well so this can't remove such a row even if triggered some
  // other way.
  // The removed row's open state goes with it; its rowKey is never handed
  // out again, so no later row could pick it up anyway.
  // Focus then moves to the row that took the removed one's place (else the
  // new last row, else "Add Review Gate"): to its delete button, or -- when
  // that is aria-disabled because the row is a not-yet-overridden default --
  // to its merged preview toggle, the row's other usable button.
  // The removal is then announced (see deleteNotice); a guarded-out call
  // removes nothing and announces nothing.
  const removeGate = (rowKey: string) => {
    const removed = gates.find(g => g.rowKey === rowKey);
    if (!removed?.isOverridden) return;
    const remaining = gates.filter(g => g.rowKey !== rowKey);
    setGates(prev => prev.filter(g => g.rowKey !== rowKey));
    setExpandedPreview(prev => {
      const { [rowKey]: _removed, ...rest } = prev;
      return rest;
    });
    const neighborKey = neighborAfterRemoval(gates.map(g => g.rowKey), rowKey, remaining.map(g => g.rowKey));
    const neighbor = remaining.find(g => g.rowKey === neighborKey);
    if (!neighbor) {
      setPendingFocus(ADD_GATE_FOCUS_KEY);
    } else {
      setPendingFocus(neighbor.isOverridden ? deleteButtonKey(neighbor.rowKey) : previewToggleKey(neighbor.rowKey));
    }
    const removedName = gateDisplayName(removed);
    announceDelete(
      removedName
        ? t('settings.reviewGates.deleteGateAnnouncement', { name: removedName })
        : t('settings.reviewGates.deleteUnnamedGateAnnouncement')
    );
  };

  const handleSave = async () => {
    clearDeleteNotice();
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
    <div ref={containerRef} className="flex flex-col gap-3 h-full min-h-0">
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
          onChange={e => {
            clearDeleteNotice();
            setMaxIterations(e.target.value === '' ? null : Number(e.target.value));
          }}
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
        {gates.map(g => {
          const inherited = inheritedGates[g.id];
          // An override's empty field inherits the default, so show that
          // default as the field's placeholder instead of a blank box.
          const inheritedPlaceholder = (value: string | null | undefined, fallback?: string) =>
            value === undefined || value === null || value === ''
              ? fallback
              : t('settings.reviewGates.inheritedPlaceholder', { value });
          const idLocked = !g.isOverridden || g.hasDefault;
          const previewOpen = !!expandedPreview[g.rowKey];
          const previewId = `${idPrefix}-${g.rowKey}-preview`;
          // What the delete button names (see gateDisplayName).
          const deleteTarget = gateDisplayName(g);
          const deleteTooltip = g.isOverridden ? t('settings.reviewGates.deleteGate') : t('settings.reviewGates.cannotDeleteDefaultHint');
          const idInvalid = emptyIdErrorShown && g.id.trim() === '';
          return (
          <div key={g.rowKey} className="border border-slate-200 dark:border-slate-800 rounded-lg p-3 space-y-2 bg-white dark:bg-slate-900">
            {/* Each text field has a small visible label above it (the
                placeholders stay as a supplementary hint); items-end keeps
                the checkbox and delete button aligned with the inputs. */}
            <div className="flex items-end gap-2">
              <div className="w-40 flex flex-col">
                <label htmlFor={`${idPrefix}-${g.rowKey}-id`} className={SMALL_LABEL_CLASS}>{t('settings.reviewGates.idLabel')}</label>
                <input
                  id={`${idPrefix}-${g.rowKey}-id`}
                  value={g.id}
                  // A gate with a default behind it keeps its ID (see
                  // GateRow's hasDefault) -- use "Add Review Gate" for a new
                  // ID instead.
                  disabled={idLocked}
                  title={idLocked ? t('settings.reviewGates.cannotChangeDefaultIdHint') : undefined}
                  aria-invalid={idInvalid || undefined}
                  aria-describedby={idInvalid ? errorId : undefined}
                  placeholder={t('settings.reviewGates.idLabel')}
                  onChange={e => updateGate(g.rowKey, { id: e.target.value })}
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
                <label htmlFor={`${idPrefix}-${g.rowKey}-name`} className={SMALL_LABEL_CLASS}>{t('settings.reviewGates.nameLabel')}</label>
                <input
                  id={`${idPrefix}-${g.rowKey}-name`}
                  value={g.name || ''}
                  disabled={!g.isOverridden}
                  placeholder={inheritedPlaceholder(inherited?.name, t('settings.reviewGates.nameLabel'))}
                  onChange={e => updateGate(g.rowKey, { name: e.target.value })}
                  title={g.isOverridden ? undefined : t('settings.reviewGates.cannotRenameDefaultHint')}
                  className="w-full bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded px-2 py-1 text-xs text-slate-900 dark:text-slate-100 disabled:opacity-60"
                />
              </div>
              <label className="flex items-center gap-1 mb-1 text-[11px] text-slate-600 dark:text-slate-400 shrink-0">
                <input
                  type="checkbox"
                  checked={g.enabled !== false}
                  onChange={e => updateGate(g.rowKey, { enabled: e.target.checked })}
                  className="rounded border-slate-300 dark:border-slate-600 text-blue-600 focus:ring-0"
                />
                {t('settings.reviewGates.enabledLabel')}
              </label>
              <IconButton
                onClick={() => {
                  if (g.isOverridden) removeGate(g.rowKey);
                }}
                data-focus-key={deleteButtonKey(g.rowKey)}
                // aria-disabled rather than disabled, so a default gate's
                // button still takes keyboard focus and shows why it cannot
                // be deleted (IconButton swallows the click).
                aria-disabled={g.isOverridden ? undefined : true}
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
                className={`p-1 text-slate-500 dark:text-slate-400 ${
                  g.isOverridden ? 'hover:text-red-600 dark:hover:text-red-400' : 'opacity-40 cursor-not-allowed'
                }`}
              >
                <Trash2 aria-hidden="true" className="w-3.5 h-3.5" />
              </IconButton>
            </div>
            <div>
              <label htmlFor={`${idPrefix}-${g.rowKey}-criteria`} className={SMALL_LABEL_CLASS}>{t('settings.reviewGates.criteriaLabel')}</label>
              <textarea
                id={`${idPrefix}-${g.rowKey}-criteria`}
                value={g.criteria || ''}
                placeholder={inheritedPlaceholder(inherited?.criteria)}
                onChange={e => updateGate(g.rowKey, { criteria: e.target.value })}
                rows={2}
                className="w-full bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded px-2 py-1 text-xs text-slate-900 dark:text-slate-100 disabled:opacity-60"
              />
            </div>
            <div>
              <label htmlFor={`${idPrefix}-${g.rowKey}-additional`} className={SMALL_LABEL_CLASS}>{t('settings.reviewGates.additionalCriteriaLabel')}</label>
              <textarea
                id={`${idPrefix}-${g.rowKey}-additional`}
                value={g.additional_criteria || ''}
                placeholder={inheritedPlaceholder(inherited?.additional_criteria)}
                onChange={e => updateGate(g.rowKey, { additional_criteria: e.target.value })}
                rows={2}
                className="w-full bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded px-2 py-1 text-xs text-slate-900 dark:text-slate-100 disabled:opacity-60"
              />
            </div>
            <div className="pt-1 border-t border-slate-100 dark:border-slate-800">
              <button
                type="button"
                onClick={() => setExpandedPreview(prev => ({ ...prev, [g.rowKey]: !prev[g.rowKey] }))}
                data-focus-key={previewToggleKey(g.rowKey)}
                // The preview is only rendered while open, so aria-controls is
                // set only then and never points at an id missing from the DOM.
                aria-expanded={previewOpen}
                aria-controls={previewOpen ? previewId : undefined}
                className="flex items-center gap-1 text-[10px] font-semibold text-slate-500 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-300"
              >
                {previewOpen ? <ChevronDown aria-hidden="true" className="w-3 h-3" /> : <ChevronRight aria-hidden="true" className="w-3 h-3" />}
                {t('settings.reviewGates.mergedPreviewLabel')}
              </button>
              {previewOpen && (
                <div id={previewId}>
                  {mergedGates[g.id] ? (
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
                  )}
                </div>
              )}
            </div>
          </div>
          );
        })}
      </div>

      <div className="flex justify-between items-center">
        <button
          onClick={addGate}
          data-focus-key={ADD_GATE_FOCUS_KEY}
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
      {/* Last child, so the empty sr-only region never shifts the layout; it
          stays mounted even when the last row is removed (DFLT-00204). */}
      <StatusLiveRegion message={deleteNotice} />
    </div>
  );
};
