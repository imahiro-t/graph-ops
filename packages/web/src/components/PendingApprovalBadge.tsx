import { useTranslation } from 'react-i18next';

// PENDING_APPROVAL_BADGE_CLASSES colors the project switcher's "tickets
// awaiting approval" count (DFLT-00144) in the amber of the ticket list's
// awaiting-approval blink. Both pairs are opaque on purpose, so the text
// contrast depends only on these two colors and not on whatever the menu
// item behind the badge is painted (its hover background included). With
// Tailwind v3's default palette (tailwind.config.js does not override
// amber) the text contrast is 6.37:1 in light (amber-800 #92400e on
// amber-100 #fef3c7) and 8.15:1 in dark (amber-100 #fef3c7 on amber-900
// #78350f), both above WCAG 1.4.3's 4.5:1. No blink here: the menu is
// already what the user opened to look at.
export const PENDING_APPROVAL_BADGE_CLASSES =
  'bg-amber-100 text-amber-800 dark:bg-amber-900 dark:text-amber-100';

// PendingApprovalBadge shows how many tickets await approval in a project.
// The visible number alone does not say what it counts, so the badge is a
// role="img" with the full phrase as its accessible name (also its title,
// for mouse users); it therefore reads as part of the menu item's name:
// "<project name> <N tickets awaiting approval> <prefix>".
export function PendingApprovalBadge({ count }: { count: number }) {
  const { t } = useTranslation();
  const label = t('projectSwitcher.pendingApprovals', { count });
  return (
    <span
      role="img"
      aria-label={label}
      title={label}
      className={`inline-flex items-center justify-center min-w-[1.25rem] px-1.5 rounded-full text-[10px] font-bold leading-4 ${PENDING_APPROVAL_BADGE_CLASSES}`}
    >
      {count}
    </span>
  );
}
