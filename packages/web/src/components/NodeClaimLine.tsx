import { useTranslation } from 'react-i18next';
import { GraphNode } from '../types';
import { formatAgo } from '../i18n/formatDate';
import { memberLabel } from '../lib/memberName';

// NodeClaimLine says who is running a node right now (DFLT-00327): for a node
// at IN PROGRESS / IN REVIEW whose claim records a name, "<name> is running
// it · claimed N min ago · last heartbeat M min ago", plus "not responding"
// once the claimer's session has gone silent past its lease. A claim with no
// name (made by an older graph-engine) shows nothing -- there is nobody to
// name. It sits on its own line under the node's header row and wraps
// anywhere, so a long name or a 200% font at 320px never pushes it out of the
// card. The times are relative to the viewer's clock and move on with each
// poll's re-render.
export function NodeClaimLine({ node, now }: { node: GraphNode; now?: number }) {
  const { t } = useTranslation();
  if ((node.status !== 'IN PROGRESS' && node.status !== 'IN REVIEW') || !node.claimed_by_name) return null;
  const parts = [t('ticketItem.claim.runningBy', { name: memberLabel(t, node.claimed_by_name, node.claimed_by_name_is_fallback) })];
  if (node.claimed_at) {
    const ago = formatAgo(t, node.claimed_at, now);
    if (ago) parts.push(t('ticketItem.claim.claimedAgo', { ago }));
  }
  if (node.claim_heartbeat) {
    const ago = formatAgo(t, node.claim_heartbeat, now);
    if (ago) parts.push(t('ticketItem.claim.heartbeatAgo', { ago }));
  }
  const expired = node.claim_lease === 'expired';
  return (
    <p
      data-testid={`node-claim-${node.id}`}
      className="px-3 pb-2 -mt-1 text-[0.6875rem] text-slate-600 dark:text-slate-300 wrap-anywhere upto-15rem:px-1"
    >
      {parts.join(' · ')}
      {expired && (
        <>
          {' · '}
          <span className="font-semibold text-amber-700 dark:text-amber-300">{t('ticketItem.claim.noResponse')}</span>
        </>
      )}
    </p>
  );
}
