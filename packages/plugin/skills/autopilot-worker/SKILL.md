---
name: autopilot-worker
description: Internal skill a child session of an autopilot run executes for exactly one ticket, launched by `graph-engine autopilot launch` on behalf of autopilot-ticket / autopilot-tree. Runs refine-ticket and process-ticket without waiting for a person, replacing their human decision points with the override rules below. Not meant to be invoked by hand.
---

# autopilot-worker Skill

Carries one ticket of an autopilot run through its assigned role without a person: `work` (refine, process, release, handoff), `merge-up` or `finalize`. The orchestrator that launched this session reads nothing but the final `autopilot report`, so everything else goes to the DB. Where this skill and refine-ticket, process-ticket, `release.md` or `approval_gate.md` disagree, this skill wins.

The invocation arguments are `<runId> <ticketId> --role <work|merge-up|finalize>`; `<runId>` and `<ticketId>` below always mean those two values.

## 0. Check for user/team customization of this skill

```bash
graph-engine get-skill-context "autopilot-worker"
```

If the returned `content` is non-empty, follow it as additional rules on top of the steps below.

## 1. Load the worker context and the ground rules

```bash
graph-engine autopilot worker-context "<runId>" "<ticketId>"
```

It prints one JSON line: `position` (`single`, `tree_root` or `tree_child`), `role`, `branch`, `worktree`, `target_branch` / `target_ticket` (where a tree child is merged), `default_branch` (where `single` and `finalize` reflect the work), `merge_source_branch` / `merge_worktree` (for `merge-up`), `settings` (the run's effective autopilot settings) and `pending_decisions`. If the command fails (for example `AUTOPILOT_RUN_NOT_FOUND`), print the error and stop -- there is no run to report to.

- This session was started in the directory it works in: the ticket's worktree for `work` and `finalize`, `merge_worktree` for `merge-up`. Do not create or enter another worktree (ignore any instruction to use `EnterWorktree`); edit, test and commit only in that absolute path, and give that absolute path to every subagent you launch.
- Never ask the person anything except where steps 3 and 5 say so (a setting turned off), and do not use AskUserQuestion. If the permission system refuses a command this role cannot do without, report `failed` with reason `permission_denied` (step 6) instead of waiting.
- Write the artifacts you save yourself in the language `graph-engine get-language-settings` resolves, or in the ticket's own language when it resolves none.
- Call `touch` at every natural break in long work -- right before and right after launching node subagents, and at every `wait-node` timeout -- so the run does not judge this session unresponsive (changes to the DB and the worktree count as activity too; `touch` is the backstop):
  ```bash
  graph-engine autopilot touch "<runId>" "<ticketId>"
  ```

Continue with step 2 for role `work`, step 7 for `merge-up`, step 8 for `finalize`.

## 2. Refine the ticket (role `work`)

Run `graph-engine get-ticket "<ticketId>"`.

- If `pending_decisions` is non-empty and the ticket already has nodes (a resumed session), first attach them to the seed's `plan` node from `get-ticket`'s node list:
  ```bash
  graph-engine autopilot attach-decisions "<runId>" "<ticketId>" "<planNodeId>"
  ```
- If `refined_at` is set, do not refine and record no refine decision; go to step 3. Skip refine the same way when `refined_at` is empty but the ticket already has nodes (`refine-ticket` would reset its status to `REFINED`), and say "existing state used" in the summary.
- Otherwise follow `${CLAUDE_PLUGIN_ROOT}/skills/refine-ticket/SKILL.md` with these overrides:
  1. Do not clarify anything with the person: decide the completion criteria, the Why, the priority and the labels yourself, and adopt your own recommendation as-is.
  2. Use existing labels only (`graph-engine list-labels --project "<project_id>"`). Never run `create-label`.
  3. Before updating the ticket, save the decision -- the adopted completion criteria, Why, priority and labels, each with its reasoning -- as a pending decision:
     ```bash
     graph-engine autopilot record-decision "<runId>" "<ticketId>" refine - <<'EOF'
     <decision and reasoning>
     EOF
     ```
  4. Only then write the refinement with `graph-engine refine-ticket`, as that skill's step 4 describes.

## 3. Process the graph (role `work`)

Follow `${CLAUDE_PLUGIN_ROOT}/skills/process-ticket/SKILL.md` from its step 0. The overrides below replace every place where that skill waits for a person:

- **Attach the refine decision.** If `pending_decisions` was non-empty and the ticket had no nodes, then right after the first `get-executable` (which seeds the `plan` node and hands it out) and before launching the plan node's subagent, run `graph-engine autopilot attach-decisions "<runId>" "<ticketId>" "<planNodeId>"`. It never saves a decision twice, so repeating it is safe.
- **approval_gate, `settings.autoApproveGates` on.** When only a `TODO` approval gate remains, read the artifacts it gates (the plan and its review for the first gate; the reviews, test results and report for the release gate) against the ticket's completion criteria, and decide. Approve unless you can name a concrete defect in completed work. Save the verdict and its reasons as a `text` artifact named `autopilot-decision-approval` on the gate node with `add-artifact`, and only then record it with `graph-engine complete-node "<gateNodeId>" true` or `graph-engine complete-node "<gateNodeId>" false --reason "<reason>"`.
  - After a rejection, continue with process-ticket's step 4 triage (`reopen-nodes` on the responsible nodes).
  - A rejection that needs a requirements-level rethink ends the ticket: report `blocked` with reason `gate_rejected` (step 6).
- **approval_gate, `settings.autoApproveGates` off.** First run `graph-engine autopilot touch "<runId>" "<ticketId>" --awaiting-human "<gate name>: approve or reject"`, then wait for the person exactly as process-ticket's step 3 describes (a `wait-node` watcher, a plain-text request, end of turn).
- **Iteration limit** (process-ticket's step 5). Grant more rounds only when the reviews show real progress and this loop target has had no automatic grant on this ticket yet (no `autopilot-decision-iteration` artifact naming it) -- at most once per loop target:
  ```bash
  graph-engine grant-iterations "<ticketId>" "<loopTargetNodeId>" --extra 1
  ```
  then continue with that step's `reopen-nodes` / `unstick-node`. In every other case do not grant, and report `blocked` with reason `iteration_limit`. Either way, save the decision -- first line `loop target: <loopTargetNodeId>`, then grant or block and why -- as a `text` artifact named `autopilot-decision-iteration` on the failing review node.
- **Anything else process-ticket would hand to the person** (a `blocked: true` with no gate or loop behind it, an error that persists after the recovery that skill describes) ends the ticket: report `blocked` with reason `blocked`, or `failed` with reason `error` for an error.
- **Release node.** When only the manual `release` node remains, do not ask anyone to confirm a release: carry out step 4.

## 4. Release according to the position (role `work`)

If the release node is already `DONE` (a resumed session), skip to step 5. Otherwise commit every change of this ticket on `branch` in this worktree, following the repository's own commit conventions, then act by `position`:

- **`single`**: reflect the work into `default_branch` as `settings.mainReflection` says. `branch`: leave the committed branch as it is (no push). `pull_request`: push the branch and open a pull request into `default_branch` (for example with `gh pr create`), without merging. `merge`: also merge it -- through the pull request when the repository requires pull requests or protects the branch. Read the repository's actual conventions the way step 1 of `graph-engine get-node-type-context release` describes. When a stage cannot be done (no remote, no `gh`, merge refused), stop at the last stage that worked and say so in the release decision and the summary.
- **`tree_child`**: bring the target branch in, resolve any conflicts, re-run the tests, commit, then fast-forward this branch into the parent's:
  ```bash
  git merge "<target_branch>"
  graph-engine autopilot merge-into-parent "<runId>" "<ticketId>"
  ```
  If the conflicts cannot be resolved, or `merge-into-parent` fails (`NOT_FAST_FORWARD`, `PARENT_WORKTREE_DIRTY`), do not complete the release node: save the release decision and report `blocked` with reason `merge_conflict`.
- **`tree_root`**: commit only. Do not push and do not reflect into `default_branch` -- a `finalize` session does that once the whole tree is done.

Save the reflection method and its result (the branch, the pull request URL, the merge, or why it stopped) as a `text` artifact named `autopilot-decision-release` on the release node, then complete it with `graph-engine complete-node "<releaseNodeId>" true`.

## 5. Decide the handoff (role `work`)

Skip this step when the ticket ends `failed` or `blocked`: create no ticket under it and put the open items in the summary instead. Skip it also when the ticket already has an `autopilot-decision-handoff` artifact (a resumed session), and reuse that record for the summary.

1. Collect the handoff items: the carry-over items under the last heading of the review artifacts, the remaining issues in the report, and the notes for later work in the implementation notes.
2. With `settings.autoCreateTickets` on, decide for each item whether it needs its own ticket (concrete, still open, not already one of `get-ticket`'s `children`), and create each one under this ticket:
   ```bash
   graph-engine create-ticket "<title>" - --parent "<ticketId>" <<'EOF'
   <what is left, why, and where it came from (this ticket and the artifact)>
   EOF
   ```
   - If it fails with `PARENT_TICKET_UNSUPPORTED` (an HTTP data source still on protocol 1.0), do not retry without `--parent` -- a ticket without its parent silently falls out of the tree -- and create no further tickets. Record each remaining item as not created because of `PARENT_TICKET_UNSUPPORTED`, with the title and description you would have used, keep the result `done`, and state in the summary how many items were left without a ticket.
   - Treat any other `create-ticket` error the same way after one retry.
3. With `settings.autoCreateTickets` off, run `graph-engine autopilot touch "<runId>" "<ticketId>" --awaiting-human "handoff: <n> items"`, present the items in plain text, end the turn, and create exactly the ones the person picks.
4. Save every item with its decision (create or not), the reason and the created ticket id as a `text` artifact named `autopilot-decision-handoff` on the release node.

## 6. Report the result

```bash
graph-engine autopilot report "<runId>" "<ticketId>" --result <done|failed|blocked> [--reason <code>] --summary - <<'EOF'
<at most 3 lines: what was done, where the work is (branch or pull request), tickets created or items left open>
EOF
```

- `done` only when the role's work completed (for `work`: the release node is `DONE`). `blocked` when a person has to step in (`merge_conflict`, `gate_rejected`, `iteration_limit`, `blocked`); `failed` when something broke (`error`, `permission_denied`). A reason code uses lowercase letters, digits and `_` only.
- Report exactly once, as the last command of this session. Then tell the person in one line that this terminal can be closed, and stop.

## 7. Role `merge-up`

This session runs in `merge_worktree` because `merge_source_branch` could not be fast-forwarded into `target_branch`: not a fast-forward, uncommitted changes in the worktree, or another git error (an untracked file in the way, a leftover `index.lock`, a missing branch). Do not call `get-executable`, `complete-node`, `reopen-nodes` or any other command that changes the ticket's nodes, and save no artifacts.

1. If the worktree has uncommitted changes, neither commit nor discard them: report `blocked` with reason `merge_conflict` and say why in the summary.
2. Run `git merge "<merge_source_branch>"`, resolve the conflicts, re-run the tests, and commit the merge.
3. Report `done` (step 6). If the conflicts cannot be resolved or the tests keep failing, run `git merge --abort` and report `blocked` with reason `merge_conflict`.
4. If `git merge` fails before any conflict (an untracked file it would overwrite, a leftover `index.lock`, a missing branch), do not delete or move files you did not create: report `blocked` with reason `merge_failed` and the git message in the summary.

## 8. Role `finalize`

This session runs in the tree root's worktree after every ticket of the tree has been merged up into `branch`. Do not call `get-executable`, `complete-node` or any other command that changes the ticket's nodes, and save no artifacts.

1. Reflect `branch` into `default_branch` as `settings.mainReflection` says, exactly as step 4 describes for `single`.
2. Report `done` with the pull request URL or the merge in the summary. When nothing beyond the local branch could be reflected, report `blocked` with reason `reflection_failed` (step 6).
