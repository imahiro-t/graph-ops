// GET /api/projects/pending-approvals (DFLT-00144): how many tickets await
// approval in each project, for the project switcher's badges.
import { PendingApprovalCounts } from '../types';

// parsePendingApprovalCounts keeps only the well-formed entries of the
// response's "counts": a positive integer per project ID. Anything else --
// a missing or null "counts", a non-object, a zero, a negative number, a
// fraction, a string -- is dropped rather than rendered, so a malformed
// answer costs at most the badges it garbled.
export function parsePendingApprovalCounts(body: unknown): PendingApprovalCounts {
  const out: PendingApprovalCounts = {};
  if (!body || typeof body !== 'object') return out;
  const counts = (body as { counts?: unknown }).counts;
  if (!counts || typeof counts !== 'object' || Array.isArray(counts)) return out;
  for (const [projectId, n] of Object.entries(counts as Record<string, unknown>)) {
    if (typeof n === 'number' && Number.isInteger(n) && n > 0) out[projectId] = n;
  }
  return out;
}

// fetchPendingApprovalCounts reads the counts, throwing on a network error, a
// non-2xx status or a body that is not JSON; the caller decides what a
// failure means (the project switcher shows no badges and stays usable).
export async function fetchPendingApprovalCounts(): Promise<PendingApprovalCounts> {
  const res = await fetch('/api/projects/pending-approvals');
  if (!res.ok) throw new Error(`GET /api/projects/pending-approvals: ${res.status}`);
  return parsePendingApprovalCounts(await res.json());
}
