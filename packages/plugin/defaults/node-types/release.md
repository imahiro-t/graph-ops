Manual step: no automated work happens on its own here. This node is the
release/deployment activity itself, once it has already been approved -- the
human carrying it out, and any agent assisting them, must first negotiate the
scope of work with the user and then act only within whatever scope was
confirmed.

## Procedure

1. Work out the candidate scopes before doing anything. Look at the target
   repository's actual operating conventions: is it PR-based (branch
   protection, required reviews, a typical merge-to-release-via-PR flow), or
   does it allow direct commits/pushes to the release branch? Does merging
   trigger an automatic deployment (CI/CD), or is there a separate, explicit
   release/publish/tag step? Look also at what this ticket actually asked for
   and what is already staged at this point in the graph (uncommitted
   changes, an already-open PR, and so on).
2. From that, enumerate a small, ordered set of concrete scope candidates that
   fit what you actually observe. In a typical PR-based repository this might
   run: commit the approved changes locally only (no push); commit, push, and
   open a pull request (no merge); merge the pull request into the target
   branch; carry out the actual release/deployment itself (tag, publish,
   trigger a deploy). Do not force that exact four-step menu on every
   repository -- some have fewer meaningful stages (no PR workflow at all, or
   merging *is* deploying because CI auto-deploys on merge to the release
   branch), so reflect the real setup rather than a fixed template.
3. Present the enumerated stages to the user and ask how far to go. Do not
   default to the broadest stage, and do not infer consent from silence or
   from an ambiguous answer -- ask again instead of guessing.
4. Execute only the confirmed scope. Carry out the work up to and including
   the confirmed stage and then stop, even when a further stage (merging, or
   deploying) is technically within reach from where you are.
5. Complete the node against the confirmed scope, not the maximal one. Done
   here means the agreed scope is finished, not that everything up to full
   deployment happened.

## Artifacts

This node saves no artifact of its own; what it produces is the release or
deployment itself.

## Notes

- The approval decision is not this node's job -- it lives in the dedicated
  `release_approval` node (type `approval_gate`) that this node depends on.
  By the time `release` is reachable at all, `release_approval` has already
  approved releasing the completed work in some form; this node's job is to
  work out *how far* to go right now and then do exactly that, no more and no
  less.
- If the release needs to be stopped entirely instead of merely scoped down,
  that happens by rejecting `release_approval` (see `approval_gate.md`), not
  by silently doing less work here.
- Going beyond the confirmed scope is not a decision this node gets to make
  unilaterally.
