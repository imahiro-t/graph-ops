// "ノード" tab: lets a scope (global/project) add or edit the agent
// instructions appended for one node type (GET/PUT
// /api/settings/node-types(/{type})). See internal/config.ResolveNodeTypeContext
// for the append-by-default merge semantics this editor exposes.
import React, { useCallback, useEffect, useId, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Save, CheckCircle2, Plus, Trash2, Check, X } from 'lucide-react';
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
import { submittingProps } from '../Submitting';
import { ErrorBox } from './ErrorBox';
import { LoadFailure, useFocusAfterRetry } from './LoadFailure';
import { LoadingLine } from './LoadingLine';
import { useSelectedTextLoader } from './useSelectedTextLoader';
import { LIST_HEADING_CLASS, LIST_ITEM_FOCUS_CLASS, LIST_LAYOUT_CLASS, LIST_PANE_CLASS } from './listPane';
import { Spinner } from '../Spinner';

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
  // See src/hooks/useLatest.ts -- keeps loadTypes below
  // insensitive to language changes (F-1).
  const tRef = useLatest(t);
  const [types, setTypes] = useState<SettingsNodeTypeInfo[]>([]);
  const [selected, setSelected] = useState<string>('');
  const [tierText, setTierText] = useState('');
  const [savedTierText, setSavedTierText] = useState('');
  const [mergedText, setMergedText] = useState('');
  // What the right-hand pane shows is derived at render time from the state
  // below and useSelectedTextLoader's (DFLT-00343, DFLT-00350; see the
  // pane's JSX for the order), so no
  // frame -- the first one, or the one right after a switch, before the
  // effect has started the next load -- ever shows an empty editor, or the
  // previous type's text or error, where the selected type's belongs.
  //
  // Whether the type list has been fetched once. False at first, so the
  // first render already shows the loading line. A list fetch that fails
  // while this is false (the first one or a retry of it) is listLoadError,
  // shown in place of the editor with a retry button; once it is true, a
  // failed re-fetch (after a save or delete) goes to the non-blocking
  // `error` instead -- the list on screen is still right then. The ref
  // mirrors it for loadTypes, which must keep a stable identity.
  const [listLoaded, setListLoaded] = useState(false);
  const listLoadedRef = useRef(false);
  const [listLoadError, setListLoadError] = useState('');
  const [listFailures, setListFailures] = useState(0);
  const [listRetrying, setListRetrying] = useState(false);
  const [saving, setSaving] = useState(false);
  // A failed save, add or delete, and a failed list re-fetch once the list
  // has been loaded: shown above the editor without hiding it.
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
  const mergedPreviewLabelId = useId();
  const listRef = useRef<HTMLDivElement>(null);
  const editorPaneRef = useRef<HTMLDivElement>(null);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  // After a successful list retry the focused retry button is gone, so
  // focus moves to the right-hand pane (the editor may still be loading the
  // selected type's text).
  const focusPaneAfterRetry = useFocusAfterRetry(() => editorPaneRef.current);
  // The selected type's text: fetched on each switch, with its failure,
  // retry and stale-answer rules (see useSelectedTextLoader). A successful
  // retry moves focus to the textarea.
  const selectedText = useSelectedTextLoader({
    selected,
    fetchText: fetchSettingsNodeType,
    onLoaded: res => {
      setTierText(res.tier_text);
      setSavedTierText(res.tier_text);
      setMergedText(res.merged_text);
    },
    onLoadStart: () => setError(''),
    getFocusTarget: () => textareaRef.current
  });
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

  // A type's name as the user sees it: the translated label for a known
  // type, the type id itself for a custom one.
  const typeDisplayName = (type: string): string => {
    const labelKey = getNodeTypeMeta(type).labelKey;
    return labelKey ? t(labelKey) : type;
  };

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
      listLoadedRef.current = true;
      setListLoaded(true);
      setListLoadError('');
      return list;
    } catch (e) {
      const message = errorMessage(e, tRef.current('errors.UNKNOWN'));
      if (listLoadedRef.current) {
        setError(message);
      } else {
        setListLoadError(message);
        setListFailures(n => n + 1);
      }
      return null;
    }
  }, [tRef]);

  useEffect(() => { loadTypes(); }, [loadTypes]);

  const retryList = async () => {
    setListRetrying(true);
    const list = await loadTypes();
    setListRetrying(false);
    if (list !== null) focusPaneAfterRetry();
  };

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
    selectedText.clearFailure();
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
    // Not before the list has been fetched: the added type would switch the
    // selection while the list failure (or loading line) hides the editor,
    // and the list a retry then fetches would replace it, unsaved type and
    // all. The add button is disabled then too.
    if (!listLoaded) return;
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

  const selectedDisplayName = typeDisplayName(selected);

  return (
    <div className={LIST_LAYOUT_CLASS}>
      {confirmDialog}
      <StatusLiveRegion message={deleteNotice} />
      {/* Left: type list. See listPane.ts for why the heading is sticky
          and the list has scroll padding. */}
      <div ref={listRef} className={`${LIST_PANE_CLASS} flex flex-col`}>
        <div className={LIST_HEADING_CLASS}>
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
          // One name for the visible select button, the delete button's
          // accessible name and the editor heading, so they always read the
          // same.
          const displayName = typeDisplayName(info.type);
          return (
            <div
              key={info.type}
              className={`w-full flex items-center gap-1 border-b border-slate-100 dark:border-slate-700 transition ${
                selected === info.type ? 'bg-white dark:bg-slate-900' : 'hover:bg-white dark:hover:bg-slate-900'
              }`}
            >
              {/* DFLT-00287: the name is always one line, cut off with an
                  ellipsis when it does not fit -- never broken mid-word. Its
                  full text stays in the button's accessible name and the
                  title tooltip, and the editor heading on the right shows it
                  in full once the type is selected. At the narrowest size
                  (upto-15rem: -- 480px and below at 200% text) the button
                  wraps and the name takes the whole first line, less the
                  icon (0.875rem) and the gap after it (0.5rem), so the
                  "default" badge / override dot move to a second line,
                  indented by the same 1.375rem to line up under the name;
                  gap-y-0.5 keeps that second line close to the name. */}
              <button
                onClick={() => void select(info.type)}
                // DFLT-00321: aria-current marks the one type whose editor
                // is shown on the right, with the same value TemplatesEditor
                // uses. undefined (not false) keeps the attribute off the
                // other items -- React would render false as "false".
                aria-current={selected === info.type ? 'true' : undefined}
                className={`flex-1 min-w-0 text-left pl-3 pr-1 py-2 text-xs flex items-center gap-2 upto-15rem:flex-wrap upto-15rem:gap-y-0.5 ${LIST_ITEM_FOCUS_CLASS} ${
                  selected === info.type ? 'font-semibold text-slate-900 dark:text-slate-100' : 'text-slate-600 dark:text-slate-400'
                }`}
              >
                <Icon aria-hidden="true" className="w-3.5 h-3.5 shrink-0 text-slate-400" />
                <span title={displayName} className="truncate flex-1 upto-15rem:basis-[calc(100%-1.375rem)]">{displayName}</span>
                {/* DFLT-00320: the badge text is 0.6875rem (11px at the
                    default 16px, the size of IconButton's tooltip and
                    LabelChip) -- 0.5625rem (9px) was too small to read.
                    leading-none sets its line-height to 1. Without it the
                    badge inherits the button's text-xs line-height as a
                    computed length (16px, 32px at 200%), not as a ratio, so
                    it was 16 + 4 = 20px tall before this change (40px on its
                    own second line at 320px / 200%) and made the item taller
                    than the name line: 36px vs 32px, 108px at 320px / 200%.
                    With it the badge is 11 + 4 = 15px (30px on the second
                    line at 320px / 200%), and the item is 32px / 98px.
                    The text colour is slate-600 / dark:slate-300 (6.15:1 on
                    slate-200, 6.97:1 on slate-700): the earlier slate-500 /
                    dark:slate-400 were 3.86:1 / 4.04:1, below the 4.5:1 that
                    WCAG 1.4.3 needs for 11px semibold text. */}
                {!hasOverride && info.has_default && (
                  <span
                    title={t('settings.nodeTypes.defaultBadgeHint')}
                    className="shrink-0 text-[0.6875rem] leading-none font-semibold px-1 py-0.5 rounded-sm bg-slate-200 dark:bg-slate-700 text-slate-600 dark:text-slate-300 upto-15rem:ml-[1.375rem]"
                  >
                    {t('settings.nodeTypes.defaultBadge')}
                  </span>
                )}
                {hasOverride && (
                  <span className="shrink-0 w-1.5 h-1.5 rounded-full bg-blue-500 upto-15rem:ml-[1.375rem]" title={t('settings.nodeTypes.overrideBadge')} />
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
              <label htmlFor={newTypeInputId} className="block text-[0.625rem] font-semibold text-slate-500 dark:text-slate-400 mb-0.5">
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
                  className="flex-1 min-w-0 bg-white dark:bg-slate-900 border border-slate-300 dark:border-slate-700 rounded-sm px-1.5 py-1 text-xs font-mono text-slate-900 dark:text-slate-100"
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
              // Until the list has been fetched (see confirmAddType).
              disabled={!listLoaded}
              className="w-full px-2 py-1.5 bg-white dark:bg-slate-900 hover:bg-slate-100 dark:hover:bg-slate-800 disabled:opacity-50 rounded-sm text-[0.6875rem] font-semibold text-slate-700 dark:text-slate-300 flex items-center justify-center gap-1.5 transition border border-slate-200 dark:border-slate-700"
            >
              <Plus aria-hidden="true" className="w-3.5 h-3.5" /> {t('settings.nodeTypes.addType')}
            </button>
          )}
        </div>
      </div>

      {/* Right: editor. tabIndex -1 and the ref: where focus goes after a
          successful list retry. No outline: it is not a control. */}
      <div ref={editorPaneRef} tabIndex={-1} className="flex-1 min-w-0 flex flex-col gap-3 focus:outline-hidden">
        {/* DFLT-00287: the selected type's full name, which the list may cut
            off with an ellipsis. It wraps (between words where it can, else
            anywhere) instead of overflowing, and stays up while the type's
            text loads. When the name differs from the type id -- a
            translated label, even one differing only in case, like "Plan"
            for plan -- the id follows in monospace so both can be matched
            to the files on disk. */}
        {selected && (
          <h3 className="text-xs font-bold text-slate-700 dark:text-slate-300 wrap-break-word">
            <span>{selectedDisplayName}</span>
            {selectedDisplayName !== selected && (
              <>
                {' '}
                <code className="font-mono font-normal text-slate-500 dark:text-slate-400">{selected}</code>
              </>
            )}
          </h3>
        )}
        {error && <ErrorBox className="p-2.5 text-[0.6875rem]">{error}</ErrorBox>}
        {/* In this order (see LoadFailure): a failure before loading, since a
            failed load never marks the item as loaded and a retry keeps the
            failure (and its focused retry button) up until its result is in.
            selected === '' is an empty list, not a load in progress: it
            falls through to the editor as before, or it would never leave
            the loading line. */}
        {listLoadError ? (
          <LoadFailure
            message={t('settings.common.loadFailed', { message: listLoadError })}
            retrying={listRetrying}
            onRetry={() => void retryList()}
            failureKey={listFailures}
          />
        ) : !listLoaded ? (
          <LoadingLine />
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
              <p id={mergedPreviewLabelId} className="block text-xs font-semibold text-slate-700 dark:text-slate-300 mb-1">{t('settings.nodeTypes.mergedPreviewLabel')}</p>
              {/* A named, focusable region so keyboard users can scroll the
                  preview and screen readers announce what it contains. */}
              <pre
                role="region"
                aria-labelledby={mergedPreviewLabelId}
                tabIndex={0}
                className="whitespace-pre-wrap text-[0.6875rem] leading-relaxed bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded-lg p-3 max-h-40 overflow-y-auto text-slate-600 dark:text-slate-400 font-mono focus:outline-hidden focus-visible:ring-2 focus-visible:ring-blue-500"
              >
                {mergedText || t('settings.common.inheritedFromDefault')}
              </pre>
            </div>
            <div className="flex-1 min-h-0 flex flex-col">
              <label htmlFor={tierTextId} className="block text-xs font-semibold text-slate-700 dark:text-slate-300 mb-1">{t('settings.nodeTypes.tierTextLabel')}</label>
              <textarea
                ref={textareaRef}
                id={tierTextId}
                value={tierText}
                onChange={e => setTierText(e.target.value)}
                placeholder={t('settings.nodeTypes.tierTextPlaceholder')}
                className="flex-1 min-h-40 w-full bg-white dark:bg-slate-900 border border-slate-300 dark:border-slate-700 rounded-lg px-3 py-2 text-xs font-mono text-slate-900 dark:text-slate-100 focus:outline-hidden focus:border-blue-600 dark:focus:border-blue-400 disabled:opacity-60 disabled:bg-slate-50 dark:disabled:bg-slate-800"
              />
              <p className="text-[0.625rem] text-slate-500 dark:text-slate-400 mt-1">{t('settings.nodeTypes.emptyOverrideHint')}</p>
            </div>
            <div className="flex justify-end items-center gap-2 narrow:flex-wrap">
              {savedFlash && (
                <span className="text-emerald-600 text-xs flex items-center gap-1">
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
