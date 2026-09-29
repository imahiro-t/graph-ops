import { useTranslation } from 'react-i18next';
import { GraphNode } from '../types';
import { formatDateTime } from '../i18n/formatDate';
import { memberLabel } from '../lib/memberName';

// NodeDecisionLine says who decided a manual node and when (DFLT-00329):
// "Approved by <name> · <time>" / "Rejected by ..." on an approval gate,
// "Completed by ..." on a release or another manual node, and "Sent back by
// ..." on a manual node whose rejection looped back -- with "(autopilot)"
// when an autopilot run decided. The server resolves the name and keeps it
// only while the node still has the status the decision set, so a rewound
// gate shows nothing. It sits on its own line under the node's header row,
// like NodeClaimLine, and wraps anywhere, so a long name or a 200% font at
// 320px never pushes it out of the card.
export function NodeDecisionLine({ node }: { node: GraphNode }) {
  const { t, i18n } = useTranslation();
  if (!node.is_manual || !node.decided_by_name) return null;
  let key: string;
  switch (node.status) {
    case 'DONE':
      key = node.type === 'approval_gate' ? 'ticketItem.decision.approvedBy' : 'ticketItem.decision.completedBy';
      break;
    case 'REJECTED':
      key = 'ticketItem.decision.rejectedBy';
      break;
    case 'AWAITING FIX':
      key = 'ticketItem.decision.sentBackBy';
      break;
    default:
      return null;
  }
  let who = memberLabel(t, node.decided_by_name, node.decided_by_name_is_fallback);
  if (node.decided_by_autopilot) who += t('ticketItem.decision.byAutopilot');
  const parts = [t(key, { name: who })];
  if (node.decided_at && !Number.isNaN(Date.parse(node.decided_at))) {
    parts.push(formatDateTime(node.decided_at, i18n.language));
  }
  return (
    <p
      data-testid={`node-decision-${node.id}`}
      className="px-3 pb-2 -mt-1 text-[0.6875rem] text-slate-600 dark:text-slate-300 wrap-anywhere upto-15rem:px-1"
    >
      {parts.join(' · ')}
    </p>
  );
}
