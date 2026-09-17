// Top-level "設定" modal: lets the user switch between "全体設定" (global,
// user tier) and "プロジェクト単位設定" (project, team tier resolved from
// the selected Project's work_dir) and, within a scope, edit node-type
// instructions / the workflow graph / review-gate configuration / the
// plan, review and report templates. See the
// execution plan (art-2aaa5d92 on DFLT-00010-00001) for the scope/tab
// design rationale and packages/core-go/internal/httpserver/settings.go for
// the backing API.
import React, { useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Settings, X, FolderCog, Globe } from 'lucide-react';
import { Project, SettingsScope } from '../types';
import { useModalDialog } from '../hooks/useModalDialog';
import { NodeTypesEditor } from './settings/NodeTypesEditor';
import { ReviewGatesEditor } from './settings/ReviewGatesEditor';
import { SkillsEditor } from './settings/SkillsEditor';
import { TemplatesEditor } from './settings/TemplatesEditor';
import { AppSettingsEditor } from './settings/AppSettingsEditor';

interface Props {
  isOpen: boolean;
  onClose: () => void;
  projects: Project[];
  currentProject: Project | null;
  // These only matter for the appSettings tab: onProjectsChanged refreshes
  // App.tsx's project list/current-project after an edit or delete;
  // onPaginationPageSizeChanged and onMyNameChanged apply their respective
  // saved values immediately (both take effect without a restart).
  onProjectsChanged: () => void;
  onPaginationPageSizeChanged: (size: number) => void;
  onMyNameChanged: (name: string) => void;
}

type Tab = 'nodeTypes' | 'reviewGates' | 'skills' | 'templates' | 'appSettings';

export const SettingsModal: React.FC<Props> = ({
  isOpen,
  onClose,
  projects,
  currentProject,
  onProjectsChanged,
  onPaginationPageSizeChanged,
  onMyNameChanged
}) => {
  const { t } = useTranslation();
  const [scope, setScope] = useState<SettingsScope>('global');
  const [selectedProjectId, setSelectedProjectId] = useState<string>(currentProject?.id || '');
  const [tab, setTab] = useState<Tab>('nodeTypes');
  // Each tab's editor reports its own dirty state up here so switching tabs
  // or closing the modal while an unsaved edit exists can warn first (see
  // the Gherkin scenario "未保存の変更がある状態でタブやモーダルを閉じよう
  // とすると確認ダイアログが出る").
  const [dirty, setDirty] = useState(false);
  const titleId = useId();
  const projectSelectId = useId();

  // Defined before the early return below so the dialog hook can take
  // handleClose: Escape goes through the same unsaved-changes confirmation
  // as the close (X) button. useModalDialog reads onEscape through a ref, so
  // this per-render function always sees the current `dirty`.
  const confirmDiscardIfDirty = (): boolean => {
    if (!dirty) return true;
    return window.confirm(t('settings.unsavedChanges.confirmMessage'));
  };

  const handleClose = () => {
    if (!confirmDiscardIfDirty()) return;
    setDirty(false);
    onClose();
  };

  // No initialFocusRef: the fixed part of the modal has no text input and
  // the tab contents load asynchronously, so focus lands on the first
  // focusable element (the close button).
  const dialogRef = useModalDialog({ isOpen, onEscape: handleClose });

  if (!isOpen) return null;

  const changeTab = (next: Tab) => {
    if (next === tab) return;
    if (!confirmDiscardIfDirty()) return;
    setDirty(false);
    setTab(next);
  };

  const changeScope = (next: SettingsScope) => {
    if (next === scope) return;
    if (!confirmDiscardIfDirty()) return;
    setDirty(false);
    setScope(next);
  };

  const changeProject = (id: string) => {
    if (id === selectedProjectId) return;
    if (!confirmDiscardIfDirty()) return;
    setDirty(false);
    setSelectedProjectId(id);
  };

  // Editing is disabled entirely when scope=project and no project is
  // selected -- see the Gherkin scenario "プロジェクトが選択されていない状態
  // ではプロジェクト単位設定タブが無効化される".
  const canEdit = scope === 'global' || !!selectedProjectId;

  const tabs: { key: Tab; labelKey: string }[] = [
    { key: 'nodeTypes', labelKey: 'settings.tabs.nodeTypes' },
    { key: 'reviewGates', labelKey: 'settings.tabs.reviewGates' },
    { key: 'skills', labelKey: 'settings.tabs.skills' },
    { key: 'templates', labelKey: 'settings.tabs.templates' },
    { key: 'appSettings', labelKey: 'settings.tabs.appSettings' }
  ];

  return (
    <div className="fixed inset-0 z-50 bg-black/40 backdrop-blur-xs flex items-center justify-center p-4">
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        tabIndex={-1}
        className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-xl w-full max-w-5xl h-[85vh] shadow-2xl overflow-hidden flex flex-col focus:outline-none"
      >
        {/* Header */}
        <div className="flex items-center justify-between px-6 py-4 border-b border-slate-200 dark:border-slate-800 bg-slate-50 dark:bg-slate-800 shrink-0">
          <h2 id={titleId} className="flex items-center gap-2 font-bold text-base text-slate-800 dark:text-slate-200">
            <Settings className="w-5 h-5 text-slate-600 dark:text-slate-400" aria-hidden="true" />
            {t('settings.modalTitle')}
          </h2>
          <button
            type="button"
            onClick={handleClose}
            aria-label={t('common.closeDialog')}
            className="p-1 hover:bg-slate-200 dark:hover:bg-slate-700 rounded-lg text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200 transition"
          >
            <X className="w-5 h-5" aria-hidden="true" />
          </button>
        </div>

        {/* Scope switcher */}
        <div className="flex items-center gap-3 px-6 py-3 border-b border-slate-200 dark:border-slate-800 shrink-0 flex-wrap">
          <span className="text-xs font-semibold text-slate-600 dark:text-slate-400">{t('settings.scope.label')}</span>
          <div className="inline-flex rounded-lg border border-slate-300 dark:border-slate-700 overflow-hidden text-xs font-medium">
            <button
              onClick={() => changeScope('global')}
              className={`px-3 py-1.5 flex items-center gap-1.5 transition ${
                scope === 'global' ? 'bg-blue-600 text-white' : 'bg-white dark:bg-slate-900 text-slate-600 dark:text-slate-400 hover:bg-slate-50 dark:hover:bg-slate-800'
              }`}
            >
              <Globe className="w-3.5 h-3.5" /> {t('settings.scope.global')}
            </button>
            <button
              onClick={() => changeScope('project')}
              className={`px-3 py-1.5 flex items-center gap-1.5 border-l border-slate-300 dark:border-slate-700 transition ${
                scope === 'project' ? 'bg-blue-600 text-white' : 'bg-white dark:bg-slate-900 text-slate-600 dark:text-slate-400 hover:bg-slate-50 dark:hover:bg-slate-800'
              }`}
            >
              <FolderCog className="w-3.5 h-3.5" /> {t('settings.scope.project')}
            </button>
          </div>

          {scope === 'project' && (
            <div className="flex items-center gap-2">
              <label htmlFor={projectSelectId} className="text-xs font-semibold text-slate-600 dark:text-slate-400">
                {t('settings.scope.projectLabel')}
              </label>
              <select
                id={projectSelectId}
                value={selectedProjectId}
                onChange={e => changeProject(e.target.value)}
                className="px-2.5 py-1.5 rounded-lg bg-white dark:bg-slate-900 border border-slate-300 dark:border-slate-700 text-xs text-slate-700 dark:text-slate-300"
              >
                <option value="">{t('settings.scope.projectPlaceholder')}</option>
                {projects.map(p => (
                  <option key={p.id} value={p.id}>{p.name}</option>
                ))}
              </select>
            </div>
          )}
        </div>

        {scope === 'project' && !selectedProjectId && (
          <div className="mx-6 mt-3 p-2.5 bg-amber-50 dark:bg-amber-950 text-amber-800 dark:text-amber-300 text-[11px] rounded-lg border border-amber-200 dark:border-amber-800 shrink-0">
            {t('settings.scope.noProjectSelected')}
          </div>
        )}

        {/* Tabs */}
        <div className="flex items-center gap-1 px-6 pt-3 border-b border-slate-200 dark:border-slate-800 shrink-0">
          {tabs.map(tb => (
            <button
              key={tb.key}
              onClick={() => changeTab(tb.key)}
              className={`px-3 py-2 text-xs font-semibold rounded-t-lg border-b-2 transition ${
                tab === tb.key
                  ? 'border-blue-600 text-blue-700 dark:text-blue-400'
                  : 'border-transparent text-slate-500 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-300'
              }`}
            >
              {t(tb.labelKey)}
            </button>
          ))}
        </div>

        {/* Tab content */}
        <div className="flex-1 min-h-0 overflow-hidden p-6">
          {tab === 'nodeTypes' && (
            <NodeTypesEditor scope={scope} projectId={selectedProjectId} canEdit={canEdit} onDirtyChange={setDirty} />
          )}
          {tab === 'reviewGates' && (
            <ReviewGatesEditor scope={scope} projectId={selectedProjectId} canEdit={canEdit} onDirtyChange={setDirty} />
          )}
          {tab === 'skills' && (
            <SkillsEditor scope={scope} projectId={selectedProjectId} canEdit={canEdit} onDirtyChange={setDirty} />
          )}
          {tab === 'templates' && (
            <TemplatesEditor scope={scope} projectId={selectedProjectId} canEdit={canEdit} onDirtyChange={setDirty} />
          )}
          {tab === 'appSettings' && (
            <AppSettingsEditor
              scope={scope}
              projects={projects}
              onDirtyChange={setDirty}
              onProjectsChanged={onProjectsChanged}
              onPaginationPageSizeChanged={onPaginationPageSizeChanged}
              onMyNameChanged={onMyNameChanged}
            />
          )}
        </div>
      </div>
    </div>
  );
};
