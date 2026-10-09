// Client for the autopilot run endpoints (DFLT-00142 phase 5) -- see
// packages/core-go/internal/httpserver/autopilot_runs.go -- and the pure
// logic that turns the runs list into what one ticket shows: its badges, and
// whether (and why) its autopilot buttons are disabled.
import { TFunction } from 'i18next';
import { AutopilotMode, AutopilotRun, AutopilotStarter, AutopilotStartResponse, ModelCap } from '../types';
import { apiFetch } from './apiFetch';
import { memberLabel } from './memberName';
import { localizedApiErrorMessage, parseApiError, translateErrorCode } from './apiError';

export async function fetchAutopilotRuns(t: TFunction, projectId: string): Promise<AutopilotRun[]> {
  const res = await apiFetch(`/api/autopilot/runs?project_id=${encodeURIComponent(projectId)}`);
  if (!res.ok) throw new Error(await localizedApiErrorMessage(t, res));
  return (await res.json()) as AutopilotRun[];
}

// Starts an autopilot run from ticketId: the server reserves the run and
// opens the orchestrator's terminal. Throws an Error carrying the localized
// message of the server's error code (AUTOPILOT_ALREADY_RUNNING,
// AUTOPILOT_ROOT_FINISHED, PROJECT_LOCAL_PATH_NOT_SET, ...).
//
// model is the run's model cap (DFLT-00375). "Not specified" ('') is sent as
// inherit: for a new run that is the same as no cap, and for a resumed run it
// clears the cap the run recorded -- the dialog's choice always applies.
export async function startAutopilot(
  t: TFunction,
  ticketId: string,
  mode: AutopilotMode,
  model: ModelCap = ''
): Promise<AutopilotStartResponse> {
  const res = await apiFetch(`/api/tickets/${encodeURIComponent(ticketId)}/autopilot`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ mode, model: model || 'inherit' })
  });
  if (!res.ok) {
    const payload = await parseApiError(res);
    if (!payload) throw new Error(t('errors.UNKNOWN'));
    throw new Error(startErrorMessage(t, payload.code, payload.details));
  }
  return (await res.json()) as AutopilotStartResponse;
}

// startErrorMessage localizes a refused start. AUTOPILOT_ALREADY_RUNNING
// names who started the overlapping run when the server says (DFLT-00326:
// another member's run on a shared database, details.started_by).
export function startErrorMessage(t: TFunction, code: string, details?: Record<string, unknown>): string {
  const name = details?.started_by;
  if (code === 'AUTOPILOT_ALREADY_RUNNING' && typeof name === 'string' && name !== '') {
    const label = starterLabel(t, { name, name_is_fallback: details?.name_is_fallback === true });
    return t('autopilot.alreadyRunningBy', { name: label });
  }
  return translateErrorCode(t, code);
}

// The badges a ticket can carry while an active run owns it:
//   running        -- it is the root of the run ("autopilot running")
//   processing     -- its child session is running now
//   awaitingHuman  -- its child session waits for a person
//   waiting        -- the run will still launch it (the run's pending list:
//                     not DONE/CLOSED, not beyond maxDepth/maxTickets, not
//                     under a ticket in progress elsewhere or a failed one)
export type AutopilotBadge = 'running' | 'processing' | 'awaitingHuman' | 'waiting';

export interface TicketAutopilotView {
  badges: AutopilotBadge[];
  // What the person is waited on for, with the awaitingHuman badge.
  awaiting: string;
  // Per mode, the root of the active run that makes a start from this
  // ticket a duplicate ('' when that mode can be started). The server's rule
  // (autopilot.Registry.Begin's overlap check): any mode is refused for a
  // ticket an active run owns; a tree start is also refused when an active
  // run's root lies among the ticket's descendants.
  blockedBy: Record<AutopilotMode, string>;
  // Per mode, who started the run in blockedBy when it is another member's
  // (DFLT-00326), null when it is this machine's own or nothing blocks.
  blockedByStarter: Record<AutopilotMode, AutopilotStarter | null>;
  // With the running badge: who started the run when it is another
  // member's, null when it is this machine's own.
  runningBy: AutopilotStarter | null;
  // Per mode, whether a start would take over this ticket's newest run of
  // that mode (stopped, or interrupted) instead of creating one -- which the
  // server allows even when the ticket is DONE by now (spec S4/S5). Only this
  // machine's runs count: another member's run is never taken over.
  resumable: Record<AutopilotMode, boolean>;
}

export const NO_AUTOPILOT: TicketAutopilotView = {
  badges: [],
  awaiting: '',
  blockedBy: { ticket: '', tree: '' },
  blockedByStarter: { ticket: null, tree: null },
  runningBy: null,
  resumable: { ticket: false, tree: false }
};

// isMine: whether run is this machine's. An older server sends no `mine`
// and lists only this machine's runs.
export function isMine(run: AutopilotRun): boolean {
  return run.mine !== false;
}

// foreignStarter is who started run when it is another member's, null for
// this machine's own run.
function foreignStarter(run: AutopilotRun): AutopilotStarter | null {
  if (isMine(run)) return null;
  return run.started_by ?? { name: '', name_is_fallback: false };
}

// Returns a lookup of every descendant of a ticket (the ticket excluded),
// built from the ticket list's parent_ticket_id.
export function descendantsIndex(
  tickets: ReadonlyArray<{ id: string; parent_ticket_id?: string | null }>
): (id: string) => Set<string> {
  const children = new Map<string, string[]>();
  for (const t of tickets) {
    if (!t.parent_ticket_id) continue;
    const list = children.get(t.parent_ticket_id) ?? [];
    list.push(t.id);
    children.set(t.parent_ticket_id, list);
  }
  const cache = new Map<string, Set<string>>();
  return (id: string) => {
    const hit = cache.get(id);
    if (hit) return hit;
    const out = new Set<string>();
    const stack = [...(children.get(id) ?? [])];
    while (stack.length > 0) {
      const c = stack.pop()!;
      if (c === id || out.has(c)) continue;
      out.add(c);
      stack.push(...(children.get(c) ?? []));
    }
    cache.set(id, out);
    return out;
  };
}

// ticketAutopilotView derives one ticket's autopilot display from the runs
// list (newest first, as the API returns it).
export function ticketAutopilotView(
  runs: ReadonlyArray<AutopilotRun>,
  ticketId: string,
  descendantsOf: (id: string) => Set<string>
): TicketAutopilotView {
  const badges: AutopilotBadge[] = [];
  const add = (b: AutopilotBadge) => {
    if (!badges.includes(b)) badges.push(b);
  };
  let awaiting = '';
  const blockedBy: Record<AutopilotMode, string> = { ticket: '', tree: '' };
  const blockedByStarter: Record<AutopilotMode, AutopilotStarter | null> = { ticket: null, tree: null };
  let runningBy: AutopilotStarter | null = null;
  const resumable: Record<AutopilotMode, boolean> = { ticket: false, tree: false };
  const newestSeen: Record<AutopilotMode, boolean> = { ticket: false, tree: false };
  const block = (mode: AutopilotMode, run: AutopilotRun) => {
    blockedBy[mode] = run.root;
    blockedByStarter[mode] = foreignStarter(run);
  };

  for (const run of runs) {
    if (run.root === ticketId && isMine(run) && !newestSeen[run.mode]) {
      // A start takes over only the newest of this machine's runs of the
      // same root and mode, and only when that one is stopped or
      // interrupted. Another member's run is never taken over (DFLT-00326).
      newestSeen[run.mode] = true;
      resumable[run.mode] = !run.active && run.state !== 'finished';
    }
    if (!run.active) continue;
    if (run.members.includes(ticketId)) {
      if (!blockedBy.ticket) block('ticket', run);
      if (!blockedBy.tree) block('tree', run);
      if (run.root === ticketId) {
        add('running');
        runningBy = foreignStarter(run);
      }
      if (run.current === ticketId) {
        if (run.awaiting_human) {
          add('awaitingHuman');
          awaiting = run.awaiting_human;
        } else {
          add('processing');
        }
      } else if (run.root !== ticketId && run.pending.includes(ticketId)) {
        add('waiting');
      }
    } else if (!blockedBy.tree && descendantsOf(ticketId).has(run.root)) {
      block('tree', run);
    }
  }
  if (badges.length === 0 && !blockedBy.ticket && !blockedBy.tree && !resumable.ticket && !resumable.tree) {
    return NO_AUTOPILOT;
  }
  return { badges, awaiting, blockedBy, blockedByStarter, runningBy, resumable };
}

// starterLabel is how a run's starter is named on screen: the name, with
// "(name not set)" after a "<OS user>@<host>" fallback.
export function starterLabel(t: TFunction, starter: AutopilotStarter): string {
  return memberLabel(t, starter.name, starter.name_is_fallback);
}
