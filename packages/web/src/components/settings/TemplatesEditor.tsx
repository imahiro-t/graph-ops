// "テンプレート" tab: a left-hand list of the fixed templates agents fill in
// (実行計画 / レビュー / レポート) and, on the right, the editor for the one
// selected -- the same list + editor layout as the node-type/skill tabs.
// Plan and review use TemplateTextEditor directly (GET/PUT
// /api/settings/{plan,review}-template, unchecked Markdown); report goes
// through ReportTemplateEditor so its `html` body field and server-side
// marker validation stay exactly as they were.
import React, { useCallback, useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  fetchSettingsPlanTemplate,
  fetchSettingsReviewTemplate,
  saveSettingsPlanTemplate,
  saveSettingsReviewTemplate
} from '../../lib/settingsApi';
import { getNodeTypeMeta } from '../../nodeTypeMeta';
import { TemplateFetcher, TemplateSaver, TemplateTextEditor } from './TemplateTextEditor';
import { ReportTemplateEditor } from './ReportTemplateEditor';

type TemplateKey = 'plan' | 'review' | 'report';

interface MarkdownTemplateConfig {
  fetchTemplate: TemplateFetcher;
  saveTemplate: TemplateSaver;
  i18nPrefix: string;
}

const TEMPLATE_KEYS: TemplateKey[] = ['plan', 'review', 'report'];

const LIST_LABEL_KEYS: Record<TemplateKey, string> = {
  plan: 'settings.templates.list.plan',
  review: 'settings.templates.list.review',
  report: 'settings.templates.list.report'
};

const MARKDOWN_TEMPLATES: Record<Exclude<TemplateKey, 'report'>, MarkdownTemplateConfig> = {
  plan: {
    fetchTemplate: fetchSettingsPlanTemplate,
    saveTemplate: saveSettingsPlanTemplate,
    i18nPrefix: 'settings.planTemplate'
  },
  review: {
    fetchTemplate: fetchSettingsReviewTemplate,
    saveTemplate: saveSettingsReviewTemplate,
    i18nPrefix: 'settings.reviewTemplate'
  }
};

interface Props {
  onDirtyChange: (dirty: boolean) => void;
}

export const TemplatesEditor: React.FC<Props> = ({ onDirtyChange }) => {
  const { t } = useTranslation();
  const listTitleId = useId();
  const [selected, setSelected] = useState<TemplateKey>('plan');
  // The selected editor's dirty state, kept here too (not only relayed to
  // SettingsModal) so switching templates in the list can warn first, the
  // same way SettingsModal's tab/scope switches do.
  const [childDirty, setChildDirty] = useState(false);

  const handleDirtyChange = useCallback((dirty: boolean) => {
    setChildDirty(dirty);
    onDirtyChange(dirty);
  }, [onDirtyChange]);

  const select = (next: TemplateKey) => {
    if (next === selected) return;
    if (childDirty && !window.confirm(t('settings.unsavedChanges.confirmMessage'))) return;
    // Discarding: clear the relayed dirty flag now so the next switch (or a
    // tab change in SettingsModal) does not ask again. The new editor
    // remounts (key={selected}) and reports its own clean state as well.
    setChildDirty(false);
    onDirtyChange(false);
    setSelected(next);
  };

  const editorProps = { onDirtyChange: handleDirtyChange };

  return (
    <div className="flex h-full min-h-0 gap-4">
      {/* Left: template list */}
      <nav
        aria-labelledby={listTitleId}
        className="w-56 shrink-0 border border-slate-200 dark:border-slate-800 rounded-lg overflow-y-auto bg-slate-50 dark:bg-slate-800 flex flex-col"
      >
        <div
          id={listTitleId}
          className="px-3 py-2 text-[11px] font-semibold text-slate-500 dark:text-slate-400 border-b border-slate-200 dark:border-slate-700 sticky top-0 bg-slate-50 dark:bg-slate-800"
        >
          {t('settings.templates.listTitle')}
        </div>
        <ul>
          {TEMPLATE_KEYS.map(key => {
            const Icon = getNodeTypeMeta(key).icon;
            const isSelected = selected === key;
            return (
              <li key={key} className="border-b border-slate-100 dark:border-slate-700">
                {/* aria-current marks the one template whose editor is
                    shown on the right. */}
                <button
                  type="button"
                  onClick={() => select(key)}
                  aria-current={isSelected ? 'true' : undefined}
                  className={`w-full text-left px-3 py-2 text-xs flex items-center gap-2 transition focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-blue-500 ${
                    isSelected
                      ? 'bg-white dark:bg-slate-900 font-semibold text-slate-900 dark:text-slate-100'
                      : 'text-slate-600 dark:text-slate-400 hover:bg-white dark:hover:bg-slate-900'
                  }`}
                >
                  <Icon className="w-3.5 h-3.5 shrink-0 text-slate-400" aria-hidden="true" />
                  <span className="truncate flex-1">{t(LIST_LABEL_KEYS[key])}</span>
                </button>
              </li>
            );
          })}
        </ul>
      </nav>

      {/* Right: editor for the selected template */}
      <div className="flex-1 min-w-0 min-h-0 flex flex-col">
        {selected === 'report' ? (
          <ReportTemplateEditor key="report" {...editorProps} />
        ) : (
          <TemplateTextEditor key={selected} {...editorProps} {...MARKDOWN_TEMPLATES[selected]} />
        )}
      </div>
    </div>
  );
};
