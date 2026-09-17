// "ノード" tab: lets a scope (global/project) add or edit the agent
// instructions appended for one node type (GET/PUT
// /api/settings/node-types(/{type})). See internal/config.ResolveNodeTypeContext
// for the append-by-default merge semantics this editor exposes.
import React, { useCallback, useEffect, useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Loader2, Save, CheckCircle2, Plus, Trash2, Check, X } from 'lucide-react';
import { SettingsNodeTypeInfo, SettingsScope } from '../../types';
import { fetchSettingsNodeType, fetchSettingsNodeTypes, saveSettingsNodeType } from '../../lib/settingsApi';
import { getNodeTypeMeta } from '../../nodeTypeMeta';
import { errorMessage } from '../../lib/apiError';
import { useLatest } from '../../hooks/useLatest';

// Mirrors config.isSafeExtensionName (packages/core-go/internal/config/
// extensions.go) so an obviously-invalid name is rejected here with a clear
// message instead of round-tripping to the server for the same rejection.
function isValidTypeName(name: string): boolean {
  return name !== '' && name !== '.' && name !== '..' && !/[/\\]/.test(name);
}

interface Props {
  scope: SettingsScope;
  projectId: string;
  canEdit: boolean;
  onDirtyChange: (dirty: boolean) => void;
}

export const NodeTypesEditor: React.FC<Props> = ({ scope, projectId, canEdit, onDirtyChange }) => {
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
  const [savedFlash, setSavedFlash] = useState(false);
  // Inline "add a node type" affordance -- mirrors レビューゲート's "Add
  // Review Gate" in spirit, but needs a name up front (there's no separate
  // id/name pair here) so it's a small text-entry row rather than a blank
  // list item.
  const [isAddingType, setIsAddingType] = useState(false);
  const [newTypeName, setNewTypeName] = useState('');
  const newTypeInputId = useId();
  const tierTextId = useId();

  const isDirty = canEdit && tierText !== savedTierText;
  useEffect(() => onDirtyChange(isDirty), [isDirty, onDirtyChange]);

  const loadTypes = useCallback(async () => {
    try {
      const list = await fetchSettingsNodeTypes(tRef.current, scope === 'project' ? projectId : '');
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
    } catch (e) {
      setError(errorMessage(e, tRef.current('errors.UNKNOWN')));
    }
  }, [scope, projectId, tRef]);

  const loadSelected = useCallback(async (type: string) => {
    if (!type) return;
    setLoading(true);
    setError('');
    try {
      const res = await fetchSettingsNodeType(tRef.current, scope, projectId, type);
      setTierText(res.tier_text);
      setSavedTierText(res.tier_text);
      setMergedText(res.merged_text);
    } catch (e) {
      setError(errorMessage(e, tRef.current('errors.UNKNOWN')));
    } finally {
      setLoading(false);
    }
  }, [scope, projectId, tRef]);

  useEffect(() => { loadTypes(); }, [loadTypes]);
  useEffect(() => { if (selected) loadSelected(selected); }, [selected, loadSelected]);

  const handleSave = async () => {
    setSaving(true);
    setError('');
    try {
      const res = await saveSettingsNodeType(t, scope, projectId, selected, tierText);
      setTierText(res.tier_text);
      setSavedTierText(res.tier_text);
      setMergedText(res.merged_text);
      setSavedFlash(true);
      setTimeout(() => setSavedFlash(false), 2000);
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
  const confirmAddType = () => {
    const name = newTypeName.trim();
    if (!name) return;
    if (!isValidTypeName(name)) {
      setError(t('settings.nodeTypes.invalidTypeName'));
      return;
    }
    setError('');
    if (!types.some(info => info.type === name)) {
      setTypes(prev => [...prev, { type: name, has_default: false, has_user_override: false, has_team_override: false }]);
    }
    setSelected(name);
    setNewTypeName('');
    setIsAddingType(false);
  };

  const cancelAddType = () => {
    setNewTypeName('');
    setIsAddingType(false);
  };

  // Clears this scope's own override text for type, which is what actually
  // "deletes" a node type here (there's no separate delete endpoint -- see
  // WriteExtensionText's "empty text deletes the file" contract). Only ever
  // reachable for a non-default type (the delete button is disabled
  // otherwise, see the JSX below): a plugin-default type has no "removed"
  // state to fall back to, only an overridden/not-yet-overridden one, exactly
  // like レビューゲート's default rows.
  const removeType = async (type: string) => {
    setError('');
    try {
      await saveSettingsNodeType(t, scope, projectId, type, '');
      if (selected === type) {
        setTierText('');
        setSavedTierText('');
        setMergedText('');
      }
      await loadTypes();
    } catch (e) {
      setError(errorMessage(e, t('errors.UNKNOWN')));
    }
  };

  return (
    <div className="flex h-full min-h-0 gap-4">
      {/* Left: type list */}
      <div className="w-56 shrink-0 border border-slate-200 dark:border-slate-800 rounded-lg overflow-y-auto bg-slate-50 dark:bg-slate-800 flex flex-col">
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
          const hasOverride = scope === 'global' ? info.has_user_override : info.has_team_override;
          // A plugin-default type (has_default) can never be fully removed
          // -- only overridden or not -- exactly like レビューゲート's
          // default rows; deleting only ever makes sense for a custom type.
          const canDelete = canEdit && !info.has_default;
          return (
            <div
              key={info.type}
              className={`w-full flex items-center gap-1 border-b border-slate-100 dark:border-slate-700 transition ${
                selected === info.type ? 'bg-white dark:bg-slate-900' : 'hover:bg-white dark:hover:bg-slate-900'
              }`}
            >
              <button
                onClick={() => setSelected(info.type)}
                className={`flex-1 min-w-0 text-left pl-3 pr-1 py-2 text-xs flex items-center gap-2 ${
                  selected === info.type ? 'font-semibold text-slate-900 dark:text-slate-100' : 'text-slate-600 dark:text-slate-400'
                }`}
              >
                <Icon className="w-3.5 h-3.5 shrink-0 text-slate-400" />
                <span className="truncate flex-1">{meta.labelKey ? t(meta.labelKey) : info.type}</span>
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
              <button
                onClick={() => removeType(info.type)}
                disabled={!canDelete}
                title={info.has_default ? t('settings.nodeTypes.cannotDeleteDefaultHint') : t('settings.nodeTypes.deleteType')}
                className="shrink-0 p-1 mr-1 text-slate-400 dark:text-slate-500 hover:text-red-600 dark:hover:text-red-400 disabled:opacity-30 rounded"
              >
                <Trash2 className="w-3 h-3" />
              </button>
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
                    if (e.key === 'Enter') confirmAddType();
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
                <button onClick={confirmAddType} className="p-1 text-emerald-600 hover:text-emerald-700 shrink-0" title={t('settings.common.yes')}>
                  <Check className="w-3.5 h-3.5" />
                </button>
                <button onClick={cancelAddType} className="p-1 text-slate-400 hover:text-slate-600 shrink-0" title={t('settings.common.no')}>
                  <X className="w-3.5 h-3.5" />
                </button>
              </div>
            </>
          ) : (
            <button
              onClick={() => setIsAddingType(true)}
              disabled={!canEdit}
              className="w-full px-2 py-1.5 bg-white dark:bg-slate-900 hover:bg-slate-100 dark:hover:bg-slate-800 disabled:opacity-50 rounded text-[11px] font-semibold text-slate-700 dark:text-slate-300 flex items-center justify-center gap-1.5 transition border border-slate-200 dark:border-slate-700"
            >
              <Plus className="w-3.5 h-3.5" /> {t('settings.nodeTypes.addType')}
            </button>
          )}
        </div>
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
                disabled={!canEdit}
                placeholder={t('settings.nodeTypes.tierTextPlaceholder')}
                className="flex-1 min-h-[10rem] w-full bg-white dark:bg-slate-900 border border-slate-300 dark:border-slate-700 rounded-lg px-3 py-2 text-xs font-mono text-slate-900 dark:text-slate-100 focus:outline-none focus:border-blue-600 dark:focus:border-blue-400 disabled:opacity-60 disabled:bg-slate-50 dark:disabled:bg-slate-800"
              />
              <p className="text-[10px] text-slate-400 dark:text-slate-500 mt-1">{t('settings.nodeTypes.emptyOverrideHint')}</p>
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
