import React, { useId } from 'react';
import { useTranslation } from 'react-i18next';
import { Bot, ExternalLink } from 'lucide-react';
import { Artifact } from '../types';
import { formatDateTime } from '../i18n/formatDate';

// The artifact name prefix of everything the autopilot records (plan D7):
// autopilot-decision-{refine,approval,iteration,release,handoff} and
// autopilot-tree-summary.
export const AUTOPILOT_ARTIFACT_PREFIX = 'autopilot-';

const KNOWN_KINDS: Record<string, string> = {
  'autopilot-decision-refine': 'refine',
  'autopilot-decision-approval': 'approval',
  'autopilot-decision-iteration': 'iteration',
  'autopilot-decision-release': 'release',
  'autopilot-decision-handoff': 'handoff',
  'autopilot-tree-summary': 'treeSummary'
};

interface Props {
  artifacts: Artifact[];
  nodes: { id: string; name: string }[];
}

// The "automatic decisions" section of an expanded ticket (DFLT-00142 phase
// 5): every artifact the autopilot saved on the ticket -- the decisions it
// made instead of a person, and the tree summary -- in saving order, each
// opening in the same preview page as any other artifact. Other artifacts are
// never listed here. Renders nothing when there are none.
export const AutopilotDecisions: React.FC<Props> = ({ artifacts, nodes }) => {
  const { t, i18n } = useTranslation();
  // Several tickets can be expanded at once: the heading's id has to be
  // unique per section.
  const headingId = useId();
  const items = artifacts.filter(a => a.name.startsWith(AUTOPILOT_ARTIFACT_PREFIX));
  if (items.length === 0) return null;
  const nodeName = new Map(nodes.map(n => [n.id, n.name]));

  return (
    <section
      data-testid="autopilot-decisions"
      aria-labelledby={headingId}
      className="bg-white dark:bg-slate-900 p-4 rounded-xl border border-slate-200 dark:border-slate-800 shadow-xs text-xs"
    >
      <h3
        id={headingId}
        className="font-bold text-slate-700 dark:text-slate-300 flex items-center gap-1.5 mb-2"
      >
        <Bot className="w-3.5 h-3.5 text-violet-600 dark:text-violet-400" aria-hidden="true" />
        {t('autopilot.decisions.title', { count: items.length })}
      </h3>
      <ul className="space-y-1.5">
        {items.map(a => {
          const kind = KNOWN_KINDS[a.name];
          const label = kind ? t(`autopilot.decisions.kinds.${kind}`) : a.name;
          const node = nodeName.get(a.node_id);
          const href = `/artifacts/${a.id}/preview?type=${a.type}&name=${encodeURIComponent(a.name)}`;
          return (
            <li key={a.id} data-testid="autopilot-decision" className="flex flex-wrap items-center gap-x-3 gap-y-1">
              <span className="font-semibold text-slate-800 dark:text-slate-200">{label}</span>
              <span className="font-mono text-[10px] text-slate-500 dark:text-slate-400">{a.name}</span>
              {node && <span className="text-slate-600 dark:text-slate-400">{node}</span>}
              <span className="text-slate-500 dark:text-slate-400">{formatDateTime(a.created_at, i18n.language)}</span>
              <a
                href={href}
                target="_blank"
                rel="noreferrer"
                aria-label={t('autopilot.decisions.open', { name: label })}
                className="text-indigo-600 dark:text-indigo-400 hover:underline flex items-center gap-1 text-[11px]"
              >
                <ExternalLink className="w-3 h-3" aria-hidden="true" />
                {t('ticketItem.openInNewTab')}
              </a>
            </li>
          );
        })}
      </ul>
    </section>
  );
};
