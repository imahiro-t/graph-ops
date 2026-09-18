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

### graph-config.json and environment variables

graph-engine reads its settings from `graph-config.json` in the directory it
starts from, or else from `$HOME/.graph-ops/config.json` (see the root
README). Three fields select and configure the HTTP data source:

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

Settings (gear button) > **Global Settings** > **App Settings** > data storage
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
validates what will be written to `graph-config.json`, and it does not look at
`GRAPH_HTTP_DATASOURCE_TOKEN`. Saving a remote URL with an empty token field
is therefore rejected ("a remote data source requires a bearer token"), even
if that variable is set. Enter a `${ENV_VAR}` reference in the token field
instead -- for example `${GRAPH_HTTP_DATASOURCE_TOKEN}` or
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
| Current project | `GET/PUT /current-project` |

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

graph-engine reads `graph-config.json` from the directory it starts in, so use
a scratch directory:

```sh
mkdir -p /tmp/graphops-plugin-dev && cd /tmp/graphops-plugin-dev
cat > graph-config.json <<'EOF'
{
  "dbBackend": "http",
  "httpDataSourceUrl": "http://127.0.0.1:8787",
  "httpDataSourceToken": "${GRAPHOPS_DATASOURCE_TOKEN}"
}
EOF
```

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
the Web UI from the same directory and check the ticket list, the execution
graph and artifact previews:

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
| `main.go` | Reads the environment variables (`JIRA_BASE_URL`, `JIRA_EMAIL`, `JIRA_API_TOKEN`, `GRAPHOPS_DATASOURCE_TOKEN`, ...), refuses to start if one is missing, sets server timeouts, shuts down gracefully |
| `server.go` | The protocol's HTTP layer: routes, bearer-token check (constant time, before any Jira call), a 25-second deadline per request, JSON errors |
| `store.go` | The 32 operations mapped onto Jira, with a per-issue lock around read-modify-write updates |
| `jira.go` | A small Jira REST API v3 client: context-aware requests, bounded retries on 429/503 |
| `state.go` | The local state file (registered projects, current project) |
| `adf.go` | Mirrors Markdown into Jira's document format for people reading Jira |
| `types.go` | The protocol's JSON types, declared locally |
| `*_test.go` | A fake Jira, the conformance test, and hardening tests |

### How the data is mapped

- A **project** is an existing Jira project; the GraphOps prefix is the Jira
  key (so keys of at most 5 characters). The project name and label
  definitions live in an issue property of a per-project metadata issue.
- A **ticket** is a Jira issue labeled `graphops`, with every GraphOps field
  in the `graphops.ticket` issue property. Its ID is the issue key.
- **Nodes and edges** live together in the ticket issue's `graphops.graph`
  property. Node IDs are `<issue key>-NN`.
- An **artifact** is a comment on the ticket issue with a `graphops.artifact`
  comment property; large or binary content goes to an attachment. Artifact
  IDs are `<issue key>-c<comment id>`.
- The **list of projects and the current project** are single values for the
  whole data source, which Jira has no ordinary-permission place for, so they
  are in a local state file.

Every ID encodes where its record is stored, so no operation has to search
for it. The sample's README has the full mapping table and what each
operation does in Jira.

### Design choices worth copying

- **IDs that lead to their storage location** avoid lookups and indexes.
- **One search for listings**: ticket lists are one JQL query that also
  returns the `graphops.ticket` property, not one request per issue.
- **Authorization on every read**: an artifact ID pointing at an issue that is
  not a managed ticket of a registered project is `ARTIFACT_NOT_FOUND`, and
  nothing is read from Jira -- otherwise the token would give access to any
  issue the Jira account can see.
- **A request deadline shorter than graph-engine's timeout**, with the
  request context passed to every Jira call, so a timed-out command does not
  keep writing in the background.
- **Explicit failures at limits**: a property that would exceed Jira's 32 KB
  limit fails with `VALIDATION_ERROR` and leaves the stored value intact.

### Limitations it illustrates

Some constraints come from the backing system, and a plugin should document
them the way the sample does: one plugin process per Jira site, keys of at
most 5 characters, the 32 KB property limit (roughly 55-60 nodes per ticket,
and a description of at most about 30 KB), no Jira workflow synchronization,
search indexing delay, and a **trust boundary** -- anyone who can edit or
comment on the Jira issues can change the GraphOps data that agents read. See
the sample's README for details.

### What the real-Jira verification showed

The sample was verified end to end against a real Jira Cloud site (details in
the sample's README). Everything worked, and it showed the cost of storing a
graph in a remote service: each protocol request stayed under 5 seconds, but
commands that update the graph many times are much slower than with SQLite --
`get-executable` took 3-6 seconds and `expand-graph` about 11 seconds, because
each step reads, modifies and writes the graph property again. If your backing
system is remote, measure your commands the same way.
