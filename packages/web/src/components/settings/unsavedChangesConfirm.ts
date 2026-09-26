// The "discard unsaved changes?" confirmation shared by the settings modal and
// the lists inside it (SettingsModal's tab change / close, and switching the
// selected item in NodeTypesEditor, SkillsEditor and TemplatesEditor). They
// all ask the same question, so the wording and tone live here and only the
// test id prefix differs per caller:
//
//   if (!(await confirm(unsavedChangesConfirmOptions(t, 'skill-discard-confirm')))) return;
import type { TFunction } from 'i18next';
import type { ConfirmOptions } from '../../hooks/useConfirmDialog';

export function unsavedChangesConfirmOptions(t: TFunction, testIdPrefix: string): ConfirmOptions {
  return {
    title: t('settings.unsavedChanges.confirmTitle'),
    message: t('settings.unsavedChanges.confirmMessage'),
    confirmLabel: t('settings.unsavedChanges.discardButton'),
    tone: 'danger',
    testIdPrefix
  };
}
