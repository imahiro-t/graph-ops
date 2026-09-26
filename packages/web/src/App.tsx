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
import { AutopilotRun, Label, TicketDetail, TicketGraph, TicketStatus, TicketPriority, PendingApprovalCounts, Project, TICKET_STATUSES, TICKET_PRIORITIES } from './types';
import { descendantsIndex, fetchAutopilotRuns, ticketAutopilotView } from './lib/autopilotApi';
import { getStatusMeta, matchesStatusFilter } from './statusMeta';
import { getPriorityMeta, matchesPriorityFilter } from './priorityMeta';
import { matchesLabelFilter } from './labelMeta';
import { assigneeFilterOptions, isUnassignedOption, matchesAssigneeFilter } from './assigneeFilter';
import { TicketItem } from './components/TicketItem';
import { StatusLiveRegion } from './components/StatusLiveRegion';
import { LabelFilter } from './components/LabelFilter';
import { MultiSelectFilter } from './components/MultiSelectFilter';
import { fetchLabels } from './lib/labelsApi';
import { ClaudeRunnerModal } from './components/ClaudeRunnerModal';
import { SettingsModal } from './components/SettingsModal';
import { ProjectSetupModal } from './components/ProjectSetupModal';
import { CreateTicketModal } from './components/CreateTicketModal';
import { PendingApprovalBadge } from './components/PendingApprovalBadge';
import { useClaudeLaunch } from './hooks/useClaudeLaunch';
import { useTheme, ThemePreference } from './hooks/useTheme';
import { formatTime } from './i18n/formatDate';
import { localizedApiErrorMessage } from './lib/apiError';
import { apiFetch } from './lib/apiFetch';
import { fetchPendingApprovalCounts } from './lib/pendingApprovals';
import { fetchAppSettings } from './lib/settingsApi';
import { focusIfLost, focusKeySelector, neighborAfterRemoval } from './lib/focusAfterRemoval';
import { useLatest } from './hooks/useLatest';
import { useTransientAnnouncement } from './hooks/useTransientAnnouncement';

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
const NO_RUNS: AutopilotRun[] = [];
// The header project switcher's popup, referenced by its button's aria-controls.
const PROJECT_MENU_ID = 'project-switcher-menu';

// How often the dashboard re-reads the ticket list. Unchanged by DFLT-00112
// (that ticket cut the number of requests per round, not their frequency);
// named here so the polling tests can advance exactly one round.
export const POLL_INTERVAL_MS = 15000;

// Rebuilds the ticket state from a list response, carrying each ticket's
// already-fetched artifacts over by id.
//
// GET /api/tickets does not return artifacts at all (DFLT-00112), so
// dropping this merge would blank the Gherkin/HTML/artifact tabs of an
// expanded ticket on every poll -- the artifacts would reappear only once
// that ticket's own detail request resolved. A ticket the response no longer
// lists simply falls out; a newly listed one starts with no artifacts until
// it is expanded.
//
// The parent/children (DFLT-00142) are detail-only too and are carried over
// the same way, so the expanded ticket's family section does not blink out
// between the list response and its detail response.
export function mergeTicketSummaries(prev: TicketDetail[], summaries: TicketGraph[]): TicketDetail[] {
  const prevById = new Map(prev.map(t => [t.id, t]));
  return summaries.map(summary => {
    const before = prevById.get(summary.id);
    const merged: TicketDetail = { ...summary, artifacts: before?.artifacts ?? [] };
    if (before?.parent !== undefined) merged.parent = before.parent;
    if (before?.children !== undefined) merged.children = before.children;
    return merged;
  });
}

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
  //      "loading" until the next poll. An expanded ticket's detail
  //      response (DFLT-00112) only ever replaces a ticket with the same id
  //      inside the loaded list, so one for a project the user has left
  //      matches nothing (see fetchTicketDetail).
  //
  // What is NOT promised: that the header follows a switch made in another
  // tab of the same environment. It shows this window's own choice until a
  // reload (out of scope for DFLT-00106), and the list follows the header.
  //
  // "Which project is current" has four outcomes, not two, and the UI must
  // not conflate them: still loading, known to be none, known to be one,
  // and "the setting could not be read". The last matters since DFLT-00106
  // moved the setting into the local home config file, which can now be
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
  const projectMenuRef = useRef<HTMLDivElement>(null);
  // Per-project count of tickets awaiting approval, badged on the switcher's
  // menu items (DFLT-00144). Refetched every time the menu opens; empty
  // while that fetch is in flight and after it fails, so the menu never
  // shows a stale count and never depends on this request to be usable.
  const [pendingApprovalCounts, setPendingApprovalCounts] = useState<PendingApprovalCounts>({});
  // Only the latest open's answer is applied: an earlier, slower response
  // arriving after a quick close-and-reopen would otherwise overwrite the
  // fresh counts with old ones.
  const pendingApprovalsSeqRef = useRef(0);
  const refreshPendingApprovalCounts = async () => {
    const seq = ++pendingApprovalsSeqRef.current;
    setPendingApprovalCounts({});
    try {
      const counts = await fetchPendingApprovalCounts();
      if (seq === pendingApprovalsSeqRef.current) setPendingApprovalCounts(counts);
    } catch (e) {
      console.error('Failed to load pending approval counts', e);
    }
  };
  const toggleProjectMenu = () => {
    if (isProjectMenuOpen) {
      setIsProjectMenuOpen(false);
      return;
    }
    setIsProjectMenuOpen(true);
    void refreshPendingApprovalCounts();
  };
  // Escape closes the open switcher menu and puts focus back on its button
  // (DFLT-00155) when focus is on the button, a menu item, or <body> (Safari
  // does not focus a button on click). The listener only exists while the
  // menu is open, so Escape elsewhere is untouched when it is closed. An
  // Escape another handler already took, or one that cancels an IME
  // composition, is left alone -- the same rules as useModalDialog.
  //
  // Focus anywhere else is left alone too. Tab past the button and the menu
  // closes the menu (DFLT-00159, handleProjectSwitcherBlur below), but a
  // click or blur() that drops focus to <body> leaves it open, so a modal
  // can still end up open on top of it. The menu's listener was registered
  // first and so runs before the modal's (useModalDialog), and taking that
  // Escape would leave the modal open with focus pulled out of it behind it.
  // Clicking the modal's backdrop also drops focus to <body>, which on its
  // own counts as "on the switcher" below. So, as a defense, while any modal
  // dialog (aria-modal="true" -- every modal in the app, dialog or
  // alertdialog; the menu itself is non-modal) is open, the menu leaves
  // Escape to it wherever focus is.
  useEffect(() => {
    if (!isProjectMenuOpen) return;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.defaultPrevented || e.key !== 'Escape') return;
      if (e.isComposing || e.keyCode === 229) return;
      if (document.querySelector('[aria-modal="true"]')) return;
      const target = e.target;
      const focusIsOnSwitcher =
        !(target instanceof Node) ||
        target === document ||
        target === document.body ||
        target === document.documentElement ||
        projectMenuButtonRef.current?.contains(target) ||
        projectMenuRef.current?.contains(target);
      if (!focusIsOnSwitcher) return;
      e.preventDefault();
      setIsProjectMenuOpen(false);
      projectMenuButtonRef.current?.focus();
    };
    document.addEventListener('keydown', onKeyDown);
    return () => document.removeEventListener('keydown', onKeyDown);
  }, [isProjectMenuOpen]);
  // DFLT-00159: the menu closes when keyboard focus leaves the button and the
  // menu. Whether a focusout came from Tab is recorded on keydown (Tab, or
  // Shift+Tab, without Ctrl/Meta/Alt, which switch tabs or apps) and
  // consumed by the very next focusout; any other key or a pointerdown
  // clears it, so a record left behind by a Tab that moved no focus never
  // outlives the next interaction.
  const tabLeavingRef = useRef(false);
  const handleProjectSwitcherKeyDown = (e: React.KeyboardEvent) => {
    tabLeavingRef.current =
      e.key === 'Tab' && !e.ctrlKey && !e.metaKey && !e.altKey && !e.nativeEvent.isComposing;
  };
  const handleProjectSwitcherPointerDown = () => {
    tabLeavingRef.current = false;
  };
  const handleProjectSwitcherBlur = (e: React.FocusEvent) => {
    const viaTab = tabLeavingRef.current;
    tabLeavingRef.current = false;
    if (!isProjectMenuOpen) return;
    const next = e.relatedTarget;
    if (
      next instanceof Node &&
      (projectMenuButtonRef.current?.contains(next) || projectMenuRef.current?.contains(next))
    ) {
      return; // Moving between the button and the menu's items.
    }
    // No next element: focus left the page (Tab out of the first element, a
    // window or tab switch) or fell to <body> (a click on something
    // unfocusable, blur()). Only Tab closes here; closing on a click would
    // unmount the menu before the click on an item or the backdrop lands.
    if (next === null && !viaTab) return;
    setIsProjectMenuOpen(false);
  };
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

  // The current project's autopilot runs (DFLT-00142 phase 5): what the
  // badges and the autopilot buttons' enabled state are derived from.
  // Refreshed like the labels -- on a project switch, with every ticket
  // (re)fetch (so the regular poll moves the badges along), and right after
  // a start from a ticket -- and tagged with the project for the same
  // reason.
  const [runList, setRunList] = useState<ProjectScoped<AutopilotRun[]> | null>(null);
  const autopilotRuns = useMemo(
    () => (currentProjectId && runList?.projectId === currentProjectId ? runList.value : NO_RUNS),
    [currentProjectId, runList]
  );
  const refreshAutopilotRuns = useCallback(
    async (projectId: string = currentProjectIdRef.current) => {
      if (!projectId) {
        setRunList(null);
        return;
      }
      try {
        const runs = await fetchAutopilotRuns(tRef.current, projectId);
        if (projectId === currentProjectIdRef.current) setRunList({ projectId, value: runs });
      } catch (e) {
        // Keep the previous runs: the server still refuses a duplicate.
        console.error('Failed to load autopilot runs', e);
      }
    },
    [currentProjectIdRef, tRef]
  );
  useEffect(() => {
    refreshAutopilotRuns(currentProject?.id ?? '');
  }, [currentProject?.id, refreshAutopilotRuns]);
  // Returns the refresh so a start's buttons wait for the new runs before
  // they leave their "starting" state (AutopilotControls, DFLT-00147).
  const handleAutopilotChanged = useCallback(() => refreshAutopilotRuns(), [refreshAutopilotRuns]);
  // Every ticket's descendants, from the whole list's parent_ticket_id: a
  // tree start is refused when an active run roots below the ticket.
  const descendantsOf = useMemo(() => descendantsIndex(tickets), [tickets]);
  // A selected label that no longer exists in the current project (deleted,
  // or left behind by a project switch) is dropped from the selection, so
  // the filter never narrows by a label the panel can't show.
  useEffect(() => {
    setFilterLabelIds(prev => {
      const next = prev.filter(id => projectLabels.some(l => l.id === id));
      return next.length === prev.length ? prev : next;
    });
  }, [projectLabels]);

  // Pagination -- every ticket, on whatever page, is loaded up front with
  // its nodes and edges (see fetchAllTickets), so this is purely a
  // client-side slice of the already-filtered list, not a server-paged
  // fetch. That is also why paging is not what decides what gets loaded:
  // the dashboard totals and the off-page cards' progress bars read the
  // graphs of tickets the current page doesn't show.
  //
  // Reset to page 1 whenever a filter changes so the user never
  // lands on a stale, now out-of-range page. The page size itself is a
  // "アプリ設定" app-setting (GET /api/settings/app); unlike that endpoint's
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
  //
  // This is why a polling round is two requests, not one: GET /api/tickets
  // and GET /api/projects/{id}/labels. That second one is deliberately kept
  // (DFLT-00112) -- it is a single request regardless of how many tickets
  // exist, so it does not reintroduce the per-ticket fan-out the ticket set
  // out to remove, and it is what makes a teammate's label edits show up
  // with the regular poll. It follows fetchAllTickets, so it stops with the
  // polling on a hidden tab too.
  useEffect(() => {
    if (lastFetchedAt) refreshProjectLabels();
  }, [lastFetchedAt, refreshProjectLabels]);
  // The autopilot runs follow every ticket fetch the same way (DFLT-00142).
  useEffect(() => {
    if (lastFetchedAt) void refreshAutopilotRuns();
  }, [lastFetchedAt, refreshAutopilotRuns]);

  // Modals
  const [isCreateOpen, setIsCreateOpen] = useState(false);
  const [isClaudeGlobalOpen, setIsClaudeGlobalOpen] = useState(false);
  const [isSettingsOpen, setIsSettingsOpen] = useState(false);

  // Fetches one ticket's detail (GET /api/tickets/{id}) -- the only response
  // that carries artifacts -- and replaces that ticket in the list. Called
  // for the tickets the user has actually expanded, since they are the only
  // ones whose panel reads ticket.artifacts (see TicketItem.tsx: every use
  // sits inside its `isExpanded &&` block).
  //
  // The replacement is by ticket id inside whatever list is loaded when the
  // response lands, and leaves that list's project tag alone. A detail that
  // comes back after the user switched projects therefore matches nothing
  // in the new project's list and changes nothing: it cannot bring a ticket
  // of the previous project back under the new header (DFLT-00106).
  const fetchTicketDetail = useCallback(async (id: string) => {
    try {
      const res = await fetch(`/api/tickets/${encodeURIComponent(id)}`);
      if (!res.ok) throw new Error(`GET /api/tickets/${id}: ${res.status}`);
      const detail: TicketDetail = await res.json();
      setTicketList(prev =>
        prev && prev.value.some(t => t.id === detail.id)
          ? { ...prev, value: prev.value.map(t => (t.id === detail.id ? detail : t)) }
          : prev
      );
    } catch (e) {
      // Keep whatever the list response gave us for this ticket rather than
      // dropping it: a failed detail fetch must not make a card disappear.
      console.error('Failed to load ticket detail', e);
    }
  }, []);

  // Fetch one project's ticket list, plus the detail of whichever of its
  // tickets are expanded.
  //
  // DFLT-00112: one round is a single GET /api/tickets (which now carries
  // every ticket's nodes/edges) plus at most one detail request per expanded
  // ticket -- it used to be one detail request per *listed* ticket, so 100
  // tickets meant 101 requests every 15s, each re-sending the full text of
  // every stored plan and review verdict.
  //
  // projectId is mandatory (DFLT-00106): an empty one means "the current
  // project isn't known yet", and the right answer to that is to fetch
  // nothing at all rather than to ask the server for its idea of a default
  // -- it no longer has one. Showing another project's tickets under this
  // project's header is precisely the bug this replaces.
  //
  // Only the most recently started run writes the list, and only while its
  // project is still the current one. Tearing down the poll on a switch
  // stops future *timers*, but it cannot recall a request that already
  // left, and an older run can land after a newer one. The result is tagged
  // with its project, so a late run for another project could not be shown
  // anyway; the guard is what stops it from knocking the current project's
  // loaded list back to "loading" -- and, for two runs of the same project
  // (a poll and the refresh after an edit), from replacing the newer result
  // with the older. setLastFetchedAt is guarded too because it drives the
  // label refresh and the "last updated" stamp, and setLoading(false)
  // because a superseded run finishing must not clear the spinner the
  // newest run put up; the newest run clears it itself. A superseded run
  // also skips its detail requests: they would be for a list that is not
  // the one on screen.
  //
  // The flip side of "only the newest run writes" is that a run must be
  // allowed to finish before another one takes its place, or nothing is
  // ever written at all. The poll therefore does not start a run while one
  // for the same project is still in flight (see the list effect below).
  // DFLT-00112 made a run much cheaper for the browser -- one list request
  // instead of one per ticket -- but not necessarily faster: the per-ticket
  // fan-out moved to the server, and against an HTTP data source without a
  // bulk read GET /api/tickets costs one sequential remote call per ticket
  // (see ticketGraphs in internal/httpserver/tickets.go, which puts the
  // crossover with the 15s interval at about 75 tickets at a 200ms RTT).
  // Past that point each poll used to supersede the run before it, and the
  // list stayed on "loading" with the spinner going for good.
  // ticketFetchesInFlightRef counts the runs per project id for that check;
  // it is per project so that a run left over from the project the user
  // just switched away from never holds back the new one. A run counts as
  // in flight until its expanded tickets' details are back too.
  //
  // expandedTicketIds is read through a ref because this callback is held by
  // the polling interval: reading the state directly would freeze whichever
  // set was expanded when the interval was set up.
  const expandedTicketIdsRef = useLatest(expandedTicketIds);
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
      if (!res.ok) throw new Error(`GET /api/tickets: ${res.status}`);
      const summaries: TicketGraph[] = await res.json();

      if (isSuperseded()) return;
      // Artifacts are carried over only from this same project's list; a
      // list tagged with another project has nothing to contribute.
      setTicketList(prev => ({
        projectId,
        value: mergeTicketSummaries(prev?.projectId === projectId ? prev.value : NO_TICKETS, summaries)
      }));
      setLastFetchedAt(new Date());

      const expanded = summaries.filter(t => expandedTicketIdsRef.current.has(t.id));
      await Promise.all(expanded.map(t => fetchTicketDetail(t.id)));
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
  }, [currentProjectIdRef, expandedTicketIdsRef, fetchTicketDetail]);

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
  // The read is now the local home config file (DFLT-00106), which can fail
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

  // Fetches the "アプリ設定" app-settings this component needs: how many
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
  // immediately and then polled every POLL_INTERVAL_MS, since ticket/graph
  // changes happen in an external terminal the app can't see directly (no
  // more SSE stream to react to). With no project resolved yet, nothing is
  // fetched at all.
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
  // While the tab is in the background nothing is polled at all
  // (DFLT-00112): a forgotten open tab used to keep hitting the DB -- every
  // 15s, per tab, for every viewer of a shared backend -- for a dashboard
  // nobody was looking at. Coming back to the foreground fetches once
  // immediately (so the view is never up to 15s stale at the moment it
  // becomes visible again) and restarts the interval from that fetch. The
  // visibility listener belongs to this effect for the same reason the
  // interval does: after a switch, becoming visible again must fetch the
  // new project, not the one the listener was registered for. The one live
  // timer is a variable of this effect run, so a switch's cleanup and a
  // hidden tab stop exactly that timer; two intervals would double the
  // request rate for the rest of the session.
  //
  // The fetch made on entering the effect (first load, or a switch) always
  // runs, visible or not -- it is what the switch (or the page load) asked
  // for. A poll tick and the fetch on becoming visible again are skipped
  // while a run for this same project is still in flight: otherwise a fetch
  // slower than the interval would be superseded by the next tick every
  // time and never be shown (see ticketFetchesInFlightRef). The skipped
  // fetch costs nothing -- the run in flight is already fetching the same
  // thing, and its result is what the tab will show.
  useEffect(() => {
    const projectId = currentProject?.id ?? '';
    void fetchAllTickets(projectId);
    if (!projectId) return;

    const pollOnce = () => {
      if (ticketFetchesInFlightRef.current.has(projectId)) return;
      void fetchAllTickets(projectId);
    };
    let timer: ReturnType<typeof setInterval> | null = null;
    const stopPolling = () => {
      if (timer !== null) {
        clearInterval(timer);
        timer = null;
      }
    };
    const startPolling = () => {
      stopPolling();
      timer = setInterval(pollOnce, POLL_INTERVAL_MS);
    };
    const onVisibilityChange = () => {
      if (document.visibilityState === 'hidden') {
        stopPolling();
        return;
      }
      pollOnce();
      startPolling();
    };

    if (document.visibilityState === 'visible') startPolling();
    document.addEventListener('visibilitychange', onVisibilityChange);
    return () => {
      stopPolling();
      document.removeEventListener('visibilitychange', onVisibilityChange);
    };
  }, [currentProject?.id, fetchAllTickets]);

  // Consumes the `?newProject=1&workDir=<dir>` query the `graph-engine ui`
  // CLI command (the `/ui` slash command's backend) appends to the root URL
  // when no project's local path (this environment's home config file
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
    const isExpanding = !expandedTicketIds.has(id);
    setExpandedTicketIds(prev => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
    // Artifacts only arrive with a ticket's own detail response, which the
    // poll now fetches for expanded tickets only -- without this immediate
    // fetch the panel's Gherkin/HTML/artifact tabs would stay empty until
    // the next round, i.e. for up to 15 seconds after the click.
    if (isExpanding) void fetchTicketDetail(id);
  };

  // Opens a related ticket from a ticket's parent/children section
  // (DFLT-00142): expands it, brings it onto the current page -- clearing
  // the filters first if they hide it, since a link that silently goes
  // nowhere would be worse -- and scrolls it into view once rendered.
  const [focusTicketId, setFocusTicketId] = useState<string | null>(null);
  // The live announcement of what opening a related ticket changed (the
  // filters it cleared), cleared after a while so the same message can be
  // announced again.
  const { message: openNotice, announce: announceOpenNotice } = useTransientAnnouncement();
  const handleOpenTicket = (id: string) => {
    // Every ticket of the current project is loaded (paging is client-side),
    // so a ticket missing here is gone or in another project: nothing to open.
    if (!tickets.some(t => t.id === id)) return;
    let visible = filteredTickets;
    if (!visible.some(t => t.id === id)) {
      setFilterQuery('');
      setFilterStatuses([]);
      setFilterAssignees([]);
      setFilterPriorities([]);
      setFilterLabelIds([]);
      visible = tickets;
      // Say so: the filters changed without the person touching them.
      announceOpenNotice(t('ticketItem.family.filtersCleared', { id }));
    }
    const index = visible.findIndex(t => t.id === id);
    if (index >= 0) setPage(Math.floor(index / ticketsPerPage) + 1);
    if (!expandedTicketIds.has(id)) {
      setExpandedTicketIds(prev => new Set(prev).add(id));
      void fetchTicketDetail(id);
    }
    setFocusTicketId(id);
  };
  // Runs after the render that applied handleOpenTicket's page/filter/expand
  // updates (React batches them with setFocusTicketId), so the card exists.
  useEffect(() => {
    if (!focusTicketId) return;
    const el = document.getElementById(`ticket-${focusTicketId}`);
    if (el) {
      const reduceMotion = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? false;
      el.scrollIntoView?.({ block: 'start', behavior: reduceMotion ? 'auto' : 'smooth' });
      el.focus({ preventScroll: true });
    }
    setFocusTicketId(null);
  }, [focusTicketId]);

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

  // Where keyboard focus goes after a ticket is deleted (DFLT-00191). The
  // deleted card -- focused delete button included -- disappears with the
  // re-fetch, which would drop focus to <body>. It moves to the ticket that
  // took the deleted one's place in the (filtered) list, else the one before
  // it, else the header's "new ticket" button -- LabelsEditor's rule (see
  // lib/focusAfterRemoval). `before` is the filtered list's ids when the
  // delete succeeded. The move waits for the deleted id to actually leave
  // the list, however long that takes: the re-fetch that follows the delete
  // can fail, or be discarded as superseded by a newer fetch, and leave the
  // card on screen until a later fetch (the next poll, say) drops it.
  // Meanwhile TicketItem keeps focus on the card's own delete button (which
  // was disabled during the request), and when the card finally goes,
  // focusIfLost moves focus on -- without taking it from wherever the user
  // has put it in the meantime. Only a project switch drops the move.
  const [pendingTicketFocus, setPendingTicketFocus] = useState<{
    removedId: string;
    projectId: string;
    before: string[];
  } | null>(null);
  const ticketListRef = useRef<HTMLDivElement>(null);
  const newTicketButtonRef = useRef<HTMLButtonElement>(null);
  const filteredTicketsRef = useLatest(filteredTickets);
  const { message: ticketDeleteNotice, announce: announceTicketDelete } = useTransientAnnouncement();

  // TicketItem's onDeleted: called once the DELETE has succeeded. When the
  // list has already dropped the ticket (a poll got there first), `before`
  // no longer holds it and neighborAfterRemoval picks the first ticket.
  // The delete is announced here rather than in the card, whose own live
  // region leaves with it (DFLT-00194).
  const handleTicketDeleted = async (ticketId: string, ticketTitle: string) => {
    announceTicketDelete(t('ticketItem.delete.success', { id: ticketId, title: ticketTitle }));
    const projectId = currentProjectIdRef.current;
    setPendingTicketFocus({ removedId: ticketId, projectId, before: filteredTicketsRef.current.map(ticket => ticket.id) });
    await refreshTickets();
  };

  useEffect(() => {
    if (pendingTicketFocus === null) return;
    const { removedId, projectId, before } = pendingTicketFocus;
    // Another project's list is on screen now: this move no longer applies.
    if (projectId !== currentProjectId) {
      setPendingTicketFocus(null);
      return;
    }
    const currentIds = filteredTickets.map(ticket => ticket.id);
    // Still listed: wait (see above) -- the card, and TicketItem's hold on
    // focus, are still there.
    if (currentIds.includes(removedId)) return;
    setPendingTicketFocus(null);
    const pagedIds = pagedTickets.map(ticket => ticket.id);
    const neighbor = neighborAfterRemoval(before, removedId, currentIds);
    // The page clamps back when the deleted ticket was the last one on the
    // last page, so the neighbor is normally on the page shown. If it is not
    // (a filter or poll reshuffled the list meanwhile), the card now at the
    // deleted one's position on this page, else the page's last, takes it.
    let target: string | null = neighbor;
    if (target === null || !pagedIds.includes(target)) {
      const position = before.indexOf(removedId) - (currentPage - 1) * ticketsPerPage;
      target = pagedIds[Math.max(position, 0)] ?? pagedIds[pagedIds.length - 1] ?? null;
    }
    const el =
      target === null
        ? newTicketButtonRef.current
        : ticketListRef.current?.querySelector<HTMLElement>(focusKeySelector(`ticket-delete-${target}`));
    focusIfLost(el);
  }, [pendingTicketFocus, currentProjectId, filteredTickets, pagedTickets, currentPage, ticketsPerPage]);

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
            <div
              className="relative"
              onKeyDown={handleProjectSwitcherKeyDown}
              onPointerDown={handleProjectSwitcherPointerDown}
              onBlur={handleProjectSwitcherBlur}
            >
              {/* aria-haspopup="dialog", not "menu" (DFLT-00155): the popup
                  is a plain non-modal group of buttons walked with Tab, so it
                  is a role="dialog". "menu" would promise the WAI-ARIA menu
                  button pattern (menuitems, arrow-key focus, Tab closes),
                  which this popup does not implement. Like one, though, it
                  closes when keyboard focus leaves the button and the popup
                  (DFLT-00159). aria-controls only while open: the popup is
                  not rendered while closed. */}
              <button
                ref={projectMenuButtonRef}
                type="button"
                onClick={toggleProjectMenu}
                aria-expanded={isProjectMenuOpen}
                aria-haspopup="dialog"
                aria-controls={isProjectMenuOpen ? PROJECT_MENU_ID : undefined}
                className="px-3 py-1.5 rounded-lg bg-white dark:bg-slate-900 hover:bg-slate-50 dark:hover:bg-slate-800 text-xs font-semibold text-slate-700 dark:text-slate-300 border border-slate-300 dark:border-slate-700 flex items-center gap-1.5 shadow-xs transition max-w-[14rem]"
                title={currentProject ? currentProject.local_path || t('settings.appSettings.projects.notSet') : undefined}
              >
                <FolderOpen aria-hidden="true" className="w-4 h-4 text-slate-500 dark:text-slate-400 shrink-0" />
                <span className="truncate">
                  {currentProject ? currentProject.name : t('projectSwitcher.noProject')}
                </span>
                {/* DFLT-00163: WCAG 1.4.11 (3:1). The arrow is the only sign that this opens a menu.
                    slate-500 is 4.76:1 on white and 4.55:1 on the slate-50 hover; slate-400 is
                    6.96:1 on slate-900 and 5.71:1 on the slate-800 hover. */}
                <ChevronDown aria-hidden="true" className="w-3.5 h-3.5 text-slate-500 dark:text-slate-400 shrink-0" />
              </button>

              {isProjectMenuOpen && (
                <>
                  {/* data-testid: jsdom has no hit testing, so tests click
                      this backdrop directly to close the menu. */}
                  <div
                    className="fixed inset-0 z-40"
                    data-testid="project-switcher-overlay"
                    onClick={() => setIsProjectMenuOpen(false)}
                  />
                  <div
                    ref={projectMenuRef}
                    id={PROJECT_MENU_ID}
                    role="dialog"
                    aria-label={t('projectSwitcher.menuLabel')}
                    className="absolute left-0 mt-1.5 w-64 bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-lg shadow-lg z-50 py-1 text-sm"
                  >
                    {projects.length === 0 && (
                      <div className="px-3 py-2 text-xs text-slate-500 dark:text-slate-400">{t('projectSwitcher.empty')}</div>
                    )}
                    {projects.map(p => {
                      // DFLT-00158: the check mark shows the current project
                      // by color alone, so the item also carries
                      // aria-current and the icon is hidden from assistive
                      // technology. Not menuitemradio/aria-checked: the popup
                      // is a dialog of Tab-reachable buttons, not an ARIA
                      // menu. The attribute is left out (undefined, never
                      // false, which React would render as "false") on every
                      // other item.
                      const isCurrent = p.id === currentProject?.id;
                      return (
                        <button
                          key={p.id}
                          onClick={() => switchToProject(p)}
                          aria-current={isCurrent ? 'true' : undefined}
                          className="w-full text-left px-3 py-1.5 hover:bg-slate-50 dark:hover:bg-slate-800 flex items-center gap-2 text-slate-700 dark:text-slate-300"
                        >
                          <Check
                            aria-hidden="true"
                            className={`w-3.5 h-3.5 shrink-0 ${isCurrent ? 'text-blue-600 dark:text-blue-400' : 'text-transparent'}`}
                          />
                          <span className="truncate">{p.name}</span>
                          <span className="ml-auto flex items-center gap-2 shrink-0">
                            {pendingApprovalCounts[p.id] > 0 && (
                              <PendingApprovalBadge count={pendingApprovalCounts[p.id]} />
                            )}
                            {/* DFLT-00156: WCAG 1.4.3 (4.5:1) on every item background. slate-500 is
                                4.76:1 on white and 4.55:1 on the slate-50 hover; slate-400 is 6.96:1
                                on slate-900 and 5.71:1 on the slate-800 hover. The light hover margin
                                is thin: recompute if the item backgrounds get darker. */}
                            <span className="text-[10px] text-slate-500 dark:text-slate-400 font-mono">{p.prefix}</span>
                          </span>
                        </button>
                      );
                    })}
                    <div className="border-t border-slate-100 dark:border-slate-800 mt-1 pt-1">
                      <button
                        onClick={() => {
                          setIsProjectMenuOpen(false);
                          setProjectSetupDirectory('');
                          setIsCreateProjectOpen(true);
                        }}
                        className="w-full text-left px-3 py-1.5 hover:bg-slate-50 dark:hover:bg-slate-800 flex items-center gap-2 text-blue-700 dark:text-blue-400 font-medium"
                      >
                        <Plus aria-hidden="true" className="w-3.5 h-3.5" />
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
              <Terminal aria-hidden="true" className="w-4 h-4 text-indigo-600 dark:text-indigo-400" />
              {t('header.launchClaude')}
            </button>

            <button
              onClick={() => i18n.changeLanguage(currentLanguage === 'ja' ? 'en' : 'ja')}
              title={t('header.language.toggleTitle', { lang: t(`header.language.${currentLanguage}`) })}
              className="px-2 py-1.5 rounded-lg bg-white dark:bg-slate-900 hover:bg-slate-50 dark:hover:bg-slate-800 text-slate-600 dark:text-slate-300 border border-slate-300 dark:border-slate-700 shadow-xs transition flex items-center gap-1.5"
            >
              <Languages aria-hidden="true" className="w-4 h-4" />
              <span className="text-xs font-semibold">{t(`header.language.${currentLanguage}`)}</span>
            </button>

            <button
              onClick={() => setThemePreference(NEXT_THEME[themePreference])}
              title={t('header.theme.toggleTitle', { mode: t(`header.theme.${themePreference}`) })}
              className="p-1.5 rounded-lg bg-white dark:bg-slate-900 hover:bg-slate-50 dark:hover:bg-slate-800 text-slate-600 dark:text-slate-300 border border-slate-300 dark:border-slate-700 shadow-xs transition"
            >
              <ThemeIcon aria-hidden="true" className="w-4 h-4" />
            </button>

            <button
              onClick={() => setIsSettingsOpen(true)}
              title={t('header.settings')}
              className="p-1.5 rounded-lg bg-white dark:bg-slate-900 hover:bg-slate-50 dark:hover:bg-slate-800 text-slate-600 dark:text-slate-300 border border-slate-300 dark:border-slate-700 shadow-xs transition"
            >
              <SettingsIcon aria-hidden="true" className="w-4 h-4" />
            </button>

            <button
              ref={newTicketButtonRef}
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
              <Plus aria-hidden="true" className="w-4 h-4" />
              {t('header.newTicket')}
            </button>
          </div>
        </div>

        {/* Filter Toolbar */}
        <div className="max-w-7xl mx-auto flex flex-wrap items-center justify-between gap-4 mt-3 pt-3 border-t border-slate-100 dark:border-slate-800 text-xs">
          <div className="flex flex-wrap items-center gap-3">
            <div className="relative">
              {/* DFLT-00163: WCAG 1.4.11 (3:1). The magnifier marks this as a search field once the
                  placeholder is gone. slate-500 is 4.55:1 on the slate-50 input; slate-400 is
                  5.71:1 on the slate-800 input. */}
              <Search aria-hidden="true" className="w-3.5 h-3.5 absolute left-2.5 top-2.5 text-slate-500 dark:text-slate-400" />
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
              <RotateCw aria-hidden="true" className={`w-3.5 h-3.5 ${loading ? 'animate-spin' : ''}`} />
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
        <div ref={ticketListRef}>
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
          <StatusLiveRegion message={openNotice} />
          <StatusLiveRegion message={ticketDeleteNotice} />
          {!isCurrentProjectResolved ? (
            <div
              className="text-center py-16 bg-white dark:bg-slate-900 rounded-xl border border-slate-200 dark:border-slate-800 text-slate-500 dark:text-slate-400 text-sm"
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
              {/* DFLT-00164: the inherited slate-500 drops to 4.34:1 on the
                  slate-100 hover background, so the button sets slate-600 /
                  slate-300 (the DFLT-00162 pair) to keep WCAG 1.4.3's 4.5:1
                  in both states and both themes. */}
              <button
                onClick={retryCurrentProject}
                className="px-3.5 py-1.5 rounded-lg border border-slate-300 dark:border-slate-700 text-slate-600 dark:text-slate-300 hover:bg-slate-100 dark:hover:bg-slate-800 text-xs font-semibold inline-flex items-center gap-1.5 transition"
              >
                <RotateCw aria-hidden="true" className="w-4 h-4" />
                {t('projectSwitcher.retry')}
              </button>
            </div>
          ) : !currentProject ? (
            <div className="text-center py-16 bg-white dark:bg-slate-900 rounded-xl border border-slate-200 dark:border-slate-800 text-slate-500 dark:text-slate-400 text-sm space-y-3">
              <p>{t('projectSwitcher.noProjectYet')}</p>
              <button
                onClick={() => {
                  setProjectSetupDirectory('');
                  setIsCreateProjectOpen(true);
                }}
                className="px-3.5 py-1.5 rounded-lg bg-blue-600 hover:bg-blue-500 text-white text-xs font-semibold inline-flex items-center gap-1.5 shadow-xs transition"
              >
                <Plus aria-hidden="true" className="w-4 h-4" />
                {t('projectSwitcher.createNew')}
              </button>
            </div>
          ) : isTicketListPending ? (
            <div
              className="text-center py-16 bg-white dark:bg-slate-900 rounded-xl border border-slate-200 dark:border-slate-800 text-slate-500 dark:text-slate-400 text-sm"
              aria-busy="true"
            >
              {t('emptyState.loadingTickets')}
            </div>
          ) : filteredTickets.length === 0 ? (
            <div className="text-center py-16 bg-white dark:bg-slate-900 rounded-xl border border-slate-200 dark:border-slate-800 text-slate-500 dark:text-slate-400 text-sm">
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
                  onOpenTicket={handleOpenTicket}
                  onRefresh={refreshTickets}
                  onDeleted={handleTicketDeleted}
                  myName={myName}
                  projectLabels={projectLabels}
                  autopilot={ticketAutopilotView(autopilotRuns, ticket.id, descendantsOf)}
                  onAutopilotChanged={handleAutopilotChanged}
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
                      aria-label={t('pagination.previous')}
                      disabled={currentPage <= 1}
                      className="p-1.5 rounded-md border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 hover:bg-slate-100 dark:hover:bg-slate-800 disabled:opacity-40 disabled:hover:bg-white dark:disabled:hover:bg-slate-900 transition"
                    >
                      <ChevronLeft aria-hidden="true" className="w-3.5 h-3.5" />
                    </button>
                    <span className="font-mono font-semibold text-slate-700 dark:text-slate-300">
                      {t('pagination.pageOf', { page: currentPage, total: totalPages })}
                    </span>
                    <button
                      onClick={() => setPage(p => Math.min(totalPages, p + 1))}
                      aria-label={t('pagination.next')}
                      disabled={currentPage >= totalPages}
                      className="p-1.5 rounded-md border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 hover:bg-slate-100 dark:hover:bg-slate-800 disabled:opacity-40 disabled:hover:bg-white dark:disabled:hover:bg-slate-900 transition"
                    >
                      <ChevronRight aria-hidden="true" className="w-3.5 h-3.5" />
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
