// The line a settings editor shows while its settings (or the selected item)
// are loading (DFLT-00350). role="status", like LabelsEditor's loading line
// and AppSettingsEditor's (DFLT-00343): the spinner is aria-hidden (see
// Spinner), so the text alone tells a screen reader that the settings are
// loading (WCAG 4.1.3).
import { useTranslation } from 'react-i18next';
import { Spinner } from '../Spinner';

export function LoadingLine() {
  const { t } = useTranslation();
  return (
    <div role="status" className="flex items-center gap-2 text-slate-500 dark:text-slate-400 text-xs py-8 justify-center">
      <Spinner className="w-4 h-4" /> {t('settings.common.loading')}
    </div>
  );
}
