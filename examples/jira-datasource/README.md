# Jira data source (sample plugin)

A sample [GraphOps HTTP data source](../../docs/http-datasource/openapi.yaml)
that stores GraphOps tickets, execution graphs (nodes and edges), artifacts,
projects and labels in **Jira Cloud**. graph-engine talks to it with
`dbBackend: "http"`; the plugin translates each of the protocol's 32
operations into Jira REST API v3 calls.

It is a standalone Go module (standard library only) and does not import
anything from graph-engine: the only contract between the two is the
published protocol. Use it as a starting point for your own plugin; the
[developer manual](../../docs/http-datasource/README.md) explains the
protocol, the security rules and how to develop a plugin locally.

## Running it

Requirements: Go 1.25 or newer, a Jira Cloud site, an Atlassian account with
access to the Jira project(s) you want to use, and an
[API token](https://id.atlassian.com/manage-profile/security/api-tokens) for
that account.

Everything is configured through environment variables, so no secret has to
be written to a file:

| Variable | Required | Meaning |
|---|---|---|
| `JIRA_BASE_URL` | yes | Your site, e.g. `https://your-site.atlassian.net` (must be `https://`) |
| `JIRA_EMAIL` | yes | The Atlassian account email used with the API token |
| `JIRA_API_TOKEN` | yes | The Atlassian API token |
| `GRAPHOPS_DATASOURCE_TOKEN` | yes | The bearer token graph-engine must send; pick a long random value |
| `LISTEN_ADDR` | no | Default `127.0.0.1:8787` |
| `JIRA_ISSUE_TYPE` | no | Issue type used for ticket (and metadata) issues, default `Task`. Use a standard issue type such as `Task` or `Story`, **not `Epic`** (see [Limitations](#limitations)) |
| `JIRA_SUBTASK_ISSUE_TYPE` | no | Sub-task issue type used for node sub-tasks, default `Subtask` (some projects call it `Sub-task`) |
| `JIRA_NODE_IN_PROGRESS_STATUS` | no | Workflow status a node sub-task is moved to while the node is under way (any status but TODO and DONE, REJECTED included), default `In Progress`. Set it to the empty string to turn this move off |
| `JIRA_NODE_DONE_STATUS` | no | Workflow status a node sub-task is moved to when the node is DONE, default `Done`. Set it to the empty string to turn this move off |
| `GRAPHOPS_STATE_FILE` | no | Local state file, default `./jira-datasource-state.json` |

The plugin refuses to start if any required variable is missing.

```sh
cd examples/jira-datasource
export JIRA_BASE_URL=https://your-site.atlassian.net
export JIRA_EMAIL=you@example.com
export JIRA_API_TOKEN=...            # never commit this
export GRAPHOPS_DATASOURCE_TOKEN="$(openssl rand -hex 32)"
go run .
```

An unset variable takes its default; a variable set to the empty string is
a deliberate "off" (for the two status variables, that direction of
workflow moves is turned off). At startup the plugin logs one line with the
ticket issue type, the sub-task issue type and the two status names (`no workflow status (off)`
when turned off); no secret is logged.

**Set the two status names to the names your workflow really uses.** The
plugin matches them (case-insensitively) against the *names* of the
statuses a sub-task can be moved to, and Jira shows localized names: on a
site whose Jira language is not English, `In Progress` and `Done` usually do
not exist. The project `GRAP` used for the verification below, a
team-managed project on a Japanese site, has the statuses `未着手`, `進行中`
and `完了`, so it needs:

```sh
export JIRA_NODE_IN_PROGRESS_STATUS=進行中
export JIRA_NODE_DONE_STATUS=完了
```

With names that do not match, every workflow move fails: GraphOps still
works and the status labels are still right, but **every update of a node
that is not TODO costs one extra Jira request (reading the sub-task's
transitions) and logs one line** such as
`node GRAP-5-n6 (sub-task GRAP-6): could not move it to workflow status "Done": no transition leads there from its current status (reachable: "未着手", "進行中", "完了")`
-- this happens even for updates that do not change the status. Fix the
names (the log line lists the reachable ones), or, if you do not want the
workflow status mirrored at all, set both variables to the empty string.

Then point graph-engine at it. Settings are read from one file,
`$HOME/.graph-ops/config.json` (never from the directory you start in), so add
these three fields there -- and keep the token itself out of the file by
referencing the environment variable:

```json
{
  "dbBackend": "http",
  "httpDataSourceUrl": "http://127.0.0.1:8787",
  "httpDataSourceToken": "${GRAPHOPS_DATASOURCE_TOKEN}"
}
```

The same three can be given as `GRAPH_DB_BACKEND`, `GRAPH_HTTP_DATASOURCE_URL`
and `GRAPH_HTTP_DATASOURCE_TOKEN` instead, which take precedence over the file
-- the better choice while you are still trying the plugin out, since it leaves
your normal configuration untouched.

graph-engine accepts plain `http://` only for loopback addresses. The plugin
itself serves plain HTTP, so to reach it from another machine put a
TLS-terminating reverse proxy in front of it and use an `https://` URL.

Finally register a Jira project as a GraphOps project. The prefix **is the
Jira project key**:

```sh
graph-engine create-project "My project" --prefix GOPS
graph-engine use-project jira-GOPS
```

## How GraphOps data is stored in Jira

| GraphOps | Stored as | ID format |
|---|---|---|
| Project | An existing Jira project (never created by the plugin). The project name and label definitions live in the `graphops.project` issue property of a per-project **metadata issue** (summary "GraphOps metadata (do not delete)", Jira label `graphops-meta`). | `jira-<KEY>`, e.g. `jira-GOPS`; the prefix is `<KEY>` |
| List of registered projects | The plugin's **local state file** (`GRAPHOPS_STATE_FILE`): each registered project key with its metadata issue key, in registration order. | - |
| Current project | The same local state file (`current_project_id`). | the project ID |
| Ticket | A Jira issue of type `JIRA_ISSUE_TYPE` with the Jira label `graphops`. Every GraphOps field (title, Markdown description, status, priority, assignee, blocked, label IDs, timestamps, ...) lives in the `graphops.ticket` issue property. The issue summary and description mirror the title and description (converted to ADF paragraphs) for people reading Jira. | the issue key, e.g. `GOPS-12` |
| Node | A **sub-task of the ticket's issue** (issue type `JIRA_SUBTASK_ISSUE_TYPE`) with the summary `<node name> [<node type>]` (the first line of the name, shortened so the summary stays within Jira's 255 characters), a fixed description saying it is a GraphOps node, and exactly one `graphops-status-<status>` label. The node's data (name, type, status, iteration count, gate criteria, ..., and the IDs of its artifact comments, `artifact_comment_ids`) lives in the sub-task's `graphops.node` issue property. | `<ticket key>-n<sub-task number>`, e.g. `GOPS-12-n15` for sub-task `GOPS-15` of ticket `GOPS-12` |
| Edge | An entry in the `graphops.edges` issue property of the ticket's issue (`edge_seq` and a compact list of edges: ID, from, to, condition -- omitted when it is `always` -- and creation time). | the ID graph-engine proposes |
| Artifact | A comment on **its node's sub-task** (never on the ticket's issue): a readable summary (name, type, node, and the content in a code block) plus the `graphops.artifact` comment property with the metadata. text/gherkin/json content up to 16 KiB is kept inline in that property; larger content and every html/image artifact is uploaded as an attachment of the same sub-task that the property points to (images are stored as their decoded bytes). The comment's ID is also added to the node's `artifact_comment_ids`. | `<node ID>-c<comment id>`, e.g. `GOPS-12-n15-c10023` |
| Label | An entry in the project's `graphops.project` property; which tickets carry it is in each ticket's `graphops.ticket.label_ids`. | `<KEY>-label-<n>`, e.g. `GOPS-label-3` |

Every ID leads to where it is stored without a search:

- a node ID splits at its last `-n` into the ticket's issue key and the
  sub-task's number (`GOPS-12-n15` is sub-task `GOPS-15` under `GOPS-12`);
- an artifact ID splits at its last `-c` into the node ID and the comment
  ID;
- a label ID's part before `-label-` is the project key, which leads to the
  metadata issue through the state file.

Numbers are digits without a leading zero (`GOPS-12-n015` is not a node ID),
so one node or artifact has exactly one ID. An ID that does not have these
shapes -- including every ID of the earlier storage format, such as
`GOPS-12-03` or `GOPS-12-c10023` -- is answered with `NOT_FOUND` without any
request to Jira.

**Label IDs did not change** when nodes moved to sub-tasks: a label ID
already names its project, and through the state file the project's
metadata issue, which is the only place labels are stored.

Why the state file: the list of registered projects and the current project
are single values for the whole data source, not per Jira project, and Jira
has no place for such a value that an ordinary (non-admin) account can write
(project and app properties need admin permissions). Losing the file is not
fatal: registering the same project key again finds its existing metadata
issue and reuses it, labels included.

### Node status: one label, and the workflow as a best-effort mirror

A node's status is shown on its sub-task in two ways.

**The status label (always).** Every node sub-task carries exactly one Jira
label starting with `graphops-status-`: the node's status in lower case, with
spaces turned into `-` -- `graphops-status-todo`, `graphops-status-in-progress`,
`graphops-status-in-review`, `graphops-status-awaiting-fix`,
`graphops-status-done`, `graphops-status-rejected`. When the status changes,
the old label is removed and the new one added in the same edit, so a
sub-task never shows two of them.

- The prefix `graphops-status-` is **reserved for GraphOps**: any other
  `graphops-status-*` label found on a node sub-task, including one a person
  added, is removed at the next status update. Do not use the prefix for your
  own labels.
- **No other label is touched.** Labels you add to a sub-task stay; the
  `graphops` label that marks tickets for listing is never put on
  sub-tasks, so sub-tasks do not show up in `list-tickets`.
- GraphOps ticket labels (the `<KEY>-label-<n>` ones) are not Jira labels. If
  they are ever mirrored as Jira labels, they will use a different prefix so
  they cannot collide with the status labels.

**The Jira workflow status (best effort).** Only two of the workflow's
statuses are used, configured by name:

| GraphOps node status | Jira workflow status of the sub-task |
|---|---|
| `TODO` | not moved: it stays in the status it was created in (or wherever an earlier update left it) |
| `DONE` | moved to `JIRA_NODE_DONE_STATUS` (default `Done`) |
| every other status: `IN PROGRESS`, `IN REVIEW`, `AWAITING FIX`, **`REJECTED`** | moved to `JIRA_NODE_IN_PROGRESS_STATUS` (default `In Progress`) |

`REJECTED` goes to the "in progress" status on purpose: a rejected node is
waiting for the next decision of a person or of `process-ticket` (fix and
retry, or give up) and its work is not finished. Showing it as done would
make a node that needs attention easy to overlook on the board.

How a move is made: the plugin reads the sub-task's available transitions and
performs the one whose target status has the configured name (compared
without regard to case). Nothing is sent when the sub-task is already in that
status. A move has its own **8-second deadline** within the request's.

A move is only a mirror, and it never makes a GraphOps write fail. The
record is the `graphops.node` property, and the label is always updated. When
the move is impossible -- no transition to that status, a name that does not
exist, a refused or timed-out Jira call -- the plugin logs one line with the
node ID, the sub-task key, the target status and the reason (including the
statuses that *are* reachable), and the request succeeds.

Moving a node back to `TODO` (the iteration loop sends a node back after a
failed review) changes the label to `graphops-status-todo` but does not move
the sub-task, so for a while the board can show a `Done` sub-task with the
`todo` label. It moves to "in progress" as soon as the node is started again.

**Order of the writes, and locking.** Within the plugin, every update of a
node runs under that sub-task's lock, and the workflow move is made inside
the same lock, after `graphops.node` and the label were written. Updates of
one node are therefore applied one at a time, move included, so two quick
updates (say IN PROGRESS, then DONE right after `get-executable`) cannot
end with the label saying `done` and the workflow saying "in progress". One
exception to the order: when the sub-task is in the "done" status and the
update moves it elsewhere (a node sent back from DONE), the move is made
first, because workflows often make finished issues read-only and the label
edit would be refused there. Operations that need both a ticket's lock and a
node's (creating and deleting a node) always take the ticket's first, so
they cannot deadlock.

### What each operation does in Jira

- **Create project**: checks the Jira project exists and is accessible
  (`VALIDATION_ERROR` otherwise), then creates (or reuses) its metadata
  issue. The prefix must be 1-5 letters/digits (`INVALID_PREFIX` otherwise),
  and there is no automatic prefix generation: the prefix is the key.
- **Create ticket**: creates an issue of type `JIRA_ISSUE_TYPE` with the
  label `graphops`, the `graphops.ticket` property and an empty
  `graphops.edges` property in one request. The ticket's issue itself is
  never moved through the workflow.
- **Update ticket**: rewrites `graphops.ticket`; if the title or description
  changed, also updates the issue summary/description. Ticket statuses are
  not synchronized with the Jira workflow (only node sub-tasks are, see
  above).
- **Create node**: under the ticket's lock, reads the ticket with its
  sub-tasks, checks the 99-node limit, and creates the sub-task -- parent,
  issue type, summary, status label, description and `graphops.node` -- in
  **one** request. A node created in a status other than TODO is then moved
  through the workflow.
- **List nodes / ticket detail**: reads the ticket's `subtasks` field and
  then the sub-tasks themselves with `bulkfetch` (100 per request), keeping
  only those that carry `graphops.node`; no JQL search is used, so a node
  created a moment ago is never missing. Nodes are ordered by creation time.
  The ticket detail reads all artifact comments of the ticket with one
  `comment/list` request (1000 per page), so its cost does not grow with the
  number of nodes.
- **Update node**: writes `graphops.node`, then -- only if the summary or
  the status label changes -- edits the sub-task once, then moves it through
  the workflow (see the previous section for the order and the exception).
- **Delete node**: removes the node's edges from `graphops.edges`, then
  **deletes the node's sub-task**, which in Jira also deletes its artifact
  comments and attachments. Edges go first: if deleting the sub-task fails,
  the request fails, and running it again finishes the job. A sub-task that
  was already deleted in Jira only has its edges removed.
- **Delete ticket**: *detaches* the ticket rather than deleting anything:
  removes the `graphops` label and the `graphops.ticket`/`graphops.edges`
  properties from the ticket's issue, then cleans up its node sub-tasks.
  **The sub-tasks stay in Jira** -- with their artifact comments,
  attachments, your own labels and their workflow status -- and only lose
  `graphops.node` and their `graphops-status-*` label. The clean-up is best
  effort (at most 4 sub-tasks at a time, at most 10 seconds per request, each
  failure logged); see [Limitations](#limitations) for what can be left
  behind.
- **Delete project**: first detaches every ticket of the project, deletes the
  metadata issue and unregisters the project (clearing the current project
  if it pointed there); only then cleans up the node sub-tasks of all those
  tickets, within the same 10-second budget. The Jira project is untouched.
- **List tickets**: one JQL search, `project = GOPS AND labels = graphops AND
  labels != graphops-meta ORDER BY created DESC`, through the
  `/rest/api/3/search/jql` API (paged with `nextPageToken`), requesting the
  `graphops.ticket` property in the same call, so listing is not one request
  per issue. Node sub-tasks never carry the `graphops` label, so they are not
  listed. A registered project whose metadata issue was deleted in Jira is
  left out of both the project list and the ticket list (instead of making
  the whole listing fail). Its label definitions were in that issue, so do
  not delete metadata issues.
- **Node reads and writes are checked first.** Get, update and delete node,
  create artifact, get artifact and list a node's artifacts act only on a
  node ID that names a *managed node*: the ID has the right shape; its
  ticket is a managed ticket of a registered project (label `graphops` and
  the `graphops.ticket` property); the sub-task's parent is that ticket and
  its key is exactly the one in the ID; and the sub-task has `graphops.node`.
  Anything else -- an issue in a Jira project that is not registered, one
  never created through GraphOps, the ticket itself, another ticket's
  sub-task, a sub-task made by hand, a detached ticket's sub-task -- is
  `NODE_NOT_FOUND` / `ARTIFACT_NOT_FOUND` (an empty list for listings), and
  nothing is written, deleted or read from it. Creating an artifact on a
  ticket that is not managed is `TICKET_NOT_FOUND`.
- **Get artifact**: additionally requires the comment to be on the node's
  sub-task (by the issue ID in the comment's `self` URL) and its
  `graphops.artifact` property to name that node. Listing a ticket's
  artifacts reads the IDs in each node's `artifact_comment_ids` and keeps
  only comments that pass the same two checks; a comment whose `self` URL
  cannot be read is not taken from that list, and its node's comments are
  read from the sub-task instead.
- **Create artifact**: under the sub-task's lock, uploads the attachment (if
  any), creates the comment and appends its ID to `artifact_comment_ids`. If
  a later step fails, the earlier ones are undone (the attachment, and the
  comment, are deleted again within their own 5-second deadline), so a
  failed call normally leaves nothing behind.

### Deadlines, cancellation and rate limiting

graph-engine gives up on a request after 30 seconds and never retries it
(see the protocol description in `openapi.yaml`). A write the plugin
finishes *after* that would still take effect, and a user or agent who saw
the timeout and ran the command again would create a second ticket, node or
artifact. The plugin therefore bounds its own work:

- Every protocol request has a **25-second deadline**, covering all of its
  Jira calls, retries and waits. The request's context is passed to every
  Jira call, so when graph-engine disconnects (or the deadline passes) no
  further Jira call is made -- in particular no write that was still to come.
  A single Jira call in flight at that moment is aborted from the plugin's
  side, but Jira may already have applied it.
- Within it, a workflow move has its own **8-second** deadline, and the
  sub-task clean-up after detaching tickets a **10-second** one, so neither
  can use up the request's time.
- A `429` or `503` answer from Jira is retried up to 4 times, waiting for
  `Retry-After` (at most 30 seconds per wait) -- but only when the wait ends
  before the request's deadline. Otherwise the plugin stops at once and
  answers `INTERNAL_ERROR` with Jira's status in the message, well before
  graph-engine's timeout. Under sustained rate limiting a command therefore
  fails instead of hanging; run it again once Jira recovers (check first
  whether a create went through).
- One Jira call attempt is limited to 20 seconds.
- Each retry, and each request that takes longer than 5 seconds, is logged
  as one line on stderr.

The 30 seconds of graph-engine apply to each protocol request, not to a whole
command: a command such as `expand-graph` makes many requests one after the
other and can take minutes in total (see [Limitations](#limitations)).

The HTTP server itself limits reading headers (10 s), reading a request
(2 min, bodies are up to 64 MiB), writing the answer and idle keep-alive
connections, and shuts down gracefully on `SIGINT`/`SIGTERM`, letting
requests in flight finish.

## Limitations

- **One plugin process per Jira site.** Properties are updated with
  read-modify-write under an in-process per-issue lock; two plugin processes
  writing the same issue could lose each other's update.
- **Project keys of at most 5 characters.** GraphOps prefixes are 1-5
  letters/digits and the prefix is the Jira key.
- **Data of the earlier storage format cannot be read, and is not
  migrated.** Earlier versions of this sample kept a ticket's whole graph in
  the `graphops.graph` property of the ticket's issue, put artifact comments
  on the ticket's issue and used node IDs like `GOPS-12-03` and artifact IDs
  like `GOPS-12-c10023`. This version ignores all of that: such a ticket is
  still listed and readable as a ticket, but shows an empty graph and no
  artifacts, and the old IDs are `NOT_FOUND`. Finish or detach old tickets
  with the version that created them (detaching one with this version
  leaves its `graphops.graph` property on the issue).
- **Size limits (Jira's 32 KB per property).** Jira limits one issue
  property to 32768 bytes of JSON; a write that would go over fails with
  `VALIDATION_ERROR` and leaves the stored property as it was. There is no
  longer a limit of about 55-60 nodes caused by one property holding the
  whole graph. The limits are now:
  - **99 nodes per ticket.** What counts is the number of the ticket's
    sub-tasks that carry `graphops.node`; sub-tasks people made by hand are
    **not** counted. (To keep node creation cheap, the plugin counts exactly
    only once the ticket has 99 or more sub-tasks of any kind; below that the
    sub-task count is enough.) Counting and creating happen under the
    ticket's lock, so parallel creations cannot go over the limit. The 100th
    node fails with `VALIDATION_ERROR`.
  - **About 300 edges per ticket.** All edges of a ticket share the ticket's
    `graphops.edges` property. An edge takes about 107 bytes in the tests and
    about 122 bytes in real Jira (104 edges were 12,637 bytes: longer edge
    IDs and conditions), which puts the limit at roughly 265-300 edges.
    Real graphs have about 1.5-1.6 edges per node, so a 99-node graph needs
    about 160 edges (about 17-20 KB). Going over fails with
    `VALIDATION_ERROR`.
  - **32 KB per node.** A node's `graphops.node` holds its criteria text
    (review gates) **and the list of its artifact comment IDs**
    (`artifact_comment_ids`, about 10 bytes per artifact), which share the
    32 KB. A node without criteria is about 240 bytes, so only very long
    criteria, or thousands of artifacts on one node, reach the limit; an
    artifact that would push the node over is not created (the comment and
    attachment are removed again).
  - **About 30 KB of ticket description.** `graphops.ticket` holds the
    ticket's **Markdown description verbatim** (plus the title and other
    fields). A ticket whose description is longer than roughly 30 KB of
    UTF-8 -- less if it has many characters that JSON escapes, such as `<`,
    `>`, `&`, quotes or newlines -- can be neither created nor updated.
- **Commands are slower than with SQLite, and slower than with the earlier
  format of this sample.** Showing each node as a sub-task costs Jira writes:
  - Every node update (each node `get-executable` starts, each
    `complete-node`) sends up to six Jira requests one after the other --
    read the ticket, read the sub-task, write `graphops.node`, edit the label,
    read the transitions, make the move -- about **2 seconds** in total
    against Jira Cloud, of which the label edit and the move (about 1.1
    seconds) are the price of the Jira-side visibility.
  - Every node creation creates a Jira issue: about **1.4 seconds**.
  - Rough durations measured against Jira Cloud (whole commands):
    `expand-graph` about **1.35 seconds per node plus 0.44 seconds per
    edge** (a 68-node, 102-edge patch took 141 seconds, so a 99-node graph
    will take about 3-4 minutes); `get-executable` about 3.5 seconds plus
    **about 2 seconds per node it starts** (19-21 seconds when it starts 8-9
    nodes); `complete-node` 4-5 seconds. Reading a ticket (`get-ticket`,
    0.7 seconds for 70 nodes) and listing tickets do not grow with the
    graph. See [Verification](#verification) for the before/after figures.
  - Each single protocol request stays short (at most 2.4 seconds measured),
    well inside the plugin's 25-second deadline and graph-engine's 30-second
    per-request timeout, so nothing fails -- it is only slower. But **a large
    `expand-graph` can run longer than 2 minutes**, the default timeout of
    many agent tools (such as a shell tool that runs graph-engine). Give such
    a call a longer timeout: a command cut off halfway leaves the graph
    partly created (the nodes and edges written so far stay), so check the
    graph with `get-ticket` before running anything again.
  - Writes to the same issue are serialized by the per-issue lock, so when
    `process-ticket` runs subagents in parallel on one ticket, their writes
    to the ticket queue up.
- **Workflow moves need a matching configuration.** The two status names
  must be the names the project's workflow shows (localized on non-English
  sites, see [Running it](#running-it)), and the workflow must have
  transitions to them. Otherwise every update of a node that is not TODO
  sends one extra request and logs one line, as described above; set the
  names right, or turn moves off with empty strings. Moves are best effort in
  any case: the Jira status can lag behind or differ from the GraphOps
  status (for example after a failed move, or `Done` with a `todo` label
  after a node is sent back to TODO). The label and `graphops.node` are
  authoritative. Moving a sub-task on the Jira board does not change the
  node's GraphOps status.
- **Read-only "done" statuses.** Some workflows make finished issues
  read-only (`jira.issue.editable=false` on the status; common for "Closed"
  in company-managed projects). Sending a node back from DONE still works,
  because the sub-task is moved out of the done status before its label is
  edited. Two things can fail there: an update from DONE straight back to
  TODO (the sub-task is not moved, so the label edit is refused and the
  update fails), and the label removal when a ticket is detached (logged,
  the label stays). To avoid both, make the done status editable, or set
  `JIRA_NODE_DONE_STATUS` to the empty string so that sub-tasks are never
  moved there. This behaviour is covered by the tests with the fake Jira
  only: in the verification project `GRAP` (team-managed, simplified
  workflow) the done status is editable, so it did not occur there.
- **Partial updates.** A node update writes `graphops.node` first and then
  edits the label. If the label edit fails, the request fails and the
  sub-task keeps its old label (and workflow status) until the update is
  run again. When a node leaves the done status, the move comes first, so if
  a later write fails the sub-task has already been moved to "in progress"
  while its label and `graphops.node` still say DONE; again, running the
  update again makes them agree. If deleting a node's sub-task fails after
  its edges were removed, the node stays without edges until the delete is
  run again.
- **Detaching can leave GraphOps data on sub-tasks.** The sub-task clean-up
  after deleting a ticket or a project is best effort and stops after 10
  seconds; against Jira Cloud that was enough for **about 50 sub-tasks**
  (detaching a 70-node ticket left 16 sub-tasks uncleaned). Sub-tasks can
  also keep data when a single removal fails (for example on a read-only
  done status), or when a node update was waiting for the sub-task while the
  ticket was being detached. What stays is the `graphops.node` property and
  one `graphops-status-*` label. It is harmless -- the ticket is no longer
  managed, so these sub-tasks cannot be read or changed as nodes -- but the
  labels stay visible in Jira, and running the delete again does not help,
  because the ticket is no longer a GraphOps ticket. Tickets whose turn never
  came, and each sub-task that kept something, are logged. To remove the
  leftovers by hand, find them with JQL, for example
  `parent = GOPS-12 AND labels in (graphops-status-todo, graphops-status-in-progress, graphops-status-in-review, graphops-status-awaiting-fix, graphops-status-done, graphops-status-rejected)`,
  remove the label (for example with bulk change, "Edit issues", removing
  the labels), and optionally delete the property with
  `DELETE /rest/api/3/issue/<sub-task key>/properties/graphops.node`.
  When a project is deleted, one 10-second budget covers the sub-tasks of
  all its tickets, so with many or large tickets much can be left; the
  project is still unregistered successfully, and the tickets whose
  clean-up did not run are named in the log.
- **Orphaned artifact comments.** If creating an artifact fails after its
  comment was created, and deleting that comment fails too, the comment
  stays on the sub-task. It then appears in the node's artifact list (which
  reads the sub-task's comments) but not in the ticket's (which follows
  `artifact_comment_ids`). The failed clean-up is logged.
- **Issue keys must not change.** Node IDs contain the ticket's and the
  sub-task's issue keys. If an issue is moved to another project (or its key
  changes otherwise), its node -- like a moved ticket -- becomes
  `NOT_FOUND`.
- **Do not use `Epic` as `JIRA_ISSUE_TYPE`.** Nodes are recognized as the
  ticket's child issues that carry `graphops.node`; with epics, ordinary
  issues in the epic are children too, and one given a `graphops.node`
  property would be treated as a node.
- **Jira users can change GraphOps data (trust boundary).** Everything the
  plugin stores is ordinary Jira data, and the plugin does not check who
  wrote it (it does not compare an author with its own account):
  - anyone who can edit a ticket's issue can change `graphops.ticket` and
    `graphops.edges` (ticket status, the graph's edges);
  - anyone who can edit a node sub-task can change its `graphops.node`
    (node status, review gate criteria, the list of artifact comment IDs),
    and anyone who can comment on it can add a comment carrying a
    `graphops.artifact` property, which the plugin returns as an artifact --
    including review verdicts;
  - the `attachment_id` in a `graphops.artifact` property is used as it is:
    a person who can edit such a comment property can make the artifact's
    content be read from any attachment the plugin's Jira account can see,
    including one in a project that is not registered;
  - the IDs in `artifact_comment_ids` are sent to Jira's `comment/list`
    before they are checked, so the plugin may *load* comments from other
    issues; it never returns them (a comment must be on the node's sub-task
    and name the node), but this is the one read that is not checked first;
  - adding a `graphops.node` property to a sub-task made by hand makes it
    look like a node of that ticket, so a later node deletion (for example
    when a graph is rebuilt) can delete that sub-task. Only direct children of
    managed tickets can be affected.

  GraphOps agents read tickets and artifacts as input, so such edits are a
  **prompt-injection path**: only use Jira projects whose editors and
  commenters you trust as much as the people who run GraphOps, and restrict
  who can edit and comment accordingly.
- **The bearer token is the only protection of the plugin's API.** Anyone
  who has `GRAPHOPS_DATASOURCE_TOKEN` can do everything the Jira account can
  do through the plugin. Use a long random value (at least 32 random bytes,
  e.g. `openssl rand -hex 32`); the plugin warns at startup when the token
  is shorter than 32 characters.
- **Logs can contain your Jira site's URL.** When a request to Jira fails
  at the network level (a timeout, a refused connection), the logged error
  includes the full request URL, and with it the site's host name. The
  Jira API token, the account email and the data-source token are never
  logged. Treat the plugin's log as internal if your site name is.
- **Attachment-backed artifacts cost one request each when listed.** Text
  artifacts larger than 16 KiB are stored as attachments, and the protocol
  requires text content in ticket listings, so listing a ticket's artifacts
  (the Web UI's ticket view, which refreshes periodically) downloads each
  such attachment every time.
- **A sample, not a hardened service.** It serves plain HTTP (put a
  TLS-terminating proxy in front of it for anything but loopback), keeps its
  state in a local file and assumes a single process.
- **Search indexing delay.** Jira's search index can lag behind writes, so a
  ticket created a moment ago may be missing from a listing for a short time.
  Reading a ticket by ID is not affected, and neither are nodes, which are
  read without a search.
- **Requirements on the project and permissions.** The project needs a
  sub-task issue type (named by `JIRA_SUBTASK_ISSUE_TYPE`) and, for the
  workflow mirror, transitions to the two configured statuses. The account
  needs to browse the project, create and edit issues and sub-tasks,
  transition sub-tasks, add and delete comments and attachments, and delete
  issues (node sub-tasks on node deletion, the metadata issue on project
  deletion).

## Tests

`go test ./...` runs against an in-memory fake of the Jira REST API
(`fakejira_test.go`) that knows sub-tasks, `bulkfetch`, `comment/list`,
configurable workflows (including a read-only done status) and transitions;
it never contacts a real Jira, needs no credentials, and is what CI runs. It
covers the storage mapping above, a conformance pass over all 32 protocol
endpoints, bearer-token checks, the 32 KB property limits (per node and for
the edges), the 99-node limit (also with parallel creations), 70-node and
99-node/160-edge graphs, the status labels and the workflow mapping, every
kind of failed workflow move (none of which fails the write), the order of
writes and moves under concurrent updates, artifacts on sub-tasks and their
clean-up on failure, node deletion and ticket/project detaching (including
the clean-up running out of time), attachment round trips, retries on `429`,
the request deadline under persistent rate limiting, no Jira writes after
graph-engine disconnects, and reads, writes and deletes refusing every ID
that does not name a managed node (unregistered projects, unmanaged issues,
detached tickets, another ticket's sub-tasks, hand-made sub-tasks, tampered
`artifact_comment_ids`, old-format and malformed IDs -- the last two without
any Jira request).

## Verification

### 2026-09-19: nodes as sub-tasks (current format)

The current format was verified end to end against a real Jira Cloud site on
2026-09-19 (UTC), at commit `8b3901d`, and compared with the previous format
at commit `d74e516`, both run the same way side by side. The site URL, the
Atlassian account and every token are deliberately not recorded here.

Setup: the Jira project `GRAP` (team-managed, simplified workflow, a
Japanese site: statuses `未着手` / `進行中` / `完了`), ticket issue type
`Task`, sub-task issue type `Subtask` (the default worked);
`JIRA_NODE_IN_PROGRESS_STATUS=進行中` and `JIRA_NODE_DONE_STATUS=完了`; the
plugins on `127.0.0.1` with a random data-source token; graph-engine
(v0.5.0 build) isolated from the real GraphOps configuration.

What was checked, all with the expected result:

- **Visibility**: on ticket `GRAP-8`, the `process-ticket` command sequence
  run by hand (plan, plan review, `expand-graph`, implementation, a failed
  review gate sending implementation back, a passed gate) created the
  sub-tasks `GRAP-9` to `GRAP-13`, named like `実装 [implementation]` and
  `Code Review [review_gate]`, with node IDs such as `GRAP-8-n11`.
- **Artifacts** (text, html, image) were comments and attachments of their
  node's sub-task; the ticket's issue had none. The html and PNG bytes read
  back were identical to the originals.
- **Status labels**: at every point each sub-task had exactly one
  `graphops-status-*` label matching the node, and a label added by hand
  (`e2e-user-label`) survived every update and the detach.
- **Workflow**: TODO stayed `未着手`; IN PROGRESS / AWAITING FIX / REJECTED
  went to `進行中`; DONE to `完了`. A node sent back from DONE to TODO kept
  `完了` with the `todo` label and moved to `進行中` when it was started
  again. Updates from DONE back to IN PROGRESS and REJECTED succeeded (in
  `GRAP` the done status is editable, and so are properties there).
- **Moves that cannot be made** (`GRAP-5`, plugin left at the English
  defaults `In Progress` / `Done`): `get-executable` and `complete-node`
  succeeded, the label went from `in-progress` to `done`, the sub-task stayed
  `未着手`, and one log line per update named the node, the sub-task, the
  target and the reachable statuses.
- **Real Jira behaviour**: a new sub-task appears in its parent's `subtasks`
  at once (all 70 were there right after `expand-graph`); the simplified
  workflow allows every move between its three statuses; `comment/list`
  returns comments with `self` URLs naming the issue ID, not necessarily in
  ID order (the plugin sorts them).
- **More than 60 nodes**: ticket `GRAP-103` got a 70-node, 104-edge graph
  and all 70 nodes were run to DONE; `graphops.edges` was 12,637 bytes and a
  node's `graphops.node` about 240 bytes. With the previous format the same
  graph could not be built: `expand-graph` failed after 66.8 seconds with
  `graphops.graph` at 32,915 bytes, over the 32,768-byte limit, leaving a
  partly built graph.
- **Listing and detaching**: with 157 node sub-tasks in `GRAP`,
  `list-tickets` listed only tickets. Deleting `GRAP-8` from the Web UI
  removed the label and properties from the ticket's issue; its five
  sub-tasks stayed, with their comments, attachments, the hand-added label
  and their workflow status, and lost `graphops.node` and the status label;
  the node and artifact IDs then answered `NOT_FOUND` and the ticket's
  artifact list was empty. Deleting the 70-node `GRAP-103` succeeded in 11.6
  seconds, but the 10-second clean-up covered 54 sub-tasks and left 16 with
  their `graphops.node` and label (still unreadable as nodes).

Timing -- **capacity improved, speed got worse.** The new format removes
the node limit of about 55-60 but every command that writes nodes is slower,
because showing nodes as sub-tasks costs Jira writes (see Limitations for
the reasons). Whole commands, including graph-engine's startup handshake,
on a ticket with about 20 nodes (2 seeded plus an 18-node, 30-edge patch),
the same steps run alternately with both versions:

| Command | Previous format (`d74e516`) | Current format (`8b3901d`) |
|---|---|---|
| `expand-graph` (18 nodes, 30 edges) | 24.5-26.1 s | 39.5-42.0 s (about 1.6 times) |
| `get-executable`, first call (creates 2 nodes, starts 1) | 5.3-5.8 s | 8.5-9.0 s |
| `get-executable`, starting 1 node | 3.0-3.9 s | 3.6-5.9 s |
| `get-executable`, starting 4 nodes | 4.9-5.0 s | 11.0-11.6 s |
| `get-executable`, starting none | 3.0-3.3 s | 3.3-3.6 s |
| `complete-node` | 2.2-2.8 s | 4.1-4.8 s (about 1.8 times) |
| `create-ticket` | 1.4-1.8 s | 1.6-1.7 s |

Per protocol request (median), node update went from 0.44 to 1.98 seconds
(six Jira requests instead of reading and writing one property: the label
edit, 0.62 s, and the workflow move, 0.49 s, are the largest) and node
creation from 0.49 to 1.40 seconds (a Jira issue is created); edge creation
(0.44 s), ticket detail (0.65 s) and ticket updates did not change. Turning
the workflow moves off should save about 0.7 seconds per node update (not
measured).

On the 70-node ticket (current format only): `expand-graph` of 68 nodes and
102 edges took 141.1 seconds; `get-executable` starting 8-9 nodes 18.9-21.4
seconds, starting none 3.6-3.7 seconds; `complete-node` 4.1-4.8 seconds;
`get-ticket` 0.73 seconds; `list-tickets` 0.44 seconds. The slowest single
protocol request took 2.43 seconds, and Jira never answered `429` or `503`.

No commit was made during verification, and a search of the working tree, of
the history and of the GraphOps configuration found none of the site host
name, the account email, the Jira API token or the data-source token. (The
plugin's own log did contain the site URL, see Limitations.)

### 2026-09-18: first verification (previous storage format)

The plugin was first verified end to end against the same Jira Cloud site on
2026-09-18 (UTC), at commit `6fab8ba`, when nodes and edges were still kept
in the ticket's `graphops.graph` property and artifacts were comments on the
ticket's issue. The parts below that do not depend on how nodes are stored
still describe the current version.

Setup: a Jira project with the key `GRAP` and issue type `Task`; the plugin on
`127.0.0.1` with a 64-character random `GRAPHOPS_DATASOURCE_TOKEN`; the Jira
credentials passed to the plugin process only, through environment variables;
graph-engine configured outside the repository with `"dbBackend": "http"` and
`"httpDataSourceToken": "${GRAPHOPS_DATASOURCE_TOKEN}"`.

What was checked, all with the expected result:

- **Projects**: `create-project --prefix GRAP` registered `jira-GRAP` and
  created the metadata issue `GRAP-1`; `use-project` and `list-projects`;
  label create, rename, recolor and delete through the Web UI API.
- **Tickets**: create with priority and labels (labels resolved
  case-insensitively; Markdown with `<`, `&` and quotes round-tripped
  unchanged); an unknown label failed with `LABEL_NOT_FOUND` and created no
  issue; `refine-ticket` updated status, priority, labels and description;
  `list-tickets` and `get-ticket` matched.
- **Execution graph**: the `process-ticket` command sequence run by hand
  (`get-executable` seeding, plan and plan review, `expand-graph --patch` to 9
  nodes and 12 edges, a failed review gate looping back to implementation,
  approval gates, release) until the ticket was `DONE`. Artifacts of every
  kind were stored and read back: text, json, a 40 KB text (stored as an
  attachment), an 84 KB html report and a PNG image. The html and image
  bytes read back through the Web UI API were identical to the originals.
- **Web UI**: `graph-engine serve` listed the Jira-backed tickets; ticket
  detail and artifact content were checked through its API.
- **Detach**: deleting ticket `GRAP-3` from the Web UI removed it from
  GraphOps and removed its label and properties in Jira; the issue itself
  stayed.
- **Limits**: a 34,000-byte description failed with a clear "over Jira's
  32768-byte issue property limit" error and left the stored ticket as it
  was.
- **Failures and restarts**: a wrong bearer token stopped `list-tickets` and
  `serve` at startup without printing either token; a request without a
  token got 401; a stopped plugin gave "connection refused"; `SIGTERM` shut
  the plugin down cleanly and all data was there after a restart; with the
  state file removed, registering `GRAP` again reused the existing metadata
  issue and its labels.

Observations: `graphops.ticket` was 243-669 bytes for ordinary
descriptions; a ticket listed about 0.1 seconds after its creation was
missing from the search and present about a second later; commands took
0.4-0.6 s (listing tickets), 0.6-1.4 s (`get-ticket`), 1.7-2.3 s
(`create-ticket`), 1.2-3 s (`add-artifact`), 2.3-4.4 s (`complete-node`),
3-6 s (`get-executable`) and about 11 s (`expand-graph` to 9 nodes); image
artifacts are stored as attachments named `graphops-artifact-<id>.bin`, so
Jira does not preview them as images (GraphOps reads the original bytes
back); registering a project again replaces its name with the new one.

Not covered against real Jira in either run (covered by the tests above
instead): `DeleteProject` (it would have removed the verification data),
`DeleteNode` (not reachable from the CLI or the Web UI), a workflow whose
done status is read-only, and the `process-ticket` skill itself with
parallel subagents -- its CLI command sequence was run instead.
