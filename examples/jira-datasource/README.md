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
| `JIRA_ISSUE_TYPE` | no | Issue type used for ticket (and metadata) issues, default `Task` |
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

Then point graph-engine at it. Keep `graph-config.json` free of the token
itself by referencing the environment variable:

```json
{
  "dbBackend": "http",
  "httpDataSourceUrl": "http://127.0.0.1:8787",
  "httpDataSourceToken": "${GRAPHOPS_DATASOURCE_TOKEN}"
}
```

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
| Ticket | A Jira issue with the Jira label `graphops`. Every GraphOps field (title, Markdown description, status, priority, assignee, blocked, label IDs, timestamps, ...) lives in the `graphops.ticket` issue property. The issue summary and description mirror the title and description (converted to ADF paragraphs) for people reading Jira. | the issue key, e.g. `GOPS-12` |
| Nodes and edges | The `graphops.graph` issue property of the ticket's issue (`node_seq`, `nodes`, `edges`). | nodes `<issue key>-NN` (e.g. `GOPS-12-03`); edges keep the ID graph-engine proposes |
| Artifact | A comment on the ticket's issue: a readable summary (name, type, node, and the content in a code block) plus the `graphops.artifact` comment property with the metadata. text/gherkin/json content up to 16 KiB is kept inline in that property; larger content and every html/image artifact is uploaded as an issue attachment that the property points to (images are stored as their decoded bytes). | `<issue key>-c<comment id>`, e.g. `GOPS-12-c10023` |
| Label | An entry in the project's `graphops.project` property; which tickets carry it is in each ticket's `graphops.ticket.label_ids`. | `<KEY>-label-<n>`, e.g. `GOPS-label-3` |

Every ID can be traced back to where it is stored without a search: a node ID
drops its last `-NN` to give the issue key, an artifact ID splits at `-c` into
the issue key and the comment ID, and a label ID's part before `-label-` is
the project key, which leads to the metadata issue through the state file.

Why the state file: the list of registered projects and the current project
are single values for the whole data source, not per Jira project, and Jira
has no place for such a value that an ordinary (non-admin) account can write
(project and app properties need admin permissions). Losing the file is not
fatal: registering the same project key again finds its existing metadata
issue and reuses it, labels included.

### What each operation does in Jira

- **Create project**: checks the Jira project exists and is accessible
  (`VALIDATION_ERROR` otherwise), then creates (or reuses) its metadata
  issue. The prefix must be 1-5 letters/digits (`INVALID_PREFIX` otherwise),
  and there is no automatic prefix generation: the prefix is the key.
- **Create ticket**: creates an issue of type `JIRA_ISSUE_TYPE` with the
  label `graphops` and both properties in one request. No workflow
  transition is ever made.
- **Update ticket**: rewrites `graphops.ticket`; if the title or description
  changed, also updates the issue summary/description. GraphOps statuses are
  not synchronized with the Jira workflow.
- **Delete ticket**: *detaches* the issue rather than deleting it: removes
  the `graphops` label and the `graphops.ticket`/`graphops.graph` properties.
  The issue and its artifact comments stay in Jira.
- **Delete node**: removes the node and its edges from `graphops.graph`, and
  deletes the node's artifact comments and attachments.
- **Delete project**: detaches every ticket of the project, deletes the
  metadata issue, and unregisters the project (clearing the current project
  if it pointed there). The Jira project is untouched.
- **List tickets**: one JQL search, `project = GOPS AND labels = graphops AND
  labels != graphops-meta ORDER BY created DESC`, through the
  `/rest/api/3/search/jql` API (paged with `nextPageToken`), requesting the
  `graphops.ticket` property in the same call, so listing is not one request
  per issue. A registered project whose metadata issue was deleted in Jira is
  left out of both the project list and the ticket list (instead of making
  the whole listing fail). Its label definitions were in that issue, so do
  not delete metadata issues.

- **Get artifact / list a node's artifacts**: first checks that the issue is
  a managed ticket of a registered project (label `graphops` and the
  `graphops.ticket` property), exactly like every other read. An artifact ID
  that points at any other issue -- one in a Jira project that is not
  registered, one never created through GraphOps, or a deleted (detached)
  ticket -- is `ARTIFACT_NOT_FOUND` (an empty list for the node listing), and
  neither its comments nor its attachments are read.
- **Create artifact**: if the comment cannot be created after the content
  was uploaded as an attachment, the attachment is deleted again, so a
  failed call leaves nothing behind.

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
- **32 KB per property.** Jira limits an issue property to 32768 bytes of
  JSON, and the write then fails with `VALIDATION_ERROR`, leaving the stored
  property as it was. Two properties can reach it:
  - `graphops.ticket` holds the ticket's **Markdown description verbatim**
    (plus the title and other fields). A ticket whose description is longer
    than roughly 30 KB of UTF-8 -- less if it has many characters that JSON
    escapes, such as `<`, `>`, `&`, quotes or newlines -- can be neither
    created nor updated.
  - `graphops.graph` holds a ticket's whole graph, including each review
    gate's criteria text, so a very large graph can hit the limit. Measured
    against real Jira, it takes about 500 bytes per node (edges and criteria
    included): a 9-node, 12-edge graph was 4,753 bytes. That puts the limit at
    roughly **55-60 nodes per ticket**, fewer when many review gates carry
    long criteria. A typical `process-ticket` graph of about 20 nodes needs
    around 11-13 KB.
- **Commands are slower than with SQLite.** Nodes and edges share one
  issue property, so every graph change reads, modifies and writes
  `graphops.graph` in Jira, and a command that changes the graph several
  times pays for each round trip. Measured against Jira Cloud,
  `get-executable` took 3-6 seconds and `expand-graph` about 11 seconds
  (most other commands 0.5-3 seconds). Each single protocol request stayed
  under 5 seconds, well inside the 25-second deadline and graph-engine's
  30-second timeout, so nothing fails -- it is only slower. Writes to the
  same issue are serialized by the per-issue lock, so when `process-ticket`
  runs subagents in parallel on one ticket, their writes queue up and the run
  feels slower still.
- **Jira users can change GraphOps data (trust boundary).** Everything the
  plugin stores is ordinary Jira data: anyone who can edit an issue can
  change its `graphops.ticket` / `graphops.graph` properties (ticket status,
  the execution graph, review gate criteria), and anyone who can comment on
  it can add a comment carrying a `graphops.artifact` property, which the
  plugin returns as an artifact -- including review verdicts. The plugin
  does not check who wrote a property or comment (it does not compare a
  comment's author with its own account). GraphOps agents read tickets and
  artifacts as input, so such edits are a **prompt-injection path**: only use
  Jira projects whose editors and commenters you trust as much as the people
  who run GraphOps, and restrict who can edit and comment accordingly.
- **The bearer token is the only protection of the plugin's API.** Anyone
  who has `GRAPHOPS_DATASOURCE_TOKEN` can do everything the Jira account can
  do through the plugin. Use a long random value (at least 32 random bytes,
  e.g. `openssl rand -hex 32`); the plugin warns at startup when the token
  is shorter than 32 characters.
- **Attachment-backed artifacts cost one request each when listed.** Text
  artifacts larger than 16 KiB are stored as attachments, and the protocol
  requires text content in ticket listings, so listing a ticket's artifacts
  (the Web UI's ticket view, which refreshes periodically) downloads each
  such attachment every time.
- **A sample, not a hardened service.** It serves plain HTTP (put a
  TLS-terminating proxy in front of it for anything but loopback), keeps its
  state in a local file and assumes a single process.
- **No workflow synchronization.** GraphOps statuses live only in
  `graphops.ticket`; moving an issue across the Jira board does not change
  its GraphOps status, and vice versa.
- **Search indexing delay.** Jira's search index can lag behind writes, so a
  ticket created a moment ago may be missing from a listing for a short time.
  Reading a ticket by ID is not affected.
- **Permissions.** The account needs to browse the project, create and edit
  issues, add and delete comments and attachments, and delete issues (for
  the metadata issue on project deletion).

## Tests

`go test ./...` runs against an in-memory fake of the Jira REST API
(`fakejira_test.go`); it never contacts a real Jira, needs no credentials, and
is what CI runs. It covers the storage mapping above, a conformance pass over
all 32 protocol endpoints, bearer-token checks, the 32 KB property limit,
concurrent node creation, attachment round trips, retries on `429`, the
request deadline under persistent rate limiting, no Jira writes after
graph-engine disconnects, and artifact reads refusing issues that are not
managed tickets of a registered project.

## Verification

The plugin was verified end to end against a real Jira Cloud site on
2026-09-18 (UTC), at commit `6fab8ba`. The site URL, the Atlassian account
and every token are deliberately not recorded here.

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
- **Jira side** (read-only REST calls): the `graphops` label and properties
  on ticket issues, one comment with a `graphops.artifact` property per
  artifact, the attachments, and no workflow transition.
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

Observations:

- Property sizes: `graphops.graph` was 4,753 bytes for 9 nodes and 12 edges
  (about 500 bytes per node, see Limitations); `graphops.ticket` was 243-669
  bytes for ordinary descriptions.
- Search lag: a ticket listed about 0.1 seconds after its creation was
  missing, and present about a second later. Reading it by key was not
  affected.
- Timing (whole commands, including graph-engine's startup handshake):
  listing tickets 0.4-0.6 s, `get-ticket` 0.6-1.4 s, `create-ticket`
  1.7-2.3 s, `add-artifact` 1.2-3 s, `complete-node` 2.3-4.4 s,
  `get-executable` 3-6 s, `expand-graph` about 11 s. No Jira call needed a
  retry and no single request took over 5 seconds.
- Image artifacts are stored as attachments named
  `graphops-artifact-<id>.bin`, so Jira does not preview them as images;
  GraphOps reads the original bytes back.
- Registering a project again replaces its name with the new one.

Not covered against real Jira (covered by the tests above instead):
`DeleteProject` (it would have removed the verification data), `DeleteNode`
(not reachable from the CLI or the Web UI), a `graphops.graph` over 32 KB, and
the `process-ticket` skill itself with parallel subagents -- its CLI command
sequence was run instead.

No commit was made during verification, and a search of the working tree and
of the full history found none of the site host name, the account email, the
Jira API token or the data-source tokens.
