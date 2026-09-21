import React, { useCallback, useState, useEffect, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Search,
  RotateCw,
  Plus,
  Terminal,
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
import { Label, Ticket, TicketDetail, TicketStatus, TicketPriority, Project, TICKET_STATUSES, TICKET_PRIORITIES } from './types';
import { getStatusMeta, matchesStatusFilter } from './statusMeta';
import { getPriorityMeta, matchesPriorityFilter } from './priorityMeta';
import { matchesLabelFilter } from './labelMeta';
import { assigneeFilterOptions, isUnassignedOption, matchesAssigneeFilter } from './assigneeFilter';
import { TicketItem } from './components/TicketItem';
import { LabelFilter } from './components/LabelFilter';
import { MultiSelectFilter } from './components/MultiSelectFilter';
import { fetchLabels } from './lib/labelsApi';
import { ClaudeRunnerModal } from './components/ClaudeRunnerModal';
import { SettingsModal } from './components/SettingsModal';
import { ProjectSetupModal } from './components/ProjectSetupModal';
import { CreateTicketModal } from './components/CreateTicketModal';
import { useClaudeLaunch } from './hooks/useClaudeLaunch';
import { useTheme, ThemePreference } from './hooks/useTheme';
import { formatTime } from './i18n/formatDate';
import { localizedApiErrorMessage } from './lib/apiError';
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

  // Project scoping (GET/POST /api/projects, GET/PUT /api/current-project).
  //
  // Since DFLT-00106 the client, not the server, decides which project the
  // ticket list shows: every fetch goes to
  // `/api/tickets?project_id=<currentProject.id>` (GET /api/tickets with no
  // project_id returns an empty list now). The server used to filter by a
  // "current project" shared by everyone on the same data source, so a
  // teammate switching projects replaced this list's contents on the next
  // 15s poll while the header still named the old project.
  //
  // "The header and the list always agree" rests on two different things,
  // and it is worth keeping them apart, because only one of them is free:
  //
  //   - Structural: every request is issued for the id the header is
  //     rendering. refreshTickets reads it from currentProjectIdRef and the
  //     ticket-list effect below keys on it, so no call site can ask for
  //     anything else, and switching projects tears the old poll down.
  //   - Guarded: that is not sufficient on its own. A request that was
  //     already in flight when the user switched still comes back, and its
  //     response would otherwise be painted under the new header (a real
  //     "header Beta / list Alpha" screen, reproduced in
  //     App.project.test.tsx). fetchAllTickets therefore drops any response
  //     whose project is no longer the current one, exactly as
  //     refreshProjectLabels does for the label options.
  //
  // currentProject is null in three different situations, which the UI must
  // not conflate: before the initial GET /api/current-project resolves, on
  // an install with no project selected, and when that GET failed.
  // isCurrentProjectResolved and hasCurrentProjectFailed tell them apart --
  // "loading", "create a project", and "the setting could not be read". The
  // third one matters since DFLT-00106 moved the setting into a local
  // graph-config.json, which can now be unreadable on its own: offering
  // "create a project" to somebody who already has one is how a shared data
  // source acquires duplicate projects.
  const [projects, setProjects] = useState<Project[]>([]);
  const [currentProject, setCurrentProject] = useState<Project | null>(null);
  const [isCurrentProjectResolved, setIsCurrentProjectResolved] = useState(false);
  const [hasCurrentProjectFailed, setHasCurrentProjectFailed] = useState(false);
  const [isProjectMenuOpen, setIsProjectMenuOpen] = useState(false);
  const [isCreateProjectOpen, setIsCreateProjectOpen] = useState(false);
  // The header's project switcher button: where ProjectSetupModal returns
  // focus on close when the element that opened it is gone (the menu item
  // unmounts with the menu) or the dialog opened by itself (?newProject=1).
  const projectMenuButtonRef = useRef<HTMLButtonElement>(null);
  // The directory `graph-engine ui` asked a project to be set up for (see
  // the newProject query effect below), or '' when the dialog was opened
  // from the header's "New project..." entry.
  const [projectSetupDirectory, setProjectSetupDirectory] = useState('');

  // Filters
  //
  // All four toolbar filters (status / assignee / priority / label) are the
  // same control -- components/MultiSelectFilter.tsx -- over the same
  // contract since DFLT-00086: the state is a list of selected values,
  // EMPTY means "don't filter by this" rather than "match nothing", and a
  // non-empty selection matches with OR while the filters combine with AND
  // (see filteredTickets). None of them is persisted: a reload resets them.
  //
  // Status and priority used to start fully checked, which made "uncheck
  // everything" empty the list with no way back, and assignee used to be a
  // single-choice radio group with an explicit "All" entry and no way to
  // ask for unassigned tickets; MultiSelectFilter.tsx has the reasoning.
  // Each filter's own state below therefore only differs in its value type
  // and where its options come from.
  const [filterQuery, setFilterQuery] = useState('');

  // Options: the fixed TICKET_STATUSES catalog. Checking every status but
  // DONE reproduces the old "hide completed" checkbox.
  const [filterStatuses, setFilterStatuses] = useState<TicketStatus[]>([]);

  // Options: derived from whichever tickets are currently loaded plus an
  // "unassigned" bucket, not a fixed list -- there is no server-side
  // catalog of assignee names, so unlike TICKET_STATUSES this can shrink or
  // grow as tickets are (un)assigned. A selected name that later drops out
  // is deliberately left in the selection (the filter keeps narrowing to
  // it); only the panel's option list follows the current ticket set. See
  // assigneeFilter.ts, which also owns the unassigned bucket's value.
  const [filterAssignees, setFilterAssignees] = useState<string[]>([]);
  const assigneeOptions = assigneeFilterOptions(tickets.map(t => t.assignee));

  // Options: exactly the three levels -- there is no unset bucket
  // (DFLT-00083).
  const [filterPriorities, setFilterPriorities] = useState<TicketPriority[]>([]);

  // Options: the current project's labels, see components/LabelFilter.tsx.
  const [filterLabelIds, setFilterLabelIds] = useState<string[]>([]);

  // Every filter change also returns to page 1, so a narrowing can never
  // leave the user on a now out-of-range page. Written once here rather
  // than in four handlers: forgetting the reset in one of them is exactly
  // the kind of per-filter inconsistency DFLT-00086 is removing. (setPage
  // is declared further down but only ever read when the returned handler
  // runs, i.e. long after this render.)
  function withPageReset<T>(set: React.Dispatch<React.SetStateAction<T[]>>): (next: T[]) => void {
    return next => {
      set(next);
      setPage(1);
    };
  }
  const changeFilterStatuses = withPageReset(setFilterStatuses);
  const changeFilterAssignees = withPageReset(setFilterAssignees);
  const changeFilterPriorities = withPageReset(setFilterPriorities);
  const changeFilterLabelIds = withPageReset(setFilterLabelIds);

  // The current project's labels: the label filter's options and the
  // ticket label picker's choices. Re-fetched on project switch, on every
  // ticket (re)fetch -- so a teammate's label edits show up with the regular
  // poll -- and after any change in the settings modal's labels tab.
  const [projectLabels, setProjectLabels] = useState<Label[]>([]);
  const currentProjectIdRef = useLatest(currentProject?.id ?? '');
  const refreshProjectLabels = useCallback(
    async (projectId: string = currentProjectIdRef.current) => {
      if (!projectId) {
        setProjectLabels([]);
        return;
      }
      try {
        const labels = await fetchLabels(tRef.current, projectId);
        // Ignore a response for a project that is no longer current.
        if (projectId === currentProjectIdRef.current) setProjectLabels(labels);
      } catch (e) {
        // Keep the previous list: clearing it would also clear the filter.
        console.error('Failed to load labels', e);
      }
    },
    [currentProjectIdRef, tRef]
  );
  useEffect(() => {
    refreshProjectLabels(currentProject?.id ?? '');
  }, [currentProject?.id, refreshProjectLabels]);
  // A selected label that no longer exists in the current project (deleted,
  // or left behind by a project switch) is dropped from the selection, so
  // the filter never narrows by a label the panel can't show.
  useEffect(() => {
    setFilterLabelIds(prev => {
      const next = prev.filter(id => projectLabels.some(l => l.id === id));
      return next.length === prev.length ? prev : next;
    });
  }, [projectLabels]);

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
  // Every completed ticket fetch (startup, the 15s poll, manual refresh,
  // after an edit) also re-fetches the current project's labels.
  useEffect(() => {
    if (lastFetchedAt) refreshProjectLabels();
  }, [lastFetchedAt, refreshProjectLabels]);

  // Modals
  const [isCreateOpen, setIsCreateOpen] = useState(false);
  const [isClaudeGlobalOpen, setIsClaudeGlobalOpen] = useState(false);
  const [isSettingsOpen, setIsSettingsOpen] = useState(false);

  // Fetch one project's tickets and their details.
  //
  // projectId is mandatory (DFLT-00106): an empty one means "the current
  // project isn't known yet", and the right answer to that is to fetch
  // nothing at all rather than to ask the server for its idea of a default
  // -- it no longer has one. Showing another project's tickets under this
  // project's header is precisely the bug this replaces.
  //
  // Every write below is guarded on projectId still being the current one.
  // Tearing down the poll on a switch stops future *timers*, but it cannot
  // recall a request that already left: this function awaits the list and
  // then one GET /api/tickets/<id> per ticket, so its in-flight window grows
  // with the list, and the old project's response routinely lands after the
  // new one's. Without the guard that late response repaints the list under
  // a header naming the other project -- the exact symptom this ticket is
  // about, arrived at from the other direction. setLastFetchedAt is guarded
  // too because it drives the label refresh and the "last updated" stamp,
  // and setLoading(false) because a stale run finishing must not clear the
  // spinner the current run put up.
  const fetchAllTickets = useCallback(async (projectId: string) => {
    if (!projectId) {
      setTickets([]);
      return;
    }
    const isStale = () => projectId !== currentProjectIdRef.current;
    setLoading(true);
    try {
      const res = await fetch(`/api/tickets?project_id=${encodeURIComponent(projectId)}`);
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

      if (isStale()) return;
      setTickets(details);
      setLastFetchedAt(new Date());
    } catch (e) {
      console.error('Failed to load tickets', e);
    } finally {
      if (!isStale()) setLoading(false);
    }
  }, [currentProjectIdRef]);

  // refreshTickets re-fetches whichever project the header is currently
  // showing. It is what every "something changed, reload the list" callback
  // uses (the toolbar's refresh button, a ticket edit, the create-ticket
  // launch, a label change), so none of them has to thread the project id
  // through by hand -- and none of them can accidentally fetch unscoped.
  const refreshTickets = useCallback(
    () => fetchAllTickets(currentProjectIdRef.current),
    [fetchAllTickets, currentProjectIdRef]
  );

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
  //
  // A failure is its own outcome, NOT "no project selected". Both end up
  // with currentProject === null, but the two must not render the same way:
  // the failure path used to fall through to "no project has been created
  // yet" plus a create button, which is the screen this component
  // deliberately withholds while the answer is unknown, for the same reason
  // -- somebody who does have a project is being invited to create a
  // duplicate, and on a shared data source that duplicate is everybody's.
  // The read is now a local graph-config.json (DFLT-00106), which can fail
  // on its own while the rest of the app is fine, so this is a path users
  // can actually reach. isCurrentProjectResolved still just records that the
  // attempt is over; hasCurrentProjectFailed records how it ended.
  const fetchCurrentProject = async () => {
    try {
      const res = await fetch('/api/current-project');
      if (!res.ok) throw new Error(`GET /api/current-project: ${res.status}`);
      const data = await res.json();
      setCurrentProject(data ?? null);
      setHasCurrentProjectFailed(false);
    } catch (e) {
      console.error('Failed to load current project', e);
      setCurrentProject(null);
      setHasCurrentProjectFailed(true);
    } finally {
      setIsCurrentProjectResolved(true);
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

  // The retry offered on the failure screen. It goes back to the unresolved
  // state first, so the user sees the loading placeholder rather than the
  // error sitting there unchanged while the request is on its way -- and so
  // a second failure reads as a second attempt. The project list is
  // re-fetched with it: a failure there is silent (fetchProjects empties the
  // switcher), so the retry would otherwise leave the menu empty.
  const retryCurrentProject = () => {
    setIsCurrentProjectResolved(false);
    setHasCurrentProjectFailed(false);
    void refreshProjects();
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

  // Startup. Deliberately does NOT fetch tickets: which project's tickets
  // those would be isn't known until fetchCurrentProject resolves, and the
  // effect below is the only thing that fetches them (DFLT-00106).
  useEffect(() => {
    fetchProjects();
    fetchCurrentProject();
    fetchTicketsPerPage();
  }, [fetchTicketsPerPage]);

  // The ticket list, keyed on the project the header is showing: fetched
  // immediately and then polled, since ticket/graph changes happen in an
  // external terminal the app can't see directly (no more SSE stream to
  // react to). With no project resolved yet, nothing is fetched at all.
  //
  // Having the poll live in an effect that *depends on* currentProject.id is
  // what keeps every request pointed at the header's project without each
  // call site having to remember: switching tears the old interval down
  // (cleanup) and starts one for the new id, so after a switch no timer can
  // ever fire for the previous project again.
  //
  // That is the whole of what the effect guarantees, and it is only half of
  // "the header and the list always agree": a request the old timer (or the
  // switch itself) already started is still on its way and will still come
  // back. Discarding that response is fetchAllTickets' job, not this
  // cleanup's -- see the guard there.
  useEffect(() => {
    const projectId = currentProject?.id ?? '';
    fetchAllTickets(projectId);
    if (!projectId) return;
    const interval = setInterval(() => fetchAllTickets(projectId), 15000);
    return () => clearInterval(interval);
  }, [currentProject?.id, fetchAllTickets]);

  // Consumes the `?newProject=1&workDir=<dir>` query the `graph-engine ui`
  // CLI command (the `/ui` slash command's backend) appends to the root URL
  // when no project's local path (this environment's graph-config.json
  // projectPaths) covers the current directory: auto-open the project setup
  // dialog for that directory, where the user either creates a new project
  // or picks an existing one from the DB (DFLT-00080). Runs once on mount,
  // and strips the query from the URL immediately after reading it (via
  // history.replaceState) so a later manual reload of the same URL doesn't
  // re-trigger the dialog. The query name `workDir` is kept for
  // compatibility with the CLI.
  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    if (params.get('newProject') !== '1') return;

    setProjectSetupDirectory(params.get('workDir') ?? '');
    setIsCreateProjectOpen(true);

    const url = new URL(window.location.href);
    url.searchParams.delete('newProject');
    url.searchParams.delete('workDir');
    window.history.replaceState({}, '', url.pathname + url.search + url.hash);
  }, []);

  // switchToProject only changes which project is current -- it must never
  // launch a terminal on its own. Switching used to also open an external
  // Claude Code terminal in the project's directory, but that surprised users
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
    // Label ids are per project, so the previous project's selection can't
    // apply to the new one.
    setFilterLabelIds([]);
    setPage(1);
    // No explicit fetch: the ticket-list effect keys on currentProject.id,
    // so setCurrentProject above already re-points it at the new project.
  };

  // ProjectSetupModal's "create new" path: the POST already happened there.
  const handleProjectCreated = async (created: Project) => {
    setProjects(prev => [...prev, created]);
    setIsCreateProjectOpen(false);
    setProjectSetupDirectory('');
    await switchToProject(created);
  };

  // ProjectSetupModal's "choose an existing project" path: the local path was
  // saved and the project already made current there, so only local state
  // needs to follow.
  const handleProjectLinked = async (project: Project) => {
    setIsCreateProjectOpen(false);
    setProjectSetupDirectory('');
    setCurrentProject(project);
    setFilterLabelIds([]);
    setPage(1);
    await fetchProjects();
    // The ticket-list effect follows setCurrentProject; see switchToProject.
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
  const { isLaunching: isCreating, lastMessage: createStatus, launch: runCreateTicket, reset: resetCreateStatus } = useClaudeLaunch(refreshTickets);

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

  // Filter calculations. The four filters are ANDed: a ticket has to pass
  // every one of them. Each predicate is OR across its own selection and
  // passes everything when that selection is empty, so a filter nobody has
  // touched contributes nothing (see components/MultiSelectFilter.tsx).
  const filteredTickets = tickets.filter(t => {
    // Unexpected DB values count as TODO, matching the badge (statusMeta.ts).
    if (!matchesStatusFilter(t.status, filterStatuses)) return false;
    // Includes the "unassigned" bucket (assigneeFilter.ts).
    if (!matchesAssigneeFilter(t.assignee, filterAssignees)) return false;
    // Unexpected values count as MEDIUM, matching the badge (priorityMeta.ts).
    if (!matchesPriorityFilter(t.priority, filterPriorities)) return false;
    if (!matchesLabelFilter(t.labels, filterLabelIds)) return false;
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
                ref={projectMenuButtonRef}
                onClick={() => setIsProjectMenuOpen(v => !v)}
                className="px-3 py-1.5 rounded-lg bg-white dark:bg-slate-900 hover:bg-slate-50 dark:hover:bg-slate-800 text-xs font-semibold text-slate-700 dark:text-slate-300 border border-slate-300 dark:border-slate-700 flex items-center gap-1.5 shadow-xs transition max-w-[14rem]"
                title={currentProject ? currentProject.local_path || t('settings.appSettings.projects.notSet') : undefined}
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
                          setProjectSetupDirectory('');
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

            {/* Status / assignee / priority / label filters. All four are
                the same MultiSelectFilter (DFLT-00086): same trigger button,
                same checkbox panel, same clear button, same keyboard and
                ARIA behaviour, and the same "nothing checked = All" meaning.
                They are AND-combined with each other and with the search box
                (see filteredTickets); only the options and the i18n keys
                differ per filter. */}
            <MultiSelectFilter
              panelId="toolbar-status-filter-panel"
              allKey="toolbar.statusAll"
              selectedKey="toolbar.statusSelected"
              groupLabelKey="toolbar.statusGroupLabel"
              /* Display only: the checkbox state and the filter still use the
                 DB value. The option text shares the status.* wording with the
                 ticket badge via statusMeta.ts (DFLT-00030). */
              options={TICKET_STATUSES.map(s => ({ value: s, label: t(getStatusMeta(s).labelKey) }))}
              selected={filterStatuses}
              onChange={changeFilterStatuses}
            />

            <MultiSelectFilter
              panelId="toolbar-assignee-filter-panel"
              allKey="toolbar.assigneeAll"
              selectedKey="toolbar.assigneeSelected"
              groupLabelKey="toolbar.assigneeGroupLabel"
              /* One option per distinct assignee among the loaded tickets,
                 plus the unassigned bucket -- which is the only one whose
                 text isn't its own value, since its value is a sentinel
                 assigneeFilter.ts owns and no user should ever see. */
              options={assigneeOptions.map(value => ({
                value,
                label: isUnassignedOption(value) ? t('toolbar.assigneeUnassigned') : value
              }))}
              selected={filterAssignees}
              onChange={changeFilterAssignees}
            />

            <MultiSelectFilter
              panelId="toolbar-priority-filter-panel"
              allKey="toolbar.priorityAll"
              selectedKey="toolbar.prioritySelected"
              groupLabelKey="toolbar.priorityGroupLabel"
              /* Same symbol + label wording as the ticket badge's selector
                 options (PrioritySelect.tsx), both via getPriorityMeta. */
              options={TICKET_PRIORITIES.map(p => ({
                value: p,
                label: `${getPriorityMeta(p).symbol} ${t(getPriorityMeta(p).labelKey)}`
              }))}
              selected={filterPriorities}
              onChange={changeFilterPriorities}
            />

            {/* The label filter needs one extra step -- Label records to
                options drawing a colored chip -- so it keeps its own
                component; see LabelFilter.tsx. */}
            <LabelFilter labels={projectLabels} selectedIds={filterLabelIds} onChange={changeFilterLabelIds} />
          </div>

          <div className="flex items-center gap-3 text-slate-500 dark:text-slate-400 text-xs">
            <button
              onClick={refreshTickets}
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
          {/* Four states, not two (DFLT-00106). "No project has been
              created yet" plus a create button is only ever right when the
              answer is known to be "none": shown to somebody who does have
              a project it invites them to create a duplicate, and on a
              shared data source that duplicate is everybody's. So the two
              cases where the answer is NOT known get their own branch --
              the request is still running (loading), or it failed (the
              error below, which offers a retry and no create button). */}
          {!isCurrentProjectResolved ? (
            <div
              className="text-center py-16 bg-white dark:bg-slate-900 rounded-xl border border-slate-200 dark:border-slate-800 text-slate-400 dark:text-slate-500 text-sm"
              aria-busy="true"
            >
              {t('emptyState.loadingTickets')}
            </div>
          ) : hasCurrentProjectFailed ? (
            <div
              className="text-center py-16 bg-white dark:bg-slate-900 rounded-xl border border-slate-200 dark:border-slate-800 text-slate-500 dark:text-slate-400 text-sm space-y-3"
              role="alert"
            >
              <p>{t('projectSwitcher.loadFailed')}</p>
              <button
                onClick={retryCurrentProject}
                className="px-3.5 py-1.5 rounded-lg border border-slate-300 dark:border-slate-700 hover:bg-slate-100 dark:hover:bg-slate-800 text-xs font-semibold inline-flex items-center gap-1.5 transition"
              >
                <RotateCw className="w-4 h-4" />
                {t('projectSwitcher.retry')}
              </button>
            </div>
          ) : !currentProject ? (
            <div className="text-center py-16 bg-white dark:bg-slate-900 rounded-xl border border-slate-200 dark:border-slate-800 text-slate-400 dark:text-slate-500 text-sm space-y-3">
              <p>{t('projectSwitcher.noProjectYet')}</p>
              <button
                onClick={() => {
                  setProjectSetupDirectory('');
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
                  onRefresh={refreshTickets}
                  myName={myName}
                  projectLabels={projectLabels}
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
        onLabelsChanged={() => {
          // A rename/recolor/delete shows on tickets, so re-fetch both.
          refreshProjectLabels();
          refreshTickets();
        }}
      />

      {/* Project setup / create dialog (DFLT-00080) */}
      <ProjectSetupModal
        isOpen={isCreateProjectOpen}
        directory={projectSetupDirectory}
        offerExisting={projectSetupDirectory !== ''}
        projects={projects}
        onClose={() => setIsCreateProjectOpen(false)}
        onCreated={handleProjectCreated}
        onLinked={handleProjectLinked}
        onProjectsChanged={fetchProjects}
        returnFocusRef={projectMenuButtonRef}
      />
    </div>
  );
};

export default App;
