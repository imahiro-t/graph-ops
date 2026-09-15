// The "テンプレート" tab's レポート entry: edits this scope's fixed report HTML
// template override (GET/PUT /api/settings/report-template) through the
// shared TemplateTextEditor. What stays report-specific is passed in here:
// the save sends the override in the `html` body field (sending `text`
// would read as an empty `html` and clear the override), the server
// rejects HTML missing a required marker with INVALID_REPORT_TEMPLATE (shown
// through the editor's normal error message), and the labels come from
// settings.reportTemplate.*. The preview is a plain text/`<pre>` dump of the
// HTML source, not a rendered iframe.
import React from 'react';
import { SettingsScope } from '../../types';
import { fetchSettingsReportTemplate, saveSettingsReportTemplate } from '../../lib/settingsApi';
import { TemplateTextEditor } from './TemplateTextEditor';

interface Props {
  scope: SettingsScope;
  projectId: string;
  canEdit: boolean;
  onDirtyChange: (dirty: boolean) => void;
}

export const ReportTemplateEditor: React.FC<Props> = props => (
  <TemplateTextEditor
    {...props}
    fetchTemplate={fetchSettingsReportTemplate}
    saveTemplate={saveSettingsReportTemplate}
    i18nPrefix="settings.reportTemplate"
  />
);
