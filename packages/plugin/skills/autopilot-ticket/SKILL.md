---
name: autopilot-ticket
description: Runs one ticket from refinement through graph execution, release and the handoff decision without human input, by launching a child session in a separate terminal and relaying only its short result. Does not do the ticket's work in this session, and does not process the child tickets it creates (autopilot-tree does that).
---

# autopilot-ticket Skill

Drives an autopilot run in `ticket` mode. `graph-engine autopilot` decides every step, a child session in another terminal (the `autopilot-worker` skill) does the ticket's work, and this session only relays commands and keeps the short results. It is invoked as `/graph-ops:autopilot-ticket <ticketId> [--run <runId>]`; the Web UI passes `--run` for a run it has already reserved.

## 0. Check for user/team customization of this skill

```bash
graph-engine get-skill-context "autopilot-ticket"
```

If the returned `content` is non-empty, follow it as additional rules on top of the steps below.

## 1. Start or resume the run

Call `autopilot start` first, passing `--run <runId>` exactly when this invocation's arguments contain it:

```bash
graph-engine autopilot start "<ticketId>" --mode ticket [--run "<runId>"]
```

It prints one JSON line; keep its `run_id`. `resumed: true` means an interrupted or stopped run of the same ticket was taken over, which is how an interrupted autopilot continues. If the command fails (`AUTOPILOT_ALREADY_RUNNING`, `AUTOPILOT_ROOT_FINISHED`, `PROJECT_LOCAL_PATH_NOT_SET`, ...), show the error to the person as-is and stop.

## 2. Carry out what `next` returns, one action at a time

```bash
graph-engine autopilot next "<runId>"
```

It prints one JSON line with `action`, `ticket`, `role`, `reason` and `command`. Carry out the action exactly as returned -- run `command` verbatim -- and never decide anything yourself:

- `launch`: run `command`. It opens a child session in another terminal and prints the `wait` command to run next. A `launch` with `--role finalize` (the finalize action) is run the same way.
- `wait`: run `command` (`graph-engine autopilot wait "<runId>" "<ticketId>"`) with the Bash tool's `run_in_background`, then end your turn. The completion notification resumes this session:
  - Exit code 0: the child session reported, or was judged unresponsive. Keep its one-line result and run `next` again.
  - Exit code 2: do not judge anything yourself; go back to `next`. When the line says `awaiting_human`, first tell the person in one line which ticket waits for them (in that ticket's terminal or the Web UI).
  - Exit code 1: an error. Run `next` again once; if that fails too, show the error and stop.
- `merge-up`: run `command`, then `next` again. (A `ticket` run never returns it.)
- `launch` exits with code 1: do not retry it and do not investigate. Keep the error's one line and run `next` again. The engine counts failed launches and records the ticket as failed (`launch_failed`) after the second one in a row, so `next` moves on by itself and `onFailure` applies. Only if `next` hands out the same command a third time after two failures in a row, show the error and go to step 3.
- `done` or `stopped`: go to step 3.

## 3. Show the summary

```bash
graph-engine autopilot summary "<runId>"
```

Show its output to the person as the result of this command. On `stopped`, add the stop reason and that running the same command again resumes the run once the cause is removed.

## 4. Rules for this session

- Never do the ticket's work here: no refinement, no node execution (`get-executable`, `complete-node`, subagents), no code edits and no git operations. Never call the child's commands (`report`, `touch`, `record-decision`, `attach-decisions`, `merge-into-parent`).
- Keep only the one-line results. Do not read the ticket, its artifacts or the child's terminal -- a long run must fit in this session's context.
- Do not use AskUserQuestion; the run needs no input from the person in this session.
- If this session is interrupted, running the same command again continues from the recorded state. Close child terminals left over from the interrupted session first, so the same ticket is not worked twice.
