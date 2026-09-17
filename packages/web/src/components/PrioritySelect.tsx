import React from 'react';
import { useTranslation } from 'react-i18next';
import { TicketPriority, TICKET_PRIORITIES } from '../types';
import { getPriorityMeta, normalizeTicketPriority } from '../priorityMeta';

interface Props {
  ticketId: string;
  priority: TicketPriority;
  disabled?: boolean;
  onChange: (next: TicketPriority) => void;
}

// The ticket's priority badge, which is also its priority selector
// (DFLT-00048, DFLT-00083). The visible chip shows only a one-character
// symbol (↑ / − / ↓, see priorityMeta.ts) to keep the header row narrow; the
// label (高/中/低, High/Medium/Low) is carried by the tooltip and the
// accessible name instead.
//
// A transparent native <select> is stretched over the chip rather than
// building a custom dropdown, so it stays keyboard/screen-reader operable
// for free (Tab, arrow keys, typeahead). Its own focus ring would be
// invisible at opacity-0, so the wrapper draws one via focus-within. The
// options spell out symbol + label, so the list is self-explanatory once
// opened. There are exactly three: a priority can be changed, never cleared.
export const PrioritySelect: React.FC<Props> = ({ ticketId, priority, disabled = false, onChange }) => {
  const { t } = useTranslation();
  const value = normalizeTicketPriority(priority);
  const meta = getPriorityMeta(value);
  const label = t(meta.labelKey);

  return (
    <span
      title={label}
      data-priority={value}
      className={`relative inline-flex items-center justify-center min-w-[1.75rem] px-2 py-0.5 rounded-full text-xs leading-4 whitespace-nowrap focus-within:ring-2 focus-within:ring-blue-500 ${disabled ? 'opacity-50' : ''} ${meta.chip.bg} ${meta.chip.text}`}
    >
      <span aria-hidden="true" data-testid="priority-symbol" className={meta.symbolClass}>
        {meta.symbol}
      </span>
      <select
        id={`priority-select-${ticketId}`}
        aria-label={`${t('ticketItem.priority.label')}: ${label}`}
        title={label}
        value={value}
        disabled={disabled}
        onChange={e => onChange(e.target.value as TicketPriority)}
        className="absolute inset-0 w-full h-full opacity-0 cursor-pointer disabled:cursor-default"
      >
        {TICKET_PRIORITIES.map(p => {
          const optionMeta = getPriorityMeta(p);
          return (
            <option key={p} value={p}>
              {`${optionMeta.symbol} ${t(optionMeta.labelKey)}`}
            </option>
          );
        })}
      </select>
    </span>
  );
};
