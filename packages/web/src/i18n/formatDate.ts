import { TFunction } from 'i18next';

// Small helpers so date/time formatting follows the current UI language
// instead of being hardcoded to a single locale. Only ja/en are supported
// UI languages (see src/i18n/index.ts), so we only need to pick between the
// two corresponding Intl locales.
const localeFor = (lng: string): string => (lng.startsWith('ja') ? 'ja-JP' : 'en-US');

export function formatDateTime(date: string | Date, lng: string): string {
  const d = typeof date === 'string' ? new Date(date) : date;
  return d.toLocaleString(localeFor(lng));
}

export function formatTime(date: string | Date, lng: string): string {
  const d = typeof date === 'string' ? new Date(date) : date;
  return d.toLocaleTimeString(localeFor(lng), { hour: '2-digit', minute: '2-digit' });
}

// formatAgo renders how long before `now` the timestamp `date` was, for the
// "N minutes ago" parts of the UI (DFLT-00327): "just now" under a minute,
// whole minutes under an hour, whole hours from there. A timestamp that does
// not parse renders as "". It is only as fresh as the last re-render (the
// ticket list re-renders on every poll), and it is measured against the
// viewer's own clock, so a clock that is off shows up in it.
export function formatAgo(t: TFunction, date: string, now: number = Date.now()): string {
  const then = Date.parse(date);
  if (Number.isNaN(then)) return '';
  const minutes = Math.floor((now - then) / 60000);
  if (minutes < 1) return t('time.justNow');
  if (minutes < 60) return t('time.minutesAgo', { count: minutes });
  return t('time.hoursAgo', { count: Math.floor(minutes / 60) });
}
