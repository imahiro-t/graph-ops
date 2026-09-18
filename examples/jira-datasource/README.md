# Jira data source (sample plugin)

A sample [GraphOps HTTP data source](../../docs/http-datasource/openapi.yaml)
that stores GraphOps tickets, execution graphs (nodes and edges), artifacts,
projects and labels in **Jira Cloud**. graph-engine talks to it with
`dbBackend: "http"`; the plugin translates each of the protocol's 32
operations into Jira REST API v3 calls.

It is a standalone Go module (standard library only) and does not import
anything from graph-engine: the only contract between the two is the
published protocol. Use it as a starting point for your own plugin.

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
| `JIRA_ISSUE_TYPE` | no | Issue type used for new issues, default `Task` |
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
  per issue.

Rate limiting: a `429` or `503` answer from Jira is retried up to 4 times,
waiting for `Retry-After` (at most 30 seconds per wait).

## Limitations

- **One plugin process per Jira site.** Properties are updated with
  read-modify-write under an in-process per-issue lock; two plugin processes
  writing the same issue could lose each other's update.
- **Project keys of at most 5 characters.** GraphOps prefixes are 1-5
  letters/digits and the prefix is the Jira key.
- **32 KB per property.** Jira limits an issue property to 32768 bytes. The
  `graphops.graph` property holds a ticket's whole graph, including each
  review gate's criteria text, so a very large graph can hit the limit; the
  write then fails with `VALIDATION_ERROR` and the stored graph is left as it
  was.
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
concurrent node creation, attachment round trips and retries on `429`.

## Verification

End-to-end verification against a real Jira Cloud site is recorded
separately (site URL and account are never written to this repository).
