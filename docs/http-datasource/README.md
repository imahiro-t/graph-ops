# Writing an HTTP custom data source

This is the developer manual for people who want GraphOps to keep its data in
a system of their own choosing -- an issue tracker, a document database, an
internal service -- instead of the built-in SQLite or MySQL backends.

- **Protocol specification:** [`openapi.yaml`](openapi.yaml) (OpenAPI 3.1,
  protocol version `1.0`). It is the normative contract; this manual explains
  it and how to work with it.
- **Sample plugin:** [`examples/jira-datasource`](../../examples/jira-datasource/README.md),
  a complete plugin that stores everything in Jira Cloud.

## Contents

1. [What the HTTP data source is](#what-the-http-data-source-is)
2. [Configuring graph-engine](#configuring-graph-engine)
3. [Security model](#security-model)
4. [The protocol](#the-protocol)
5. [Developing and verifying a plugin locally](#developing-and-verifying-a-plugin-locally)
6. [Walkthrough: the Jira sample](#walkthrough-the-jira-sample)

## What the HTTP data source is

graph-engine -- the GraphOps CLI that agents call, and the Web UI server
(`graph-engine serve`) -- stores tickets, execution graphs (nodes and edges),
artifacts, projects, labels and the current project through one storage
interface with 32 operations. With `dbBackend: "http"`, every one of those
operations becomes one HTTP request to a server you provide, the **plugin**.
Nothing else changes: the engine, the CLI commands, the `process-ticket`
skill and the Web UI behave exactly as they do with SQLite.

```
 Claude Code agents ──> graph-engine CLI ─┐
                                          ├──> HTTP (JSON) ──> your plugin ──> your storage
 Browser ──> graph-engine serve (Web UI) ─┘
```

Use it when:

- your team already tracks work in another system (Jira, a database, an
  internal service) and does not want to keep a second copy of the data in
  SQLite/MySQL, or
- you need a storage backend GraphOps does not ship, and would rather run it
  as a separate program than change GraphOps itself.

Stay with SQLite (the default) or MySQL when you only need a local or shared
database: they need no extra process, and they are faster (see
[the Jira sample's measurements](#what-the-real-jira-verification-showed)).

The plugin is an independent program in any language. The only contract
between it and GraphOps is the published protocol; a plugin does not import
or link anything from GraphOps.

## Configuring graph-engine

### The home config file and environment variables

graph-engine reads its settings from one file, `$HOME/.graph-ops/config.json`,
and from environment variables -- nothing is read out of the directory it
starts in (see the root README). Three fields select and configure the HTTP
data source:

| Field | Environment variable (takes precedence) | Meaning |
|---|---|---|
| `dbBackend` | `GRAPH_DB_BACKEND` | `"http"` selects the HTTP data source (`"sqlite"` and `"mysql"` are the others) |
| `httpDataSourceUrl` | `GRAPH_HTTP_DATASOURCE_URL` | The plugin's base URL; every protocol path is appended to it |
| `httpDataSourceToken` | `GRAPH_HTTP_DATASOURCE_TOKEN` | The bearer token sent to the plugin. Plain value or a `${ENV_VAR}` reference |

```json
{
  "dbBackend": "http",
  "httpDataSourceUrl": "https://graphops-store.example.com",
  "httpDataSourceToken": "${GRAPHOPS_DATASOURCE_TOKEN}"
}
```

- The HTTP fields are read and validated only when `dbBackend` is `"http"`.
  With `"sqlite"` or `"mysql"`, leftover `httpDataSource*` values are ignored.
- The settings are validated at startup, before any request is sent. An
  invalid combination (see [Security model](#security-model)) stops every
  command -- `list-tickets`, `serve`, and the rest -- with an error that
  names the offending field.
- At startup graph-engine calls `GET /protocol` (the version handshake) and
  then `POST /init`. Each CLI command is a new process, so every command
  performs these two requests before its real work.
- The request timeout is fixed at 30 seconds and is not configurable (see
  [Timeouts, retries and uncertain writes](#timeouts-retries-and-uncertain-writes)).

### Keeping the token out of files: `${ENV_VAR}` references

`httpDataSourceToken` accepts the same secret-reference syntax as the MySQL
password: a value that is exactly `${NAME}` is replaced, at startup, by the
value of the environment variable `NAME`. The file then contains only the
variable's name. If the variable is not set, startup stops with an error
naming it.

The variable must be set in the environment of the graph-engine process that
uses it -- your shell for CLI commands, and whatever started the Web UI server
for `serve` (for example the Claude Code session that ran `/graph-ops:ui`).

A plain value in the file also works, but it is stored in plaintext; prefer a
reference.

### Configuring it from the Web UI

Settings (gear button) > **App Settings** > data storage
offers **HTTP custom data source**, with an **Endpoint URL** field and a
**Bearer token** field. Storage changes take effect the next time the server
starts.

- The token is never sent back to the browser. A saved plaintext token is
  shown as "saved"; leave the field empty to keep it. A saved `${ENV_VAR}`
  reference is shown as a reference (the variable's name, never its value).
- **Token re-entry rule.** When you change the endpoint URL, the saved token
  (or saved `${ENV_VAR}` reference) can no longer be reused: you must type the
  token again, or the save is rejected with
  `HTTP_DATASOURCE_TOKEN_RETYPE_REQUIRED`. This stops a changed URL from
  silently receiving the token that was meant for the old one -- the same
  protection the MySQL password has. URLs are compared after normalization
  (scheme and host case, an explicit default port such as `:443`, trailing
  slashes), so `https://Store.example.com/` and
  `https://store.example.com:443` count as the same URL.
- The form checks the URL as you type (required, `http(s)://` only, no user
  information / query / fragment, plaintext only for loopback, token required
  for remote URLs). The server repeats the checks and its answer is final.
  Write loopback addresses as `localhost`, `127.0.0.1` or `[::1]`: unusual
  spellings such as `http://127.1` or `http://[::ffff:127.0.0.1]` can be judged
  differently by the browser's URL parser and the server, so the form may
  show no error and the save still fail (or the other way round).

**Token supplied only through `GRAPH_HTTP_DATASOURCE_TOKEN`.** The Web UI
validates what will be written to `$HOME/.graph-ops/config.json`, and it does
not look at `GRAPH_HTTP_DATASOURCE_TOKEN`. Saving a remote URL with an empty
token field is therefore rejected ("a remote data source requires a bearer
token"), even if that variable is set. Enter a `${ENV_VAR}` reference in the
token field instead -- for example `${GRAPH_HTTP_DATASOURCE_TOKEN}` or
`${GRAPHOPS_DATASOURCE_TOKEN}` -- which keeps the secret out of the file and
is saved without problems.

### Operator warning: what the re-entry rule does not cover

The re-entry rule only catches the *stored* token being echoed back for a new
URL. It cannot tell whether a *newly typed* value is appropriate for the new
URL, and some configurations are outside it altogether:

- Someone who can use the settings screen can change the URL **and** type a
  different reference such as `${SOME_OTHER_VARIABLE}`. That is accepted as a
  fresh entry, and from the next start graph-engine sends that variable's
  value to the new URL.
- When the token comes from the `GRAPH_HTTP_DATASOURCE_TOKEN` environment
  variable while the URL is in the file (or comes from
  `GRAPH_HTTP_DATASOURCE_URL`), changing the URL requires no re-entry at all:
  the environment-supplied token goes to whatever URL is configured.

This matters more than for MySQL: MySQL authentication does not send the
password itself, but a **bearer token is sent as-is** on every request, so
whoever controls the URL receives a usable credential. Therefore:

- Treat anyone who can change graph-engine's configuration (the file, the
  environment, or the Web UI settings screen, which listens on loopback only
  by default) as able to obtain the token.
- Only put secrets that are meant for the data source into the environment of
  graph-engine processes, and give the data source a dedicated token (not a
  personal credential that also works elsewhere).
- After changing the URL, check it before restarting, and rotate the token if
  it may have been sent somewhere unintended.

## Security model

graph-engine enforces these rules itself; a plugin cannot relax them.

| Rule | Detail |
|---|---|
| Plaintext only on loopback | `http://` is accepted only when the host is loopback: `localhost`, `127.0.0.0/8` or `::1`. Any other `http://` URL stops startup with an error. `localhost` is accepted **by name**; graph-engine assumes it resolves to a loopback address, as it does by default. Do not point `localhost` elsewhere in your hosts file. |
| HTTPS and a token for anything remote | A non-loopback URL must be `https://` and must have a token; a remote URL without a token stops startup with an error. On loopback the token is optional but recommended. |
| Bearer authentication | The token is sent as `Authorization: Bearer <token>` on every request, including `GET /protocol`. A 401 or 403 answer to the handshake stops startup with "the HTTP data source rejected the bearer token". |
| Certificates are verified | TLS 1.2 or newer, full certificate verification. There is no option to skip verification. |
| No fallback, no redirects | A failed TLS connection is an error; graph-engine never retries over plaintext. Redirects are never followed (a 3xx answer is an error), so the token cannot be forwarded to another host. |
| URL shape | The URL must not contain user information (`https://user:pass@...`), a query string or a fragment. Credentials go in the token. |
| Secrets stay out of output | Error messages and body excerpts coming back from the plugin have the token replaced with `[redacted]` (tokens shorter than 4 characters are not redacted, so use a real, long token). The Web UI settings API never returns the token. |

What this means for a plugin:

- **Verify the token** on every request, with a constant-time comparison, and
  answer 401 before doing any work when it is missing or wrong. Use a long
  random value (for example `openssl rand -hex 32`).
- **Serve TLS for anything that is not loopback.** A plugin may itself speak
  plain HTTP on `127.0.0.1` and sit behind a TLS-terminating reverse proxy
  for remote access, as the Jira sample does.
- **Do not answer with redirects**, and never put secrets (the bearer token,
  credentials for your backing system) in error messages.
- **Treat the token as full access.** Whoever has it can do everything the
  plugin's own credentials allow in your backing system.

## The protocol

[`openapi.yaml`](openapi.yaml) specifies every endpoint, request and response
body, and error. This section summarizes the rules that are easy to miss.

### Endpoints

Each operation is one request; the `operationId` is the storage method's name
in lowerCamelCase.

| Area | Requests |
|---|---|
| Handshake | `GET /protocol` (`protocol`), `POST /init` (`init`, must be idempotent) |
| Tickets | `POST /projects/{projectId}/tickets`, `GET /projects/{projectId}/tickets`, `GET /tickets`, `GET/PATCH/DELETE /tickets/{ticketId}`, `GET /tickets/{ticketId}/detail` |
| Nodes | `POST/GET /tickets/{ticketId}/nodes`, `GET/PATCH/DELETE /nodes/{nodeId}` |
| Edges | `POST/GET/DELETE /tickets/{ticketId}/edges` |
| Artifacts | `POST/GET /tickets/{ticketId}/artifacts`, `GET /artifacts/{artifactId}`, `GET /nodes/{nodeId}/artifacts` |
| Projects | `POST/GET /projects`, `GET/PATCH/DELETE /projects/{projectId}` |
| Labels | `POST/GET /projects/{projectId}/labels`, `GET/PATCH/DELETE /labels/{labelId}` |
| Current project | `GET/PUT /current-project` (**deprecated**) |

`/current-project` is deprecated: the current project is a per-user choice, so
graph-engine now keeps it in each user's home config file
(`$HOME/.graph-ops/config.json`) rather than in the data source. Keep implementing `GET` -- it is not a one-off migration call
that can be removed once an upgrade is done. graph-engine calls `GET` only
while that home config file has no `currentProjectId` of its
own: once on an environment upgrading with a selection to inherit (a non-empty
answer is written there, which carries that selection over, and `GET` is not
called again in that environment), and on each read until a project is
selected on one that has none (an answer of `""` is not written, so the next
read calls `GET` again -- the Web UI reads the current project on every page
load). For as long as it is called, an error from `GET` is an error from
graph-engine's own current-project read, which makes graph-engine answer
`500`, so answer it, with `""` when nothing is stored. `PUT` is no longer
called at all. Nothing else changes for a plugin: `GET /tickets`
still returns every ticket, and narrowing by project is still
`GET /projects/{projectId}/tickets`.

Path parameters are percent-encoded by graph-engine (an ID may contain `/` or
spaces); decode them before use.

### Handshake and versioning

`info.version` in the spec is the protocol version, `MAJOR.MINOR` (currently
`1.0`). `GET /protocol` must answer:

```json
{ "protocol": "graph-ops-datasource", "version": "1.0" }
```

graph-engine stops at startup with a clear error if `protocol` is anything
else, if the endpoint is missing or does not return this JSON, or if the MAJOR
version differs from its own (for example "incompatible data source protocol:
server speaks 2.0, graph-engine requires 1.x"). A different MINOR is accepted,
because minor versions only add optional things. graph-engine sends its own
version on every request in the `GraphOps-Protocol-Version` header.

### Errors

A non-2xx answer should carry
`{"error":{"code":"TICKET_NOT_FOUND","message":"..."}}`. graph-engine reads it
in this order:

1. **401 or 403** always means "token rejected", whatever the body says.
2. **A known `code`** (the `ErrorCode` enum: `TICKET_NOT_FOUND`,
   `NODE_NOT_FOUND`, `ARTIFACT_NOT_FOUND`, `PROJECT_NOT_FOUND`,
   `LABEL_NOT_FOUND`, `LABEL_NAME_TAKEN`, `INVALID_LABEL_NAME`,
   `INVALID_LABEL_COLOR`, `INVALID_PREFIX`, `PREFIX_TAKEN`,
   `VALIDATION_ERROR`, `INTERNAL_ERROR`) becomes the same domain error the
   built-in backends raise, with your message -- **regardless of the HTTP
   status**. Still use the status the code suggests (404 for `*_NOT_FOUND`,
   409 for `*_TAKEN`, 400 for validation codes, 500 for `INTERNAL_ERROR`).
3. **Anything else** (an unknown code, a non-JSON body, an empty body) becomes
   a generic error naming the method, path and status.

The Get operations (`getTicket`, `getTicketDetail`, `getNode`, `getArtifact`,
`getProject`, `getLabel`) answer a missing entity with 404 and the matching
`*_NOT_FOUND` code; graph-engine treats that as "does not exist", not as a
failure. Deleting a missing ticket, node or project succeeds as a no-op;
`deleteLabel` on a missing label is `LABEL_NOT_FOUND`.

### Partial updates: absent, `null`, value

In every PATCH body an absent key means "leave unchanged". Two fields have a
third state, so read the raw JSON, not just a decoded struct with defaults:

| Field | Absent key | `null` | Value |
|---|---|---|---|
| `assignee` (tickets, nodes) | unchanged | clear it | set it |
| `label_ids` (tickets) | unchanged | - | replace the ticket's labels with exactly this set (`[]` removes all) |

Every ID in `label_ids` must be a label of the ticket's own project; otherwise
the **whole** patch fails with `LABEL_NOT_FOUND` and no other field of it is
written.

### Identifiers

IDs are strings the plugin chooses; graph-engine never parses them. The
recommended formats are the built-in ones (tickets `<prefix>-<seq:05d>`, nodes
`<ticket id>-<seq:02d>`, at most 99 nodes per ticket). For edges and artifacts
graph-engine proposes an `id` in the request body; a plugin may keep it or
replace it, and graph-engine uses whatever the response returns. For
`createProject`, deriving an empty prefix from the name is recommended, but a
plugin may impose its own rule (the Jira sample requires the prefix to be an
existing Jira project key) and answer `VALIDATION_ERROR` or `INVALID_PREFIX`.

### Atomicity

There are no cross-request transactions, so **each request must be atomic**:
either all of its effects happen or none does. This covers minting an ID and
storing the row, a ticket PATCH with `label_ids`, `DELETE /projects/{id}`
(cascades to the project's tickets, their nodes, edges and artifacts, and its
labels, and clears the current project if it pointed there), and
`DELETE /labels/{id}` (detaches the label from every ticket and returns
`removed_from_tickets`). Concurrent requests do arrive -- `process-ticket` runs
subagents in parallel -- so serialize read-modify-write updates of shared
records.

One thing graph-engine cannot make atomic over this protocol is **claiming a
node** -- the step where `get-executable` takes ownership of a runnable node by
moving it to `IN PROGRESS`/`IN REVIEW`. Against SQLite and MySQL that is a
single conditional statement (`UPDATE nodes SET status=... WHERE id=... AND
status NOT IN ('DONE','IN PROGRESS','IN REVIEW')`), so of two callers racing
for the same node exactly one wins and the other is simply not offered it.
The protocol has no conditional update, so against an HTTP data source
graph-engine claims with `GET /nodes/{id}` followed by `PATCH /nodes/{id}`
instead, and a claim that lands between those two requests is invisible to it:
**the same node can be handed out twice**. A plugin cannot close this gap on
its own -- the PATCH it receives carries no expected-current-status to check
against -- so treat it as a property of this backend. In practice one
`process-ticket` session issues its `get-executable` calls one at a time and
waits for the subagents it launched, so the exposure is two sessions (or two
people) driving the same ticket at once; avoid that, and the duplicate work is
avoided with it. If it does happen, the second agent's `complete-node` is
refused with `INVALID_NODE_STATE` and writes nothing, so the record stays
correct even though the work was done twice.

### Artifact content in listings

`GET /tickets/{ticketId}/artifacts` is polled by the Web UI, so a plugin may
(and should) omit `content` for `html` and `image` artifacts there.
`has_content` must still be correct for every artifact, and `content` of
`text`, `gherkin` and `json` artifacts must be included. `GET /artifacts/{id}`
and `GET /nodes/{nodeId}/artifacts` always include content. Image content is a
base64 string. graph-engine reads at most 64 MiB of any response body.

### Timeouts, retries and uncertain writes

- graph-engine gives up on a request after **30 seconds** (connecting,
  sending and reading the answer together). The value is fixed.
- graph-engine **never retries** automatically -- not on a timeout, a
  connection error or a 5xx. The error goes to the user or agent that ran the
  command.
- A plugin must answer within 30 seconds and should keep a margin (the Jira
  sample uses a 25-second deadline per request). Bound your own retries and
  waits for slow or rate-limited services by that budget.
- After a timeout or disconnect, the outcome of a write is **uncertain**: the
  plugin may still have applied it. Do not blindly repeat a create
  (`createTicket`, `createNode`, `createEdge`, `createArtifact`,
  `createProject`, `createLabel`); read back first whether it took effect.
  Plugins should stop working on a request when the client disconnects (for
  example by passing the request's context to every downstream call), so that
  writes that have not started yet never happen.

## Developing and verifying a plugin locally

### 1. Start from the spec and a reference

- Read [`openapi.yaml`](openapi.yaml). Generating server stubs from it is
  fine; the schemas match the JSON graph-engine sends and expects.
- For the exact expected behavior of each operation, read the in-memory
  reference plugin
  [`packages/core-go/internal/store/httpdatasourcetest/plugin.go`](../../packages/core-go/internal/store/httpdatasourcetest/plugin.go).
  It implements the whole protocol following the SQLite backend's semantics
  (ID minting, label validation, cascading deletes, content omission in
  listings), and graph-engine's own tests run against it -- including a test
  that drives the engine end to end through it and checks that the results
  are identical to SQLite's. It is an internal Go package, so it cannot be
  imported from outside `packages/core-go`; use it as a readable, tested
  reference rather than a library.
- The [Jira sample](#walkthrough-the-jira-sample) shows a real plugin
  structure: configuration from environment variables, authentication,
  per-request deadlines, and a fake of the backing system for tests.

### 2. Run your plugin on loopback

Listen on `127.0.0.1` (plain HTTP is allowed there) and give it a token even
locally, so the authentication path is exercised:

```sh
export GRAPHOPS_DATASOURCE_TOKEN="$(openssl rand -hex 32)"
# start your plugin so that it listens on 127.0.0.1:8787 and expects that token

curl -s -H "Authorization: Bearer $GRAPHOPS_DATASOURCE_TOKEN" http://127.0.0.1:8787/protocol
# {"protocol":"graph-ops-datasource","version":"1.0"}
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8787/protocol
# 401
```

### 3. Point graph-engine at it -- without touching your real data

Do this with environment variables, not with a config file. graph-engine reads
settings from `$HOME/.graph-ops/config.json` alone, whichever directory you run
it in, so writing a `graph-config.json` into a scratch directory and working
there does **not** isolate anything: the file is never read, and every command
you run would quietly go to your real database instead. Environment variables
are the one channel that is deliberate -- they are set by whoever starts the
process, for that process, and they take precedence over the file -- which is
exactly what a verification run needs:

```sh
export GRAPH_DB_BACKEND=http
export GRAPH_HTTP_DATASOURCE_URL=http://127.0.0.1:8787
export GRAPH_HTTP_DATASOURCE_TOKEN="$GRAPHOPS_DATASOURCE_TOKEN"
```

Set them in one shell and use only that shell for the steps below; a shell
without them keeps reading your normal configuration, so your real tickets are
never touched. Your `$HOME/.graph-ops/config.json` stays exactly as it is --
there is nothing to edit and nothing to put back afterwards. To check which
settings a command will actually use, unset the variables again and compare
(`graph-engine list-projects` against your real data, with them set against the
plugin).

Use the `graph-engine` installed with the plugin, or build one from this
repository (`cd packages/core-go && go build -o graph-engine ./cmd/graph-engine`).
Then exercise the operations the way GraphOps uses them:

```sh
graph-engine list-projects                      # handshake + init + listProjects
graph-engine create-project "Plugin test" --prefix DEV
graph-engine use-project <project id printed above>
graph-engine create-ticket "First ticket" "Created through my plugin" --priority HIGH
graph-engine list-tickets
graph-engine get-executable <ticket id>          # seeds plan/plan_review nodes and edges
graph-engine add-artifact <ticket id> <node id> notes text "hello"
graph-engine complete-node <node id> true
graph-engine get-ticket <ticket id>              # nodes, edges and artifacts read back
```

Run `graph-engine` without arguments for the full command list. Then start
the Web UI from that same shell -- so it inherits the three variables -- and
check the ticket list, the execution graph and artifact previews:

```sh
graph-engine serve --host 127.0.0.1 --port 49180
```

Also check the failure paths: a wrong token (startup stops with "rejected the
bearer token"), the plugin stopped (a connection error), and a missing entity
(for example `get-ticket` with an unknown ID must report that the ticket
was not found, not a generic error -- which is what happens when your 404
answer lacks the `TICKET_NOT_FOUND` code).

### 4. Test it automatically

- Write black-box tests that call every endpoint over HTTP, like the Jira
  sample's `TestConformance_All32Endpoints` in
  [`plugin_test.go`](../../examples/jira-datasource/plugin_test.go): status
  codes, error codes, PATCH three-state fields, content omission in ticket
  listings, and cascading deletes.
- Replace the backing system with an in-memory fake (the sample's
  [`fakejira_test.go`](../../examples/jira-datasource/fakejira_test.go)), so
  tests need no credentials and can run in CI.
- Test deadlines and cancellation: a slow or rate-limited backing system must
  make the plugin fail before graph-engine's 30 seconds, and a disconnected
  client must not lead to further writes (see the sample's
  [`hardening_test.go`](../../examples/jira-datasource/hardening_test.go)).

### 5. Before you deploy remotely

Put the plugin behind HTTPS with a certificate graph-engine will verify, use a
long random token, and configure graph-engine with an `https://` URL and a
`${ENV_VAR}` token reference.

## Walkthrough: the Jira sample

[`examples/jira-datasource`](../../examples/jira-datasource/README.md) stores
GraphOps data in Jira Cloud. It is a standalone Go module (standard library
only, no GraphOps imports) of about a dozen files:

| File | Role |
|---|---|
| `main.go` | Reads the environment variables (`JIRA_BASE_URL`, `JIRA_EMAIL`, `JIRA_API_TOKEN`, `GRAPHOPS_DATASOURCE_TOKEN`, the issue types and the workflow status names, ...), refuses to start if a required one is missing, sets server timeouts, shuts down gracefully |
| `server.go` | The protocol's HTTP layer: routes, bearer-token check (constant time, before any Jira call), a 25-second deadline per request, JSON errors |
| `store.go` | Projects, tickets and labels mapped onto Jira, with a per-issue lock around read-modify-write updates |
| `nodes.go` | Nodes as sub-tasks, edges, artifacts, node status labels and workflow moves, the managed-node checks, and the clean-up when a ticket is detached |
| `jira.go` | A small Jira REST API v3 client: context-aware requests, bounded retries on 429/503 |
| `state.go` | The local state file (registered projects, current project) |
| `adf.go` | Mirrors Markdown into Jira's document format for people reading Jira |
| `types.go` | The protocol's JSON types, declared locally |
| `*_test.go` | A fake Jira (with sub-tasks, workflows and transitions), the conformance test, and hardening tests |

### How the data is mapped

- A **project** is an existing Jira project; the GraphOps prefix is the Jira
  key (so keys of at most 5 characters). The project name and label
  definitions live in an issue property of a per-project metadata issue.
- A **ticket** is a Jira issue labeled `graphops`, with every GraphOps field
  in the `graphops.ticket` issue property. Its ID is the issue key.
- A **node** is a **sub-task of the ticket's issue**, named
  `<node name> [<node type>]`, with its data in the sub-task's
  `graphops.node` property. Node IDs are `<ticket key>-n<sub-task number>`
  (`GOPS-12-n15` is sub-task `GOPS-15`). The node's status is shown on the
  sub-task as exactly one `graphops-status-<status>` label and, best effort,
  as its workflow status (TODO: not moved; DONE: the configured "done"
  status; anything else, REJECTED included: the configured "in progress"
  status).
- **Edges** live in the ticket issue's `graphops.edges` property.
- An **artifact** is a comment on its node's sub-task with a
  `graphops.artifact` comment property; large or binary content goes to an
  attachment of the same sub-task. Artifact IDs are
  `<node ID>-c<comment id>`.
- The **list of projects and the current project** are single values for the
  whole data source, which Jira has no ordinary-permission place for, so they
  are in a local state file.

Every ID encodes where its record is stored, so no operation has to search
for it. Deleting a node deletes its sub-task; deleting a ticket detaches it
and leaves its sub-tasks in Jira without their GraphOps data. The sample's
README has the full mapping table, the status mapping and what each
operation does in Jira. (Earlier versions of the sample kept the whole graph
in one `graphops.graph` property of the ticket and the artifacts on the
ticket's issue; that data is not read by the current version and not
migrated.)

### Design choices worth copying

- **IDs that lead to their storage location** avoid lookups and indexes.
- **Map records onto what users see in the backing system**, not only onto
  hidden storage: nodes as sub-tasks, with their artifacts on them and a
  status label, make the execution graph readable in Jira itself.
- **One search for listings, none for nodes**: ticket lists are one JQL
  query that also returns the `graphops.ticket` property, not one request
  per issue; a ticket's nodes are read through its `subtasks` field and a
  bulk fetch, so Jira's search indexing delay never hides a new node.
- **Authorization on every read and write**: an ID that does not name a
  managed node of a managed ticket of a registered project is `NOT_FOUND`,
  and nothing is read, written or deleted -- otherwise the token would give
  access to any issue the Jira account can see.
- **Mirrors are best effort; the record is authoritative**: a workflow move
  that fails is logged and never fails the write, because the record is the
  property.
- **A fixed lock order** (ticket before node sub-task) and the workflow move
  made inside the node's lock, so concurrent updates cannot deadlock or leave
  the Jira status out of step with the label.
- **A request deadline shorter than graph-engine's timeout**, with the
  request context passed to every Jira call, so a timed-out command does not
  keep writing in the background; best-effort work (workflow moves, clean-up
  after a detach) gets its own shorter deadline and runs after the work the
  request's success depends on.
- **Explicit failures at limits**: a property that would exceed Jira's 32 KB
  limit fails with `VALIDATION_ERROR` and leaves the stored value intact.

### Limitations it illustrates

Some constraints come from the backing system, and a plugin should document
them the way the sample does: one plugin process per Jira site, keys of at
most 5 characters, 99 nodes per ticket (counting the sub-tasks that carry
`graphops.node`), about 300 edges per ticket (all edges share one 32 KB
property, about 107-122 bytes each), 32 KB per node and a ticket
description of at most about 30 KB, workflow status names that must be
configured to the workflow's (possibly localized) names, commands that are
slower than with SQLite, best-effort clean-up that can leave GraphOps data
on the sub-tasks of a large detached ticket, search indexing delay, and a
**trust boundary** -- anyone who can edit or comment on the Jira issues and
sub-tasks can change the GraphOps data that agents read. See the sample's
README for details.

### What the real-Jira verification showed

The sample was verified end to end against a real Jira Cloud site (details,
including the before/after table, in the sample's README). On 2026-09-19 the
current format (commit `8b3901d`) showed every node as a sub-task with its
artifacts, one status label and the mirrored workflow status, and it built
and ran a 70-node, 104-edge graph; with the previous format (commit
`d74e516`, the whole graph in one property) the same graph failed at 32,915
bytes, over Jira's 32 KB property limit.

It also showed the price of that visibility, which is worth measuring for any
remote backend: **capacity improved, but every command that writes nodes got
slower.** A node update became six Jira requests (about 2 seconds instead of
0.44) and a node creation creates a Jira issue (about 1.4 seconds instead of
0.49). On a 20-node graph `expand-graph` went from about 25 to about 41
seconds and `complete-node` from 2.2-2.8 to 4.1-4.8 seconds, and
`get-executable` costs about 2 seconds more for each node it starts. Each
protocol request stayed under 2.5 seconds, far from the 30-second timeout,
but a whole `expand-graph` of 68 nodes took 141 seconds -- longer than the
2-minute default timeout of many agent tools. Reads did not slow down with
the size of the graph (`get-ticket` 0.7 seconds for 70 nodes). If your
backing system is remote, measure your commands the same way, per command
and per protocol request, before and after a change of format.
