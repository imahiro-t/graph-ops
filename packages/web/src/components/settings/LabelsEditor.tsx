// Settings modal "ラベル" tab (DFLT-00084): create, rename, recolor and delete
// the selected project's labels. Labels are DB rows shared by everyone using
// the same backend -- not team-tier files -- so, unlike the other
// project-scoped editors, this tab needs no local path, and every change is
// saved immediately (there is no dirty/unsaved state to protect).
import React, { useCallback, useEffect, useId, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Loader2, Pencil, Plus, Tag, Trash2 } from 'lucide-react';
import { LabelColor, LabelUsage, LABEL_COLORS, LABEL_NAME_MAX_LENGTH } from '../../types';
import { getLabelColorMeta } from '../../labelMeta';
import { createLabel, deleteLabel, fetchLabels, updateLabel } from '../../lib/labelsApi';
import { errorMessage } from '../../lib/apiError';
import { LabelChip } from '../LabelChip';

interface Props {
  // The project whose labels are edited; '' disables the whole tab.
  projectId: string;
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
// via aria-pressed, so the choice is not conveyed by color alone.
export const LabelColorPalette: React.FC<PaletteProps> = ({ value, onChange, disabled = false, busy = false, groupLabel, size = 'md' }) => {
  const { t } = useTranslation();
  const dim = size === 'md' ? 'w-6 h-6' : 'w-4 h-4';
  return (
    <div role="group" aria-label={groupLabel} className="flex flex-wrap items-center gap-1.5">
      {LABEL_COLORS.map(color => {
        const meta = getLabelColorMeta(color);
        const selected = value === color;
        return (
          <button
            key={color}
            type="button"
            aria-label={t(meta.nameKey)}
            aria-pressed={selected}
            aria-disabled={busy || undefined}
            title={t(meta.nameKey)}
            disabled={disabled}
            onClick={() => {
              if (!busy) onChange(color);
            }}
            className={`${dim} rounded-full ${meta.swatch} disabled:opacity-40 disabled:cursor-not-allowed aria-disabled:opacity-40 aria-disabled:cursor-wait focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500 focus-visible:ring-offset-1 ${
              selected ? 'ring-2 ring-offset-2 ring-slate-700 dark:ring-slate-200 dark:ring-offset-slate-900' : ''
            }`}
          />
        );
      })}
    </div>
  );
};

export const LabelsEditor: React.FC<Props> = ({ projectId, onLabelsChanged }) => {
  const { t } = useTranslation();
  const canEdit = projectId !== '';
  const nameInputId = useId();
  const errorId = useId();
  const containerRef = useRef<HTMLDivElement>(null);

  const [labels, setLabels] = useState<LabelUsage[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  const [newName, setNewName] = useState('');
  const [newColor, setNewColor] = useState<LabelColor>('gray');
  const [creating, setCreating] = useState(false);
  // Whether `error` is about the create form's name (it then marks the name
  // input aria-invalid and describes it).
  const [createFailed, setCreateFailed] = useState(false);

  // Inline rename: which row is being renamed, and its draft.
  const [renamingId, setRenamingId] = useState<string | null>(null);
  const [renameDraft, setRenameDraft] = useState('');
  // The row with a request in flight, if any.
  const [busyId, setBusyId] = useState<string | null>(null);

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
    // labels/renamingId/busyId/creating are what mount, unmount, disable and
    // re-enable the target; they are listed so the effect re-runs on them.
  }, [pendingFocus, labels, renamingId, busyId, creating]);

  const load = useCallback(async () => {
    if (!projectId) {
      setLabels([]);
      return;
    }
    setLoading(true);
    setError('');
    try {
      setLabels(sortLabels(await fetchLabels(t, projectId)));
    } catch (e) {
      setError(errorMessage(e, t('errors.UNKNOWN')));
    } finally {
      setLoading(false);
    }
    // t is deliberately left out: a language switch must not re-fetch.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projectId]);

  useEffect(() => {
    setRenamingId(null);
    load();
  }, [load]);

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!canEdit || creating || newName.trim() === '') return;
    setCreating(true);
    setError('');
    setCreateFailed(false);
    try {
      const created = await createLabel(t, projectId, newName, newColor);
      setLabels(prev => sortLabels([...prev, { ...created, ticket_count: 0 }]));
      setNewName('');
      onLabelsChanged?.();
    } catch (err) {
      setError(errorMessage(err, t('errors.UNKNOWN')));
      setCreateFailed(true);
    } finally {
      setCreating(false);
      // The submit button was disabled while creating; the name input is
      // where the next label (or the fix for this one) is typed.
      setPendingFocus(CREATE_NAME_FOCUS_KEY);
    }
  };

  const applyUpdate = async (label: LabelUsage, patch: { name?: string; color?: LabelColor }) => {
    setBusyId(label.id);
    setError('');
    setCreateFailed(false);
    try {
      const updated = await updateLabel(t, label.id, patch);
      setLabels(prev => sortLabels(prev.map(l => (l.id === label.id ? { ...updated, ticket_count: l.ticket_count } : l))));
      onLabelsChanged?.();
      return true;
    } catch (err) {
      setError(errorMessage(err, t('errors.UNKNOWN')));
      return false;
    } finally {
      setBusyId(null);
    }
  };

  const finishRename = (label: LabelUsage) => {
    setRenamingId(null);
    setPendingFocus(renameButtonKey(label.id));
  };

  const handleRenameSave = async (label: LabelUsage) => {
    if (busyId === label.id) return;
    if (await applyUpdate(label, { name: renameDraft })) {
      finishRename(label);
    } else {
      // Stay in the rename, back in its input, to fix the name.
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
    if (busyId === label.id) return;
    setBusyId(label.id);
    setError('');
    setCreateFailed(false);
    try {
      // Re-read the usage counts first: labels are shared, so tickets may
      // have gained or lost this label since the tab loaded, and the
      // confirmation must state the count as of now.
      let fresh: LabelUsage[];
      try {
        fresh = sortLabels(await fetchLabels(t, projectId));
      } catch (err) {
        setError(errorMessage(err, t('errors.UNKNOWN')));
        setPendingFocus(deleteButtonKey(label.id));
        return;
      }
      setLabels(fresh);
      const current = fresh.find(l => l.id === label.id);
      if (!current) {
        // Already deleted by someone else: nothing to confirm.
        focusAfterRemoval(label, fresh);
        onLabelsChanged?.();
        return;
      }
      const message =
        current.ticket_count > 0
          ? t('settings.labels.confirmDeleteInUse', { name: current.name, count: current.ticket_count })
          : t('settings.labels.confirmDelete', { name: current.name });
      if (!window.confirm(message)) {
        setPendingFocus(deleteButtonKey(label.id));
        return;
      }
      try {
        await deleteLabel(t, label.id);
      } catch (err) {
        setError(errorMessage(err, t('errors.UNKNOWN')));
        setPendingFocus(deleteButtonKey(label.id));
        return;
      }
      const remaining = fresh.filter(l => l.id !== label.id);
      setLabels(prev => prev.filter(l => l.id !== label.id));
      focusAfterRemoval(label, remaining);
      onLabelsChanged?.();
    } finally {
      setBusyId(null);
    }
  };

  const errorDescribesName = createFailed && error !== '';

  return (
    <div ref={containerRef} className="h-full overflow-y-auto space-y-4 text-xs">
      <div>
        <h3 className="flex items-center gap-1.5 font-bold text-sm text-slate-800 dark:text-slate-200">
          <Tag className="w-4 h-4 text-slate-500 dark:text-slate-400" aria-hidden="true" />
          {t('settings.labels.title')}
        </h3>
        <p className="mt-1 text-slate-500 dark:text-slate-400">{t('settings.labels.description')}</p>
      </div>

      {!canEdit && (
        <div
          role="status"
          className="p-2.5 bg-amber-50 dark:bg-amber-950 text-amber-800 dark:text-amber-300 rounded-lg border border-amber-200 dark:border-amber-800"
        >
          {t('settings.labels.selectProject')}
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
            className="px-3 py-1.5 rounded-lg bg-blue-600 hover:bg-blue-500 disabled:opacity-50 disabled:hover:bg-blue-600 text-white font-semibold flex items-center gap-1"
          >
            {creating ? <Loader2 className="w-3.5 h-3.5 animate-spin" aria-hidden="true" /> : <Plus className="w-3.5 h-3.5" aria-hidden="true" />}
            {t('settings.labels.create')}
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

      {error && (
        <div
          id={errorId}
          role="alert"
          className="p-2.5 bg-red-50 dark:bg-red-950 text-red-700 dark:text-red-300 rounded-lg border border-red-200 dark:border-red-800"
        >
          {error}
        </div>
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
            const busy = busyId === label.id;
            const renaming = renamingId === label.id;
            return (
              <li key={label.id} data-testid={`label-row-${label.id}`} className="p-3 flex flex-wrap items-center gap-3">
                <div className="min-w-[10rem] flex items-center gap-2">
                  {renaming ? (
                    <input
                      type="text"
                      autoFocus
                      value={renameDraft}
                      onChange={e => setRenameDraft(e.target.value)}
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
                      data-focus-key={renameInputKey(label.id)}
                      className="px-2 py-1 w-44 rounded bg-white dark:bg-slate-900 border border-slate-300 dark:border-slate-700 text-slate-900 dark:text-slate-100"
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
                  {busy && <Loader2 className="w-3.5 h-3.5 animate-spin text-slate-400" aria-hidden="true" />}
                  {renaming ? (
                    <>
                      <button
                        type="button"
                        onClick={() => handleRenameSave(label)}
                        disabled={busy}
                        className="px-2 py-1 rounded bg-blue-600 hover:bg-blue-500 disabled:opacity-50 text-white font-semibold"
                      >
                        {t('settings.labels.save')}
                      </button>
                      <button
                        type="button"
                        onClick={() => finishRename(label)}
                        disabled={busy}
                        className="px-2 py-1 rounded text-slate-500 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-200 font-semibold"
                      >
                        {t('settings.labels.cancel')}
                      </button>
                    </>
                  ) : (
                    <button
                      type="button"
                      onClick={() => {
                        setRenamingId(label.id);
                        setRenameDraft(label.name);
                      }}
                      disabled={busy}
                      aria-label={`${t('settings.labels.rename')}: ${label.name}`}
                      data-focus-key={renameButtonKey(label.id)}
                      className="px-2 py-1 rounded text-slate-600 dark:text-slate-300 hover:bg-slate-100 dark:hover:bg-slate-800 flex items-center gap-1 font-semibold disabled:opacity-50"
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
                    className="px-2 py-1 rounded text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-950 flex items-center gap-1 font-semibold disabled:opacity-50"
                  >
                    <Trash2 className="w-3.5 h-3.5" aria-hidden="true" />
                    {t('settings.labels.delete')}
                  </button>
                </div>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
};
