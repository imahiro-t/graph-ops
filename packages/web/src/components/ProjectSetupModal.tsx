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
//
// "Create new" can fail half-way: the project is inserted into the shared DB
// but saving its local path to graph-config.json fails (500
// PROJECT_CREATED_LOCAL_PATH_NOT_SAVED). Pressing "create" again would then
// insert a duplicate project, so instead the dialog re-fetches the project
// list, says the project already exists, and disables "create". With the
// `graph-engine ui` entry it also moves to "choose an existing project" with
// the new project preselected, so confirming saves the path via PATCH; the
// header entry points the user at the settings screen instead.
//
// Focus (WCAG 2.4.3 / 2.1.2): opening moves focus into the dialog, Tab and
// Shift+Tab wrap inside it, Escape closes it, and closing returns focus to
// the element that had it before opening -- or, when that element is gone
// (the header menu item unmounts as the menu closes; the `graph-engine ui`
// entry opens with nothing focused), to `returnFocusRef`.
import React, { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Loader2 } from 'lucide-react';
import { Project } from '../types';
import { apiFetch } from '../lib/apiFetch';
import { errorMessage, localizedApiErrorMessage, parseApiError, translateErrorCode } from '../lib/apiError';
import { useLatest } from '../hooks/useLatest';

type Mode = 'create' | 'existing';

// Error code POST /api/projects returns when the DB insert succeeded but the
// local path could not be saved (see internal/httpserver/projects.go).
const CREATED_LOCAL_PATH_NOT_SAVED = 'PROJECT_CREATED_LOCAL_PATH_NOT_SAVED';

const FOCUSABLE_SELECTOR =
  'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

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
  returnFocusRef?: React.RefObject<HTMLElement | null>;
}

const inputClass =
  'w-full bg-slate-50 dark:bg-slate-800 border border-slate-300 dark:border-slate-700 rounded-lg px-3 py-2 text-sm text-slate-900 dark:text-slate-100 focus:outline-none focus:border-blue-500 disabled:opacity-60';

// Tab stops inside `root`, in DOM order. A radio group is one stop: only its
// checked radio (or its first one when none is checked) is kept.
function tabStops(root: HTMLElement): HTMLElement[] {
  const all = Array.from(root.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR));
  const seenGroups = new Set<string>();
  const stops: HTMLElement[] = [];
  for (const el of all) {
    if (el instanceof HTMLInputElement && el.type === 'radio' && el.name) {
      if (seenGroups.has(el.name)) continue;
      seenGroups.add(el.name);
      const group = all.filter(
        (o): o is HTMLInputElement => o instanceof HTMLInputElement && o.type === 'radio' && o.name === el.name
      );
      stops.push(group.find(r => r.checked) ?? group[0]);
      continue;
    }
    stops.push(el);
  }
  return stops;
}

// Whether `active` is the tab stop `stop` (any radio of the same group counts).
function isStop(active: Element | null, stop: HTMLElement | undefined): boolean {
  if (!active || !stop) return false;
  if (active === stop) return true;
  return (
    active instanceof HTMLInputElement &&
    stop instanceof HTMLInputElement &&
    active.type === 'radio' &&
    stop.type === 'radio' &&
    active.name !== '' &&
    active.name === stop.name
  );
}

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

  const dialogRef = useRef<HTMLDivElement>(null);
  const onCloseRef = useLatest(onClose);
  const offerExistingRef = useLatest(offerExisting);
  const returnFocusRefRef = useLatest(returnFocusRef);

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

  // Focus management while open: initial focus, Tab trap, Escape, and
  // returning focus on close.
  useEffect(() => {
    if (!isOpen) return;
    const previouslyFocused = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    // The RefObject itself is stable; its .current is read at close time.
    const fallbackHolder = returnFocusRefRef.current;
    const dialog = dialogRef.current;
    if (dialog) {
      // The re-seed above always starts in "create" mode.
      const initial = offerExistingRef.current
        ? dialog.querySelector<HTMLElement>('input[name="project-setup-mode"][value="create"]')
        : dialog.querySelector<HTMLElement>('#project-setup-name');
      (initial ?? tabStops(dialog)[0])?.focus();
    }

    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        e.stopPropagation();
        onCloseRef.current();
        return;
      }
      if (e.key !== 'Tab') return;
      const root = dialogRef.current;
      if (!root) return;
      const stops = tabStops(root);
      if (stops.length === 0) {
        e.preventDefault();
        return;
      }
      const first = stops[0];
      const last = stops[stops.length - 1];
      const active = document.activeElement;
      if (!active || !root.contains(active)) {
        e.preventDefault();
        (e.shiftKey ? last : first).focus();
      } else if (e.shiftKey && isStop(active, first)) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && isStop(active, last)) {
        e.preventDefault();
        first.focus();
      }
    };
    document.addEventListener('keydown', onKeyDown, true);

    return () => {
      document.removeEventListener('keydown', onKeyDown, true);
      const fallback = fallbackHolder?.current ?? null;
      const target =
        previouslyFocused && previouslyFocused !== document.body && previouslyFocused.isConnected
          ? previouslyFocused
          : fallback;
      target?.focus();
    };
  }, [isOpen, onCloseRef, offerExistingRef, returnFocusRefRef]);

  // After a partial create, preselect the new project once the re-fetched
  // list contains it (`graph-engine ui` entry only -- it has the list).
  useEffect(() => {
    if (!partialCreate || !offerExisting || selectedId) return;
    const candidates = projects.filter(p => p.name === partialCreate.name);
    const created = candidates.find(p => !partialCreate.knownIds.includes(p.id)) ?? candidates[candidates.length - 1];
    if (created) setSelectedId(created.id);
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
        className="px-4 py-2 bg-blue-600 hover:bg-blue-500 disabled:opacity-50 rounded-lg text-xs font-semibold text-white shadow-xs transition flex items-center gap-1.5"
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
