// Single source of truth for how a ticket's or node's *status* (as opposed to
// a node's type -- see nodeTypeMeta.ts) is labeled and colored, and how an
// unexpected status value is interpreted. Read by:
//   - TicketItem.tsx: the ticket status badge, the node status badge and the
//     progress indicator tooltip (getStatusMeta)
//   - App.tsx: the option labels of the toolbar's status filter dropdown
//     (getStatusMeta) and the status filter itself (normalizeTicketStatus)
// so a status shared by tickets and nodes (TODO / IN PROGRESS / IN REVIEW /
// DONE) is guaranteed the same wording and colors on every surface rather
// than several hard-coded switches happening to agree (DFLT-00030).
//
// Only the label key and the color pair live here. Size, font weight, the
// IN PROGRESS pulse dot and padding are deliberately left to each caller: the
// ticket badge and the node badge are intentionally different designs.
//
// Labels are uppercase in the English translation data itself (matching the
// DB values), not via CSS text-transform, so callers must not add `uppercase`.
//
// A status string not listed here (e.g. a value written directly to the DB --
// the Go side does not restrict status values) is treated as TODO
// (FALLBACK_STATUS), as the old switch's `default:` did, rather than breaking
// the UI or leaking the raw value. The badge and the filter share that
// fallback, so a ticket badged "TODO" is also matched by the TODO filter.
import { NodeStatus, TicketStatus, TICKET_STATUSES } from './types';

export interface StatusMeta {
  // i18n key under `status.*` (see en/ja translation.json).
  labelKey: string;
  // Badge colors, each including its dark-theme variant.
  chip: { bg: string; text: string };
}

// TODO and REFINED share one slate chip; they are told apart by label only.
// Before DFLT-00030 the ticket badge used text-slate-700/300 and the node
// badge text-slate-600/400 for this case; 700/300 was kept because it has the
// higher contrast against the slate-100/800 background in both themes.
const SLATE_CHIP = { bg: 'bg-slate-100 dark:bg-slate-800', text: 'text-slate-700 dark:text-slate-300' };

// Exported so callers can ask "is this the not-started entry?" by identity
// (`getStatusMeta(x) === TODO_META`, true for TODO and for unknown values)
// instead of comparing the i18n key string.
export const TODO_META: StatusMeta = { labelKey: 'status.todo', chip: SLATE_CHIP };

const STATUS_META: Record<TicketStatus | NodeStatus, StatusMeta> = {
  TODO: TODO_META,
  REFINED: { labelKey: 'status.refined', chip: SLATE_CHIP },
  'IN PROGRESS': {
    labelKey: 'status.inProgress',
    chip: { bg: 'bg-blue-100 dark:bg-blue-950', text: 'text-blue-800 dark:text-blue-300' }
  },
  'IN REVIEW': {
    labelKey: 'status.inReview',
    chip: { bg: 'bg-purple-100 dark:bg-purple-950', text: 'text-purple-800 dark:text-purple-300' }
  },
  // Ticket-only.
  'IN RELEASE': {
    labelKey: 'status.inRelease',
    chip: { bg: 'bg-amber-100 dark:bg-amber-950', text: 'text-amber-800 dark:text-amber-300' }
  },
  DONE: {
    labelKey: 'status.done',
    chip: { bg: 'bg-emerald-100 dark:bg-emerald-950', text: 'text-emerald-800 dark:text-emerald-300' }
  },
  // Ticket-only (DFLT-00043). A withdrawn-without-completing ticket, distinct
  // from DONE (emerald, actually finished) and from TODO/REFINED (slate,
  // still to do) -- a flatter, more neutral gray reads as "closed out", not
  // "not started yet".
  CLOSED: {
    labelKey: 'status.closed',
    chip: { bg: 'bg-gray-200 dark:bg-gray-800', text: 'text-gray-700 dark:text-gray-300' }
  },
  // Node-only (approval_gate, DFLT-00016). Deliberately distinct from TODO:
  // TODO means "never judged yet", REJECTED means "judged and sent back".
  REJECTED: {
    labelKey: 'status.rejected',
    chip: { bg: 'bg-red-100 dark:bg-red-950', text: 'text-red-800 dark:text-red-300' }
  },
  // Node-only (review/review_gate that sent its target back, DFLT-00042).
  // Orange, so it is distinct from TODO (slate), IN REVIEW (purple),
  // REJECTED (red), and the amber already used for pending approval and
  // loop-back edges.
  'AWAITING FIX': {
    labelKey: 'status.awaitingFix',
    chip: { bg: 'bg-orange-100 dark:bg-orange-950', text: 'text-orange-800 dark:text-orange-300' }
  }
};

// The status an unexpected value is treated as, for both display and
// filtering. Typed as a status valid for tickets *and* nodes so it can stand
// in for either.
const FALLBACK_STATUS: TicketStatus & NodeStatus = 'TODO';

export function getStatusMeta(status: string): StatusMeta {
  return Object.prototype.hasOwnProperty.call(STATUS_META, status)
    ? STATUS_META[status as TicketStatus | NodeStatus]
    : STATUS_META[FALLBACK_STATUS];
}

// A ticket's status as the status filter should see it: itself when it is one
// of TICKET_STATUSES (the filter's options), FALLBACK_STATUS otherwise. Without
// this, a ticket whose DB value is outside TICKET_STATUSES could never be
// selected by any filter state (not even "all") and would vanish from the list.
export function normalizeTicketStatus(status: string): TicketStatus {
  return (TICKET_STATUSES as readonly string[]).includes(status) ? (status as TicketStatus) : FALLBACK_STATUS;
}
