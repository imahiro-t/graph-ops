import React, { useCallback, useState, useEffect, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Search,
  RotateCw,
  Plus,
  Terminal,
  Loader2,
  FolderOpen,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  Check,
  Settings as SettingsIcon,
  Sun,
  Moon,
  MonitorCog,
  Languages
} from 'lucide-react';
import { Ticket, TicketDetail, TicketStatus, TicketPriority, Project, TICKET_STATUSES, TICKET_PRIORITIES } from './types';
import { getStatusMeta, normalizeTicketStatus } from './statusMeta';
import { getPriorityMeta, normalizeTicketPriority } from './priorityMeta';
import { TicketItem } from './components/TicketItem';
import { ClaudeRunnerModal } from './components/ClaudeRunnerModal';
import { SettingsModal } from './components/SettingsModal';
import { CreateTicketModal } from './components/CreateTicketModal';
import { useClaudeLaunch } from './hooks/useClaudeLaunch';
import { useTheme, ThemePreference } from './hooks/useTheme';
import { formatTime } from './i18n/formatDate';
import { localizedApiErrorMessage, errorMessage } from './lib/apiError';
import { apiFetch } from './lib/apiFetch';
import { fetchAppSettings } from './lib/settingsApi';
import { useLatest } from './hooks/useLatest';

// Cycles through the three-way theme preference in a fixed order, used by
// the header toggle button (light -> dark -> system -> light -> ...).
const NEXT_THEME: Record<ThemePreference, ThemePreference> = {
  light: 'dark',
  dark: 'system',
  system: 'light'
};

const THEME_ICON: Record<ThemePreference, React.FC<{ className?: string }>> = {
  light: Sun,
  dark: Moon,
  system: MonitorCog
};

export const App: React.FC = () => {
  const { t, i18n } = useTranslation();
  // See src/hooks/useLatest.ts -- keeps fetchTicketsPerPage below
  // insensitive to language changes (F-1/L-2: switching language must not
  // re-run the startup effect that calls it).
  const tRef = useLatest(t);
  const { preference: themePreference, setPreference: setThemePreference } = useTheme();
  const [tickets, setTickets] = useState<TicketDetail[]>([]);
  const [loading, setLoading] = useState(false);
  const [expandedTicketIds, setExpandedTicketIds] = useState<Set<string>>(new Set());

  // The viewer's own display name (GET/PUT /api/settings/app's "myName"),
  // used by TicketItem's "assign to me"/"unassign" buttons. Empty until the
  // fetch resolves or when nothing has been configured yet, which hides
  // those buttons.
  const [myName, setMyName] = useState('');

  // Project scoping (GET/POST /api/projects, GET/PUT /api/current-project):
  // the ticket list the server returns is always scoped to whichever
  // project is "current" (see fetchAllTickets's plain `/api/tickets` call --
  // project filtering is the server's default behavior, not something the
  // client requests explicitly). currentProject is null both before the
  // initial GET /api/current-project resolves and, permanently, on an
  // install that has never created a project yet.
  const [projects, setProjects] = useState<Project[]>([]);
  const [currentProject, setCurrentProject] = useState<Project | null>(null);
  const [isProjectMenuOpen, setIsProjectMenuOpen] = useState(false);
  const [isCreateProjectOpen, setIsCreateProjectOpen] = useState(false);
  const [newProjectName, setNewProjectName] = useState('');
  const [newProjectPrefix, setNewProjectPrefix] = useState('');
  const [newProjectWorkDir, setNewProjectWorkDir] = useState('');
  const [projectFormError, setProjectFormError] = useState('');
  const [isSavingProject, setIsSavingProject] = useState(false);

  // Filters
  const [filterQuery, setFilterQuery] = useState('');
  // Status multi-select: starts with every status selected (the full list,
  // same as before this filter existed). Not persisted -- a reload resets it.
  // Unchecking only DONE reproduces the old "hide completed" checkbox.
  const [filterStatuses, setFilterStatuses] = useState<TicketStatus[]>(() => [...TICKET_STATUSES]);
  const [isStatusMenuOpen, setIsStatusMenuOpen] = useState(false);
  const statusMenuButtonRef = useRef<HTMLButtonElement>(null);
  const toggleFilterStatus = (s: TicketStatus) => {
    // Re-derive from TICKET_STATUSES when adding so the array always stays in
    // display order, whatever order the user clicked in.
    setFilterStatuses(prev =>
      prev.includes(s) ? prev.filter(x => x !== s) : TICKET_STATUSES.filter(x => x === s || prev.includes(x))
    );
    setPage(1);
  };

  // Assignee single-select (DFLT-00047): null means no filter ("all"). Same
  // trigger-button + panel structure as the status filter above (completion
  // criterion: same look/feel), but single-select rather than a checkbox
  // group -- "pick one assignee to narrow the list to" reads as one choice,
  // not several to combine. Not persisted, same as filterStatuses.
  const [filterAssignee, setFilterAssignee] = useState<string | null>(null);
  const [isAssigneeMenuOpen, setIsAssigneeMenuOpen] = useState(false);
  const assigneeMenuButtonRef = useRef<HTMLButtonElement>(null);
  // Options are derived from whichever tickets are currently loaded, not a
  // fixed list (there is no server-side catalog of assignee names) --
  // unlike TICKET_STATUSES, this can shrink or grow as tickets are
  // (un)assigned. If the selected name later drops out of this list, the
  // filter itself is left alone (still narrows to that now-invisible name,
  // matching how filterStatuses behaves) -- only the panel's option list
  // reflects the current ticket set.
  const assigneeOptions = Array.from(
    new Set(tickets.map(t => t.assignee).filter((v): v is string => !!v))
  ).sort();
  const selectFilterAssignee = (name: string | null) => {
    setFilterAssignee(name);
    setIsAssigneeMenuOpen(false);
    setPage(1);
    // Closing the panel unmounts the radio the user just interacted with, so
    // without this the focus that was on it simply vanishes (falls back to
    // <body>) instead of moving anywhere -- same failure the Escape-key
    // handler above already guards against with the same focus() call.
    assigneeMenuButtonRef.current?.focus();
  };

  // Priority multi-select (DFLT-00048). Same trigger + panel + checkbox-group
  // structure as the status filter above -- several priorities can be
  // combined, matching filterStatuses rather than filterAssignee's
  // single-choice radio group. 'UNSET' is a filter-only pseudo-value (there
  // is no such TicketPriority member -- see types.ts) standing in for
  // t.priority being null/undefined, so the "no priority set" bucket can be
  // selected/deselected exactly like the three real levels. Starts with
  // every value selected (including UNSET), matching filterStatuses's
  // "everything visible until narrowed" default.
  const PRIORITY_FILTER_VALUES: readonly (TicketPriority | 'UNSET')[] = [...TICKET_PRIORITIES, 'UNSET'];
  const [filterPriorities, setFilterPriorities] = useState<(TicketPriority | 'UNSET')[]>(() => [
    ...PRIORITY_FILTER_VALUES
  ]);
  const [isPriorityMenuOpen, setIsPriorityMenuOpen] = useState(false);
  const priorityMenuButtonRef = useRef<HTMLButtonElement>(null);
  const toggleFilterPriority = (p: TicketPriority | 'UNSET') => {
    // Re-derive from PRIORITY_FILTER_VALUES when adding so the array always
    // stays in display order, matching toggleFilterStatus.
    setFilterPriorities(prev =>
      prev.includes(p) ? prev.filter(x => x !== p) : PRIORITY_FILTER_VALUES.filter(x => x === p || prev.includes(x))
    );
    setPage(1);
  };

  // Pagination -- ticket details (nodes/edges/artifacts) are fetched for
  // every ticket up front (see fetchAllTickets), so this is purely a
  // client-side slice of the already-filtered list, not a server-paged
  // fetch. Reset to page 1 whenever a filter changes so the user never
  // lands on a stale, now out-of-range page. The page size itself is a
  // "全体設定" app-setting (GET /api/settings/app); unlike that endpoint's
  // other fields, it takes effect immediately on save (see
  // onPaginationPageSizeChanged below) since it's pure frontend behavior
  // with nothing to restart.
  const [ticketsPerPage, setTicketsPerPage] = useState(10);
  const [page, setPage] = useState(1);
  // Stored as a Date (not a pre-formatted string) so the displayed text
  // re-formats itself if the UI language changes without a refetch.
  const [lastFetchedAt, setLastFetchedAt] = useState<Date | null>(null);

  // Modals
  const [isCreateOpen, setIsCreateOpen] = useState(false);
  const [isClaudeGlobalOpen, setIsClaudeGlobalOpen] = useState(false);
  const [isSettingsOpen, setIsSettingsOpen] = useState(false);

  // Fetch tickets and their details
  const fetchAllTickets = async () => {
    setLoading(true);
    try {
      const res = await fetch('/api/tickets');
      const ticketSummaries: Ticket[] = await res.json();

      const details = await Promise.all(
        ticketSummaries.map(async t => {
          try {
            const dRes = await fetch(`/api/tickets/${t.id}`);
            return await dRes.json();
          } catch {
            return { ...t, nodes: [], edges: [], artifacts: [] };
          }
        })
      );

      setTickets(details);
      setLastFetchedAt(new Date());
    } catch (e) {
      console.error('Failed to load tickets', e);
    } finally {
      setLoading(false);
    }
  };

  // Fetches the full project list (for the project switcher menu).
  const fetchProjects = async () => {
    try {
      const res = await fetch('/api/projects');
      if (!res.ok) throw new Error(`GET /api/projects: ${res.status}`);
      setProjects(await res.json());
    } catch (e) {
      console.error('Failed to load projects', e);
      setProjects([]);
    }
  };

  // Fetches whichever project is currently selected (GET /api/current-project
  // returns JSON null before any project has ever been created/selected).
  // This is only ever used to *restore* state on load -- it must never
  // trigger a Claude Code terminal launch (see switchToProject, which is the
  // only path that does), or every page refresh would pop open a terminal.
  const fetchCurrentProject = async () => {
    try {
      const res = await fetch('/api/current-project');
      if (!res.ok) throw new Error(`GET /api/current-project: ${res.status}`);
      const data = await res.json();
      setCurrentProject(data ?? null);
    } catch (e) {
      console.error('Failed to load current project', e);
      setCurrentProject(null);
    }
  };

  // Refreshes both the project list and whichever project is currently
  // selected -- passed to the settings UI's project-management section so an
  // edited/deleted project (which may have been the current one, cleared
  // server-side by DeleteProject) is reflected everywhere else in the app
  // immediately, not just after a manual page reload.
  const refreshProjects = async () => {
    await Promise.all([fetchProjects(), fetchCurrentProject()]);
  };

  // Fetches the "全体設定" app-settings this component needs: how many
  // tickets to show per page, and the viewer's own display name (myName).
  // Only read once at startup here -- after that, the settings UI applies a
  // change directly via setTicketsPerPage/setMyName (see
  // onPaginationPageSizeChanged/onMyNameChanged), since re-fetching would
  // otherwise show the pre-save value until the next full reload.
  // useCallback(..., [tRef]) stabilizes this function's identity (tRef never
  // changes) so including it in the startup effect's dependency array below
  // does not turn a language switch into a second startup fetch (L-2).
  const fetchTicketsPerPage = useCallback(async () => {
    try {
      const data = await fetchAppSettings(tRef.current);
      setTicketsPerPage(data.effective.paginationPageSize);
      setMyName(data.file.myName || '');
    } catch (e) {
      console.error('Failed to load app settings', e);
    }
  }, [tRef]);

  useEffect(() => {
    fetchAllTickets();
    fetchProjects();
    fetchCurrentProject();
    fetchTicketsPerPage();
    // Ticket/graph changes now happen in an external terminal the app can't
    // see directly (no more SSE stream to react to), so poll periodically to
    // keep the dashboard reasonably live.
    const interval = setInterval(fetchAllTickets, 15000);
    return () => clearInterval(interval);
  }, [fetchTicketsPerPage]);

  // Consumes the `?newProject=1&workDir=<dir>` query the `graph-engine ui`
  // CLI command (the `/ui` slash command's backend) appends to the root URL
  // when the current directory doesn't match any registered project's
  // work_dir: auto-open the create-project dialog with that directory
  // pre-filled, instead of requiring the user to click "new project" and
  // retype the path themselves. Runs once on mount, and strips the query
  // from the URL immediately after reading it (via history.replaceState) so
  // a later manual reload of the same URL doesn't re-trigger the dialog.
  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    if (params.get('newProject') !== '1') return;

    const workDir = params.get('workDir');
    setIsCreateProjectOpen(true);
    if (workDir) {
      setNewProjectWorkDir(workDir);
    }

    const url = new URL(window.location.href);
    url.searchParams.delete('newProject');
    url.searchParams.delete('workDir');
    window.history.replaceState({}, '', url.pathname + url.search + url.hash);
  }, []);

  // switchToProject only changes which project is current -- it must never
  // launch a terminal on its own. Switching used to also open an external
  // Claude Code terminal in the project's work_dir, but that surprised users
  // (a terminal popping open just from picking a project in the switcher),
  // so the only path that launches a terminal now is an explicit action
  // like the create-ticket flow.
  const switchToProject = async (project: Project) => {
    setIsProjectMenuOpen(false);
    // Re-selecting the already-active project is a no-op.
    if (project.id === currentProject?.id) {
      return;
    }
    try {
      const res = await apiFetch('/api/current-project', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ project_id: project.id })
      });
      if (!res.ok) throw new Error(await localizedApiErrorMessage(t, res));
    } catch (e) {
      console.error('Failed to switch current project', e);
      return;
    }
    setCurrentProject(project);
    setPage(1);
    fetchAllTickets();
  };

  const handleCreateProjectSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!newProjectName.trim() || !newProjectWorkDir.trim()) return;
    setIsSavingProject(true);
    setProjectFormError('');
    try {
      const res = await apiFetch('/api/projects', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          name: newProjectName,
          prefix: newProjectPrefix || undefined,
          work_dir: newProjectWorkDir
        })
      });
      if (!res.ok) {
        setProjectFormError(await localizedApiErrorMessage(t, res));
        return;
      }
      const created: Project = await res.json();
      setProjects(prev => [...prev, created]);
      setIsCreateProjectOpen(false);
      setNewProjectName('');
      setNewProjectPrefix('');
      setNewProjectWorkDir('');
      await switchToProject(created);
    } catch (e) {
      setProjectFormError(errorMessage(e, t('errors.UNKNOWN')));
    } finally {
      setIsSavingProject(false);
    }
  };

  const handleToggleExpand = (id: string) => {
    setExpandedTicketIds(prev => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  // Ticket creation goes through the create-ticket skill (opened in an
  // external, interactive terminal via /api/claude/launch) rather than
  // POSTing to /api/tickets directly, so it's always the skill - not a raw
  // form-to-REST path - that decides what a "created ticket" looks like.
  // It also deliberately does NOT trigger refine/graph-building afterward:
  // those are separate, later steps in the ticket lifecycle now.
  const { isLaunching: isCreating, lastMessage: createStatus, launch: runCreateTicket, reset: resetCreateStatus } = useClaudeLaunch(fetchAllTickets);

  // Empty/whitespace-only requests never reach here: CreateTicketModal guards
  // both the button and the Cmd/Ctrl+Enter path. Returns whether the launch
  // succeeded so the modal keeps the request text after a failure.
  const handleCreateTicket = async (request: string): Promise<boolean> => {
    // Assignment is deliberately never decided at creation time -- it's set
    // afterward via TicketItem's "assign to me" button (see
    // ticket.assignee), so the prompt never mentions one. The title and
    // description aren't decided here either: the create-ticket skill works
    // them out from this free-form request and confirms them with the user.
    const prompt = t('claudePrompts.createTicket', { request });
    const succeeded = await runCreateTicket(prompt, undefined, currentProject?.id);
    // Close automatically once the terminal has launched, instead of
    // leaving the user to hit the (now-relabeled) "close" button -- a
    // failed launch keeps the modal open so the status message is visible.
    if (succeeded) {
      resetCreateStatus();
      setIsCreateOpen(false);
    }
    return succeeded;
  };

  // Filter calculations
  const filteredTickets = tickets.filter(t => {
    // Unexpected DB values count as TODO, matching the badge (statusMeta.ts).
    if (!filterStatuses.includes(normalizeTicketStatus(t.status))) return false;
    if (filterAssignee && t.assignee !== filterAssignee) return false;
    if (!filterPriorities.includes(normalizeTicketPriority(t.priority) ?? 'UNSET')) return false;
    if (filterQuery) {
      const q = filterQuery.toLowerCase();
      const matchId = t.id.toLowerCase().includes(q);
      const matchTitle = t.title.toLowerCase().includes(q);
      const matchDesc = (t.description || '').toLowerCase().includes(q);
      if (!matchId && !matchTitle && !matchDesc) return false;
    }
    return true;
  });

  const totalPages = Math.max(1, Math.ceil(filteredTickets.length / ticketsPerPage));
  // Clamp rather than reset via effect: a shorter list (a filter narrowing,
  // or a ticket getting deleted) can leave `page` past the new last page,
  // and this settles it back on render without an extra state update.
  const currentPage = Math.min(page, totalPages);
  const pagedTickets = filteredTickets.slice(
    (currentPage - 1) * ticketsPerPage,
    currentPage * ticketsPerPage
  );

  // Metrics
  const totalCount = tickets.length;
  const inProgressCount = tickets.filter(t => t.status === 'IN PROGRESS').length;
  const inReviewCount = tickets.filter(t => t.status === 'IN REVIEW').length;
  const doneCount = tickets.filter(t => t.status === 'DONE').length;
  const totalNodesCount = tickets.reduce((acc, t) => acc + t.nodes.length, 0);
  const doneNodesCount = tickets.reduce(
    (acc, t) => acc + t.nodes.filter(n => n.status === 'DONE').length,
    0
  );

  const ThemeIcon = THEME_ICON[themePreference];
  // i18n.language is normalized to exactly "ja" or "en" (see i18n/index.ts's
  // `load: 'languageOnly'` and `supportedLngs`); the ternary is just a
  // defensive fallback rather than an expectation of a third value.
  const currentLanguage: 'ja' | 'en' = i18n.language === 'ja' ? 'ja' : 'en';

  return (
    <div className="min-h-screen bg-slate-50 dark:bg-slate-950 text-slate-900 dark:text-slate-100 font-sans">
      {/* Top Header */}
      <header className="bg-white dark:bg-slate-900 border-b border-slate-200 dark:border-slate-800 px-6 py-3.5 sticky top-0 z-30 shadow-xs">
        <div className="max-w-7xl mx-auto flex items-center justify-between gap-4">
          <div className="flex items-center gap-2">
            {/* Same file as the favicon, so the two never drift apart. It paints its own indigo
                background, so it stays visible on both light and dark headers without `dark:` variants.
                Decorative: the adjacent "GraphOps" text already names the app. */}
            <img src="/favicon.svg" alt="" aria-hidden="true" className="w-6 h-6 shrink-0" />
            <span className="font-extrabold text-lg tracking-tight text-slate-900 dark:text-slate-100">
              GraphOps
            </span>
            <span className="text-xs text-slate-500 dark:text-slate-400 font-medium">{t('header.subtitle')}</span>
          </div>

          <div className="flex items-center gap-3">
            <div className="relative">
              <button
                onClick={() => setIsProjectMenuOpen(v => !v)}
                className="px-3 py-1.5 rounded-lg bg-white dark:bg-slate-900 hover:bg-slate-50 dark:hover:bg-slate-800 text-xs font-semibold text-slate-700 dark:text-slate-300 border border-slate-300 dark:border-slate-700 flex items-center gap-1.5 shadow-xs transition max-w-[14rem]"
                title={currentProject?.work_dir}
              >
                <FolderOpen className="w-4 h-4 text-slate-500 dark:text-slate-400 shrink-0" />
                <span className="truncate">
                  {currentProject ? currentProject.name : t('projectSwitcher.noProject')}
                </span>
                <ChevronDown className="w-3.5 h-3.5 text-slate-400 shrink-0" />
              </button>

              {isProjectMenuOpen && (
                <>
                  <div className="fixed inset-0 z-40" onClick={() => setIsProjectMenuOpen(false)} />
                  <div className="absolute left-0 mt-1.5 w-64 bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-lg shadow-lg z-50 py-1 text-sm">
                    {projects.length === 0 && (
                      <div className="px-3 py-2 text-xs text-slate-400 dark:text-slate-500">{t('projectSwitcher.empty')}</div>
                    )}
                    {projects.map(p => (
                      <button
                        key={p.id}
                        onClick={() => switchToProject(p)}
                        className="w-full text-left px-3 py-1.5 hover:bg-slate-50 dark:hover:bg-slate-800 flex items-center gap-2 text-slate-700 dark:text-slate-300"
                      >
                        <Check className={`w-3.5 h-3.5 shrink-0 ${p.id === currentProject?.id ? 'text-blue-600 dark:text-blue-400' : 'text-transparent'}`} />
                        <span className="truncate">{p.name}</span>
                        <span className="ml-auto text-[10px] text-slate-400 dark:text-slate-500 font-mono">{p.prefix}</span>
                      </button>
                    ))}
                    <div className="border-t border-slate-100 dark:border-slate-800 mt-1 pt-1">
                      <button
                        onClick={() => {
                          setIsProjectMenuOpen(false);
                          setProjectFormError('');
                          setIsCreateProjectOpen(true);
                        }}
                        className="w-full text-left px-3 py-1.5 hover:bg-slate-50 dark:hover:bg-slate-800 flex items-center gap-2 text-blue-700 dark:text-blue-400 font-medium"
                      >
                        <Plus className="w-3.5 h-3.5" />
                        {t('projectSwitcher.createNew')}
                      </button>
                    </div>
                  </div>
                </>
              )}
            </div>

            <button
              onClick={() => setIsClaudeGlobalOpen(true)}
              className="px-3 py-1.5 rounded-lg bg-white dark:bg-slate-900 hover:bg-slate-50 dark:hover:bg-slate-800 text-xs font-semibold text-indigo-700 dark:text-indigo-400 border border-slate-300 dark:border-slate-700 flex items-center gap-1.5 shadow-xs transition"
            >
              <Terminal className="w-4 h-4 text-indigo-600 dark:text-indigo-400" />
              {t('header.launchClaude')}
            </button>

            <button
              onClick={() => i18n.changeLanguage(currentLanguage === 'ja' ? 'en' : 'ja')}
              title={t('header.language.toggleTitle', { lang: t(`header.language.${currentLanguage}`) })}
              className="px-2 py-1.5 rounded-lg bg-white dark:bg-slate-900 hover:bg-slate-50 dark:hover:bg-slate-800 text-slate-600 dark:text-slate-300 border border-slate-300 dark:border-slate-700 shadow-xs transition flex items-center gap-1.5"
            >
              <Languages className="w-4 h-4" />
              <span className="text-xs font-semibold">{t(`header.language.${currentLanguage}`)}</span>
            </button>

            <button
              onClick={() => setThemePreference(NEXT_THEME[themePreference])}
              title={t('header.theme.toggleTitle', { mode: t(`header.theme.${themePreference}`) })}
              className="p-1.5 rounded-lg bg-white dark:bg-slate-900 hover:bg-slate-50 dark:hover:bg-slate-800 text-slate-600 dark:text-slate-300 border border-slate-300 dark:border-slate-700 shadow-xs transition"
            >
              <ThemeIcon className="w-4 h-4" />
            </button>

            <button
              onClick={() => setIsSettingsOpen(true)}
              title={t('header.settings')}
              className="p-1.5 rounded-lg bg-white dark:bg-slate-900 hover:bg-slate-50 dark:hover:bg-slate-800 text-slate-600 dark:text-slate-300 border border-slate-300 dark:border-slate-700 shadow-xs transition"
            >
              <SettingsIcon className="w-4 h-4" />
            </button>

            <button
              onClick={() => {
                // Clear any leftover status message from a previous create
                // attempt before the form reopens -- the form's own request
                // field lives in CreateTicketModal and starts out empty on
                // every open, but createStatus otherwise persists
                // since this component stays mounted between opens.
                resetCreateStatus();
                setIsCreateOpen(true);
              }}
              disabled={!currentProject}
              title={currentProject ? undefined : t('projectSwitcher.selectFirst')}
              className="px-3.5 py-1.5 rounded-lg bg-blue-600 hover:bg-blue-500 disabled:opacity-50 disabled:hover:bg-blue-600 text-white text-xs font-semibold flex items-center gap-1.5 shadow-xs transition"
            >
              <Plus className="w-4 h-4" />
              {t('header.newTicket')}
            </button>
          </div>
        </div>

        {/* Filter Toolbar */}
        <div className="max-w-7xl mx-auto flex flex-wrap items-center justify-between gap-4 mt-3 pt-3 border-t border-slate-100 dark:border-slate-800 text-xs">
          <div className="flex flex-wrap items-center gap-3">
            <div className="relative">
              <Search className="w-3.5 h-3.5 absolute left-2.5 top-2.5 text-slate-400" />
              <input
                type="text"
                value={filterQuery}
                onChange={e => { setFilterQuery(e.target.value); setPage(1); }}
                placeholder={t('toolbar.searchPlaceholder')}
                className="pl-8 pr-3 py-1.5 rounded-lg bg-slate-50 dark:bg-slate-800 border border-slate-300 dark:border-slate-700 text-xs focus:outline-none focus:border-blue-500 w-56 text-slate-900 dark:text-slate-100"
              />
            </div>

            {/* Status multi-select. Same open/close pattern as the project
                switcher above (transparent full-screen overlay closes it on an
                outside click, so that click can't also hit whatever is
                underneath). Toggling a checkbox keeps the panel open so several
                statuses can be changed in a row. The panel is a labelled group
                of checkboxes rather than an ARIA menu, so the trigger exposes
                aria-expanded/aria-controls but not aria-haspopup. */}
            <div
              className="relative"
              onKeyDown={e => {
                if (e.key === 'Escape' && isStatusMenuOpen) {
                  e.stopPropagation();
                  setIsStatusMenuOpen(false);
                  statusMenuButtonRef.current?.focus();
                }
              }}
            >
              <button
                ref={statusMenuButtonRef}
                type="button"
                onClick={() => setIsStatusMenuOpen(v => !v)}
                aria-expanded={isStatusMenuOpen}
                aria-controls="toolbar-status-filter-panel"
                className="px-3 py-1.5 rounded-lg bg-slate-50 dark:bg-slate-800 border border-slate-300 dark:border-slate-700 text-xs font-medium text-slate-700 dark:text-slate-300 flex items-center gap-1.5 whitespace-nowrap"
              >
                {filterStatuses.length === TICKET_STATUSES.length
                  ? t('toolbar.statusAll')
                  : filterStatuses.length === 0
                    ? t('toolbar.statusNone')
                    : t('toolbar.statusSelected', { count: filterStatuses.length })}
                <ChevronDown className="w-3.5 h-3.5 text-slate-400 shrink-0" aria-hidden="true" />
              </button>

              {isStatusMenuOpen && (
                <>
                  <div className="fixed inset-0 z-40" onClick={() => setIsStatusMenuOpen(false)} />
                  <div
                    id="toolbar-status-filter-panel"
                    role="group"
                    aria-label={t('toolbar.statusGroupLabel')}
                    className="absolute left-0 mt-1.5 w-48 bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-lg shadow-lg z-50 py-1 text-xs"
                  >
                    {TICKET_STATUSES.map(s => (
                      <label
                        key={s}
                        className="flex items-center gap-2 px-3 py-1.5 hover:bg-slate-50 dark:hover:bg-slate-800 cursor-pointer text-slate-700 dark:text-slate-300 font-medium whitespace-nowrap"
                      >
                        <input
                          type="checkbox"
                          checked={filterStatuses.includes(s)}
                          onChange={() => toggleFilterStatus(s)}
                          className="rounded border-slate-300 dark:border-slate-600 text-blue-600 focus:ring-0"
                        />
                        {/* Display only: the checkbox state and filter still
                            use the DB value `s`. The label shares the
                            status.* wording with the ticket badge via
                            statusMeta.ts (DFLT-00030). */}
                        {t(getStatusMeta(s).labelKey)}
                      </label>
                    ))}
                  </div>
                </>
              )}
            </div>

            {/* Assignee single-select (DFLT-00047). Same trigger + panel
                structure as the status filter above, but a single choice per
                click rather than independently toggled checkboxes -- picking
                a name replaces the previous selection instead of adding to
                it, and re-picking the same name (or "All") clears it. */}
            <div
              className="relative"
              onKeyDown={e => {
                if (e.key === 'Escape' && isAssigneeMenuOpen) {
                  e.stopPropagation();
                  setIsAssigneeMenuOpen(false);
                  assigneeMenuButtonRef.current?.focus();
                }
              }}
            >
              <button
                ref={assigneeMenuButtonRef}
                type="button"
                onClick={() => setIsAssigneeMenuOpen(v => !v)}
                aria-expanded={isAssigneeMenuOpen}
                aria-controls="toolbar-assignee-filter-panel"
                className="px-3 py-1.5 rounded-lg bg-slate-50 dark:bg-slate-800 border border-slate-300 dark:border-slate-700 text-xs font-medium text-slate-700 dark:text-slate-300 flex items-center gap-1.5 whitespace-nowrap"
              >
                {filterAssignee ? t('toolbar.assigneeSelected', { name: filterAssignee }) : t('toolbar.assigneeAll')}
                <ChevronDown className="w-3.5 h-3.5 text-slate-400 shrink-0" aria-hidden="true" />
              </button>

              {isAssigneeMenuOpen && (
                <>
                  <div className="fixed inset-0 z-40" onClick={() => setIsAssigneeMenuOpen(false)} />
                  <div
                    id="toolbar-assignee-filter-panel"
                    role="group"
                    aria-label={t('toolbar.assigneeGroupLabel')}
                    className="absolute left-0 mt-1.5 w-48 bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-lg shadow-lg z-50 py-1 text-xs"
                  >
                    <label className="flex items-center gap-2 px-3 py-1.5 hover:bg-slate-50 dark:hover:bg-slate-800 cursor-pointer text-slate-700 dark:text-slate-300 font-medium whitespace-nowrap">
                      <input
                        type="radio"
                        name="assignee-filter"
                        checked={filterAssignee === null}
                        onChange={() => selectFilterAssignee(null)}
                        className="border-slate-300 dark:border-slate-600 text-blue-600 focus:ring-0"
                      />
                      {t('toolbar.assigneeAll')}
                    </label>
                    {assigneeOptions.map(name => (
                      <label
                        key={name}
                        className="flex items-center gap-2 px-3 py-1.5 hover:bg-slate-50 dark:hover:bg-slate-800 cursor-pointer text-slate-700 dark:text-slate-300 font-medium whitespace-nowrap"
                      >
                        <input
                          type="radio"
                          name="assignee-filter"
                          checked={filterAssignee === name}
                          onChange={() => selectFilterAssignee(name)}
                          className="border-slate-300 dark:border-slate-600 text-blue-600 focus:ring-0"
                        />
                        {name}
                      </label>
                    ))}
                  </div>
                </>
              )}
            </div>

            {/* Priority multi-select (DFLT-00048). Same open/close and
                checkbox-group pattern as the status filter above. */}
            <div
              className="relative"
              onKeyDown={e => {
                if (e.key === 'Escape' && isPriorityMenuOpen) {
                  e.stopPropagation();
                  setIsPriorityMenuOpen(false);
                  priorityMenuButtonRef.current?.focus();
                }
              }}
            >
              <button
                ref={priorityMenuButtonRef}
                type="button"
                onClick={() => setIsPriorityMenuOpen(v => !v)}
                aria-expanded={isPriorityMenuOpen}
                aria-controls="toolbar-priority-filter-panel"
                className="px-3 py-1.5 rounded-lg bg-slate-50 dark:bg-slate-800 border border-slate-300 dark:border-slate-700 text-xs font-medium text-slate-700 dark:text-slate-300 flex items-center gap-1.5 whitespace-nowrap"
              >
                {filterPriorities.length === PRIORITY_FILTER_VALUES.length
                  ? t('toolbar.priorityAll')
                  : filterPriorities.length === 0
                    ? t('toolbar.priorityNone')
                    : t('toolbar.prioritySelected', { count: filterPriorities.length })}
                <ChevronDown className="w-3.5 h-3.5 text-slate-400 shrink-0" aria-hidden="true" />
              </button>

              {isPriorityMenuOpen && (
                <>
                  <div className="fixed inset-0 z-40" onClick={() => setIsPriorityMenuOpen(false)} />
                  <div
                    id="toolbar-priority-filter-panel"
                    role="group"
                    aria-label={t('toolbar.priorityGroupLabel')}
                    className="absolute left-0 mt-1.5 w-48 bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-lg shadow-lg z-50 py-1 text-xs"
                  >
                    {PRIORITY_FILTER_VALUES.map(p => (
                      <label
                        key={p}
                        className="flex items-center gap-2 px-3 py-1.5 hover:bg-slate-50 dark:hover:bg-slate-800 cursor-pointer text-slate-700 dark:text-slate-300 font-medium whitespace-nowrap"
                      >
                        <input
                          type="checkbox"
                          checked={filterPriorities.includes(p)}
                          onChange={() => toggleFilterPriority(p)}
                          className="rounded border-slate-300 dark:border-slate-600 text-blue-600 focus:ring-0"
                        />
                        {/* 'UNSET' shares its wording with the ticket badge's
                            "no priority" state via getPriorityMeta/
                            priority.unset; the three real levels share
                            priority.high/medium/low the same way. */}
                        {t(p === 'UNSET' ? 'priority.unset' : getPriorityMeta(p).labelKey)}
                      </label>
                    ))}
                  </div>
                </>
              )}
            </div>
          </div>

          <div className="flex items-center gap-3 text-slate-500 dark:text-slate-400 text-xs">
            <button
              onClick={fetchAllTickets}
              disabled={loading}
              className="p-1.5 hover:bg-slate-100 dark:hover:bg-slate-800 rounded-md transition"
              title={t('toolbar.refreshTitle')}
            >
              <RotateCw className={`w-3.5 h-3.5 ${loading ? 'animate-spin' : ''}`} />
            </button>
            <span>
              {t('toolbar.updatedAt', {
                time: lastFetchedAt ? formatTime(lastFetchedAt, i18n.language) : t('common.justNow')
              })}
            </span>
          </div>
        </div>
      </header>

      {/* Main Container */}
      <main className="max-w-7xl mx-auto px-6 py-6 space-y-6">
        {/* Simple Summary Metrics */}
        <div className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-xl p-4 shadow-xs flex flex-wrap items-center justify-between gap-4">
          <div className="flex items-center gap-2">
            <span className="font-bold text-slate-800 dark:text-slate-200 text-sm">{t('summary.title')}</span>
            <span className="text-xs text-slate-500 dark:text-slate-400">{t('summary.subtitle')}</span>
          </div>

          <div className="flex items-center gap-6 divide-x divide-slate-200 dark:divide-slate-700 text-xs">
            <div className="text-center px-3">
              <div className="text-lg font-bold text-slate-800 dark:text-slate-200">{totalCount}</div>
              <div className="text-[11px] text-slate-500 dark:text-slate-400">{t('summary.total')}</div>
            </div>
            <div className="text-center px-3">
              <div className="text-lg font-bold text-blue-600 dark:text-blue-400">{inProgressCount}</div>
              <div className="text-[11px] text-slate-500 dark:text-slate-400">{t('summary.inProgress')}</div>
            </div>
            <div className="text-center px-3">
              <div className="text-lg font-bold text-purple-600 dark:text-purple-400">{inReviewCount}</div>
              <div className="text-[11px] text-slate-500 dark:text-slate-400">{t('summary.inReview')}</div>
            </div>
            <div className="text-center px-3">
              <div className="text-lg font-bold text-emerald-600 dark:text-emerald-400">{doneCount}</div>
              <div className="text-[11px] text-slate-500 dark:text-slate-400">{t('summary.done')}</div>
            </div>
            <div className="text-center px-3">
              <div className="text-lg font-bold text-slate-800 dark:text-slate-200">
                {doneNodesCount}/{totalNodesCount}
              </div>
              <div className="text-[11px] text-slate-500 dark:text-slate-400">{t('summary.nodeProgress')}</div>
            </div>
          </div>
        </div>

        {/* Tickets Accordion List */}
        <div>
          {!currentProject ? (
            <div className="text-center py-16 bg-white dark:bg-slate-900 rounded-xl border border-slate-200 dark:border-slate-800 text-slate-400 dark:text-slate-500 text-sm space-y-3">
              <p>{t('projectSwitcher.noProjectYet')}</p>
              <button
                onClick={() => {
                  setProjectFormError('');
                  setIsCreateProjectOpen(true);
                }}
                className="px-3.5 py-1.5 rounded-lg bg-blue-600 hover:bg-blue-500 text-white text-xs font-semibold inline-flex items-center gap-1.5 shadow-xs transition"
              >
                <Plus className="w-4 h-4" />
                {t('projectSwitcher.createNew')}
              </button>
            </div>
          ) : filteredTickets.length === 0 ? (
            <div className="text-center py-16 bg-white dark:bg-slate-900 rounded-xl border border-slate-200 dark:border-slate-800 text-slate-400 dark:text-slate-500 text-sm">
              {t('emptyState.noTicketsMatch')}
            </div>
          ) : (
            <>
              {pagedTickets.map(ticket => (
                <TicketItem
                  key={ticket.id}
                  ticket={ticket}
                  isExpanded={expandedTicketIds.has(ticket.id)}
                  onToggleExpand={() => handleToggleExpand(ticket.id)}
                  onRefresh={fetchAllTickets}
                  myName={myName}
                />
              ))}

              {totalPages > 1 && (
                <div className="flex items-center justify-between mt-2 px-1 text-xs text-slate-500 dark:text-slate-400">
                  <span>
                    {t('pagination.range', {
                      from: (currentPage - 1) * ticketsPerPage + 1,
                      to: Math.min(currentPage * ticketsPerPage, filteredTickets.length),
                      total: filteredTickets.length
                    })}
                  </span>
                  <div className="flex items-center gap-3">
                    <button
                      onClick={() => setPage(p => Math.max(1, p - 1))}
                      disabled={currentPage <= 1}
                      className="p-1.5 rounded-md border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 hover:bg-slate-100 dark:hover:bg-slate-800 disabled:opacity-40 disabled:hover:bg-white dark:disabled:hover:bg-slate-900 transition"
                    >
                      <ChevronLeft className="w-3.5 h-3.5" />
                    </button>
                    <span className="font-mono font-semibold text-slate-700 dark:text-slate-300">
                      {t('pagination.pageOf', { page: currentPage, total: totalPages })}
                    </span>
                    <button
                      onClick={() => setPage(p => Math.min(totalPages, p + 1))}
                      disabled={currentPage >= totalPages}
                      className="p-1.5 rounded-md border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 hover:bg-slate-100 dark:hover:bg-slate-800 disabled:opacity-40 disabled:hover:bg-white dark:disabled:hover:bg-slate-900 transition"
                    >
                      <ChevronRight className="w-3.5 h-3.5" />
                    </button>
                  </div>
                </div>
              )}
            </>
          )}
        </div>
      </main>

      {/* Create Ticket Modal */}
      {isCreateOpen && (
        <CreateTicketModal
          onSubmit={handleCreateTicket}
          onClose={() => {
            resetCreateStatus();
            setIsCreateOpen(false);
          }}
          isCreating={isCreating}
          status={createStatus}
        />
      )}

      {/* Global Claude Modal */}
      <ClaudeRunnerModal
        isOpen={isClaudeGlobalOpen}
        onClose={() => setIsClaudeGlobalOpen(false)}
        projectId={currentProject?.id}
      />

      {/* Settings Modal */}
      <SettingsModal
        isOpen={isSettingsOpen}
        onClose={() => setIsSettingsOpen(false)}
        projects={projects}
        currentProject={currentProject}
        onProjectsChanged={refreshProjects}
        onPaginationPageSizeChanged={setTicketsPerPage}
        onMyNameChanged={setMyName}
      />

      {/* Create Project Modal */}
      {isCreateProjectOpen && (
        <div className="fixed inset-0 z-50 bg-black/40 backdrop-blur-xs flex items-center justify-center p-4">
          <div className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-xl w-full max-w-md p-6 shadow-2xl">
            <h2 className="text-lg font-bold text-slate-900 dark:text-slate-100 mb-4">{t('createProjectModal.title')}</h2>
            <p className="text-xs text-slate-500 dark:text-slate-400 mb-4">{t('createProjectModal.description')}</p>
            <form onSubmit={handleCreateProjectSubmit} className="space-y-4">
              <div>
                <label className="block text-xs font-semibold text-slate-700 dark:text-slate-300 mb-1">{t('createProjectModal.nameLabel')}</label>
                <input
                  type="text"
                  required
                  disabled={isSavingProject}
                  className="w-full bg-slate-50 dark:bg-slate-800 border border-slate-300 dark:border-slate-700 rounded-lg px-3 py-2 text-sm text-slate-900 dark:text-slate-100 focus:outline-none focus:border-blue-500 disabled:opacity-60"
                  value={newProjectName}
                  onChange={e => setNewProjectName(e.target.value)}
                  placeholder={t('createProjectModal.namePlaceholder')}
                />
              </div>
              <div>
                <label className="block text-xs font-semibold text-slate-700 dark:text-slate-300 mb-1">{t('createProjectModal.prefixLabel')}</label>
                <input
                  type="text"
                  maxLength={5}
                  disabled={isSavingProject}
                  className="w-full bg-slate-50 dark:bg-slate-800 border border-slate-300 dark:border-slate-700 rounded-lg px-3 py-2 text-sm text-slate-900 dark:text-slate-100 focus:outline-none focus:border-blue-500 disabled:opacity-60 font-mono uppercase"
                  value={newProjectPrefix}
                  onChange={e => setNewProjectPrefix(e.target.value.toUpperCase())}
                  placeholder={t('createProjectModal.prefixPlaceholder')}
                />
              </div>
              <div>
                <label className="block text-xs font-semibold text-slate-700 dark:text-slate-300 mb-1">{t('createProjectModal.workDirLabel')}</label>
                <input
                  type="text"
                  required
                  disabled={isSavingProject}
                  className="w-full bg-slate-50 dark:bg-slate-800 border border-slate-300 dark:border-slate-700 rounded-lg px-3 py-2 text-sm text-slate-900 dark:text-slate-100 font-mono focus:outline-none focus:border-blue-500 disabled:opacity-60"
                  value={newProjectWorkDir}
                  onChange={e => setNewProjectWorkDir(e.target.value)}
                  placeholder={t('createProjectModal.workDirPlaceholder')}
                />
              </div>

              {projectFormError && (
                <div className="p-2.5 bg-red-50 dark:bg-red-950 text-red-700 dark:text-red-300 text-[11px] rounded-lg border border-red-200 dark:border-red-900">
                  {projectFormError}
                </div>
              )}

              <div className="flex justify-end gap-2 pt-2">
                <button
                  type="button"
                  onClick={() => setIsCreateProjectOpen(false)}
                  className="px-4 py-2 bg-slate-100 dark:bg-slate-800 hover:bg-slate-200 dark:hover:bg-slate-700 rounded-lg text-xs font-medium text-slate-700 dark:text-slate-300 transition"
                >
                  {t('createModal.cancel')}
                </button>
                <button
                  type="submit"
                  disabled={isSavingProject || !newProjectName.trim() || !newProjectWorkDir.trim()}
                  className="px-4 py-2 bg-blue-600 hover:bg-blue-500 disabled:opacity-50 rounded-lg text-xs font-semibold text-white shadow-xs transition flex items-center gap-1.5"
                >
                  {isSavingProject && <Loader2 className="w-3.5 h-3.5 animate-spin" />}
                  {t('createModal.submit')}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}
    </div>
  );
};

export default App;
