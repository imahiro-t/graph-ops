// Top-level "設定" modal: edit node-type instructions / review-gate
// configuration / skill instructions / the plan, review and report templates
// / labels / app settings -- one tab each, see `tabs` below. (There is no
// workflow-graph tab: the skeleton is fixed by the plugin default and only
// review gates are overridable -- see internal/config.Merge's
// WORKFLOW_NODES_LOCKED.) See
// packages/core-go/internal/httpserver/settings.go for the backing API.
//
// There is no scope switcher. Every tab here edits the one user tier
// ($HOME/.graph-ops, or userExtensionsDir); per-project settings were
// removed in DFLT-00124, and a team shares settings by pointing
// teamExtensionsDir at a shared directory. The app settings tab can name that
// directory (DFLT-00153), but no tab here edits or previews its contents,
// since it is shared state curated outside the app. Labels are the exception, and the reason the modal still takes the
// project list: they are per-project DB rows, so that tab carries a project
// selector of its own.
import React, { useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Settings, X } from 'lucide-react';
import { Project } from '../types';
import { useModalDialog } from '../hooks/useModalDialog';
import { NodeTypesEditor } from './settings/NodeTypesEditor';
import { ReviewGatesEditor } from './settings/ReviewGatesEditor';
import { SkillsEditor } from './settings/SkillsEditor';
import { TemplatesEditor } from './settings/TemplatesEditor';
import { AppSettingsEditor } from './settings/AppSettingsEditor';
import { LabelsEditor } from './settings/LabelsEditor';
import { AutopilotSettingsEditor } from './settings/AutopilotSettingsEditor';

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
  // The labels tab (DFLT-00084) calls this after every saved label change so
  // App.tsx can re-fetch its label list and tickets.
  onLabelsChanged?: () => void;
}

type Tab = 'nodeTypes' | 'reviewGates' | 'skills' | 'templates' | 'labels' | 'autopilot' | 'appSettings';

export const SettingsModal: React.FC<Props> = ({
  isOpen,
  onClose,
  projects,
  currentProject,
  onProjectsChanged,
  onPaginationPageSizeChanged,
  onMyNameChanged,
  onLabelsChanged
}) => {
  const { t } = useTranslation();
  const [tab, setTab] = useState<Tab>('nodeTypes');
  // Each tab's editor reports its own dirty state up here so switching tabs
  // or closing the modal while an unsaved edit exists can warn first (see
  // the Gherkin scenario "未保存の変更がある状態でタブやモーダルを閉じよう
  // とすると確認ダイアログが出る").
  const [dirty, setDirty] = useState(false);
  const titleId = useId();

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

  const tabs: { key: Tab; labelKey: string }[] = [
    { key: 'nodeTypes', labelKey: 'settings.tabs.nodeTypes' },
    { key: 'reviewGates', labelKey: 'settings.tabs.reviewGates' },
    { key: 'skills', labelKey: 'settings.tabs.skills' },
    { key: 'templates', labelKey: 'settings.tabs.templates' },
    { key: 'labels', labelKey: 'settings.tabs.labels' },
    { key: 'autopilot', labelKey: 'settings.tabs.autopilot' },
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
          {tab === 'nodeTypes' && <NodeTypesEditor onDirtyChange={setDirty} />}
          {tab === 'reviewGates' && <ReviewGatesEditor onDirtyChange={setDirty} />}
          {tab === 'skills' && <SkillsEditor onDirtyChange={setDirty} />}
          {tab === 'templates' && <TemplatesEditor onDirtyChange={setDirty} />}
          {/* Labels (DFLT-00084) are per-project DB rows rather than files in
              a settings tier, so this tab picks its own project (DFLT-00124,
              completion criterion 9) -- starting from the one the app has
              selected. */}
          {tab === 'labels' && (
            <LabelsEditor
              projects={projects}
              initialProjectId={currentProject?.id ?? ''}
              onLabelsChanged={onLabelsChanged}
            />
          )}
          {/* Autopilot settings (DFLT-00142) are per project too, but are
              local to this environment (the home config's
              autopilotSettings.<projectId>); the tab edits the project the
              app has selected. */}
          {tab === 'autopilot' && (
            <AutopilotSettingsEditor
              projectId={currentProject?.id ?? ''}
              projectName={currentProject?.name}
              onDirtyChange={setDirty}
            />
          )}
          {tab === 'appSettings' && (
            <AppSettingsEditor
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
