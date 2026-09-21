import React, { useCallback, useMemo, useState, useEffect, useRef } from 'react';
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

// Where the answer to "which project is current?" stands. One value rather
// than a project plus two flags: with separate flags, every path that sets a
// project had to remember to clear "failed" as well, and the ones that
// didn't (switching or linking from the failure screen) left an error
// under a header that already named the new project (DFLT-00106).
type CurrentProjectState =
  | { kind: 'loading' }
  | { kind: 'failed' }
  | { kind: 'resolved'; project: Project | null };

// Data fetched for one project, tagged with that project's id. What is
// rendered is only ever the value whose id matches the header's project;
// anything else -- the previous project's list during a switch, a response
// for a project the user has since left -- renders as "not loaded yet".
interface ProjectScoped<T> {
  projectId: string;
  value: T;
}

const NO_LABELS: Label[] = [];
const NO_TICKETS: TicketDetail[] = [];

export const App: React.FC = () => {
  const { t, i18n } = useTranslation();
  // See src/hooks/useLatest.ts -- keeps fetchTicketsPerPage below
  // insensitive to language changes (F-1/L-2: switching language must not
  // re-run the startup effect that calls it).
  const tRef = useLatest(t);
  const { preference: themePreference, setPreference: setThemePreference } = useTheme();
  // The last ticket list fetched, tagged with the project it belongs to.
  // Read it through `tickets` (derived below, next to currentProject), which
  // is empty unless the tag matches the header's project.
  const [ticketList, setTicketList] = useState<ProjectScoped<TicketDetail[]> | null>(null);
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
  // What keeps the header and the body describing the same project, from
  // the broadest guarantee to the narrowest:
  //
  //   1. Rendering. Everything project-scoped that reaches the screen -- the
  //      ticket list, the summary counts, the assignee and label options,
  //      the labels drawn on tickets -- is derived from a value tagged with
  //      the project it was fetched for (ProjectScoped), and only when that
  //      tag equals the header's project. Until it does, the list area says
  //      "loading", never the previous project's tickets. This holds on
  //      every path that moves the header (startup, switch, link, create,
  //      delete/rename via the settings modal, retry) whatever state the
  //      fetches are in, so none of those paths has to clean up after the
  //      previous project by hand.
  //   2. Requests. Every request is issued for the id the header is
  //      rendering: refreshTickets reads it from currentProjectIdRef and the
  //      ticket-list effect below keys on it, so no call site can ask for
  //      anything else, and switching projects tears the old poll down.
  //   3. Writes. A request already in flight when the header moves still
  //      comes back. fetchAllTickets drops it (a newer run, or a different
  //      current project, supersedes it), as refreshProjectLabels does for
  //      labels. (1) already stops such a response from being *shown*; (3)
  //      stops it from knocking the current project's loaded list back to
  //      "loading" until the next poll.
  //
  // What is NOT promised: that the header follows a switch made in another
  // tab of the same environment. It shows this window's own choice until a
  // reload (out of scope for DFLT-00106), and the list follows the header.
  //
  // "Which project is current" has four outcomes, not two, and the UI must
  // not conflate them: still loading, known to be none, known to be one,
  // and "the setting could not be read". The last matters since DFLT-00106
  // moved the setting into a local graph-config.json, which can now be
  // unreadable on its own: offering "create a project" to somebody who
  // already has one is how a shared data source acquires duplicate
  // projects. All four live in one CurrentProjectState, so choosing a
  // project (switch, link, create) leaves "failed" behind by construction.
  const [projects, setProjects] = useState<Project[]>([]);
  const [currentProjectState, setCurrentProjectState] = useState<CurrentProjectState>({ kind: 'loading' });
  const currentProject = currentProjectState.kind === 'resolved' ? currentProjectState.project : null;
  const isCurrentProjectResolved = currentProjectState.kind !== 'loading';
  const hasCurrentProjectFailed = currentProjectState.kind === 'failed';
  // Bumped whenever the user picks a project (switch, create, link). A
  // GET /api/current-project issued before the pick -- the startup read, a
  // retry, a refresh after a settings edit -- may have been answered with
  // the old setting, and must not put the old project back over the
  // user's choice (fetchCurrentProject drops it).
  const projectChoiceSeqRef = useRef(0);
  const chooseProject = (project: Project) => {
    projectChoiceSeqRef.current += 1;
    setCurrentProjectState({ kind: 'resolved', project });
  };
  // Only ever the header's project's tickets; see ProjectScoped.
  const tickets =
    currentProject && ticketList?.projectId === currentProject.id ? ticketList.value : NO_TICKETS;
  // A project is selected but its list has not arrived yet (first load, or
  // just after a switch): the list area says "loading", not "no tickets".
  const isTicketListPending = !!currentProject && ticketList?.projectId !== currentProject.id;
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
  //
  // Tagged like ticketList, so the previous project's labels are never
  // offered (or drawn on tickets) under the new project's header while the
  // new ones load.
  const [labelList, setLabelList] = useState<ProjectScoped<Label[]> | null>(null);
  const currentProjectId = currentProject?.id ?? '';
  const projectLabels = useMemo(
    () => (currentProjectId && labelList?.projectId === currentProjectId ? labelList.value : NO_LABELS),
    [currentProjectId, labelList]
  );
  const currentProjectIdRef = useLatest(currentProjectId);
  const refreshProjectLabels = useCallback(
    async (projectId: string = currentProjectIdRef.current) => {
      if (!projectId) {
        setLabelList(null);
        return;
      }
      try {
        const labels = await fetchLabels(tRef.current, projectId);
        // Ignore a response for a project that is no longer current.
        if (projectId === currentProjectIdRef.current) setLabelList({ projectId, value: labels });
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
  // Only the most recently started run writes anything, and only while its
  // project is still the current one. Tearing down the poll on a switch
  // stops future *timers*, but it cannot recall a request that already
  // left: this function awaits the list and then one GET /api/tickets/<id>
  // per ticket, so its in-flight window grows with the list, and an older
  // run routinely lands after a newer one. The result is tagged with its
  // project, so a late run for another project could not be shown anyway;
  // the guard is what stops it from knocking the current project's loaded
  // list back to "loading" -- and, for two runs of the same project (a
  // poll and the refresh after an edit), from replacing the newer result
  // with the older. setLastFetchedAt is guarded too because it drives the
  // label refresh and the "last updated" stamp, and setLoading(false)
  // because a superseded run finishing must not clear the spinner the
  // newest run put up; the newest run clears it itself.
  //
  // The flip side of "only the newest run writes" is that a run must be
  // allowed to finish before another one takes its place, or nothing is
  // ever written at all. The 15s poll therefore does not start a run while
  // one for the same project is still in flight (see the list effect
  // below): on a project whose full fetch takes longer than the interval --
  // many tickets, or a slow HTTP data source -- each poll used to supersede
  // the run before it, and the list stayed on "loading" with the spinner
  // going for good. ticketFetchesInFlightRef counts the runs per project id
  // for that check; it is per project so that a run left over from the
  // project the user just switched away from never holds back the new one.
  const ticketFetchSeqRef = useRef(0);
  const ticketFetchesInFlightRef = useRef(new Map<string, number>());
  const fetchAllTickets = useCallback(async (projectId: string) => {
    const seq = ++ticketFetchSeqRef.current;
    if (!projectId) {
      setTicketList(null);
      // This run supersedes any still in flight (for a project that was
      // just deleted from the settings modal, say), and those skip their
      // own setLoading(false). Nothing else would clear the spinner.
      setLoading(false);
      return;
    }
    const isSuperseded = () => seq !== ticketFetchSeqRef.current || projectId !== currentProjectIdRef.current;
    const inFlight = ticketFetchesInFlightRef.current;
    inFlight.set(projectId, (inFlight.get(projectId) ?? 0) + 1);
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

      if (isSuperseded()) return;
      setTicketList({ projectId, value: details });
      setLastFetchedAt(new Date());
    } catch (e) {
      console.error('Failed to load tickets', e);
      // A failed refresh keeps the list it already had for this project. A
      // failed *first* load for it settles on an empty list rather than
      // "loading" forever -- the screen a failed first load always gave --
      // and the next poll tries again.
      if (!isSuperseded()) {
        setTicketList(prev => (prev?.projectId === projectId ? prev : { projectId, value: [] }));
      }
    } finally {
      const remaining = (inFlight.get(projectId) ?? 1) - 1;
      if (remaining > 0) inFlight.set(projectId, remaining);
      else inFlight.delete(projectId);
      if (!isSuperseded()) setLoading(false);
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
  // can actually reach.
  //
  // An answer that arrives after the user has picked a project themselves
  // is dropped (see projectChoiceSeqRef): it describes the setting as it
  // was before the pick.
  const fetchCurrentProject = async () => {
    const seq = projectChoiceSeqRef.current;
    let next: CurrentProjectState;
    try {
      const res = await fetch('/api/current-project');
      if (!res.ok) throw new Error(`GET /api/current-project: ${res.status}`);
      const data = await res.json();
      next = { kind: 'resolved', project: data ?? null };
    } catch (e) {
      console.error('Failed to load current project', e);
      next = { kind: 'failed' };
    }
    if (seq === projectChoiceSeqRef.current) setCurrentProjectState(next);
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
    setCurrentProjectState({ kind: 'loading' });
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
  // This effect is layer 2 ("requests") of the project-scoping comment at
  // the state declarations. Having the poll live in an effect that *depends
  // on* currentProject.id keeps every request pointed at the header's
  // project without each call site having to remember: switching tears the
  // old interval down (cleanup) and starts one for the new id, so after a
  // switch no timer can ever fire for the previous project again.
  //
  // It does not decide what is shown -- the tag check at render (layer 1)
  // does -- and it cannot recall a request the old timer (or the switch
  // itself) already started. Dropping that response when it comes back is
  // fetchAllTickets' guard (layer 3).
  //
  // The fetch made on entering the effect (first load, or a switch) always
  // runs. Only a poll tick is skipped, and only while a run for this same
  // project is still in flight: otherwise a fetch slower than the interval
  // would be superseded by the next tick every time and never be shown
  // (see ticketFetchesInFlightRef). The skipped tick costs nothing -- the
  // run in flight is already fetching the same thing.
  useEffect(() => {
    const projectId = currentProject?.id ?? '';
    fetchAllTickets(projectId);
    if (!projectId) return;
    const interval = setInterval(() => {
      if (ticketFetchesInFlightRef.current.has(projectId)) return;
      fetchAllTickets(projectId);
    }, 15000);
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
    // Also leaves a failed or still-running read behind: the answer is now
    // the user's own choice.
    chooseProject(project);
    // Label ids are per project, so the previous project's selection can't
    // apply to the new one.
    setFilterLabelIds([]);
    setPage(1);
    // No explicit fetch: the ticket-list effect keys on currentProject.id,
    // so chooseProject above already re-points it at the new project, and
    // until that fetch lands the list area says "loading" (ProjectScoped).
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
    chooseProject(project);
    setFilterLabelIds([]);
    setPage(1);
    await fetchProjects();
    // The ticket-list effect follows chooseProject; see switchToProject.
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
              error below, which offers a retry and no create button). The
              flags all come from one CurrentProjectState, so "failed" and
              "a project is selected" cannot both hold. Once a project is
              selected, its list not having arrived yet is "loading" too --
              not the empty state, and never the previous project's list. */}
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
          ) : isTicketListPending ? (
            <div
              className="text-center py-16 bg-white dark:bg-slate-900 rounded-xl border border-slate-200 dark:border-slate-800 text-slate-400 dark:text-slate-500 text-sm"
              aria-busy="true"
            >
              {t('emptyState.loadingTickets')}
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
