Manual gate: no automated work happens here. A human must explicitly approve
or reject this node before the graph can proceed -- there is no automated
pass/fail judgment at this node, which is what `review_gate` is for.

## Procedure

1. Present what is being approved to the human and obtain an explicit approve
   or reject decision. The human may answer in the terminal session or
   approve/reject the node in the Web UI; both write the same result.
2. On approval, mark the node DONE. Successor nodes then become runnable.
3. On rejection, take a free-text reason and mark the node REJECTED -- not
   DONE, and not left at TODO. TODO means "not judged yet"; REJECTED means
   "judged and sent back".

## Artifacts

An approval saves no artifact. A rejection must persist its reason as a
`rejection_reason` artifact of type `text` on this node, in the same call that
rejects it. The CLI's `complete-node --reason "<text>"` does this for you; a
caller going through the HTTP API directly must include it as an inline
artifact on the same `POST /api/nodes/{id}/complete` request.

## Notes

- This one file governs every `approval_gate`-typed node regardless of its
  catalog id or name -- the default workflow's `plan_approval` (right after
  plan_review) and `release_approval` (right before release/deployment)
  alike, as well as any ticket-specific one an `expand-graph --patch` inserts
  elsewhere. Agent instructions are resolved by node TYPE, not by id or name,
  so there is no separate `release_approval.md` or `plan_approval.md`;
  anything that differs between them -- what "successor nodes" or "already
  completed work" means in context -- is inferred from the surrounding graph,
  not from this file.
- Rejecting blocks the whole ticket immediately: all automatic execution
  halts, regardless of any `iteration_loop` edge that happens to be wired out
  of this node.
- Resuming after a rejection is NOT automatic and is NOT a human choosing
  which nodes to redo. Deciding what to do next is process-ticket's (the
  LLM's) job, not this engine's -- the engine only exposes mechanical
  primitives. When process-ticket next looks at a blocked ticket and finds a
  REJECTED approval_gate, it reads that gate's `rejection_reason` artifact
  together with the ticket's other artifacts (plan, reviews, implementation
  notes, and so on) and judges one of two outcomes:
  - **The reason points at specific already-completed work**: process-ticket
    identifies the node(s) responsible and calls
    `reopen-nodes <ticketId> <nodeId,...>`, which resets exactly those nodes
    (plus anything downstream that already ran off them) back to TODO, bumps
    the iteration count of each one that had actually finished (DONE or
    REJECTED; ones reset from TODO or AWAITING FIX spend no iteration), and
    un-blocks the ticket so execution resumes automatically from there.
  - **The reason requires a requirements-level rethink** that redoing existing
    nodes cannot fix: process-ticket calls nothing and reports back to the
    user instead. The ticket stays blocked and the REJECTED gate stays
    REJECTED until a human intervenes (for example via refine-ticket or
    further discussion).
- There is no always-on trigger and no server-to-session notification in this
  architecture. Instead, a process-ticket session that reaches a gate runs
  `graph-engine wait-node <nodeId...>` in the background and ends its turn;
  the command polls the DB and exits once the gate leaves TODO, so a decision
  made in the Web UI resumes that session automatically (approval continues
  the graph, rejection goes to the triage above). The resumed session checks
  the gate's status with get-ticket first and never records the same decision
  twice. With no session waiting, a Web UI decision just sits in the DB -- a
  rejection stays blocked -- until process-ticket is invoked on the ticket
  again.
