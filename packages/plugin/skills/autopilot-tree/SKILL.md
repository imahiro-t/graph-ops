---
name: autopilot-tree
description: Runs a ticket and then every descendant ticket derived from it (including those created along the way) serially and without human input, each in a child session in a separate terminal, merging each child's work into its parent's branch and ending with a summary of the whole tree. Does not do any ticket's work in this session.
---

# autopilot-tree Skill

Drives an autopilot run in `tree` mode. `graph-engine autopilot` decides every step -- which ticket comes next, the limits, merging finished subtrees up and the final reflection into the main branch -- a child session in another terminal (the `autopilot-worker` skill) does each ticket's work, and this session only relays commands and keeps the short results. It is invoked as `/graph-ops:autopilot-tree <ticketId> [--run <runId>]`; the Web UI passes `--run` for a run it has already reserved.

## 0. Check for user/team customization of this skill

```bash
graph-engine get-skill-context "autopilot-tree"
```

If the returned `content` is non-empty, follow it as additional rules on top of the steps below.

## 1. Start or resume the run

Call `autopilot start` first, passing `--run <runId>` exactly when this invocation's arguments contain it:

```bash
graph-engine autopilot start "<ticketId>" --mode tree [--run "<runId>"]
```

It prints one JSON line; keep its `run_id`. `resumed: true` means an interrupted or stopped run of the same tree was taken over, which is how an interrupted autopilot continues. If the command fails (`AUTOPILOT_ALREADY_RUNNING`, `AUTOPILOT_ROOT_FINISHED`, `PROJECT_LOCAL_PATH_NOT_SET`, ...), show the error to the person as-is and stop.

## 2. Carry out what `next` returns, one action at a time

```bash
graph-engine autopilot next "<runId>"
```

It prints one JSON line with `action`, `ticket`, `role`, `reason` and `command`. Carry out the action exactly as returned -- run `command` verbatim -- and never decide anything yourself:

- `launch`: run `command`. It opens a child session in another terminal and prints the `wait` command to run next. A `launch` with `--role merge-up` (a merge that could not fast-forward) or `--role finalize` (the finalize action: the reflection into the main branch once the whole tree is done) is run the same way.
- `wait`: run `command` (`graph-engine autopilot wait "<runId>" "<ticketId>"`) with the Bash tool's `run_in_background`, then end your turn. The completion notification resumes this session:
  - Exit code 0: the child session reported, or was judged unresponsive. Keep its one-line result and run `next` again.
  - Exit code 2: do not judge anything yourself; go back to `next`. When the line says `awaiting_human`, first tell the person in one line which ticket waits for them (in that ticket's terminal or the Web UI).
  - Exit code 1: an error. Run `next` again once; if that fails too, show the error and stop.
- `merge-up`: run `command` (it fast-forwards a finished subtree into its parent's branch), then `next` again.
- `launch` or `merge-up` exits with code 1: do not retry it and do not investigate. Keep the error's one line and run `next` again. The engine counts failed launches and records the ticket as failed (`launch_failed`) after the second one in a row, and hands a merge-up that cannot fast-forward for any reason to a merge-up session, so `next` moves on by itself and `onFailure` applies. Only if `next` hands out the same command a third time after two failures in a row, show the error and go to step 3.
- `done` or `stopped`: go to step 3.

Tickets are processed one at a time, so expect many `launch` / `wait` rounds; that is normal.

## 3. Show the summary

```bash
graph-engine autopilot summary "<runId>"
```

Show its output -- every ticket's result, branch and open items -- to the person as the result of this command. On `stopped`, add the stop reason and that running the same command again resumes the run once the cause is removed.

## 4. Rules for this session

- Never do any ticket's work here: no refinement, no node execution (`get-executable`, `complete-node`, subagents), no code edits and no git operations. Never call the child's commands (`report`, `touch`, `record-decision`, `attach-decisions`, `merge-into-parent`).
- Keep only the one-line results. Do not read tickets, artifacts or the child terminals -- a tree of 20 tickets must fit in this session's context.
- Do not use AskUserQuestion; the run needs no input from the person in this session.
- If this session is interrupted, running the same command again continues from the recorded state. Close child terminals left over from the interrupted session first, so the same ticket is not worked twice.
