import { TFunction } from 'i18next';

// memberLabel is how another member is named on screen (DFLT-00326,
// DFLT-00327): the name, with "(name not set)" after a "<OS user>@<host>"
// stand-in, and "Another member" when there is no name at all. Autopilot
// starters and node claimers are both named through it, so the two read the
// same.
export function memberLabel(t: TFunction, name: string | undefined | null, isFallback: boolean | undefined | null): string {
  const shown = name || t('autopilot.unknownMember');
  return name && isFallback ? t('autopilot.fallbackName', { name: shown }) : shown;
}
