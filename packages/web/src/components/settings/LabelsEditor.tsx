// Settings modal "ラベル" tab (DFLT-00084): create, rename, recolor and delete
// the selected project's labels. Labels are DB rows shared by everyone using
// the same backend -- not files in a settings tier -- so this tab needs no
// local path, and every change is saved immediately (there is no
// dirty/unsaved state to protect).
//
// It carries its own project selector. Labels stayed per-project when the
// settings modal's global/project scope switcher was removed (DFLT-00124,
// completion criterion 9), so "which project?" is this tab's own question to
// ask -- and asking it here means any project's labels can be edited, not
// just the one the app currently has open.
import React, { useCallback, useEffect, useId, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Loader2, Pencil, Plus, Tag, Trash2 } from 'lucide-react';
import { LabelColor, LabelUsage, LABEL_COLORS, LABEL_NAME_MAX_LENGTH, Project } from '../../types';
import { getLabelColorMeta } from '../../labelMeta';
import { createLabel, deleteLabel, fetchLabels, updateLabel } from '../../lib/labelsApi';
import { errorMessage } from '../../lib/apiError';
import { LabelChip } from '../LabelChip';
import { useConfirmDialog } from '../../hooks/useConfirmDialog';
import { useTransientAnnouncement } from '../../hooks/useTransientAnnouncement';
import { StatusLiveRegion } from '../StatusLiveRegion';
import { IconButton } from '../IconButton';
import { SubmittingText, submittingProps } from '../Submitting';
import { ErrorBox } from './ErrorBox';

interface Props {
  // Every project that can be picked. An empty list disables the tab: there
  // is nothing to attach a label to.
  projects: Project[];
  // Which project to start on -- the one the app has open. The user can
  // switch to any other from the selector below.
  initialProjectId: string;
  // Called after every successful create/rename/recolor/delete, so the app
  // can re-fetch its label list and tickets (a rename shows on every ticket).
  onLabelsChanged?: () => void;
}

function sortLabels(labels: LabelUsage[]): LabelUsage[] {
  return [...labels].sort((a, b) => {
    const la = a.name.toLowerCase();
    const lb = b.name.toLowerCase();
    if (la !== lb) return la < lb ? -1 : 1;
    return a.name < b.name ? -1 : a.name > b.name ? 1 : 0;
  });
}

// Keys pressed while an IME composition is in progress (confirming or
// cancelling a conversion) must not save/cancel a rename -- the same guard
// useModalDialog applies.
function isImeComposing(e: React.KeyboardEvent): boolean {
  return e.nativeEvent.isComposing || e.keyCode === 229;
}

// data-focus-key values of the elements focus is moved to after an action
// unmounts or disables the focused one (see pendingFocus).
const CREATE_NAME_FOCUS_KEY = 'create-name';
const renameButtonKey = (id: string) => `rename-${id}`;
const renameInputKey = (id: string) => `rename-input-${id}`;
const deleteButtonKey = (id: string) => `delete-${id}`;
// The rename's "save" button (DFLT-00213). Together with the rename input it
// is what a rename is operated from, and so where focus sits when the rename
// was just saved: only from these two does a finished save move focus on
// (see focusIsInRenameOf). The row's color buttons stay focusable while it
// saves, but moving to one starts a different action, not a rename step.
const renameSaveKey = (id: string) => `rename-save-${id}`;

// Whether keyboard focus is still on `id`'s rename -- its name input or its
// "save" button -- or nowhere (<body>, where a disabled "save" button may drop
// it in a browser). Only then may a finished save move focus: the user has
// otherwise gone on to something else (another row's rename or delete
// confirmation, a color button, the create form), and a save started earlier
// must not pull them away from it (DFLT-00213). Deliberately not "anywhere in
// the row": the row's own color buttons are a separate action.
function focusIsInRenameOf(id: string): boolean {
  const active = document.activeElement;
  if (active === null || active === document.body) return true;
  const key = active.getAttribute('data-focus-key');
  return key === renameInputKey(id) || key === renameSaveKey(id);
}

interface PaletteProps {
  value: LabelColor | null;
  onChange: (color: LabelColor) => void;
  // Not operable at all (natively disabled).
  disabled?: boolean;
  // A request is in flight: clicks are ignored and the buttons are
  // aria-disabled, but not natively disabled -- that would drop the keyboard
  // focus of the button just pressed.
  busy?: boolean;
  groupLabel: string;
  size?: 'md' | 'sm';
}

// The fixed color palette as a group of toggle buttons: each button is named
// by its translated color name and reports whether it is the current color
// via aria-pressed, so the choice is not conveyed by color alone. The color
// name is also shown as a tooltip on hover and keyboard focus (IconButton,
// DFLT-00171), since the swatch itself has no text.
export const LabelColorPalette: React.FC<PaletteProps> = ({ value, onChange, disabled = false, busy = false, groupLabel, size = 'md' }) => {
  const { t } = useTranslation();
  const dim = size === 'md' ? 'w-6 h-6' : 'w-4 h-4';
  return (
    <div role="group" aria-label={groupLabel} className="flex flex-wrap items-center gap-1.5">
      {LABEL_COLORS.map(color => {
        const meta = getLabelColorMeta(color);
        const selected = value === color;
        return (
          <IconButton
            key={color}
            label={t(meta.nameKey)}
            aria-pressed={selected}
            aria-disabled={busy || undefined}
            disabled={disabled}
            onClick={() => {
              if (!busy) onChange(color);
            }}
            className={`${dim} rounded-full ${meta.swatch} disabled:opacity-40 disabled:cursor-not-allowed aria-disabled:opacity-40 aria-disabled:cursor-wait focus:outline-hidden focus-visible:ring-2 focus-visible:ring-blue-500 focus-visible:ring-offset-1 ${
              selected ? 'ring-2 ring-offset-2 ring-slate-700 dark:ring-slate-200 dark:ring-offset-slate-900' : ''
            }`}
          />
        );
      })}
    </div>
  );
};

export const LabelsEditor: React.FC<Props> = ({ projects, initialProjectId, onLabelsChanged }) => {
  const { t } = useTranslation();
  const { confirm, confirmDialog } = useConfirmDialog();
  // Announces what happens to a row. A delete (DFLT-00197, as DFLT-00194 did
  // for node types, projects and tickets): focus moves to a neighbor
  // afterwards, and this says why -- which label is gone. A rename or recolor
  // (DFLT-00210): "saving" when it starts and "saved" once it succeeds, since
  // the row's spinner is aria-hidden and its disabled buttons only say they
  // cannot be pressed. One region for both, so the announcements never talk
  // over each other.
  const { message: rowNotice, announce: announceRow, clear: clearRowNotice } = useTransientAnnouncement();
  // Start on the app's current project, but fall back to the first one that
  // exists: an app with no project selected yet would otherwise open this
  // tab disabled even though there are projects whose labels could be
  // edited.
  const [projectId, setProjectId] = useState<string>(
    () => initialProjectId || projects[0]?.id || ''
  );
  const canEdit = projectId !== '';
  const nameInputId = useId();
  const projectSelectId = useId();
  const errorId = useId();
  // Each row's error element id is this plus the label id.
  const rowErrorIdBase = useId();
  const containerRef = useRef<HTMLDivElement>(null);

  const [labels, setLabels] = useState<LabelUsage[]>([]);
  const [loading, setLoading] = useState(false);
  // Errors are kept apart by what raised them (DFLT-00214), so that starting
  // one action never silently removes the report of another one's failure:
  // - loadError: the list could not be fetched. Cleared only by the next
  //   load (a project switch).
  // - createError: the create form's last attempt failed. Cleared only by
  //   the next create (or a load), never by a row's action.
  // - rowErrors: per label id, the last failed rename/recolor/delete of that
  //   row. Cleared only when that same row starts its next rename save,
  //   recolor or delete (or by a load) -- not by another row's action and not
  //   by a create. The same idea as busyIds (DFLT-00211): a row's state lives
  //   and dies with that row's own requests.
  const [loadError, setLoadError] = useState('');
  const [createError, setCreateError] = useState('');
  const [rowErrors, setRowErrors] = useState<ReadonlyMap<string, string>>(() => new Map());
  // Always a new Map (React would not see an in-place change), and the
  // previous one when nothing changes, so no re-render is spent on it.
  const setRowError = (id: string, message: string) =>
    setRowErrors(prev => {
      if (prev.get(id) === message) return prev;
      const next = new Map(prev);
      next.set(id, message);
      return next;
    });
  const clearRowError = (id: string) =>
    setRowErrors(prev => {
      if (!prev.has(id)) return prev;
      const next = new Map(prev);
      next.delete(id);
      return next;
    });

  const [newName, setNewName] = useState('');
  const [newColor, setNewColor] = useState<LabelColor>('gray');
  const [creating, setCreating] = useState(false);
  // Whether createError is about the name as it is still typed (it then marks
  // the name input aria-invalid and describes it). Editing the name drops
  // this, but leaves the error text on screen.
  const [createFailed, setCreateFailed] = useState(false);
  // The row whose open rename input holds a name the server just rejected
  // (it then marks that input aria-invalid and describes it with the row's
  // error). Like createFailed, editing the draft drops it while the error
  // text stays. One id is enough: only one row is renamed at a time.
  const [renameFailedId, setRenameFailedId] = useState<string | null>(null);

  // Inline rename: which row is being renamed, and its draft. One row at a
  // time. A finished save only closes the rename if its own row is still the
  // one being renamed -- another row's rename may have been opened while it
  // was saving, and must keep its input and draft -- and only moves focus if
  // focus is still on its own name input or save button, or nowhere
  // (DFLT-00213). renamingIdRef holds the latest value for that check, which
  // runs after an await where the render's renamingId is stale; it is only
  // ever updated through setRenaming, together with the state, so it is
  // current from the moment a handler changes it, not only after the next
  // render.
  const [renamingId, setRenamingId] = useState<string | null>(null);
  const renamingIdRef = useRef<string | null>(null);
  const setRenaming = useCallback((id: string | null) => {
    renamingIdRef.current = id;
    setRenamingId(id);
  }, []);
  const [renameDraft, setRenameDraft] = useState('');
  // The rows with a request in flight (DFLT-00211). A set, not a single id:
  // another row can be renamed, recolored or deleted while one is saving,
  // and each row must stay busy until its own request settles -- neither
  // another row's start nor another row's finish may change it.
  const [busyIds, setBusyIds] = useState<ReadonlySet<string>>(() => new Set());
  // Always a new Set (React would not see an in-place change), and the
  // previous one when nothing changes, so no re-render is spent on it.
  const markBusy = (id: string) =>
    setBusyIds(prev => {
      if (prev.has(id)) return prev;
      const next = new Set(prev);
      next.add(id);
      return next;
    });
  const clearBusy = (id: string) =>
    setBusyIds(prev => {
      if (!prev.has(id)) return prev;
      const next = new Set(prev);
      next.delete(id);
      return next;
    });

  // Where keyboard focus goes once the next render has settled: finishing a
  // rename unmounts its input and buttons, and deleting removes the focused
  // button's whole row, which would otherwise drop focus to <body> (and the
  // modal's focus trap would then send the next Tab to the dialog's start).
  const [pendingFocus, setPendingFocus] = useState<string | null>(null);
  useEffect(() => {
    if (pendingFocus === null) return;
    const el = containerRef.current?.querySelector<HTMLElement>(`[data-focus-key="${pendingFocus}"]`);
    if (el && (el as HTMLButtonElement).disabled) return; // retry once re-enabled
    el?.focus();
    setPendingFocus(null);
    // labels/renamingId/busyIds/creating are what mount, unmount, disable and
    // re-enable the target; they are listed so the effect re-runs on them.
  }, [pendingFocus, labels, renamingId, busyIds, creating]);

  // The project whose response may still be applied. Switching the selector
  // starts a new fetch without cancelling the previous one, so a slow backend
  // (MySQL, or the HTTP data source) can resolve project A's request after
  // B's; applying it would leave A's labels on screen under B's selection,
  // and rename/delete go by label id, so the next action would edit A's
  // labels while the user is looking at B (DFLT-00124, non-functional review
  // condition NF-1). App.tsx's refreshProjectLabels guards the same way.
  const requestedProjectIdRef = useRef(projectId);

  const load = useCallback(async () => {
    requestedProjectIdRef.current = projectId;
    // Not just `labels`: an error from the project being left, and the
    // aria-invalid/role="alert" state that goes with it, must not survive
    // into a selection where the form it describes is disabled
    // (accessibility review condition A-2). Every kind of error goes: the
    // rows they belong to are being replaced (DFLT-00214).
    setLoadError('');
    setCreateError('');
    setCreateFailed(false);
    setRowErrors(prev => (prev.size === 0 ? prev : new Map()));
    setRenameFailedId(null);
    if (!projectId) {
      setLabels([]);
      setLoading(false);
      return;
    }
    setLoading(true);
    try {
      const fetched = sortLabels(await fetchLabels(t, projectId));
      if (requestedProjectIdRef.current !== projectId) return;
      setLabels(fetched);
    } catch (e) {
      if (requestedProjectIdRef.current !== projectId) return;
      setLoadError(errorMessage(e, t('errors.UNKNOWN')));
    } finally {
      // loading too: a late response must not clear the spinner belonging to
      // the request that is still in flight.
      if (requestedProjectIdRef.current === projectId) setLoading(false);
    }
    // t is deliberately left out: a language switch must not re-fetch.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projectId]);

  useEffect(() => {
    setRenaming(null);
    load();
  }, [load, setRenaming]);

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!canEdit || creating || newName.trim() === '') return;
    setCreating(true);
    // Only the create form's own error: a row's failure stays on its row, and
    // a load failure stays until the next load (DFLT-00214) -- creating a
    // label does not re-fetch the list, so the list is no less incomplete.
    setCreateError('');
    setCreateFailed(false);
    try {
      const created = await createLabel(t, projectId, newName, newColor);
      setLabels(prev => sortLabels([...prev, { ...created, ticket_count: 0 }]));
      setNewName('');
      onLabelsChanged?.();
    } catch (err) {
      setCreateError(errorMessage(err, t('errors.UNKNOWN')));
      setCreateFailed(true);
    } finally {
      setCreating(false);
      // The submit button was disabled while creating; the name input is
      // where the next label (or the fix for this one) is typed.
      setPendingFocus(CREATE_NAME_FOCUS_KEY);
    }
  };

  // Records a failure of `label`'s own action on its row -- unless the
  // selector has moved on to another project meanwhile, whose rows the error
  // does not belong to (the same late-response rule as load()).
  const failRow = (label: LabelUsage, err: unknown, requestedFor: string) => {
    if (requestedProjectIdRef.current !== requestedFor) return;
    setRowError(label.id, errorMessage(err, t('errors.UNKNOWN')));
  };

  // A row's own action starts: its previous error has been dealt with (or is
  // being retried), so it goes -- and only its own (DFLT-00214).
  const startRowAction = (label: LabelUsage) => {
    markBusy(label.id);
    clearRowError(label.id);
    setRenameFailedId(prev => (prev === label.id ? null : prev));
  };

  const applyUpdate = async (label: LabelUsage, patch: { name?: string; color?: LabelColor }) => {
    startRowAction(label);
    // Named by the current name, not the rename draft: the draft may be empty
    // or rejected by the server.
    const savingText = t('settings.labels.saving', { name: label.name });
    announceRow(savingText);
    try {
      const updated = await updateLabel(t, label.id, patch);
      setLabels(prev => sortLabels(prev.map(l => (l.id === label.id ? { ...updated, ticket_count: l.ticket_count } : l))));
      // The name as saved -- the new one after a rename.
      announceRow(t('settings.labels.saveSuccess', { name: updated.name }));
      onLabelsChanged?.();
      return true;
    } catch (err) {
      failRow(label, err, projectId);
      // A rejected name marks the rename input, if this row's rename is
      // still the one open (see renameFailedId).
      if (patch.name !== undefined && renamingIdRef.current === label.id) setRenameFailedId(label.id);
      // The role="alert" error says what went wrong; "saving" no longer
      // holds. Only this save's text is cleared, not a newer announcement.
      clearRowNotice(savingText);
      return false;
    } finally {
      clearBusy(label.id);
    }
  };

  // Cancel / Escape: the user acted on this row's rename, so it always closes
  // and focus goes back to its "rename" button.
  const finishRename = (label: LabelUsage) => {
    setRenaming(null);
    setPendingFocus(renameButtonKey(label.id));
  };

  // A successful save. Checked as of now, before the next render unmounts the
  // input (which would drop focus to <body> and make the focus check always
  // pass): see renamingId and focusIsInRenameOf.
  const finishRenameAfterSave = (label: LabelUsage) => {
    // Another row's rename opened while this one saved: leave it open. This
    // row already shows its chip, which applyUpdate gave the new name.
    if (renamingIdRef.current !== label.id) return;
    const focusHere = focusIsInRenameOf(label.id);
    setRenaming(null);
    // Closed either way, but focus only moves on from this rename's own
    // controls -- not away from, say, another row's delete confirmation.
    if (focusHere) setPendingFocus(renameButtonKey(label.id));
  };

  const handleRenameSave = async (label: LabelUsage) => {
    if (busyIds.has(label.id)) return;
    if (await applyUpdate(label, { name: renameDraft })) {
      finishRenameAfterSave(label);
    } else if (renamingIdRef.current === label.id && focusIsInRenameOf(label.id)) {
      // Stay in the rename, back in its input, to fix the name -- unless
      // another row's rename has replaced it or focus has moved elsewhere
      // meanwhile (DFLT-00213). The error is shown either way.
      setPendingFocus(renameInputKey(label.id));
    }
  };

  // Where focus goes when `removed` leaves the list: the next row's delete
  // button, else the previous row's, else (no labels left) the create
  // form's name input.
  const focusAfterRemoval = (removed: LabelUsage, remaining: LabelUsage[]) => {
    const index = labels.findIndex(l => l.id === removed.id);
    const neighbor = remaining[Math.max(index, 0)] ?? remaining[remaining.length - 1];
    setPendingFocus(neighbor ? deleteButtonKey(neighbor.id) : CREATE_NAME_FOCUS_KEY);
  };

  const handleDelete = async (label: LabelUsage) => {
    if (busyIds.has(label.id)) return;
    startRowAction(label);
    try {
      // Re-read the usage counts first: labels are shared, so tickets may
      // have gained or lost this label since the tab loaded, and the
      // confirmation must state the count as of now.
      let fresh: LabelUsage[];
      try {
        fresh = sortLabels(await fetchLabels(t, projectId));
      } catch (err) {
        failRow(label, err, projectId);
        setPendingFocus(deleteButtonKey(label.id));
        return;
      }
      // Same late-response rule as load(): if the selector moved on while
      // this re-read was in flight, this list belongs to a project the user
      // is no longer looking at (NF-1).
      if (requestedProjectIdRef.current !== projectId) return;
      setLabels(fresh);
      const current = fresh.find(l => l.id === label.id);
      if (!current) {
        // Already deleted by someone else: nothing to confirm. The row still
        // vanishes and focus still moves because of the user's click, so say
        // what happened -- but not "deleted", since this user did not.
        announceRow(t('settings.labels.deleteAlreadyGone', { name: label.name }));
        focusAfterRemoval(label, fresh);
        onLabelsChanged?.();
        return;
      }
      const message =
        current.ticket_count > 0
          ? t('settings.labels.confirmDeleteInUse', { name: current.name, count: current.ticket_count })
          : t('settings.labels.confirmDelete', { name: current.name });
      // The in-app ConfirmDialog (DFLT-00148), on top of the settings modal.
      // The row stays busy (its buttons disabled) while it is open, so the
      // dialog cannot put focus back on the delete button when it closes;
      // pendingFocus does, once the row leaves busyIds below.
      const confirmed = await confirm({
        title: t('settings.labels.confirmDeleteTitle'),
        message,
        confirmLabel: t('settings.labels.confirmDeleteButton'),
        tone: 'danger',
        testIdPrefix: 'label-delete-confirm'
      });
      if (!confirmed) {
        setPendingFocus(deleteButtonKey(label.id));
        return;
      }
      try {
        await deleteLabel(t, label.id);
      } catch (err) {
        failRow(label, err, projectId);
        setPendingFocus(deleteButtonKey(label.id));
        return;
      }
      // Named as the confirmation named it: the re-read name, which may be
      // newer than the row the user clicked.
      announceRow(t('settings.labels.deleteSuccess', { name: current.name }));
      const remaining = fresh.filter(l => l.id !== label.id);
      setLabels(prev => prev.filter(l => l.id !== label.id));
      focusAfterRemoval(label, remaining);
      onLabelsChanged?.();
    } finally {
      // Only this row: a row that is still saving stays busy. After a
      // successful delete the id is simply dropped from the set.
      clearBusy(label.id);
    }
  };

  const errorDescribesName = createFailed && createError !== '';

  return (
    <div ref={containerRef} className="h-full overflow-y-auto space-y-4 text-xs">
      {confirmDialog}
      <div>
        <h3 className="flex items-center gap-1.5 font-bold text-sm text-slate-800 dark:text-slate-200">
          <Tag className="w-4 h-4 text-slate-500 dark:text-slate-400" aria-hidden="true" />
          {t('settings.labels.title')}
        </h3>
        <p className="mt-1 text-slate-500 dark:text-slate-400">{t('settings.labels.description')}</p>
      </div>

      <div className="flex items-center gap-2">
        <label htmlFor={projectSelectId} className="font-semibold text-slate-600 dark:text-slate-400">
          {t('settings.labels.projectLabel')}
        </label>
        <select
          id={projectSelectId}
          value={projectId}
          onChange={e => setProjectId(e.target.value)}
          disabled={projects.length === 0}
          className="px-2.5 py-1.5 rounded-lg bg-white dark:bg-slate-900 border border-slate-300 dark:border-slate-700 text-xs text-slate-700 dark:text-slate-300 disabled:opacity-50"
        >
          <option value="">{t('settings.labels.projectPlaceholder')}</option>
          {projects.map(p => (
            <option key={p.id} value={p.id}>{p.name}</option>
          ))}
        </select>
      </div>

      {!canEdit && (
        <div
          role="status"
          className="p-2.5 bg-amber-50 dark:bg-amber-950 text-amber-800 dark:text-amber-300 rounded-lg border border-amber-200 dark:border-amber-800"
        >
          {t(projects.length === 0 ? 'settings.labels.noProjects' : 'settings.labels.selectProject')}
        </div>
      )}

      {/* Create form */}
      <form
        onSubmit={handleCreate}
        className="p-3 rounded-lg border border-slate-200 dark:border-slate-800 bg-slate-50 dark:bg-slate-800/40 space-y-2"
        data-testid="label-create-form"
      >
        <div className="flex flex-wrap items-center gap-2">
          <label htmlFor={nameInputId} className="font-semibold text-slate-600 dark:text-slate-400">
            {t('settings.labels.name')}
          </label>
          {/* readOnly (not disabled) while creating, so pressing Enter here
              keeps focus in the input. maxLength counts UTF-16 code units
              where the server counts characters, so it is at most stricter
              (for emoji and other supplementary-plane characters). */}
          <input
            id={nameInputId}
            type="text"
            value={newName}
            onChange={e => {
              setNewName(e.target.value);
              setCreateFailed(false);
            }}
            placeholder={t('settings.labels.namePlaceholder')}
            maxLength={LABEL_NAME_MAX_LENGTH}
            disabled={!canEdit}
            readOnly={creating}
            aria-invalid={errorDescribesName || undefined}
            aria-describedby={errorDescribesName ? errorId : undefined}
            data-focus-key={CREATE_NAME_FOCUS_KEY}
            className="px-2.5 py-1.5 w-56 rounded-lg bg-white dark:bg-slate-900 border border-slate-300 dark:border-slate-700 text-slate-900 dark:text-slate-100 disabled:opacity-50"
          />
          <button
            type="submit"
            disabled={!canEdit || creating || newName.trim() === ''}
            {...submittingProps(creating)}
            className="px-3 py-1.5 rounded-lg bg-blue-600 hover:bg-blue-500 disabled:opacity-50 disabled:hover:bg-blue-600 text-white font-semibold flex items-center gap-1"
          >
            {creating ? <Loader2 className="w-3.5 h-3.5 animate-spin" aria-hidden="true" /> : <Plus className="w-3.5 h-3.5" aria-hidden="true" />}
            {t('settings.labels.create')}
            <SubmittingText busy={creating} />
          </button>
          {newName.trim() !== '' && <LabelChip name={newName.trim()} color={newColor} />}
        </div>
        <div className="flex items-center gap-2">
          <span className="font-semibold text-slate-600 dark:text-slate-400">{t('settings.labels.color')}</span>
          <LabelColorPalette
            value={newColor}
            onChange={setNewColor}
            disabled={!canEdit}
            busy={creating}
            groupLabel={t('settings.labels.newColorGroup')}
          />
        </div>
      </form>

      {/* Two separate alerts, so clearing one never takes the other with it
          (DFLT-00214). Only the create error describes the name input. */}
      {loadError && (
        <ErrorBox role="alert" className="p-2.5">
          {loadError}
        </ErrorBox>
      )}
      {createError && (
        <ErrorBox id={errorId} role="alert" className="p-2.5">
          {createError}
        </ErrorBox>
      )}

      {/* List */}
      {canEdit && loading && (
        <div role="status" className="text-slate-500 dark:text-slate-400">
          {t('settings.labels.loading')}
        </div>
      )}
      {canEdit && !loading && labels.length === 0 && (
        <div className="text-slate-500 dark:text-slate-400">{t('settings.labels.empty')}</div>
      )}
      {labels.length > 0 && (
        <ul className="divide-y divide-slate-200 dark:divide-slate-800 border border-slate-200 dark:border-slate-800 rounded-lg">
          {labels.map(label => {
            const busy = busyIds.has(label.id);
            const renaming = renamingId === label.id;
            const rowError = rowErrors.get(label.id);
            const rowErrorId = `${rowErrorIdBase}-${label.id}`;
            const renameInvalid = renaming && rowError !== undefined && renameFailedId === label.id;
            return (
              // aria-busy (DFLT-00210): the row is being saved or deleted --
              // from the usage re-read through the confirmation to the
              // delete itself -- and is dropped once that settles, whatever
              // the outcome.
              <li
                key={label.id}
                data-testid={`label-row-${label.id}`}
                aria-busy={busy || undefined}
                className="p-3 flex flex-wrap items-center gap-3"
              >
                <div className="min-w-40 flex items-center gap-2">
                  {renaming ? (
                    <input
                      type="text"
                      autoFocus
                      value={renameDraft}
                      onChange={e => {
                        setRenameDraft(e.target.value);
                        // As in the create form: a changed name is no longer
                        // the rejected one, but the error text stays.
                        setRenameFailedId(prev => (prev === label.id ? null : prev));
                      }}
                      onKeyDown={e => {
                        if (isImeComposing(e)) return;
                        if (e.key === 'Enter') {
                          e.preventDefault();
                          handleRenameSave(label);
                        } else if (e.key === 'Escape') {
                          e.stopPropagation();
                          if (!busy) finishRename(label);
                        }
                      }}
                      aria-label={t('settings.labels.renameLabel', { name: label.name })}
                      maxLength={LABEL_NAME_MAX_LENGTH}
                      readOnly={busy}
                      aria-invalid={renameInvalid || undefined}
                      aria-describedby={renameInvalid ? rowErrorId : undefined}
                      data-focus-key={renameInputKey(label.id)}
                      className="px-2 py-1 w-44 rounded-sm bg-white dark:bg-slate-900 border border-slate-300 dark:border-slate-700 text-slate-900 dark:text-slate-100"
                    />
                  ) : (
                    <LabelChip name={label.name} color={label.color} />
                  )}
                </div>
                <span className="text-slate-500 dark:text-slate-400" data-testid="label-usage">
                  {t('settings.labels.usage', { count: label.ticket_count })}
                </span>
                <LabelColorPalette
                  value={label.color}
                  onChange={color => {
                    if (color !== label.color) applyUpdate(label, { color });
                  }}
                  busy={busy}
                  groupLabel={t('settings.labels.colorGroup', { name: label.name })}
                  size="sm"
                />
                <div className="ml-auto flex items-center gap-2">
                  {/* DFLT-00168: the spinner is the only direct sign that this
                      row's save is in progress, so it needs 3:1 (WCAG 1.4.11)
                      against white / slate-900: 4.76:1 / 6.96:1. */}
                  {busy && <Loader2 className="w-3.5 h-3.5 animate-spin text-slate-500 dark:text-slate-400" aria-hidden="true" />}
                  {renaming ? (
                    <>
                      <button
                        type="button"
                        onClick={() => handleRenameSave(label)}
                        disabled={busy}
                        data-focus-key={renameSaveKey(label.id)}
                        className="px-2 py-1 rounded-sm bg-blue-600 hover:bg-blue-500 disabled:opacity-50 text-white font-semibold"
                      >
                        {t('settings.labels.save')}
                      </button>
                      <button
                        type="button"
                        onClick={() => finishRename(label)}
                        disabled={busy}
                        className="px-2 py-1 rounded-sm text-slate-500 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-200 font-semibold"
                      >
                        {t('settings.labels.cancel')}
                      </button>
                    </>
                  ) : (
                    <button
                      type="button"
                      onClick={() => {
                        setRenaming(label.id);
                        setRenameDraft(label.name);
                        // A fresh draft (the current name) is not the name
                        // that was rejected. The row's error itself stays
                        // until the next save starts.
                        setRenameFailedId(null);
                      }}
                      disabled={busy}
                      aria-label={`${t('settings.labels.rename')}: ${label.name}`}
                      data-focus-key={renameButtonKey(label.id)}
                      className="px-2 py-1 rounded-sm text-slate-600 dark:text-slate-300 hover:bg-slate-100 dark:hover:bg-slate-800 flex items-center gap-1 font-semibold disabled:opacity-50"
                    >
                      <Pencil className="w-3.5 h-3.5" aria-hidden="true" />
                      {t('settings.labels.rename')}
                    </button>
                  )}
                  <button
                    type="button"
                    onClick={() => handleDelete(label)}
                    disabled={busy}
                    aria-label={`${t('settings.labels.delete')}: ${label.name}`}
                    data-focus-key={deleteButtonKey(label.id)}
                    className="px-2 py-1 rounded-sm text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-950 flex items-center gap-1 font-semibold disabled:opacity-50"
                  >
                    <Trash2 className="w-3.5 h-3.5" aria-hidden="true" />
                    {t('settings.labels.delete')}
                  </button>
                </div>
                {/* The row's own last failure (DFLT-00214), on a line of its
                    own under the row and naming the label, so it is clear
                    which row failed. Inside the keyed <li>, so another row's
                    action or re-render neither removes nor re-announces it;
                    this row's next action removes it, so a repeated failure
                    is announced again. The same ErrorBox as the create/load alert. */}
                {rowError !== undefined && (
                  <ErrorBox id={rowErrorId} role="alert" className="basis-full p-2">
                    {t('settings.labels.rowError', { name: label.name, message: rowError })}
                  </ErrorBox>
                )}
              </li>
            );
          })}
        </ul>
      )}

      {/* Row announcements (saving, saved, deleted). Outside the list:
          deleting the last label unmounts the <ul>, and the announcement
          must outlive it. Last child on purpose: this
          container spaces its children with space-y-4, whose sibling
          selector gives every child after the first a top margin even
          though the region is absolutely positioned. As the first child it
          would push the heading down by 1rem; as the last one only the
          region itself takes that margin, so nothing visible moves. */}
      <StatusLiveRegion message={rowNotice} />
    </div>
  );
};
