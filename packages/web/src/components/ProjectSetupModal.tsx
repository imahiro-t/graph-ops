// Project setup dialog (DFLT-00080). Two entry points share it:
//
//  - `graph-engine ui` run from a directory that no project's local path
//    covers opens the Web UI with `?newProject=1&workDir=<dir>`. Then the
//    dialog offers both "create a new project" and "choose an existing
//    project" (one a teammate sharing the DB may already have created), and
//    either way maps <dir> to that project in this environment's
//    home config file, $HOME/.graph-ops/config.json (projectPaths) --
//    never in the shared DB.
//  - The header's "New project..." entry opens it in create-only mode, with
//    the local path optional.
//
// "Choose an existing project" is PATCH /api/projects/{id} {local_path}
// followed by PUT /api/current-project. If the switch fails after the PATCH
// succeeded, the dialog stays open with the error so confirming again simply
// repeats both calls (sending the same local_path twice is harmless).
//
// "Create new" can fail half-way: the project is inserted into the shared DB
// but saving its local path to the home config file fails (500
// PROJECT_CREATED_LOCAL_PATH_NOT_SAVED). Pressing "create" again would then
// insert a duplicate project, so instead the dialog re-fetches the project
// list, says the project already exists, and disables "create". With the
// `graph-engine ui` entry it also moves to "choose an existing project" with
// the new project preselected, so confirming saves the path via PATCH; the
// header entry points the user at the settings screen instead.
//
// Focus (WCAG 2.4.3 / 2.1.2) is the shared useModalDialog hook (DFLT-00074):
// opening moves focus into the dialog, Tab and Shift+Tab wrap inside it,
// Escape closes it (but not while an IME composition is in progress), and
// closing returns focus to the element that had it before opening -- or,
// when that element is gone (the header menu item unmounts as the menu
// closes; the `graph-engine ui` entry opens with nothing focused), to
// `returnFocusRef`.
import React, { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Loader2 } from 'lucide-react';
import { Project } from '../types';
import { apiFetch } from '../lib/apiFetch';
import { errorMessage, localizedApiErrorMessage, parseApiError, translateErrorCode } from '../lib/apiError';
import { useModalDialog } from '../hooks/useModalDialog';

type Mode = 'create' | 'existing';

// Error code POST /api/projects returns when the DB insert succeeded but the
// local path could not be saved (see internal/httpserver/projects.go).
const CREATED_LOCAL_PATH_NOT_SAVED = 'PROJECT_CREATED_LOCAL_PATH_NOT_SAVED';

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
  // Re-fetches `projects`. Called when a create turned out to have inserted
  // the project even though the request failed, so the list shows it.
  onProjectsChanged?: () => void | Promise<void>;
  // Where focus goes on close when the element focused before opening is no
  // longer in the document (or nothing was focused).
  returnFocusRef?: React.RefObject<HTMLElement>;
}

const inputClass =
  'w-full bg-slate-50 dark:bg-slate-800 border border-slate-300 dark:border-slate-700 rounded-lg px-3 py-2 text-sm text-slate-900 dark:text-slate-100 focus:outline-none focus:border-blue-600 dark:focus:border-blue-400 disabled:opacity-60';

export const ProjectSetupModal: React.FC<Props> = ({
  isOpen,
  directory,
  offerExisting,
  projects,
  onClose,
  onCreated,
  onLinked,
  onProjectsChanged,
  returnFocusRef
}) => {
  const { t } = useTranslation();
  const [mode, setMode] = useState<Mode>('create');
  const [name, setName] = useState('');
  const [prefix, setPrefix] = useState('');
  const [localPath, setLocalPath] = useState(directory);
  const [selectedId, setSelectedId] = useState('');
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);
  // Set when a create inserted the project but failed to save its local
  // path: the created project's name plus the project IDs that existed
  // before that create (to find the new one once the list is re-fetched).
  const [partialCreate, setPartialCreate] = useState<{ name: string; knownIds: string[] } | null>(null);

  // Initial focus target: the "create" mode radio for the `graph-engine ui`
  // entry, the name input for the create-only header entry. The re-seed
  // below always starts in "create" mode, so both exist on open.
  const initialFocusRef = useRef<HTMLInputElement>(null);
  const dialogRef = useModalDialog<HTMLDivElement>({
    isOpen,
    onEscape: onClose,
    initialFocusRef,
    returnFocusFallbackRef: returnFocusRef
  });

  // Re-seed the form whenever the dialog is (re)opened for a directory.
  useEffect(() => {
    if (!isOpen) return;
    setMode('create');
    setName('');
    setPrefix('');
    setLocalPath(directory);
    setSelectedId('');
    setError('');
    setPartialCreate(null);
  }, [isOpen, directory]);

  // After a partial create, preselect the new project once the re-fetched
  // list contains it (`graph-engine ui` entry only -- it has the list).
  // Only a same-named project whose id was NOT in the list before the POST
  // counts: the render before the (async) re-fetch still shows the old list,
  // and a pre-existing project with the same name must never be picked --
  // confirming would overwrite that other project's local path. If the
  // re-fetch fails or the match is ambiguous, nothing is preselected and the
  // user picks from the list.
  useEffect(() => {
    if (!partialCreate || !offerExisting || selectedId) return;
    const fresh = projects.filter(p => p.name === partialCreate.name && !partialCreate.knownIds.includes(p.id));
    if (fresh.length === 1) setSelectedId(fresh[0].id);
  }, [partialCreate, offerExisting, projects, selectedId]);

  if (!isOpen) return null;

  const canChooseExisting = offerExisting && projects.length > 0;
  const selected = projects.find(p => p.id === selectedId) ?? null;
  const willOverwrite = !!selected && !!selected.local_path && selected.local_path !== directory;

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!name.trim() || partialCreate) return;
    setSaving(true);
    setError('');
    const knownIds = projects.map(p => p.id);
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
        const payload = await parseApiError(res);
        if (payload?.code === CREATED_LOCAL_PATH_NOT_SAVED) {
          setPartialCreate({ name, knownIds });
          if (offerExisting) setMode('existing');
          await onProjectsChanged?.();
          return;
        }
        setError(payload ? translateErrorCode(t, payload.code) : t('errors.UNKNOWN'));
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

  const modeButton = (value: Mode, label: string, disabled: boolean, describedBy?: string) => (
    <label
      className={`flex-1 flex items-center gap-2 px-3 py-2 text-xs font-semibold border rounded-lg cursor-pointer transition ${
        mode === value
          ? 'border-blue-500 bg-blue-50 dark:bg-blue-950 text-blue-700 dark:text-blue-300'
          : 'border-slate-300 dark:border-slate-700 text-slate-600 dark:text-slate-400'
      } ${disabled ? 'opacity-50 cursor-not-allowed' : ''}`}
    >
      <input
        ref={value === 'create' ? initialFocusRef : undefined}
        type="radio"
        name="project-setup-mode"
        value={value}
        checked={mode === value}
        disabled={disabled || saving}
        aria-describedby={describedBy}
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

  // Kept apart from `error` (which a radio change clears): the user must
  // keep seeing why "create" is no longer offered.
  const partialCreateBox = partialCreate && (
    <div
      role="alert"
      data-testid="project-setup-partial-create"
      className="p-2.5 bg-red-50 dark:bg-red-950 text-red-700 dark:text-red-300 text-[11px] rounded-lg border border-red-200 dark:border-red-900 break-all"
    >
      {offerExisting
        ? t('projectSetupModal.createdButLocalPathNotSavedChooseExisting', { name: partialCreate.name })
        : t('projectSetupModal.createdButLocalPathNotSavedUseSettings', { name: partialCreate.name })}
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
        className="px-4 py-2 bg-blue-600 hover:bg-blue-700 disabled:opacity-50 rounded-lg text-xs font-semibold text-white shadow-xs transition flex items-center gap-1.5"
      >
        {saving && <Loader2 className="w-3.5 h-3.5 animate-spin" aria-hidden="true" />}
        {submitLabel}
      </button>
    </div>
  );

  return (
    <div className="fixed inset-0 z-50 bg-black/40 backdrop-blur-xs flex items-center justify-center p-4">
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby="project-setup-title"
        tabIndex={-1}
        className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-xl w-full max-w-md p-6 shadow-2xl focus:outline-none"
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
              {modeButton(
                'existing',
                t('projectSetupModal.modeExisting'),
                !canChooseExisting,
                projects.length === 0 ? 'project-setup-no-existing' : undefined
              )}
            </div>
            {projects.length === 0 && (
              <p id="project-setup-no-existing" className="mt-2 text-[11px] text-slate-500 dark:text-slate-400">
                {t('projectSetupModal.noExistingProjects')}
              </p>
            )}
          </fieldset>
        )}

        {mode === 'create' ? (
          <form onSubmit={handleCreate} className="space-y-4">
            {partialCreateBox}
            <div>
              <label htmlFor="project-setup-name" className="block text-xs font-semibold text-slate-700 dark:text-slate-300 mb-1">
                {t('createProjectModal.nameLabel')}
              </label>
              <input
                ref={offerExisting ? undefined : initialFocusRef}
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
            {footer(t('createModal.submit'), !name.trim() || !!partialCreate)}
          </form>
        ) : (
          <form onSubmit={handleChooseExisting} className="space-y-4">
            {partialCreateBox}
            <fieldset>
              <legend className="block text-xs font-semibold text-slate-700 dark:text-slate-300 mb-1">
                {t('projectSetupModal.existingListLabel')}
              </legend>
              <div className="max-h-60 overflow-y-auto space-y-1.5">
                {projects.map(p => {
                  const pathId = `project-setup-local-path-${p.id}`;
                  return (
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
                        aria-describedby={pathId}
                      />
                      <span className="min-w-0">
                        <span className="block font-semibold text-slate-800 dark:text-slate-200 truncate">{p.name}</span>
                        {/* slate-600 keeps >= 4.5:1 on the selected card's blue-50 too (slate-500 was 4.37:1). */}
                        <span
                          id={pathId}
                          data-testid={pathId}
                          className={`block font-mono break-all ${p.local_path ? 'text-slate-600 dark:text-slate-400' : 'text-amber-700 dark:text-amber-400'}`}
                        >
                          {p.local_path || t('projectSetupModal.notSet')}
                        </span>
                      </span>
                    </label>
                  );
                })}
              </div>
            </fieldset>
            {/* The live region is always mounted so the warning appearing
                inside it is announced (a region inserted together with its
                text often is not). */}
            <div role="status" data-testid="project-setup-overwrite-status">
              {willOverwrite && selected && (
                <div className="p-2.5 bg-amber-50 dark:bg-amber-950 text-amber-800 dark:text-amber-300 text-[11px] rounded-lg border border-amber-200 dark:border-amber-800 break-all">
                  {t('projectSetupModal.overwriteWarning', { current: selected.local_path, next: directory })}
                </div>
              )}
            </div>
            {errorBox}
            {footer(t('projectSetupModal.confirmExisting'), !selected)}
          </form>
        )}
      </div>
    </div>
  );
};
