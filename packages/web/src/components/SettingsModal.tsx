// Top-level "設定" modal: edit node-type instructions / review-gate
// configuration / skill instructions / the plan, review and report templates
// / labels / app settings -- one tab each, see SETTINGS_TABS below. (There is no
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
// since it is shared state curated outside the app. Labels are the
// exception, and the reason the modal still takes the project list: they are
// per-project DB rows, so that tab carries a project selector of its own.
import React, { useEffect, useId, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Settings, X } from 'lucide-react';
import { Project } from '../types';
import { useModalDialog } from '../hooks/useModalDialog';
import { useConfirmDialog } from '../hooks/useConfirmDialog';
import { unsavedChangesConfirmOptions } from './settings/unsavedChangesConfirm';
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

// The tabs, in display order. Their labels are settings.tabs.<key>.
const SETTINGS_TABS = ['nodeTypes', 'reviewGates', 'skills', 'templates', 'labels', 'autopilot', 'appSettings'] as const;
type Tab = (typeof SETTINGS_TABS)[number];

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
  // The tabs follow the WAI-ARIA tabs pattern, like the artifact tabs in
  // TicketItem (DFLT-00243 / DFLT-00254): a named tablist, roving tabIndex,
  // and one tabpanel labelled by the selected tab. useId keeps the ids
  // unique if two modals are ever rendered at once.
  const tabsIdBase = useId();
  const tabId = (key: Tab) => `${tabsIdBase}-tab-${key}`;
  const panelId = `${tabsIdBase}-panel`;
  const tabRefs = useRef<Partial<Record<Tab, HTMLButtonElement | null>>>({});
  // Set when a key (arrow / Home / End) asked for a tab change. The focus
  // move to the new tab is done in the effect below, after the change is
  // committed, rather than right after changeTab resolves: when an unsaved
  // edit made changeTab ask first, closing the confirmation puts focus back
  // on the tab that was pressed (useModalDialog's cleanup), and that must
  // happen before -- not after -- focus moves to the new tab. React runs the
  // unmounted dialog's cleanup before this component's new effects.
  const focusTabAfterChangeRef = useRef(false);
  useEffect(() => {
    if (!focusTabAfterChangeRef.current) return;
    focusTabAfterChangeRef.current = false;
    tabRefs.current[tab]?.focus();
  }, [tab]);

  // The unsaved-changes question is the in-app ConfirmDialog (DFLT-00148),
  // opened on top of this modal. useModalDialog lets only the topmost dialog
  // handle keys, so while it is open Escape and Tab belong to it, and closing
  // it puts focus back on the button in this modal that asked (a tab, or the
  // close button).
  const { confirm, confirmDialog } = useConfirmDialog();

  // Defined before the early return below so the dialog hook can take
  // handleClose: Escape goes through the same unsaved-changes confirmation
  // as the close (X) button. useModalDialog reads onEscape through a ref, so
  // this per-render function always sees the current `dirty`.
  const confirmDiscardIfDirty = async (): Promise<boolean> => {
    if (!dirty) return true;
    return confirm(unsavedChangesConfirmOptions(t, 'settings-discard-confirm'));
  };

  const handleClose = async () => {
    if (!(await confirmDiscardIfDirty())) return;
    setDirty(false);
    onClose();
  };

  // No initialFocusRef: the fixed part of the modal has no text input and
  // the tab contents load asynchronously, so focus lands on the first
  // focusable element (the close button).
  const dialogRef = useModalDialog({
    isOpen,
    onEscape: () => {
      void handleClose();
    }
  });

  if (!isOpen) return null;

  // Resolves to whether the tab was switched: false when `next` is already
  // selected or the user kept an unsaved edit in the confirmation.
  const changeTab = async (next: Tab): Promise<boolean> => {
    if (next === tab) return false;
    if (!(await confirmDiscardIfDirty())) return false;
    setDirty(false);
    setTab(next);
    return true;
  };

  // Arrow keys (wrapping), Home and End select a tab and move focus to it,
  // per the APG tabs pattern with automatic activation -- but through
  // changeTab, so an unsaved edit still asks first. If the user keeps the
  // edit, the selection stays and focus returns to the pressed tab. Modified
  // keys are left alone so browser shortcuts such as Alt+Left still work.
  const handleTabKeyDown = (event: React.KeyboardEvent<HTMLButtonElement>) => {
    if (event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) return;
    const index = SETTINGS_TABS.indexOf(tab);
    const last = SETTINGS_TABS.length - 1;
    let next: number;
    switch (event.key) {
      case 'ArrowRight':
        next = index === last ? 0 : index + 1;
        break;
      case 'ArrowLeft':
        next = index === 0 ? last : index - 1;
        break;
      case 'Home':
        next = 0;
        break;
      case 'End':
        next = last;
        break;
      default:
        return;
    }
    event.preventDefault();
    focusTabAfterChangeRef.current = true;
    void changeTab(SETTINGS_TABS[next]).then(switched => {
      // Nothing changed (already selected, or the edit was kept): drop the
      // flag so a later click-driven change does not move focus by itself.
      if (!switched) focusTabAfterChangeRef.current = false;
    });
  };

  // Overlay and panel follow ConfirmDialog (DFLT-00233 / DFLT-00254): the
  // overlay scrolls vertically and the panel is centred with `m-auto`
  // instead of `items-center` / `justify-center`, so when the panel is
  // taller than the window it starts at the top of the scroll area and the
  // title, close button and tabs stay reachable. `min-w-0` lets it shrink to
  // the overlay's width. The panel stays the overlay's direct child
  // ({confirmDialog} portals itself to document.body).
  //
  // The panel keeps its 85vh height, with the tab content scrolling inside
  // it, but never gets shorter than 32rem: at a 200% default font on a short
  // 320px-wide screen 85vh would squeeze the tab content to nothing below
  // the header and the wrapped tab row (and `overflow-hidden` would clip
  // them). The minimum is in rem so it grows with the font size. Measured in
  // Chromium at a 32px (200%) default font on a 320px-wide screen: the
  // header takes about 4rem and the wrapped tab row about 19rem, which
  // leaves the tab content about 9rem (with the paddings below tightened
  // under 15rem, the same query as DFLT-00253). At 100% the minimum is 512px,
  // so only a window shorter than about 600px gets a scrolling overlay. A
  // panel taller than the window is reached by scrolling the overlay.
  return (
    <div className="fixed inset-0 z-50 bg-black/40 backdrop-blur-xs flex p-4 overflow-y-auto overscroll-contain">
      {confirmDialog}
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        tabIndex={-1}
        className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-xl w-full max-w-5xl h-[85vh] min-h-[32rem] m-auto min-w-0 shadow-2xl overflow-hidden flex flex-col focus:outline-none"
      >
        {/* Header */}
        <div className="flex items-center justify-between gap-2 px-6 [@media(max-width:15rem)]:px-3 py-4 border-b border-slate-200 dark:border-slate-800 bg-slate-50 dark:bg-slate-800 shrink-0">
          <h2 id={titleId} className="flex items-center gap-2 min-w-0 break-words font-bold text-base text-slate-800 dark:text-slate-200">
            <Settings className="w-5 h-5 shrink-0 text-slate-600 dark:text-slate-400" aria-hidden="true" />
            {t('settings.modalTitle')}
          </h2>
          <button
            type="button"
            onClick={() => void handleClose()}
            aria-label={t('common.closeDialog')}
            className="shrink-0 p-1 hover:bg-slate-200 dark:hover:bg-slate-700 rounded-lg text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200 transition"
          >
            <X className="w-5 h-5" aria-hidden="true" />
          </button>
        </div>

        {/* Tabs. The row wraps (and a tab is at most as wide as the row) so
            that at 200% on a 320px screen the seven tabs are not cut off by
            the panel's overflow-hidden. The look (underline, colours) is
            unchanged. */}
        <div
          role="tablist"
          aria-label={t('settings.tabListLabel')}
          className="flex flex-wrap items-center gap-1 px-6 [@media(max-width:15rem)]:px-3 pt-3 border-b border-slate-200 dark:border-slate-800 shrink-0"
        >
          {SETTINGS_TABS.map(key => {
            const selected = tab === key;
            return (
              <button
                key={key}
                ref={el => {
                  tabRefs.current[key] = el;
                }}
                type="button"
                role="tab"
                id={tabId(key)}
                aria-selected={selected}
                aria-controls={panelId}
                tabIndex={selected ? 0 : -1}
                onClick={() => void changeTab(key)}
                onKeyDown={handleTabKeyDown}
                className={`max-w-full break-words px-3 py-2 text-xs font-semibold rounded-t-lg border-b-2 transition ${
                  selected
                    ? 'border-blue-600 text-blue-700 dark:text-blue-400'
                    : 'border-transparent text-slate-500 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-300'
                }`}
              >
                {t(`settings.tabs.${key}`)}
              </button>
            );
          })}
        </div>

        {/* Tab content: one tabpanel labelled by the selected tab. It is
            focusable (tabIndex 0, as the APG recommends when a panel may
            hold nothing focusable yet -- the editors load asynchronously);
            the ring shows only on keyboard focus and is inset so the
            panel's overflow-hidden does not clip it. */}
        <div
          role="tabpanel"
          id={panelId}
          aria-labelledby={tabId(tab)}
          tabIndex={0}
          className="flex-1 min-h-0 overflow-hidden p-6 [@media(max-width:15rem)]:p-3 focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-blue-500 dark:focus-visible:ring-blue-400"
        >
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
