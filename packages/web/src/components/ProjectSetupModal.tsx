// Project setup dialog (DFLT-00080). Two entry points share it:
//
//  - `graph-engine ui` run from a directory that no project's local path
//    covers opens the Web UI with `?newProject=1&workDir=<dir>`. Then the
//    dialog offers both "create a new project" and "choose an existing
//    project" (one a teammate sharing the DB may already have created), and
//    either way maps <dir> to that project in this environment's
//    graph-config.json (projectPaths) -- never in the shared DB.
//  - The header's "New project..." entry opens it in create-only mode, with
//    the local path optional.
//
// "Choose an existing project" is PATCH /api/projects/{id} {local_path}
// followed by PUT /api/current-project. If the switch fails after the PATCH
// succeeded, the dialog stays open with the error so confirming again simply
// repeats both calls (sending the same local_path twice is harmless).
import React, { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Loader2 } from 'lucide-react';
import { Project } from '../types';
import { apiFetch } from '../lib/apiFetch';
import { errorMessage, localizedApiErrorMessage } from '../lib/apiError';

type Mode = 'create' | 'existing';

interface Props {
  isOpen: boolean;
  // The directory `graph-engine ui` was run from, or '' when opened from the
  // header menu.
  directory: string;
  // true only for the `graph-engine ui` entry point: offers the
  // create/choose-existing switch. The header entry is create-only.
  offerExisting: boolean;
  projects: Project[];
  onClose: () => void;
  // Called with the newly created project; the caller switches to it.
  onCreated: (project: Project) => void | Promise<void>;
  // Called once an existing project has been mapped to `directory` and made
  // the current project (both requests already succeeded).
  onLinked: (project: Project) => void | Promise<void>;
}

const inputClass =
  'w-full bg-slate-50 dark:bg-slate-800 border border-slate-300 dark:border-slate-700 rounded-lg px-3 py-2 text-sm text-slate-900 dark:text-slate-100 focus:outline-none focus:border-blue-500 disabled:opacity-60';

export const ProjectSetupModal: React.FC<Props> = ({
  isOpen,
  directory,
  offerExisting,
  projects,
  onClose,
  onCreated,
  onLinked
}) => {
  const { t } = useTranslation();
  const [mode, setMode] = useState<Mode>('create');
  const [name, setName] = useState('');
  const [prefix, setPrefix] = useState('');
  const [localPath, setLocalPath] = useState(directory);
  const [selectedId, setSelectedId] = useState('');
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);

  // Re-seed the form whenever the dialog is (re)opened for a directory.
  useEffect(() => {
    if (!isOpen) return;
    setMode('create');
    setName('');
    setPrefix('');
    setLocalPath(directory);
    setSelectedId('');
    setError('');
  }, [isOpen, directory]);

  if (!isOpen) return null;

  const canChooseExisting = offerExisting && projects.length > 0;
  const selected = projects.find(p => p.id === selectedId) ?? null;
  const willOverwrite = !!selected && !!selected.local_path && selected.local_path !== directory;

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!name.trim()) return;
    setSaving(true);
    setError('');
    try {
      const body: Record<string, string> = { name };
      if (prefix) body.prefix = prefix;
      if (localPath.trim()) body.local_path = localPath.trim();
      const res = await apiFetch('/api/projects', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body)
      });
      if (!res.ok) {
        setError(await localizedApiErrorMessage(t, res));
        return;
      }
      const created: Project = await res.json();
      await onCreated(created);
    } catch (err) {
      setError(errorMessage(err, t('errors.UNKNOWN')));
    } finally {
      setSaving(false);
    }
  };

  const handleChooseExisting = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!selected) return;
    setSaving(true);
    setError('');
    try {
      const patchRes = await apiFetch(`/api/projects/${selected.id}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ local_path: directory })
      });
      if (!patchRes.ok) {
        setError(await localizedApiErrorMessage(t, patchRes));
        return;
      }
      const switchRes = await apiFetch('/api/current-project', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ project_id: selected.id })
      });
      if (!switchRes.ok) {
        setError(await localizedApiErrorMessage(t, switchRes));
        return;
      }
      const current: Project = await switchRes.json();
      await onLinked(current);
    } catch (err) {
      setError(errorMessage(err, t('errors.UNKNOWN')));
    } finally {
      setSaving(false);
    }
  };

  const modeButton = (value: Mode, label: string, disabled: boolean) => (
    <label
      className={`flex-1 flex items-center gap-2 px-3 py-2 text-xs font-semibold border rounded-lg cursor-pointer transition ${
        mode === value
          ? 'border-blue-500 bg-blue-50 dark:bg-blue-950 text-blue-700 dark:text-blue-300'
          : 'border-slate-300 dark:border-slate-700 text-slate-600 dark:text-slate-400'
      } ${disabled ? 'opacity-50 cursor-not-allowed' : ''}`}
    >
      <input
        type="radio"
        name="project-setup-mode"
        value={value}
        checked={mode === value}
        disabled={disabled || saving}
        onChange={() => {
          setMode(value);
          setError('');
        }}
      />
      {label}
    </label>
  );

  const errorBox = error && (
    <div role="alert" className="p-2.5 bg-red-50 dark:bg-red-950 text-red-700 dark:text-red-300 text-[11px] rounded-lg border border-red-200 dark:border-red-900">
      {error}
    </div>
  );

  const footer = (submitLabel: string, submitDisabled: boolean) => (
    <div className="flex justify-end gap-2 pt-2">
      <button
        type="button"
        onClick={onClose}
        className="px-4 py-2 bg-slate-100 dark:bg-slate-800 hover:bg-slate-200 dark:hover:bg-slate-700 rounded-lg text-xs font-medium text-slate-700 dark:text-slate-300 transition"
      >
        {t('createModal.cancel')}
      </button>
      <button
        type="submit"
        disabled={saving || submitDisabled}
        className="px-4 py-2 bg-blue-600 hover:bg-blue-500 disabled:opacity-50 rounded-lg text-xs font-semibold text-white shadow-xs transition flex items-center gap-1.5"
      >
        {saving && <Loader2 className="w-3.5 h-3.5 animate-spin" />}
        {submitLabel}
      </button>
    </div>
  );

  return (
    <div className="fixed inset-0 z-50 bg-black/40 backdrop-blur-xs flex items-center justify-center p-4">
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="project-setup-title"
        className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-xl w-full max-w-md p-6 shadow-2xl"
      >
        <h2 id="project-setup-title" className="text-lg font-bold text-slate-900 dark:text-slate-100 mb-4">
          {offerExisting ? t('projectSetupModal.title') : t('createProjectModal.title')}
        </h2>
        <p className="text-xs text-slate-500 dark:text-slate-400 mb-4 break-all">
          {offerExisting ? t('projectSetupModal.description', { dir: directory }) : t('createProjectModal.description')}
        </p>

        {offerExisting && (
          <fieldset className="mb-4">
            <legend className="sr-only">{t('projectSetupModal.modeLabel')}</legend>
            <div className="flex gap-2">
              {modeButton('create', t('projectSetupModal.modeCreate'), false)}
              {modeButton('existing', t('projectSetupModal.modeExisting'), !canChooseExisting)}
            </div>
            {projects.length === 0 && (
              <p className="mt-2 text-[11px] text-slate-500 dark:text-slate-400">{t('projectSetupModal.noExistingProjects')}</p>
            )}
          </fieldset>
        )}

        {mode === 'create' ? (
          <form onSubmit={handleCreate} className="space-y-4">
            <div>
              <label htmlFor="project-setup-name" className="block text-xs font-semibold text-slate-700 dark:text-slate-300 mb-1">
                {t('createProjectModal.nameLabel')}
              </label>
              <input
                id="project-setup-name"
                type="text"
                required
                disabled={saving}
                className={inputClass}
                value={name}
                onChange={e => setName(e.target.value)}
                placeholder={t('createProjectModal.namePlaceholder')}
              />
            </div>
            <div>
              <label htmlFor="project-setup-prefix" className="block text-xs font-semibold text-slate-700 dark:text-slate-300 mb-1">
                {t('createProjectModal.prefixLabel')}
              </label>
              <input
                id="project-setup-prefix"
                type="text"
                maxLength={5}
                disabled={saving}
                className={`${inputClass} font-mono uppercase`}
                value={prefix}
                onChange={e => setPrefix(e.target.value.toUpperCase())}
                placeholder={t('createProjectModal.prefixPlaceholder')}
              />
            </div>
            <div>
              <label htmlFor="project-setup-local-path" className="block text-xs font-semibold text-slate-700 dark:text-slate-300 mb-1">
                {t('createProjectModal.localPathLabel')}
              </label>
              <input
                id="project-setup-local-path"
                type="text"
                disabled={saving}
                className={`${inputClass} font-mono`}
                value={localPath}
                onChange={e => setLocalPath(e.target.value)}
                placeholder={t('createProjectModal.localPathPlaceholder')}
              />
            </div>
            {errorBox}
            {footer(t('createModal.submit'), !name.trim())}
          </form>
        ) : (
          <form onSubmit={handleChooseExisting} className="space-y-4">
            <fieldset>
              <legend className="block text-xs font-semibold text-slate-700 dark:text-slate-300 mb-1">
                {t('projectSetupModal.existingListLabel')}
              </legend>
              <div className="max-h-60 overflow-y-auto space-y-1.5">
                {projects.map(p => (
                  <label
                    key={p.id}
                    className={`flex items-start gap-2 px-3 py-2 border rounded-lg cursor-pointer text-xs ${
                      p.id === selectedId
                        ? 'border-blue-500 bg-blue-50 dark:bg-blue-950'
                        : 'border-slate-200 dark:border-slate-700'
                    }`}
                  >
                    <input
                      type="radio"
                      name="project-setup-existing"
                      value={p.id}
                      checked={p.id === selectedId}
                      disabled={saving}
                      onChange={() => {
                        setSelectedId(p.id);
                        setError('');
                      }}
                      aria-label={p.name}
                    />
                    <span className="min-w-0">
                      <span className="block font-semibold text-slate-800 dark:text-slate-200 truncate">{p.name}</span>
                      <span
                        data-testid={`project-setup-local-path-${p.id}`}
                        className={`block font-mono break-all ${p.local_path ? 'text-slate-500 dark:text-slate-400' : 'text-amber-700 dark:text-amber-400'}`}
                      >
                        {p.local_path || t('projectSetupModal.notSet')}
                      </span>
                    </span>
                  </label>
                ))}
              </div>
            </fieldset>
            {willOverwrite && selected && (
              <div className="p-2.5 bg-amber-50 dark:bg-amber-950 text-amber-800 dark:text-amber-300 text-[11px] rounded-lg border border-amber-200 dark:border-amber-800 break-all">
                {t('projectSetupModal.overwriteWarning', { current: selected.local_path, next: directory })}
              </div>
            )}
            {errorBox}
            {footer(t('projectSetupModal.confirmExisting'), !selected)}
          </form>
        )}
      </div>
    </div>
  );
};
