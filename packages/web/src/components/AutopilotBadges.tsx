import React from 'react';
import { useTranslation } from 'react-i18next';
import { Bot, Hand, Hourglass, Loader2 } from 'lucide-react';
import { AutopilotBadge, TicketAutopilotView } from '../lib/autopilotApi';

// The autopilot badges of one ticket (DFLT-00142 phase 5), shown in its
// header row -- so both the collapsed list and the expanded detail show them.
// Renders nothing for a ticket no active run owns.
//
// Each badge carries its meaning as text (never color or the icon alone);
// the icons are decorative.
const STYLE: Record<AutopilotBadge, string> = {
  running: 'bg-violet-50 dark:bg-violet-950 border-violet-200 dark:border-violet-800 text-violet-800 dark:text-violet-200',
  processing: 'bg-blue-50 dark:bg-blue-950 border-blue-200 dark:border-blue-800 text-blue-800 dark:text-blue-200',
  awaitingHuman: 'bg-amber-50 dark:bg-amber-950 border-amber-300 dark:border-amber-700 text-amber-900 dark:text-amber-100',
  waiting: 'bg-slate-50 dark:bg-slate-800 border-slate-200 dark:border-slate-700 text-slate-700 dark:text-slate-300'
};

const ICON: Record<AutopilotBadge, React.ReactNode> = {
  running: <Bot className="w-3 h-3 shrink-0" aria-hidden="true" />,
  processing: <Loader2 className="w-3 h-3 shrink-0 motion-safe:animate-spin" aria-hidden="true" />,
  awaitingHuman: <Hand className="w-3 h-3 shrink-0" aria-hidden="true" />,
  waiting: <Hourglass className="w-3 h-3 shrink-0" aria-hidden="true" />
};

export const AutopilotBadges: React.FC<{ view: TicketAutopilotView }> = ({ view }) => {
  const { t } = useTranslation();
  if (view.badges.length === 0) return null;
  return (
    <span className="flex items-center gap-1 shrink-0" data-testid="autopilot-badges">
      {view.badges.map(b => (
        <span
          key={b}
          data-testid={`autopilot-badge-${b}`}
          title={b === 'awaitingHuman' && view.awaiting ? t('autopilot.badges.awaitingTitle', { what: view.awaiting }) : undefined}
          className={`flex items-center gap-1 px-2 py-0.5 rounded-full border text-[11px] font-bold whitespace-nowrap ${STYLE[b]}`}
        >
          {ICON[b]}
          {t(`autopilot.badges.${b}`)}
          {b === 'awaitingHuman' && view.awaiting && (
            <span className="sr-only">{t('autopilot.badges.awaitingTitle', { what: view.awaiting })}</span>
          )}
        </span>
      ))}
    </span>
  );
};
