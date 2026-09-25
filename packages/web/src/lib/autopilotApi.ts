// Client for the autopilot run endpoints (DFLT-00142 phase 5) -- see
// packages/core-go/internal/httpserver/autopilot_runs.go -- and the pure
// logic that turns the runs list into what one ticket shows: its badges, and
// whether (and why) its autopilot buttons are disabled.
import { TFunction } from 'i18next';
import { AutopilotMode, AutopilotRun, AutopilotStartResponse } from '../types';
import { apiFetch } from './apiFetch';
import { localizedApiErrorMessage } from './apiError';

export async function fetchAutopilotRuns(t: TFunction, projectId: string): Promise<AutopilotRun[]> {
  const res = await apiFetch(`/api/autopilot/runs?project_id=${encodeURIComponent(projectId)}`);
  if (!res.ok) throw new Error(await localizedApiErrorMessage(t, res));
  return (await res.json()) as AutopilotRun[];
}

// Starts an autopilot run from ticketId: the server reserves the run and
// opens the orchestrator's terminal. Throws an Error carrying the localized
// message of the server's error code (AUTOPILOT_ALREADY_RUNNING,
// AUTOPILOT_ROOT_FINISHED, PROJECT_LOCAL_PATH_NOT_SET, ...).
export async function startAutopilot(
  t: TFunction,
  ticketId: string,
  mode: AutopilotMode
): Promise<AutopilotStartResponse> {
  const res = await apiFetch(`/api/tickets/${encodeURIComponent(ticketId)}/autopilot`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ mode })
  });
  if (!res.ok) throw new Error(await localizedApiErrorMessage(t, res));
  return (await res.json()) as AutopilotStartResponse;
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
  // Per mode, whether a start would take over this ticket's newest run of
  // that mode (stopped, or interrupted) instead of creating one -- which the
  // server allows even when the ticket is DONE by now (spec S4/S5).
  resumable: Record<AutopilotMode, boolean>;
}

export const NO_AUTOPILOT: TicketAutopilotView = {
  badges: [],
  awaiting: '',
  blockedBy: { ticket: '', tree: '' },
  resumable: { ticket: false, tree: false }
};

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
  const resumable: Record<AutopilotMode, boolean> = { ticket: false, tree: false };
  const newestSeen: Record<AutopilotMode, boolean> = { ticket: false, tree: false };

  for (const run of runs) {
    if (run.root === ticketId && !newestSeen[run.mode]) {
      // A start takes over only the newest run of the same root and mode,
      // and only when that one is stopped or interrupted.
      newestSeen[run.mode] = true;
      resumable[run.mode] = !run.active && run.state !== 'finished';
    }
    if (!run.active) continue;
    if (run.members.includes(ticketId)) {
      if (!blockedBy.ticket) blockedBy.ticket = run.root;
      if (!blockedBy.tree) blockedBy.tree = run.root;
      if (run.root === ticketId) add('running');
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
      blockedBy.tree = run.root;
    }
  }
  if (badges.length === 0 && !blockedBy.ticket && !blockedBy.tree && !resumable.ticket && !resumable.tree) {
    return NO_AUTOPILOT;
  }
  return { badges, awaiting, blockedBy, resumable };
}
