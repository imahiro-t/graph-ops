// Single source of truth for how a ticket's *priority* is labeled, colored
// and drawn (DFLT-00048, DFLT-00083) -- the priority counterpart to
// statusMeta.ts's getStatusMeta. Read by:
//   - components/PrioritySelect.tsx: the one-character priority badge/selector
//     in each ticket's header row
//   - App.tsx: the option labels of the toolbar's priority filter dropdown
// so the badge and the filter panel can never drift apart on wording,
// symbol or color, the same reasoning DFLT-00030 applied to status.
//
// A ticket's priority is always one of the three levels (DFLT-00083): there
// is no "unset" state, and the backend defaults to MEDIUM and backfills old
// NULL rows by migration. The only fallback left here is a UI-side guard
// against an unexpected value (see getPriorityMeta) -- the Go store/engine/
// API deliberately do no such read-time substitution.
import { TicketPriority, TICKET_PRIORITIES } from './types';

export interface PriorityMeta {
  // i18n key under `priority.*` (see en/ja translation.json). Used for the
  // badge's tooltip/aria-label and the filter/option text, since the badge
  // itself shows only `symbol`.
  labelKey: string;
  // The single character the badge shows. MEDIUM uses U+2212 MINUS SIGN,
  // not the ASCII hyphen-minus, so it reads as a proper dash at the same
  // width as the arrows.
  symbol: string;
  // Extra classes for the symbol only. MEDIUM is the default, so it is drawn
  // deliberately understated -- `font-normal` (plus a lighter tone) versus
  // `font-bold` for HIGH/LOW; `font-normal` is the MEDIUM-only marker tests
  // key on.
  symbolClass: string;
  // Badge colors, each including its dark-theme variant.
  chip: { bg: string; text: string };
}

// Chosen to avoid collision with the chip colors statusMeta.ts already uses
// for ticket/node status (blue/purple/amber/emerald/gray/red/orange), so a
// ticket's status badge and priority badge are never confusable at a glance.
const PRIORITY_META: Record<TicketPriority, PriorityMeta> = {
  HIGH: {
    labelKey: 'priority.high',
    symbol: '↑', // ↑
    symbolClass: 'font-bold',
    chip: { bg: 'bg-rose-100 dark:bg-rose-950', text: 'text-rose-700 dark:text-rose-300' }
  },
  MEDIUM: {
    labelKey: 'priority.medium',
    symbol: '−', // − (MINUS SIGN)
    symbolClass: 'font-normal opacity-60',
    chip: { bg: 'bg-amber-100 dark:bg-amber-950', text: 'text-amber-700 dark:text-amber-300' }
  },
  LOW: {
    labelKey: 'priority.low',
    symbol: '↓', // ↓
    symbolClass: 'font-bold',
    chip: { bg: 'bg-sky-100 dark:bg-sky-950', text: 'text-sky-700 dark:text-sky-300' }
  }
};

// A ticket's priority as the UI should treat it: itself when it is one of
// TICKET_PRIORITIES, MEDIUM otherwise. Ticket.priority is typed as never
// being anything else, so the fallback is purely defensive -- e.g. a ticket
// written by an older graph-engine sharing the same MySQL, which reads back
// as "" until the next Init backfills it. Showing and filtering it as MEDIUM
// keeps the UI from breaking without the backend ever treating the data
// that way.
export function normalizeTicketPriority(priority: string | null | undefined): TicketPriority {
  return priority != null && (TICKET_PRIORITIES as readonly string[]).includes(priority)
    ? (priority as TicketPriority)
    : 'MEDIUM';
}

export function getPriorityMeta(priority: string | null | undefined): PriorityMeta {
  return PRIORITY_META[normalizeTicketPriority(priority)];
}

// Whether a ticket with this priority passes the toolbar's priority filter
// (App.tsx), given the levels currently checked there. Uses the same
// normalization as the badge, so a ticket is always filtered under the
// level it is displayed as.
export function matchesPriorityFilter(
  priority: string | null | undefined,
  selected: readonly TicketPriority[]
): boolean {
  return selected.includes(normalizeTicketPriority(priority));
}
