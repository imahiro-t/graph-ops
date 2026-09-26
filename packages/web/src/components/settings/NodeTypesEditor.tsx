// "ノード" tab: lets a scope (global/project) add or edit the agent
// instructions appended for one node type (GET/PUT
// /api/settings/node-types(/{type})). See internal/config.ResolveNodeTypeContext
// for the append-by-default merge semantics this editor exposes.
import React, { useCallback, useEffect, useId, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Loader2, Save, CheckCircle2, Plus, Trash2, Check, X } from 'lucide-react';
import { SettingsNodeTypeInfo } from '../../types';
import { fetchSettingsNodeType, fetchSettingsNodeTypes, saveSettingsNodeType } from '../../lib/settingsApi';
import { getNodeTypeMeta } from '../../nodeTypeMeta';
import { errorMessage } from '../../lib/apiError';
import { useLatest } from '../../hooks/useLatest';
import { useSavedFlash } from '../../hooks/useSavedFlash';
import { useTransientAnnouncement } from '../../hooks/useTransientAnnouncement';
import { StatusLiveRegion } from '../StatusLiveRegion';
import { IconButton } from '../IconButton';
import { useConfirmDialog } from '../../hooks/useConfirmDialog';
import { unsavedChangesConfirmOptions } from './unsavedChangesConfirm';
import { focusIfLost, focusKeySelector, neighborAfterRemoval } from '../../lib/focusAfterRemoval';

// Mirrors config.isSafeExtensionName (packages/core-go/internal/config/
// extensions.go) so an obviously-invalid name is rejected here with a clear
// message instead of round-tripping to the server for the same rejection.
function isValidTypeName(name: string): boolean {
  return name !== '' && name !== '.' && name !== '..' && !/[/\\]/.test(name);
}

// data-focus-key values of the controls removeType moves focus to once the
// deleted type's row is gone (see pendingFocus).
const deleteButtonKey = (type: string) => `delete-${type}`;
const ADD_TYPE_FOCUS_KEY = 'add-type';

interface Props {
  onDirtyChange: (dirty: boolean) => void;
}

export const NodeTypesEditor: React.FC<Props> = ({ onDirtyChange }) => {
  const { t } = useTranslation();
  // See src/hooks/useLatest.ts -- keeps loadTypes/loadSelected below
  // insensitive to language changes (F-1).
  const tRef = useLatest(t);
  const [types, setTypes] = useState<SettingsNodeTypeInfo[]>([]);
  const [selected, setSelected] = useState<string>('');
  const [tierText, setTierText] = useState('');
  const [savedTierText, setSavedTierText] = useState('');
  const [mergedText, setMergedText] = useState('');
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const { savedFlash, showSavedFlash } = useSavedFlash();
  // Announces a successful delete (DFLT-00194): focus moves to a neighbor
  // afterwards, and this says why -- which override is gone.
  const { message: deleteNotice, announce: announceDelete } = useTransientAnnouncement();
  // Inline "add a node type" affordance -- mirrors レビューゲート's "Add
  // Review Gate" in spirit, but needs a name up front (there's no separate
  // id/name pair here) so it's a small text-entry row rather than a blank
  // list item.
  const [isAddingType, setIsAddingType] = useState(false);
  const [newTypeName, setNewTypeName] = useState('');
  const newTypeInputId = useId();
  const addTypeButtonRef = useRef<HTMLButtonElement>(null);
  // Set by confirmAddType when it closes the input row, so the effect below
  // can put focus on the "add type" button that replaces it instead of
  // leaving it on <body> (the focused input is removed from the DOM).
  const focusAddButtonAfterAddRef = useRef(false);
  // In-app confirmations (DFLT-00148), opened on top of the settings modal.
  const { confirm, confirmDialog } = useConfirmDialog();
  const tierTextId = useId();
  const listRef = useRef<HTMLDivElement>(null);
  // Where keyboard focus goes once the next render has settled (DFLT-00191):
  // deleting a type removes its row, focused delete button included, which
  // would otherwise drop focus to <body>. Same pattern as LabelsEditor's.
  const [pendingFocus, setPendingFocus] = useState<string | null>(null);
  useEffect(() => {
    if (pendingFocus === null) return;
    const el = listRef.current?.querySelector<HTMLElement>(focusKeySelector(pendingFocus));
    if (el && (el as HTMLButtonElement).disabled) return; // retry once re-enabled
    focusIfLost(el);
    setPendingFocus(null);
    // types/isAddingType are what mount and unmount the targets; they are
    // listed so the effect re-runs on them.
  }, [pendingFocus, types, isAddingType]);

  const isDirty = tierText !== savedTierText;
  useEffect(() => onDirtyChange(isDirty), [isDirty, onDirtyChange]);

  useEffect(() => {
    if (isAddingType || !focusAddButtonAfterAddRef.current) return;
    focusAddButtonAfterAddRef.current = false;
    if (!document.activeElement || document.activeElement === document.body) addTypeButtonRef.current?.focus();
  }, [isAddingType]);

  // Resolves to the refreshed list, or null when it could not be fetched
  // (the error is shown and the list on screen is left as it was).
  const loadTypes = useCallback(async (): Promise<SettingsNodeTypeInfo[] | null> => {
    try {
      const list = await fetchSettingsNodeTypes(tRef.current);
      setTypes(list);
      // Re-select a valid entry whenever the currently-selected type is no
      // longer in the refreshed list -- covers both the initial mount
      // (selected === '') and a type just deleted out from under the
      // current selection (see removeType). Written as a setState updater
      // (reading `selected` via `prev`, not the outer closure) so this
      // callback doesn't need `selected` in its own dependency array --
      // otherwise picking a different type in the left-hand list would
      // change loadTypes's identity and re-fetch the whole list for no
      // reason (#3).
      setSelected(prev => (list.length > 0 && !list.some(info => info.type === prev) ? list[0].type : prev));
      return list;
    } catch (e) {
      setError(errorMessage(e, tRef.current('errors.UNKNOWN')));
      return null;
    }
  }, [tRef]);

  const loadSelected = useCallback(async (type: string) => {
    if (!type) return;
    setLoading(true);
    setError('');
    try {
      const res = await fetchSettingsNodeType(tRef.current, type);
      setTierText(res.tier_text);
      setSavedTierText(res.tier_text);
      setMergedText(res.merged_text);
    } catch (e) {
      setError(errorMessage(e, tRef.current('errors.UNKNOWN')));
    } finally {
      setLoading(false);
    }
  }, [tRef]);

  useEffect(() => { loadTypes(); }, [loadTypes]);
  useEffect(() => { if (selected) loadSelected(selected); }, [selected, loadSelected]);

  // Switches the selected type, asking first when the current one has
  // unsaved edits -- same shape as TemplatesEditor's select and the same
  // wording (unsavedChangesConfirmOptions), so every list in the settings
  // modal behaves alike. Returns whether the
  // switch happened (confirmAddType keeps its input row open on a cancel).
  // Asynchronous since DFLT-00148: the question is the in-app ConfirmDialog.
  // The code after the await uses the values from when it was asked
  // (savedTierText among them); the dialog keeps the editor out of reach
  // meanwhile, so they cannot have changed.
  const select = async (next: string): Promise<boolean> => {
    if (next === selected) return true;
    if (isDirty) {
      const discard = await confirm(unsavedChangesConfirmOptions(t, 'node-type-discard-confirm'));
      if (!discard) return false;
    }
    // Discarding: put the text back to its saved value first so isDirty is
    // already false while the next type loads -- otherwise the effect above
    // would re-send onDirtyChange(true) until the fetch lands, and the next
    // tab switch in SettingsModal would ask a second time.
    setTierText(savedTierText);
    onDirtyChange(false);
    setSelected(next);
    return true;
  };

  const handleSave = async () => {
    setSaving(true);
    setError('');
    try {
      const res = await saveSettingsNodeType(t, selected, tierText);
      setTierText(res.tier_text);
      setSavedTierText(res.tier_text);
      setMergedText(res.merged_text);
      showSavedFlash();
      await loadTypes();
    } catch (e) {
      setError(errorMessage(e, t('errors.UNKNOWN')));
    } finally {
      setSaving(false);
    }
  };

  // Adds a new type to the local list only -- nothing is persisted until the
  // user writes instructions for it and hits Save (handleSave), which PUTs
  // the text and creates the override file; nothing is lost if they navigate
  // away first (matches レビューゲート's unsaved-new-row behavior).
  const confirmAddType = async () => {
    const name = newTypeName.trim();
    if (!name) return;
    if (!isValidTypeName(name)) {
      setError(t('settings.nodeTypes.invalidTypeName'));
      return;
    }
    setError('');
    // Switch first (it may ask about unsaved edits); on a cancel, keep the
    // input row open and leave the list untouched.
    if (!(await select(name))) return;
    if (!types.some(info => info.type === name)) {
      setTypes(prev => [...prev, { type: name, has_default: false, has_user_override: false }]);
    }
    setNewTypeName('');
    focusAddButtonAfterAddRef.current = true;
    setIsAddingType(false);
  };

  const cancelAddType = () => {
    setNewTypeName('');
    setIsAddingType(false);
  };

  // Clears this scope's own override text for type, which is what actually
  // "deletes" a node type here (there's no separate delete endpoint -- see
  // WriteExtensionText's "empty text deletes the file" contract). Only for a
  // non-default type: a plugin-default type has no "removed" state to fall
  // back to, only an overridden/not-yet-overridden one, exactly like
  // レビューゲート's default rows. Its delete button is aria-disabled (see the
  // JSX below) and IconButton swallows its clicks; the check at the top of
  // this function keeps a default type from being deleted even if it is
  // reached some other way.
  // The saved instructions cannot be restored afterwards, so ask first
  // (the same in-app ConfirmDialog as AppSettingsEditor's handleDeleteProject).
  // Only custom types reach here and they have no translated label, so the
  // type name itself is what the user sees in the list.
  // Once the row is gone, focus moves to the row that took its place (else
  // the one before it, else the "add node type" button) -- the same rule as
  // LabelsEditor (lib/focusAfterRemoval). On a cancel or a failed request the
  // row stays, and the dialog has already put focus back on its delete
  // button, so nothing is moved then.
  const removeType = async (type: string) => {
    if (types.find(info => info.type === type)?.has_default) return;
    // The list as it was when the user asked: the dialog keeps it out of
    // reach until it closes, so this is also the list as of the confirm.
    const before = types.map(info => info.type);
    const confirmed = await confirm({
      title: t('settings.nodeTypes.confirmDeleteTypeTitle'),
      message: t('settings.nodeTypes.confirmDeleteType', { name: type }),
      confirmLabel: t('settings.nodeTypes.confirmDeleteTypeButton'),
      tone: 'danger',
      testIdPrefix: 'node-type-delete-confirm'
    });
    if (!confirmed) return;
    setError('');
    try {
      await saveSettingsNodeType(t, type, '');
      // The override is gone on the server now, whether or not the refresh
      // below succeeds or another tier keeps the row listed.
      announceDelete(t('settings.nodeTypes.deleteTypeSuccess', { name: type }));
      if (selected === type) {
        setTierText('');
        setSavedTierText('');
        setMergedText('');
      }
      const list = await loadTypes();
      // null: the refresh failed, so the row is still on screen with focus
      // on its button. Still listed: another tier still defines the type,
      // so its row (and focused button) stays.
      if (list === null || list.some(info => info.type === type)) return;
      // Same column, next row: the neighbor's delete button, even on a
      // plugin-default row -- that button is aria-disabled, not disabled, so
      // it takes focus and tells (tooltip and description) why that row
      // cannot be deleted.
      const neighbor = neighborAfterRemoval(before, type, list.map(info => info.type));
      setPendingFocus(neighbor === null ? ADD_TYPE_FOCUS_KEY : deleteButtonKey(neighbor));
    } catch (e) {
      setError(errorMessage(e, t('errors.UNKNOWN')));
    }
  };

  return (
    <div className="flex h-full min-h-0 gap-4">
      {confirmDialog}
      <StatusLiveRegion message={deleteNotice} />
      {/* Left: type list */}
      <div ref={listRef} className="w-56 shrink-0 border border-slate-200 dark:border-slate-800 rounded-lg overflow-y-auto bg-slate-50 dark:bg-slate-800 flex flex-col">
        <div className="px-3 py-2 text-[11px] font-semibold text-slate-500 dark:text-slate-400 border-b border-slate-200 dark:border-slate-700 sticky top-0 bg-slate-50 dark:bg-slate-800">
          {t('settings.nodeTypes.listTitle')}
        </div>
        {types.map(info => {
          const meta = getNodeTypeMeta(info.type);
          const Icon = meta.icon;
          // Read the two override flags by name rather than indexing info
          // with a scope-derived key string: the computed-key form needed an
          // `as any` to typecheck (DFLT-00023 M-1), and that cast disabled
          // the one check that would flag a renamed or removed flag on
          // SettingsNodeTypeInfo -- the exact breakage it was silencing.
          const hasOverride = info.has_user_override;
          // A plugin-default type (has_default) can never be fully removed
          // -- only overridden or not -- exactly like レビューゲート's
          // default rows; deleting only ever makes sense for a custom type.
          const canDelete = !info.has_default;
          // One name for both the visible select button and the delete
          // button's accessible name, so the two always read the same.
          const displayName = meta.labelKey ? t(meta.labelKey) : info.type;
          return (
            <div
              key={info.type}
              className={`w-full flex items-center gap-1 border-b border-slate-100 dark:border-slate-700 transition ${
                selected === info.type ? 'bg-white dark:bg-slate-900' : 'hover:bg-white dark:hover:bg-slate-900'
              }`}
            >
              <button
                onClick={() => void select(info.type)}
                className={`flex-1 min-w-0 text-left pl-3 pr-1 py-2 text-xs flex items-center gap-2 ${
                  selected === info.type ? 'font-semibold text-slate-900 dark:text-slate-100' : 'text-slate-600 dark:text-slate-400'
                }`}
              >
                <Icon aria-hidden="true" className="w-3.5 h-3.5 shrink-0 text-slate-400" />
                <span className="truncate flex-1">{displayName}</span>
                {!hasOverride && info.has_default && (
                  <span
                    title={t('settings.nodeTypes.defaultBadgeHint')}
                    className="shrink-0 text-[9px] font-semibold px-1 py-0.5 rounded bg-slate-200 dark:bg-slate-700 text-slate-500 dark:text-slate-400"
                  >
                    {t('settings.nodeTypes.defaultBadge')}
                  </span>
                )}
                {hasOverride && (
                  <span className="shrink-0 w-1.5 h-1.5 rounded-full bg-blue-500" title={t('settings.nodeTypes.overrideBadge')} />
                )}
              </button>
              <IconButton
                data-focus-key={deleteButtonKey(info.type)}
                onClick={() => {
                  if (canDelete) void removeType(info.type);
                }}
                // aria-disabled rather than disabled, so the button still
                // takes keyboard focus and shows why it cannot be deleted
                // (IconButton swallows the click).
                aria-disabled={canDelete ? undefined : true}
                // The name carries the type so a screen reader can tell which
                // row focus is on. The tooltip says "delete" -- already part
                // of the name -- or, on a type with a plugin default, why it
                // cannot be deleted; only that reason is added as the
                // description, so the name is not read twice.
                label={t('settings.nodeTypes.deleteTypeAriaLabel', { name: displayName })}
                tooltip={info.has_default ? t('settings.nodeTypes.cannotDeleteDefaultHint') : t('settings.nodeTypes.deleteType')}
                describeWithTooltip={info.has_default}
                wrapperClassName="shrink-0 mr-1"
                className={`p-1 text-slate-500 dark:text-slate-400 rounded ${
                  canDelete ? 'hover:text-red-600 dark:hover:text-red-400' : 'opacity-30 cursor-not-allowed'
                }`}
              >
                <Trash2 aria-hidden="true" className="w-3 h-3" />
              </IconButton>
            </div>
          );
        })}

        {/* Add a brand-new custom node type (not yet known to any tier). */}
        <div className="mt-auto border-t border-slate-200 dark:border-slate-700 p-2">
          {isAddingType ? (
            <>
              <label htmlFor={newTypeInputId} className="block text-[10px] font-semibold text-slate-500 dark:text-slate-400 mb-0.5">
                {t('settings.nodeTypes.newTypeLabel')}
              </label>
              <div className="flex items-center gap-1">
                <input
                  id={newTypeInputId}
                  autoFocus
                  value={newTypeName}
                  onChange={e => setNewTypeName(e.target.value)}
                  onKeyDown={e => {
                    if (e.key === 'Enter') {
                      // preventDefault: confirmAddType may open the in-app
                      // unsaved-changes dialog, which moves focus onto its
                      // cancel button while this key is still being handled;
                      // without it the key's follow-up (keypress) would land
                      // on that button and press it at once.
                      e.preventDefault();
                      void confirmAddType();
                    }
                    if (e.key === 'Escape') {
                      // preventDefault tells the enclosing SettingsModal's
                      // dialog hook (useModalDialog) that this Escape was
                      // handled here, so it only cancels the add and does not
                      // also close the modal.
                      e.preventDefault();
                      cancelAddType();
                    }
                  }}
                  placeholder={t('settings.nodeTypes.newTypePlaceholder')}
                  className="flex-1 min-w-0 bg-white dark:bg-slate-900 border border-slate-300 dark:border-slate-700 rounded px-1.5 py-1 text-xs font-mono text-slate-900 dark:text-slate-100"
                />
                <IconButton
                  onClick={() => void confirmAddType()}
                  label={t('settings.nodeTypes.confirmAddType')}
                  tooltipSide="top"
                  wrapperClassName="shrink-0"
                  className="p-1 text-emerald-600 hover:text-emerald-700"
                >
                  <Check aria-hidden="true" className="w-3.5 h-3.5" />
                </IconButton>
                {/* DFLT-00168: icon-only button, so WCAG 1.4.11 asks for 3:1
                    against the list panel (slate-50 / slate-800). slate-500 /
                    dark:slate-400 gives 4.55:1 / 5.71:1, and the hover darkens
                    (lightens in dark) instead of fading into slate-800. */}
                <IconButton
                  onClick={cancelAddType}
                  label={t('settings.nodeTypes.cancelAddType')}
                  tooltipSide="top"
                  wrapperClassName="shrink-0"
                  className="p-1 text-slate-500 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-200"
                >
                  <X aria-hidden="true" className="w-3.5 h-3.5" />
                </IconButton>
              </div>
            </>
          ) : (
            <button
              ref={addTypeButtonRef}
              data-focus-key={ADD_TYPE_FOCUS_KEY}
              onClick={() => setIsAddingType(true)}
              className="w-full px-2 py-1.5 bg-white dark:bg-slate-900 hover:bg-slate-100 dark:hover:bg-slate-800 disabled:opacity-50 rounded text-[11px] font-semibold text-slate-700 dark:text-slate-300 flex items-center justify-center gap-1.5 transition border border-slate-200 dark:border-slate-700"
            >
              <Plus aria-hidden="true" className="w-3.5 h-3.5" /> {t('settings.nodeTypes.addType')}
            </button>
          )}
        </div>
      </div>

      {/* Right: editor */}
      <div className="flex-1 min-w-0 flex flex-col gap-3">
        {error && <div className="p-2.5 bg-red-50 dark:bg-red-950 text-red-700 dark:text-red-300 text-[11px] rounded-lg border border-red-200 dark:border-red-900">{error}</div>}
        {loading ? (
          <div className="flex items-center gap-2 text-slate-500 dark:text-slate-400 text-xs py-8 justify-center">
            <Loader2 aria-hidden="true" className="w-4 h-4 animate-spin" /> {t('settings.common.loading')}
          </div>
        ) : (
          <>
            <div>
              <label className="block text-xs font-semibold text-slate-700 dark:text-slate-300 mb-1">{t('settings.nodeTypes.mergedPreviewLabel')}</label>
              <pre className="whitespace-pre-wrap text-[11px] leading-relaxed bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded-lg p-3 max-h-40 overflow-y-auto text-slate-600 dark:text-slate-400 font-mono">
                {mergedText || t('settings.common.inheritedFromDefault')}
              </pre>
            </div>
            <div className="flex-1 min-h-0 flex flex-col">
              <label htmlFor={tierTextId} className="block text-xs font-semibold text-slate-700 dark:text-slate-300 mb-1">{t('settings.nodeTypes.tierTextLabel')}</label>
              <textarea
                id={tierTextId}
                value={tierText}
                onChange={e => setTierText(e.target.value)}
                placeholder={t('settings.nodeTypes.tierTextPlaceholder')}
                className="flex-1 min-h-[10rem] w-full bg-white dark:bg-slate-900 border border-slate-300 dark:border-slate-700 rounded-lg px-3 py-2 text-xs font-mono text-slate-900 dark:text-slate-100 focus:outline-none focus:border-blue-600 dark:focus:border-blue-400 disabled:opacity-60 disabled:bg-slate-50 dark:disabled:bg-slate-800"
              />
              <p className="text-[10px] text-slate-500 dark:text-slate-400 mt-1">{t('settings.nodeTypes.emptyOverrideHint')}</p>
            </div>
            <div className="flex justify-end items-center gap-2">
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
          </>
        )}
      </div>
    </div>
  );
};
