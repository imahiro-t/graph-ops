import React, { useId } from 'react';
import { useTranslation } from 'react-i18next';
import { CornerLeftUp, GitFork } from 'lucide-react';
import { TicketRef } from '../types';
import { getStatusMeta } from '../statusMeta';

interface Props {
  // null/undefined: no parent (or the detail has not been fetched yet).
  parent?: TicketRef | null;
  childTickets?: TicketRef[];
  // Opens (expands, pages to and scrolls to) another ticket of the list.
  onOpenTicket?: (id: string) => void;
}

// A ticket's parent and children (DFLT-00142), shown in the expanded
// ticket's detail. Renders nothing when the ticket has neither, so tickets
// that are not part of a tree look exactly as before.
export const TicketFamily: React.FC<Props> = ({ parent, childTickets = [], onOpenTicket }) => {
  const { t } = useTranslation();
  const childrenHeadingId = useId();
  if (!parent && childTickets.length === 0) return null;

  // DFLT-00290: from 7.5rem down (a 32px default font in a 240px window or
  // narrower) a link may wrap onto several lines (upto-7_5rem:flex-wrap); at
  // 160px its label and status chip ran past the viewport.
  const link = (ref: TicketRef, testId: string) => {
    const meta = getStatusMeta(ref.status);
    return (
      <button
        type="button"
        data-testid={testId}
        onClick={() => onOpenTicket?.(ref.id)}
        className="inline-flex upto-7_5rem:flex-wrap items-center gap-2 max-w-full text-left rounded-md px-1.5 py-0.5 hover:bg-slate-100 dark:hover:bg-slate-800 focus:outline-hidden focus-visible:ring-2 focus-visible:ring-indigo-500"
        title={t('ticketItem.family.open', { id: ref.id })}
      >
        <span className="font-mono text-indigo-600 dark:text-indigo-400 font-semibold">{ref.id}</span>
        <span className="text-slate-700 dark:text-slate-300 truncate min-w-0 max-w-md">{ref.title}</span>
        <span className={`text-[0.625rem] font-semibold px-1.5 py-0.5 rounded-xs ${meta.chip.bg} ${meta.chip.text}`}>
          {t(meta.labelKey)}
        </span>
      </button>
    );
  };

  // DFLT-00290: from 7.5rem down (a 32px default font in a 240px window or
  // narrower) the card pads 0.25rem (upto-7_5rem:p-1), leaving its links
  // room to wrap inside it.
  return (
    <div
      data-testid="ticket-family"
      className="bg-white dark:bg-slate-900 p-4 upto-7_5rem:p-1 rounded-xl border border-slate-200 dark:border-slate-800 space-y-3 text-xs"
    >
      {parent && (
        <div className="flex flex-wrap items-center gap-2">
          <span className="font-bold text-slate-700 dark:text-slate-300 flex items-center gap-1.5">
            <CornerLeftUp className="w-3.5 h-3.5 text-indigo-500" aria-hidden="true" />
            {t('ticketItem.family.parent')}
          </span>
          {link(parent, 'ticket-family-parent')}
        </div>
      )}
      {childTickets.length > 0 && (
        <div>
          <div id={childrenHeadingId} className="font-bold text-slate-700 dark:text-slate-300 flex items-center gap-1.5 mb-1.5">
            <GitFork className="w-3.5 h-3.5 text-indigo-500" aria-hidden="true" />
            {t('ticketItem.family.children', { count: childTickets.length })}
          </div>
          <ul className="space-y-1" data-testid="ticket-family-children" aria-labelledby={childrenHeadingId}>
            {childTickets.map(c => (
              <li key={c.id}>{link(c, 'ticket-family-child')}</li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
};
