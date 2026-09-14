// Single source of truth for how a ticket's *priority* is labeled and
// colored (DFLT-00048) -- the priority counterpart to statusMeta.ts's
// getStatusMeta. Read by:
//   - TicketItem.tsx: the ticket priority badge/selector in the detail view
//   - App.tsx: the option labels of the toolbar's priority filter dropdown
// so the badge and the filter panel can never drift apart on wording or
// color, the same reasoning DFLT-00030 applied to status.
//
// Unlike TicketStatus, TicketPriority itself has no "unset" member --
// Ticket.priority is simply null/undefined for that case (see types.ts).
// getPriorityMeta accepts that directly (as null/undefined) rather than
// forcing every caller to invent its own sentinel, and UNSET_PRIORITY_META
// is exported so callers can compare by identity the same way
// statusMeta.ts's TODO_META does.
import { TicketPriority, TICKET_PRIORITIES } from './types';

export interface PriorityMeta {
  // i18n key under `priority.*` (see en/ja translation.json).
  labelKey: string;
  // Badge colors, each including its dark-theme variant.
  chip: { bg: string; text: string };
}

// Chosen to avoid collision with the chip colors statusMeta.ts already uses
// for ticket/node status (blue/purple/amber/emerald/gray/red/orange), so a
// ticket's status badge and priority badge are never confusable at a glance.
const PRIORITY_META: Record<TicketPriority, PriorityMeta> = {
  HIGH: {
    labelKey: 'priority.high',
    chip: { bg: 'bg-rose-100 dark:bg-rose-950', text: 'text-rose-700 dark:text-rose-300' }
  },
  MEDIUM: {
    labelKey: 'priority.medium',
    chip: { bg: 'bg-amber-100 dark:bg-amber-950', text: 'text-amber-700 dark:text-amber-300' }
  },
  LOW: {
    labelKey: 'priority.low',
    chip: { bg: 'bg-sky-100 dark:bg-sky-950', text: 'text-sky-700 dark:text-sky-300' }
  }
};

// The "no priority set" entry -- a neutral slate chip distinct from every
// PRIORITY_META color, matching statusMeta.ts's SLATE_CHIP usage for
// TODO/REFINED. Exported (like TODO_META) so callers can compare by
// identity.
export const UNSET_PRIORITY_META: PriorityMeta = {
  labelKey: 'priority.unset',
  chip: { bg: 'bg-slate-100 dark:bg-slate-800', text: 'text-slate-700 dark:text-slate-400' }
};

// priority is whatever Ticket.priority holds: one of the three levels,
// null/undefined (never set), or -- in principle -- an unexpected raw DB
// value (the Go side does not restrict this column's contents beyond what
// handleUpdateTicket's own validation allows through). Every case besides
// the three known levels falls back to UNSET_PRIORITY_META, the same
// "unrecognized value reads as the neutral case" policy statusMeta.ts uses.
export function getPriorityMeta(priority: string | null | undefined): PriorityMeta {
  return priority != null && Object.prototype.hasOwnProperty.call(PRIORITY_META, priority)
    ? PRIORITY_META[priority as TicketPriority]
    : UNSET_PRIORITY_META;
}

// A ticket's priority as the priority filter should see it: itself when it
// is one of TICKET_PRIORITIES, null otherwise (covering both an actually
// unset ticket and, defensively, an unexpected raw DB value -- matching
// statusMeta.ts's normalizeTicketStatus). App.tsx's filter treats a null
// result as the 'UNSET' bucket, so an unrecognized value is never silently
// dropped from every filter selection the way it would be if it were kept
// verbatim and matched nothing.
export function normalizeTicketPriority(priority: string | null | undefined): TicketPriority | null {
  return priority != null && (TICKET_PRIORITIES as readonly string[]).includes(priority)
    ? (priority as TicketPriority)
    : null;
}
