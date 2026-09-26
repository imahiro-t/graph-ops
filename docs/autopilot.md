# Autopilot

The autopilot takes a ticket from refinement to release without a person in the loop: `/graph-ops:autopilot-ticket` does it for one ticket, and `/graph-ops:autopilot-tree` does it for a ticket and then every ticket derived from it (its descendants, including the ones created along the way). This page is the reference: how a run works, the settings, the branch strategy, the `graph-engine autopilot` CLI, the HTTP API, the error codes, and the things to be careful about. The [README](../README.md#autopilot) has the short version.

- [How a run works](#how-a-run-works)
- [Terminal.app: child sessions as tabs](#terminalapp-child-sessions-as-tabs)
- [Parent and child tickets](#parent-and-child-tickets)
- [Starting a run](#starting-a-run)
- [Which tickets a run processes](#which-tickets-a-run-processes)
- [Branches](#branches)
- [Automatic decisions](#automatic-decisions)
- [Settings](#settings)
- [Interrupting, resuming and unresponsive sessions](#interrupting-resuming-and-unresponsive-sessions)
- [The run registry](#the-run-registry)
- [The `graph-engine autopilot` CLI](#the-graph-engine-autopilot-cli)
- [Error and reason codes](#error-and-reason-codes)
- [HTTP API](#http-api)
- [Cautions and limitations](#cautions-and-limitations)

## How a run works

A run has two kinds of Claude Code sessions:

- **The orchestrator** is the session you start the command in (or, from the Web UI, a session opened for you in a new terminal). It does no ticket work itself. It calls `graph-engine autopilot start`, then repeatedly `graph-engine autopilot next` and runs exactly the command `next` hands back, and finally prints `graph-engine autopilot summary`. Every decision -- which ticket comes next, the limits, skips, failures, merges -- is made by `graph-engine`, and each answer is one short JSON line, so the orchestrator keeps only a few hundred bytes per ticket. That is what lets one orchestrator drive a tree of 20 or more tickets.
- **Child sessions** do the work, one ticket at a time, each in a new terminal opened the same way as the Web UI's "Launch Claude" buttons (see [Troubleshooting](../README.md#troubleshooting) for how the terminal is chosen) -- except that in macOS's Terminal.app, a child session opens as a new tab of the orchestrator's window (see [Terminal.app: child sessions as tabs](#terminalapp-child-sessions-as-tabs)). A child session runs the internal `autopilot-worker` skill, which follows `refine-ticket` and `process-ticket` but replaces every point where those skills wait for a person with an automatic decision. A child session has one of three roles:
  - `work`: refine the ticket (only if it has not been refined and has no nodes yet), run its execution graph, release it (see [Branches](#branches)), decide which open items become follow-up tickets, and report.
  - `merge-up`: merge a finished subtree into its parent's branch when a fast-forward was not possible.
  - `finalize`: bring the tree root's branch into the main branch once the whole tree is done.

  When a child session ends, it reports its result (`done`, `failed` or `blocked`, a reason code, and a summary of at most three lines) with `graph-engine autopilot report`, tells you its terminal can be closed, and stops. Child terminals are not closed automatically.

Tickets are processed strictly one at a time within a run. Processing several tickets of one tree in parallel is not supported.

## Terminal.app: child sessions as tabs

On macOS, when no `terminalCommand` is set and the orchestrator is not inside tmux, the terminal is Terminal.app. There, every child session of a run -- `work`, `merge-up` and `finalize`, for the root, its children and their descendants at any depth -- opens as **a new tab of the Terminal.app window the orchestrator runs in**, instead of one new window per session. A run's sessions stay together in one window. tmux, a custom `terminalCommand` (iTerm2, WezTerm, ...), Windows, and Terminal.app launches outside the autopilot (the Web UI's "Launch Claude" buttons, and the orchestrator the Web UI's autopilot buttons open) are unchanged: they still open a new window.

**How the window is found.** When a run is started (`autopilot start`, including a takeover of an interrupted or stopped run, and the orchestrator adopting a run the Web UI reserved), `graph-engine` looks up the tty of the Terminal.app tab the orchestrator runs in (for example `/dev/ttys003`) with `ps`, walking up from its own process. This needs no permission, and is done only when `TERM_PROGRAM` is `Apple_Terminal` and `TMUX` is empty. The tty is kept in the run file as `terminal_tty`, and every later launch of the run -- including after resuming -- opens its tab in the window that has a tab on that tty. A takeover or an adoption replaces it with the new orchestrator's tty (or with nothing, when the new orchestrator is not in Terminal.app), so a tty left over from an earlier orchestrator is never used. The Web UI server itself never records a tty.

**Permissions.** Terminal.app has no scripting command that opens a tab, so `graph-engine` runs a fixed AppleScript with `osascript` that brings the window to the front and sends Cmd+T through System Events, then runs the session's command in the new tab. The new tab is recognized by its tty, which no tab of any Terminal window had before the Cmd+T, and only when it belongs to the orchestrator's window (current macOS reports each tab of a window to AppleScript as a window of its own, sharing the window's bounds; that is how it is matched). That needs two macOS privacy permissions for the app the orchestrator runs in (Terminal.app): **Accessibility** (to send the keystroke) and **Automation** (to control Terminal and System Events). macOS may ask for them the first time a tab is opened; you can also grant them beforehand in System Settings > Privacy & Security. Without them the sessions still open, in new windows (see below). The wording of the macOS prompts varies between versions; what matters is that Terminal is allowed both.

**Terminal comes to the front on every tab launch.** Opening a tab means bringing the orchestrator's Terminal window to the front and sending it a keystroke, so each child session's launch takes the focus away from whatever app you are using at that moment. `graph-engine` sends Cmd+T only after it has seen Terminal frontmost with that window in front; if you switch apps in the few milliseconds between that check and the keystroke, the Cmd+T can still reach another app or window, in which case no new tab appears in the orchestrator's window and the session opens in a new window instead (an empty tab may be left where the keystroke went).

**When a tab is not used, the session opens in a new window** (the same `open -a Terminal <script>.command` as before), and the launch itself does not fail on the tab's account; only when the new window cannot be opened either does `launch` fail as before. Each launch that tries a tab and fails, or gives up waiting for the tab lock (the lock row below), falls back with one warning to the orchestrator's stderr, `graph-engine: warning: the <role> session of <ticket> opened in a new Terminal window because a tab could not be opened (<reason>)`, and the one-line JSON on stdout is unchanged. No warning is printed when no tab is tried at all: when no tty was found (the first row below), and for the launches after the tab has been disabled for the run (the last row below; the launch that disabled it has already warned, saying so). There are two kinds of fallback:

| Condition | What happens next |
| --- | --- |
| No tty was found when the run started: `ps` failed or was refused (for example by a sandbox around the orchestrator's commands), or the orchestrator is not in a Terminal.app tab. | Every session of the run opens in a new window, with no attempt and no warning. |
| No Terminal window has a tab on the recorded tty any more (the orchestrator's tab or window was closed), or the window went away while the tab was being opened. | This launch opens a new window; the next launch tries the tab again. |
| Terminal did not come to the front with the orchestrator's window in front within about 2 seconds (you are working in another app that keeps the focus, for example). No keystroke is sent. | This launch opens a new window; the next launch tries again. |
| No new tab appeared within about 3 seconds of Cmd+T, it closed before the command could be run in it, or it opened in another Terminal window than the orchestrator's (the Cmd+T reached another window). The command is not run in it. | This launch opens a new window; the next launch tries again. A tab that opens late, or in another window, is left empty for you to close; `graph-engine` does not close it because a late tab may appear only after the tab script has finished and Terminal's scripting has no command that closes a single tab: typing `exit` into it does not close it under every profile and would mean typing into a tab the launch never confirmed as its own, and closing a window could close one of yours. The empty tab runs nothing but an idle shell. |
| More than one new tab appeared in Terminal at once (another run, or you, opened a tab in any Terminal window at the same moment), so the new tab cannot be told apart. The command is run in none of them. | This launch opens a new window; the next launch tries again. The tab this launch opened is left empty among them, for the same reason. |
| Another `graph-engine` process was opening a Terminal tab and did not finish within 15 seconds (see the lock below). | This launch opens a new window without running the tab script (with the warning above); the next launch tries again. |
| Anything else: a missing Accessibility permission (macOS reports it as error 1002, "osascript is not allowed to send keystrokes") or Automation permission, a sandbox that refuses Apple events, `osascript` missing or failing in any other way, `osascript` not finishing within 10 seconds (for example while a permission prompt is left unanswered), or Terminal's state that cannot be read at all while waiting for it to come to the front. | This launch opens a new window, and **the tab is disabled for the rest of the run**: the warning of this launch says so, and later sessions go straight to new windows without a warning. |

The script's own failures (the second to fifth rows) all happen before the session's command is run, so a fallback never starts a session twice.

**The per-run disable.** When a tab is disabled, the reason (when longer than 300 characters, cut to 300 followed by `...`) is kept in the run file as `terminal_tab_disabled`, so the rest of the run does not wait for the same failure on every launch. It is cleared, and the tab tried again, when the run is taken over (running the same command again after an interruption) or adopted by a new orchestrator. So after granting a missing permission, interrupting the orchestrator and running the same command again brings the tabs back for that run. A `launch` that failed altogether (not even a new window opened) never disables the tab.

**One tab at a time across processes.** Tab launches are serialized through a lock on `$HOME/.graph-ops/autopilot/terminal-tab.lock` (an advisory `flock`, released when the process ends, so a crash never leaves it held), held only while `osascript` runs. Two runs whose orchestrators share a window therefore do not open their tabs at the same moment; a launch that waits more than 15 seconds for the lock falls back as in the table. The lock wait is longer than the 10-second limit on `osascript`, so a waiting launch does not give up while the holder's `osascript` can still finish. If the lock file cannot be created, the tab is tried without the lock -- identifying the new tab by its tty, and refusing when there is more than one, is what keeps a command out of the wrong tab.

**How long a launch can take.** In the worst case, `launch` waits up to 15 seconds for the lock, up to 10 seconds for `osascript`, and up to 10 seconds for `open -a Terminal`, about 35 seconds in all. This happens outside the run registry's lock, so other commands are not held up.

## Parent and child tickets

A ticket can have a parent ticket -- the ticket it was derived from. The autopilot uses this relation to find a tree's tickets, but the relation does not bind how a ticket is processed: a child ticket can still be run on its own with `/graph-ops:autopilot-ticket`, or with the usual `/graph-ops:refine-ticket` and `/graph-ops:process-ticket`.

- **Create a child ticket** with `graph-engine create-ticket "<title>" "<description>" --parent <ticketId>`. Without `--project`, the child goes into the parent's project; a `--project` other than the parent's is `VALIDATION_ERROR` and an unknown parent is `TICKET_NOT_FOUND`, and no ticket is created in either case. The parent is set at creation only; there is no command to change or remove it later. `POST /api/tickets` takes `parent_ticket_id` the same way; the Web UI's "New Ticket" form does not set a parent.
- **See the relation** with `graph-engine get-ticket <ticketId>`: its output has `parent_ticket_id`, `parent` (`{id, title, status}`, or `null`) and `children` (the same shape, in creation order; `[]` when there are none). `GET /api/tickets/{id}` returns the same, and every ticket in `GET /api/tickets` carries `parent_ticket_id`. In the Web UI, an expanded ticket shows a "Parent ticket" link and a "Child tickets (n)" list, each with its status; clicking one opens that ticket (clearing the filters if they hide it).
- **Deleting a parent** leaves its children in place with no parent.
- **Storage**: SQLite and MySQL store it in a new `tickets.parent_ticket_id` column, added automatically the first time the new version opens an existing database. An HTTP custom data source needs [protocol 1.1](http-datasource/README.md); a plugin that still speaks 1.0 keeps working for everything else, but creating a ticket with a parent fails with `PARENT_TICKET_UNSUPPORTED` before anything is sent, so a parent is never dropped silently. The [Jira sample plugin](../examples/jira-datasource/README.md) speaks 1.1 and keeps the parent in its `graphops.ticket` issue property.

In an autopilot run, the `work` session creates follow-up tickets with `--parent` set to the ticket it worked on. If that fails with `PARENT_TICKET_UNSUPPORTED`, it does not retry without `--parent` (the ticket would silently fall out of the tree) and creates no further tickets: the items that were not created are listed, with the title and description they would have had, in the `autopilot-decision-handoff` artifact, the ticket's result stays `done`, and its summary says how many items were left without a ticket.

## Starting a run

The project must have a **local path** in your environment (App Settings > project management, or `/graph-ops:ui` in the project's directory), because every child session works in a git worktree under it. Without one, starting from the Web UI fails with `PROJECT_LOCAL_PATH_NOT_SET` and launching a session from the CLI fails the same way.

**From Claude Code**, in the project's directory:

```
/graph-ops:autopilot-ticket <ticketId>
/graph-ops:autopilot-tree <ticketId>
```

The session you type this in becomes the orchestrator, so it runs with your own session's permission mode. Leave it alone while it runs: it launches child terminals, waits for them in the background, and prints the tree's summary at the end. Do not invoke `/graph-ops:autopilot-worker` yourself; it is started by `graph-engine autopilot launch`.

**From the Web UI**, expand a ticket and press "Autopilot (single)" or "Autopilot (tree)", then confirm. The server reserves a run and opens the orchestrator in a new terminal in the project's local path, with `--permission-mode` set to the project's `permissionMode` setting and the prompt `/graph-ops:autopilot-<ticket|tree> <ticketId> --run <runId>`. While a run is active, the Web UI shows, in the ticket list and on the expanded ticket (refreshed with the list, every 15 seconds):

| Badge | Shown on |
| --- | --- |
| "Autopilot running" | the root ticket of an active run |
| "Processing" | the ticket whose session is running now |
| "Waiting for a person" | the running ticket, when its session waits for your decision (the hover text and the expanded ticket say what it waits for) |
| "Waiting" | tickets the run is still going to process |

The autopilot buttons are disabled, with the reason shown under them and as a tooltip, when the start would be refused:

- the ticket belongs to an active run (it is that run's root, or in tree mode one of its descendants): both buttons;
- a descendant of the ticket is the root of an active run: "Autopilot (tree)" only;
- the ticket is `DONE` or `CLOSED`, unless the latest run of that mode from this ticket is stopped or interrupted -- then the button resumes that run (the confirmation says so). Only the five most recent inactive runs of the project are considered here; an older stopped run can still be resumed from the CLI (run the same slash command again).

## Which tickets a run processes

In `ticket` mode a run processes just its root. In `tree` mode it walks the tree depth first, children in creation order, and it re-reads the tree from the database at every step, so tickets created while the run is going are picked up. For each ticket:

- **`DONE` / `CLOSED`** tickets are skipped (`already_done` / `closed`), but their children are still processed.
- **A ticket that is `IN PROGRESS`, `IN REVIEW` or `IN RELEASE`** and was not launched by this run is taken to be worked on elsewhere, for example on another machine sharing the database: it is skipped (`in_progress_elsewhere`), and so is everything under it (`ancestor_in_progress`). A ticket left `IN PROGRESS` by an earlier run of this machine's registry does not count as "elsewhere".
- **The root is never skipped.** Starting a new run from a `DONE` or `CLOSED` ticket is refused with `AUTOPILOT_ROOT_FINISHED` (start from the descendant you want instead); a root that is `IN PROGRESS` is processed. See [Cautions and limitations](#cautions-and-limitations).
- **Limits**: `maxDepth` skips tickets deeper than the limit (`limit_depth`; the root is depth 0, counted by actual depth even across skipped tickets). `maxTickets` caps the number of tickets a run launches as `work`, the root included (`limit_tickets`). A ticket counts once: launching the same ticket again (a retry after a failure, or a relaunch in a run that was taken over) does not count again, a launch that fails is given back, and `merge-up` and `finalize` sessions do not count. Reaching a limit is not a failure: what was done is still merged up and finalized.
- **Failures** (`failed` or `blocked`, including a session judged unresponsive and a session that could not be launched twice in a row) follow `onFailure`:
  - `stop` stops the run (`ticket_failed`): nothing more is launched, merged up or finalized. Running the same command again resumes it.
  - `continue` skips the failed ticket's not-yet-started descendants (`parent_failed`), leaves its branch out of its parent's (`not_merged`), treats its parent's subtree as finished, and carries on with the rest. The summary lists the failed tickets' branches under "Work not in the root branch".
- **Retries**: a ticket that failed or was blocked in an earlier run is launched once more each time you start the same run again (after you have removed the cause); the child session continues from the ticket's node states in the database. Skips caused by a limit or by a ticket's status are re-evaluated with the current settings at that point, so raising `maxTickets` and running the command again processes the tickets it skipped.

## Branches

Every ticket is worked on in its own git worktree and branch, named the way Claude Code's `EnterWorktree` names them, so you can step into one yourself afterwards:

- worktree: `<project local path>/.claude/worktrees/<ticketId>`
- branch: `worktree-<ticketId>`

An existing worktree or branch of that name is reused (this is how a resumed run continues). A ticket ID that cannot name a branch -- anything other than letters, digits, `.`, `_` and `-`, starting with a letter or digit -- fails the launch.

- **Base branch**: a `ticket` run and a tree's root branch off the repository's default branch (the branch `origin/HEAD` points at, or `main` when that cannot be read). A child in a tree branches off the branch of its nearest ancestor that this run launched as `work` -- normally its parent. A skipped ancestor is passed over, so a leftover branch of a skipped ticket is never used.
- **A child merges into its parent's branch as part of its release, without approval**: its session merges the parent's branch into its own (`git merge`), resolves any conflicts, re-runs the tests, and then calls `graph-engine autopilot merge-into-parent`, which fast-forwards the parent's branch. If the conflicts cannot be resolved, or the fast-forward is refused (`NOT_FAST_FORWARD`, or `PARENT_WORKTREE_DIRTY` when the parent's worktree has uncommitted changes to tracked files), the child ends `blocked` with reason `merge_conflict` and `onFailure` applies.
- **Merge-up on the way back**: a child creates its own children only after it has merged into its parent, so a grandchild's work lands in the child's branch after that merge. To carry it up, when a ticket's whole subtree is finished, `next` returns `merge-up` for it and `graph-engine autopilot merge-up` fast-forwards the ticket's branch into its parent's again. Done bottom-up, one level at a time, this brings every finished descendant's work, at any depth, into the root's branch. When the fast-forward is not possible for any reason (not a fast-forward, a dirty target worktree, or another git error such as an untracked file in the way), nothing is changed and the result is `needs_merge_session`; the next `next` launches a `merge-up` session in the parent's worktree, which merges, resolves conflicts and re-runs the tests, or ends `blocked` (`merge_conflict`, or `merge_failed` when `git merge` fails before any conflict).
- **Reflection into the main branch** (`mainReflection`) applies only to the root -- in `ticket` mode, the ticket itself:
  - `branch` (default): leave the branch as it is. Nothing is pushed.
  - `pull_request`: push the branch and open a pull request into the default branch, without merging.
  - `merge`: open the pull request and merge it (through the pull request when the repository requires pull requests or protects the branch).

  In `ticket` mode the `work` session does this in its release step. In `tree` mode the root's release step only commits, and once every ticket of the tree has been merged up, `next` launches a `finalize` session in the root's worktree that does it for the whole tree. When a stage cannot be done (no remote, no `gh`, merge refused), the session stops at the last stage that worked and says so; a `finalize` that could not reflect anything beyond the local branch ends `blocked` (`reflection_failed`) and the run stops with `finalize_failed`. With `branch`, no `finalize` session is launched. When the root itself fails, nothing is reflected.
- A child ticket processed on its own (by `autopilot-ticket` or `process-ticket`) is not tied to its parent: it branches off the default branch as usual.

Do not move the tree's branches by hand while a run is going: a commit on an ancestor's branch turns the next fast-forward into a merge session.

## Automatic decisions

Every decision the autopilot makes on your behalf is saved on the ticket as a `text` artifact, with its reasons, so you can review it afterwards. An expanded ticket in the Web UI lists them in an "Automatic decisions (n)" section, each with a link that opens it in a new tab.

| Artifact | Saved on | Contents |
| --- | --- | --- |
| `autopilot-decision-refine` | the first node of the graph | the completion criteria, "why", priority and labels adopted in refinement, and why (not saved when the ticket was already refined) |
| `autopilot-decision-approval` | each `approval_gate` | approve or reject, and why |
| `autopilot-decision-iteration` | the failing review node | at a review iteration limit: whether one more round was granted (at most once per loop target) or the ticket was reported blocked |
| `autopilot-decision-release` | the `release` node | how the work was reflected, and the result |
| `autopilot-decision-handoff` | the `release` node | each open item, whether it became a follow-up ticket, why, and the created ticket's ID |
| `autopilot-tree-summary` | the root's `release` node | the run's summary (see `summary` below) |

The refinement decision is recorded in the run first (`record-decision`) and attached to the plan node once the graph exists (`attach-decisions`), so it survives an interruption and is never saved twice.

## Settings

Autopilot settings are per project. Edit your own values in the Web UI under Settings > Autopilot (for the project shown in the header); they are saved in your `$HOME/.graph-ops/config.json` under `autopilotSettings.<projectId>` and are never written to the database. `graph-engine autopilot settings [--project <id>]` prints the values in effect; it is read-only.

| Key | Values | Default | Meaning |
| --- | --- | --- | --- |
| `mainReflection` | `branch` / `pull_request` / `merge` | `branch` | How the root's work reaches the main branch (see [Branches](#branches)). |
| `permissionMode` | `acceptEdits` / `auto` / `dontAsk` / `bypassPermissions` | `auto` | The `claude --permission-mode` of every child session, and of the orchestrator when it is started from the Web UI. `default` and `plan` are not accepted, since a session in those modes cannot run on its own. |
| `autoApproveGates` | `true` / `false` | `true` | Whether the child session decides `approval_gate`s. When off, it waits for your decision as `process-ticket` does. |
| `autoCreateTickets` | `true` / `false` | `true` | Whether the child session decides which open items become follow-up tickets and creates them. When off, it lists the items in its terminal and waits for you to pick. |
| `maxTickets` | 1-100 | 20 | Tickets launched as `work` per run, the root included. Relaunching the same ticket does not count again, a failed launch is given back, and `merge-up` / `finalize` sessions do not count. |
| `maxDepth` | 0-10 | 3 | How deep derived tickets are processed (the root is 0). |
| `onFailure` | `stop` / `continue` | `stop` | Stop the whole run on a failure or block, or skip only that ticket's subtree and continue. |
| `stallTimeoutMinutes` | 15-1440 | 60 | A child session with no sign of activity for this long is failed as unresponsive. |

When `autoApproveGates` or `autoCreateTickets` is off, a child terminal asks you for input while the run is going. Such a session is marked "Waiting for a person" and is never judged unresponsive while it waits.

### Team settings and precedence

A team can set autopilot settings in `autopilot.yaml` at the root of the shared directory `teamExtensionsDir` points at (next to `workflow.yaml`):

```yaml
version: 1
defaults:            # every project
  mainReflection: pull_request
projects:
  proj-64195d2b:     # a project ID
    maxTickets: 10
```

Each key is resolved on its own, first match wins:

1. the team file's `projects.<projectId>`
2. the team file's `defaults`
3. your local value (`autopilotSettings.<projectId>` in `$HOME/.graph-ops/config.json`)
4. the built-in default

A key the team file sets is **locked**: the Web UI shows it read-only as "Fixed by team settings (shared default)" or "(this project)", with your own local value, if you have one, in parentheses. The local value stays in your file and takes effect again once the team file stops setting the key.

- **`bypassPermissions` is never taken from the team file.** A shared directory must not be able to make everyone's child sessions run without confirmation. The value is ignored with the warning `AUTOPILOT_TEAM_BYPASS_PERMISSIONS_IGNORED`, the key is not locked, and resolution falls through to your local value or the default. You can still choose `bypassPermissions` locally; the Web UI shows a warning when you do.
- **Invalid team values are ignored, not fatal.** An unreadable file or a `version` other than 1 ignores the whole file (`AUTOPILOT_TEAM_FILE_INVALID`); an invalid value or a value of the wrong type (`"10"` for a number, `yes` for a boolean) ignores that key (`AUTOPILOT_INVALID_VALUE`); an unknown key is reported as `AUTOPILOT_UNKNOWN_KEY`. The same warnings apply to hand-edited local values. They are shown in the Web UI and returned in `warnings`.
- **Saving** (`PUT /api/projects/{id}/autopilot-settings`) takes a partial object of the keys to store locally: a key with a value is stored, a key set to `null` removes your local value, and a key left out is unchanged. A body that contains a locked key -- even with the team's own value, or `null` -- is refused as a whole with `400 AUTOPILOT_SETTING_LOCKED` (`details.keys` names the keys), and so is an invalid value (`400 VALIDATION_ERROR`); nothing is saved in either case.

## Interrupting, resuming and unresponsive sessions

- **Resuming**: if the orchestrator is interrupted (the terminal closed, the session ended), run the same command again -- `/graph-ops:autopilot-tree <root>` or `/graph-ops:autopilot-ticket <root>`, or the same Web UI button. A run is active while its heartbeat is less than 10 minutes old; the orchestrator's commands (`start`, `next`, `launch`, `wait`, `merge-up`) refresh it, and a waiting `wait` refreshes it every 30 seconds. Once the heartbeat is older, the latest run with the same root and mode is taken over: the same run continues, with the settings as they are now, and the work to do is recomputed from the database. A stopped run is taken over the same way. A child session that was still marked running is waited for first, not relaunched. **Close the child terminals left over from the interrupted run before resuming**, so that no ticket is worked on twice.
- **Unresponsive sessions**: a child terminal is not a process the engine can watch, so a session that ends without reporting (a crash, a closed terminal, a refused permission, a question to a person, an overflowing context) is detected from the absence of activity. Activity is any `graph-engine autopilot` call of the child session, any change to the ticket, its nodes or its artifacts in the database, and any change to its worktree (new commits, changed or new files). When none of these changes for `stallTimeoutMinutes`, the ticket is failed with reason `unresponsive` and `onFailure` applies; the summary names the worktree and asks you to check the terminal if it is still open. A report that arrives after that is kept as a late report in the summary and does not change the run.
- **`wait` never blocks forever**: it returns after `--timeout` (10 minutes by default) with exit code 2, and the orchestrator goes back to `next`, so every wait ends within `stallTimeoutMinutes` plus the timeout.

## The run registry

Runs are kept in local files, not in the database, per project:

```
$HOME/.graph-ops/autopilot/<projectId>/runs/<runId>.json   one file per run
$HOME/.graph-ops/autopilot/<projectId>/.lock                the project's lock
$HOME/.graph-ops/autopilot/terminal-tab.lock                 serializes Terminal.app tab launches (all projects)
```

Every project has its own directory and lock, so runs in different projects never interfere. Within a project, a start is refused with `AUTOPILOT_ALREADY_RUNNING` when an active run owns the ticket (the run's root and, for a tree run, all of its descendants) or, for a tree start, when an active run's root is among the ticket's descendants.

- **The lock** is held for milliseconds (reading, deciding and writing run files; git, database and network work happen outside it). Its holder refreshes it every 10 seconds, and a lock not refreshed for 2 minutes is treated as left behind by a process that is gone and removed (the removal is logged). A command that cannot get the lock within 60 seconds, or finds it was taken over while it held it, fails with `AUTOPILOT_REGISTRY_LOCKED` and saves nothing; run it again.
- **Old runs are pruned** whenever a new run is created: of the settled runs -- finished ones, and ones replaced by a newer run with the same root and mode -- the newest 20 are kept. The latest run for each root and mode that is stopped or interrupted is kept however old it is, because running the same command again resumes it. So a stopped run you do not intend to resume stays until you delete it: when it is not active (check with `graph-engine autopilot status`), delete its `runs/<runId>.json` by hand. A ticket that a deleted run left `IN PROGRESS` then counts as "in progress elsewhere" for later runs.
- **Terminal.app fields**: a run file also keeps `terminal_tty`, the tty of the orchestrator's Terminal.app tab, and `terminal_tab_disabled`, the reason the tab was disabled for the run, both left out when empty (see [Terminal.app: child sessions as tabs](#terminalapp-child-sessions-as-tabs)). Run files written by an earlier version are read as having neither.
- **A run file that cannot be read** is skipped with a warning naming its path (on stderr as `graph-engine: warning: ...` for the CLI, in the server log with `event=autopilot_registry` for the Web UI), and every start in that project is refused with `AUTOPILOT_REGISTRY_CORRUPT` until you repair or delete the file, since the duplicate-run check cannot be made without it.

## The `graph-engine autopilot` CLI

The slash commands and the Web UI drive these subcommands; you rarely need to run them yourself, except `status` and `settings`. Every run subcommand prints one line of compact JSON (`summary` prints Markdown; `status` and `settings` print indented JSON). Errors go to stderr after `Error:` -- most of them start with their code, as in `Error: AUTOPILOT_ALREADY_RUNNING: ...` -- with exit code 1.

Orchestrator side:

| Subcommand | Output |
| --- | --- |
| `start <ticketId> --mode ticket\|tree [--run <runId>]` | Creates a run, takes over the latest interrupted or stopped run with the same root and mode, or (`--run`) adopts a run the Web UI reserved. `{"run_id","project_id","mode","root","state","created","resumed","adopted","next"}`; `next` is the command to run next. |
| `next <runId>` | The one next action: `{"action","ticket","role","reason","detail","worktree","command"}`, with empty fields left out. `action` is `launch` (`command` is the `launch` to run, with `--role work`, `merge-up` or `finalize`), `wait` (`worktree` is where that session runs), `merge-up`, `done` or `stopped` (`reason` and `ticket` say why; `command` is the `summary` to run). |
| `launch <runId> <ticketId> [--role work\|merge-up\|finalize]` | Prepares the worktree and opens the child session. `{"launched","role","worktree","branch","base_branch","next"}`; `next` is the `wait` command. A failure prints the error with `attempt n of 2`; the second failure in a row records the ticket as failed (`launch_failed`). In Terminal.app, when a tab was tried and failed and the session opened in a new window instead, it also prints a `graph-engine: warning:` line on stderr (see [Terminal.app: child sessions as tabs](#terminalapp-child-sessions-as-tabs)). |
| `wait <runId> <ticketId> [--timeout <duration>]` | Blocks until the session reports. Exit 0 with `{"state":"reported","ticket","role","result","reason","detail","summary"}` (also when the session was just failed as `unresponsive`); exit 2 on timeout with `{"state":"waiting","ticket","idle_minutes","stall_timeout_minutes"}` or `{"state":"awaiting_human","ticket","awaiting"}`; exit 1 on an error. The default timeout is 10 minutes (a Go duration such as `30s` or `10m`). |
| `merge-up <runId> <ticketId>` | `{"result","ticket","branch","target_branch","reason","detail"}`. `result` is `merged`, `up_to_date` or `needs_merge_session`; with `needs_merge_session`, `reason` is `NOT_FAST_FORWARD`, `PARENT_WORKTREE_DIRTY` or `GIT_ERROR`, and for `GIT_ERROR` `detail` holds the git message (up to 300 characters). |
| `summary <runId>` | The run's summary as Markdown: each ticket's result, reason, branch, merge state and summary, the stop reason, the main-branch reflection, "Work not in the root branch", unresponsive sessions with their worktrees, and late reports. It is also saved on the root's `release` node as `autopilot-tree-summary` (not saved again when unchanged). |
| `status [--project <id>]` | `{"project_id","runs":[{"run_id","project_id","mode","root","state","active","heartbeat","current","current_role","awaiting_human","stop_reason","tickets":{"<ticketId>":"<state>"}}]}`, newest first. |
| `settings [--project <id>]` | `{"project_id","settings","items":[{"key","value","source","locked","local","team","default"}],"warnings","team_file"}`; `source` is `default`, `local`, `team_defaults` or `team_project`. |

`status` and `settings` pick the project the way `list-labels` does (`--project`, else the current directory's local path, else the current project).

Child-session side (used by the `autopilot-worker` skill):

| Subcommand | Output |
| --- | --- |
| `worker-context <runId> <ticketId>` | `{"run_id","ticket","mode","run_state","position","role","branch","worktree","base_branch","target_branch","target_ticket","default_branch","merge_source_branch","merge_worktree","settings","pending_decisions"}`; `position` is `single`, `tree_root` or `tree_child`. |
| `record-decision <runId> <ticketId> <kind> <content\|->` | Keeps a decision in the run until it can be saved as an artifact. `{"recorded","ticket"}` |
| `attach-decisions <runId> <ticketId> <nodeId>` | Saves the kept decisions on the node, skipping any already on the ticket. `{"attached":[...],"skipped":[...]}` |
| `touch <runId> <ticketId> [--awaiting-human <what>]` | Records activity; `--awaiting-human` marks the session as waiting for a person. `{"touched","awaiting_human"}` |
| `report <runId> <ticketId> --result done\|failed\|blocked [--reason <code>] --summary <text\|->` | Records the session's result; the summary is cut to 3 lines and 500 characters, and the last report wins. `{"recorded","late_report","ticket","result"}` |
| `merge-into-parent <runId> <ticketId>` | Fast-forwards the ticket's branch into its parent's. `{"result","ticket","branch","target_branch"}` with `merged` or `up_to_date`; fails with `NOT_FAST_FORWARD`, `PARENT_WORKTREE_DIRTY` or a git error, changing nothing. |

Exit codes follow `wait-node`: 0 success, 1 error, 2 a `wait` timeout.

## Error and reason codes

Errors of the CLI and the HTTP API (HTTP status in parentheses):

| Code | Meaning |
| --- | --- |
| `AUTOPILOT_ALREADY_RUNNING` (409) | An active run overlaps the ticket or its tree. `details` names the run. |
| `AUTOPILOT_ROOT_FINISHED` (409) | A new run cannot start from a `DONE` or `CLOSED` ticket. |
| `AUTOPILOT_REGISTRY_CORRUPT` (409) | A run file of the project cannot be read; repair or delete it (its path is in the warning and in `details`). |
| `AUTOPILOT_REGISTRY_LOCKED` (409) | The project's lock could not be taken within 60 seconds, or was taken over; nothing was saved. Run the command again. |
| `AUTOPILOT_INVALID_STATE` (409) | The command does not fit the run's current state (for example `next` on a run the Web UI reserved but no orchestrator has adopted yet). |
| `AUTOPILOT_RUN_NOT_FOUND` (404) | No run with that ID in any project's registry. |
| `AUTOPILOT_SETTING_LOCKED` (400) | A settings save contained a key the team file sets. |
| `PROJECT_LOCAL_PATH_NOT_SET` (400) | The project has no local path in this environment. From the Web UI nothing is reserved. During a run, a `launch` fails with it and counts as a failed launch, so the second one in a row records the ticket as `launch_failed`. |
| `PARENT_TICKET_UNSUPPORTED` (400) | `create-ticket --parent` against an HTTP data source that speaks protocol 1.0. |
| `NOT_FAST_FORWARD`, `PARENT_WORKTREE_DIRTY` | A fast-forward into a parent's branch was refused: the branches have diverged, or the parent's worktree has uncommitted changes to tracked files. Untracked files do not count as uncommitted changes; if one is in the way of the fast-forward, git itself fails instead (`GIT_ERROR` for a merge-up). |

Reasons in a run (in `next`, `wait`, `status`, the summary and the Web UI):

- Skips: `already_done`, `closed`, `in_progress_elsewhere`, `ancestor_in_progress`, `limit_tickets`, `limit_depth`, `parent_failed`.
- Failures recorded by the engine: `unresponsive` (no activity for `stallTimeoutMinutes`), `launch_failed` (the session could not be launched twice in a row; `detail` has the error), `merge_conflict` (a merge-up session could not merge).
- Reported by child sessions: `merge_conflict`, `merge_failed`, `gate_rejected`, `iteration_limit`, `blocked`, `error`, `permission_denied`, `reflection_failed`.
- Run stops: `ticket_failed`, `finalize_failed`.
- Merge states of a ticket's branch: `merged_self` (merged into its parent by its own release), `merged_subtree` (merged up with its subtree), `not_merged` (left out after a failure).

## HTTP API

The Web UI uses these endpoints of the local UI server. Like every state-changing route, `POST` and `PUT` require the CSRF header and the loopback checks the server already applies.

- `POST /api/tickets/{id}/autopilot` with `{"mode":"ticket"|"tree"}`: checks the mode (`400 VALIDATION_ERROR`), the ticket (`404 TICKET_NOT_FOUND`) and the project's local path (`400 PROJECT_LOCAL_PATH_NOT_SET`) before reserving anything; then reserves a run through the same check as `autopilot start` -- taking over the latest stopped or interrupted run of the same root and mode, then refusing a new run from a `DONE` / `CLOSED` ticket, then refusing an overlap (the 409 codes above) -- and opens the orchestrator's terminal. If the terminal does not open, the reservation is undone (a taken-over run returns to its previous state) and the answer is `500`. On success: `{"run_id","mode","root","state","created","resumed"}`. Launches and launch failures are logged (`event=autopilot_launched` / `autopilot_launch_failed`).
- `GET /api/autopilot/runs?project_id=<id>`: a JSON array of the project's active runs and its five most recent other runs, newest first. Each has the fields of `autopilot status` plus:
  - `members`: every ticket an active run owns -- the root and, for a tree run, all of its descendants as the database has them now, including those not reached yet. This is the set the duplicate-start check uses, and the Web UI disables the autopilot buttons for it. `[]` for a run that is not active.
  - `pending`: the members the run may still launch as `work`, in processing order -- leaving out `DONE` / `CLOSED` tickets and tickets beyond the limits, under a ticket in progress elsewhere, or under a failed ticket. The Web UI's "Waiting" badge. `[]` for a run that is not active.
- `GET /api/projects/{id}/autopilot-settings` and `PUT /api/projects/{id}/autopilot-settings`: see [Settings](#settings). `GET` answers in the shape `autopilot settings` prints.

## Cautions and limitations

- **Shared backends run other people's tickets with your permissions.** With MySQL, Jira or another HTTP data source, `autopilot-tree` processes every unfinished descendant in the database -- including child tickets other people created, even while the run is going -- on your machine, with your child sessions' permission mode, and with approval gates decided automatically. Use tree mode on a shared backend only when you trust whoever can create tickets under the root, and do not combine it with `permissionMode: bypassPermissions`.
- **Do not start from a ticket someone is working on elsewhere.** Descendants that are `IN PROGRESS` elsewhere are skipped, but the root is not, since you named it explicitly: starting from a ticket that is being worked on on another machine works on it a second time.
- **Check the permission mode before `pull_request` or `merge`.** Pushing and opening or merging a pull request run `git push` and `gh` in the child session. If its permission mode refuses them (the default `auto` may), the session reports `failed` with `permission_denied` (or stops at the last stage that worked). Make sure your permission settings allow those commands for the child sessions before choosing these values.
- **Close leftover terminals before resuming**, and do not move the tree's branches by hand while a run is going (see above).
- **Untracked files and the dirty check**: `PARENT_WORKTREE_DIRTY` looks only at uncommitted changes to tracked files; untracked files in the parent's worktree are ignored unless the fast-forward would overwrite them, in which case git fails.
- **`.claude/worktrees`**: in a repository that does not ignore `.claude/worktrees/`, the ticket worktrees show up as untracked files in the main working tree. Add it to `.gitignore` if that bothers you.
- **Many terminals**: a 20-ticket tree opens about 20 child terminals, which stay open until you close them. In Terminal.app they are tabs of the orchestrator's window (when the permissions allow it); elsewhere, a `terminalCommand` that opens tmux windows (or tabs) keeps them manageable.
- **Terminal.app takes the focus** each time a child session opens as a tab, and needs the Accessibility and Automation permissions to do so; without them, or when a tab cannot be opened, sessions open in new windows. See [Terminal.app: child sessions as tabs](#terminalapp-child-sessions-as-tabs).
- **Wrong-but-busy sessions**: a legitimate step that shows no activity at all for longer than `stallTimeoutMinutes` (a very long test run with no file changes, say) is failed as unresponsive; raise the setting (up to 24 hours) for such projects. Running the same command again retries the ticket once.
- **Automatic decisions can be wrong.** Review the "Automatic decisions" of important tickets. At a review iteration limit, the autopilot grants at most one extra round per loop target before reporting the ticket blocked.
- **The "Waiting" badge can briefly miss tickets** that a run will relaunch after a retry (children skipped as `parent_failed` whose parent is being retried). Processing is not affected.
