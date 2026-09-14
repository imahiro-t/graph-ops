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
