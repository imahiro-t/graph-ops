// Settings modal "ラベル" tab (DFLT-00084): create, rename, recolor and delete
// the selected project's labels. Labels are DB rows shared by everyone using
// the same backend -- not team-tier files -- so, unlike the other
// project-scoped editors, this tab needs no local path, and every change is
// saved immediately (there is no dirty/unsaved state to protect).
import React, { useCallback, useEffect, useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Loader2, Pencil, Plus, Tag, Trash2 } from 'lucide-react';
import { LabelColor, LabelUsage, LABEL_COLORS } from '../../types';
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

interface PaletteProps {
  value: LabelColor | null;
  onChange: (color: LabelColor) => void;
  disabled?: boolean;
  groupLabel: string;
  size?: 'md' | 'sm';
}

// The fixed color palette as a group of toggle buttons: each button is named
// by its translated color name and reports whether it is the current color
// via aria-pressed, so the choice is not conveyed by color alone.
export const LabelColorPalette: React.FC<PaletteProps> = ({ value, onChange, disabled = false, groupLabel, size = 'md' }) => {
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
            title={t(meta.nameKey)}
            disabled={disabled}
            onClick={() => onChange(color)}
            className={`${dim} rounded-full ${meta.swatch} disabled:opacity-40 disabled:cursor-not-allowed focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500 focus-visible:ring-offset-1 ${
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

  const [labels, setLabels] = useState<LabelUsage[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  const [newName, setNewName] = useState('');
  const [newColor, setNewColor] = useState<LabelColor>('gray');
  const [creating, setCreating] = useState(false);

  // Inline rename: which row is being renamed, and its draft.
  const [renamingId, setRenamingId] = useState<string | null>(null);
  const [renameDraft, setRenameDraft] = useState('');
  // The row with a request in flight, if any.
  const [busyId, setBusyId] = useState<string | null>(null);

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
    if (!canEdit || creating) return;
    setCreating(true);
    setError('');
    try {
      const created = await createLabel(t, projectId, newName, newColor);
      setLabels(prev => sortLabels([...prev, { ...created, ticket_count: 0 }]));
      setNewName('');
      onLabelsChanged?.();
    } catch (err) {
      setError(errorMessage(err, t('errors.UNKNOWN')));
    } finally {
      setCreating(false);
    }
  };

  const applyUpdate = async (label: LabelUsage, patch: { name?: string; color?: LabelColor }) => {
    setBusyId(label.id);
    setError('');
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

  const handleRenameSave = async (label: LabelUsage) => {
    if (await applyUpdate(label, { name: renameDraft })) {
      setRenamingId(null);
    }
  };

  const handleDelete = async (label: LabelUsage) => {
    const message =
      label.ticket_count > 0
        ? t('settings.labels.confirmDeleteInUse', { name: label.name, count: label.ticket_count })
        : t('settings.labels.confirmDelete', { name: label.name });
    if (!window.confirm(message)) return;
    setBusyId(label.id);
    setError('');
    try {
      await deleteLabel(t, label.id);
      setLabels(prev => prev.filter(l => l.id !== label.id));
      onLabelsChanged?.();
    } catch (err) {
      setError(errorMessage(err, t('errors.UNKNOWN')));
    } finally {
      setBusyId(null);
    }
  };

  return (
    <div className="h-full overflow-y-auto space-y-4 text-xs">
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
          <input
            id={nameInputId}
            type="text"
            value={newName}
            onChange={e => setNewName(e.target.value)}
            placeholder={t('settings.labels.namePlaceholder')}
            maxLength={100}
            disabled={!canEdit || creating}
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
            disabled={!canEdit || creating}
            groupLabel={t('settings.labels.newColorGroup')}
          />
        </div>
      </form>

      {error && (
        <div role="alert" className="p-2.5 bg-red-50 dark:bg-red-950 text-red-700 dark:text-red-300 rounded-lg border border-red-200 dark:border-red-800">
          {error}
        </div>
      )}

      {/* List */}
      {canEdit && loading && <div className="text-slate-400 dark:text-slate-500">{t('settings.labels.loading')}</div>}
      {canEdit && !loading && labels.length === 0 && (
        <div className="text-slate-400 dark:text-slate-500">{t('settings.labels.empty')}</div>
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
                        if (e.key === 'Enter') {
                          e.preventDefault();
                          handleRenameSave(label);
                        } else if (e.key === 'Escape') {
                          e.stopPropagation();
                          setRenamingId(null);
                        }
                      }}
                      aria-label={t('settings.labels.renameLabel', { name: label.name })}
                      maxLength={100}
                      disabled={busy}
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
                  disabled={busy}
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
                        onClick={() => setRenamingId(null)}
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
