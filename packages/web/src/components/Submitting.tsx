// Shared pieces for a button that is disabled (or aria-disabled) while the
// user's own action is being sent and shows an aria-hidden spinner meanwhile
// (DFLT-00176 for the approval gate, generalised in DFLT-00206). A disabled
// button with a hidden spinner only reads as "unavailable", so while the
// action is in flight the button also:
//
// - carries aria-busy="true" (submittingProps), the standard hint, and
// - says so in its accessible name, with the shared common.submitting text
//   appended: "Approve(submitting)" / 「承認（送信中）」.
//
// Both go away once the action settles, success or failure. Nothing visible
// changes -- the spinner and label stay as they were.
//
// How to use them on a new button:
//
// - Name from content: spread submittingProps(busy) on the <button> and put
//   <SubmittingText busy={busy} /> right after the visible label.
// - Name from aria-label: spread submittingProps(busy) and build the label
//   with useSubmittingLabel()(label, busy) -- sr-only children do not count
//   toward a name given by aria-label. IconButton does this for you through
//   its `busy` prop.
// - A button whose visible label already switches to an in-progress wording
//   ("Saving...", "Testing connection...") only takes submittingProps: the
//   name already says it, and appending the suffix would read it twice.
import { useCallback } from 'react';
import { useTranslation } from 'react-i18next';

// aria-busy while busy; the attribute is left out entirely otherwise.
export function submittingProps(busy: boolean): { 'aria-busy'?: true } {
  return busy ? { 'aria-busy': true } : {};
}

// The visually hidden "(submitting)" text, rendered only while busy.
export function SubmittingText({ busy }: { busy: boolean }) {
  const { t } = useTranslation();
  if (!busy) return null;
  return <span className="sr-only">{t('common.submitting')}</span>;
}

// Joins the "(submitting)" suffix to an aria-label while busy.
export function useSubmittingLabel(): (label: string, busy: boolean) => string {
  const { t } = useTranslation();
  return useCallback((label: string, busy: boolean) => (busy ? `${label}${t('common.submitting')}` : label), [t]);
}
