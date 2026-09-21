# Local MySQL for development

A Docker Compose setup for running the MySQL store tests locally and trying
GraphOps' MySQL backend. It serves TLS with a throwaway CA, so all three TLS
modes (`verify-full`, `verify-ca`, `disabled`) can be exercised. The
credentials are fixed dev values; do not use this setup for real data.

Nothing is bind-mounted from the checkout: the certificates are generated
into a named volume on first start. The container therefore keeps working
when the git worktree that started it is removed, and every worktree shares
the same container.

## Running the MySQL tests

The MySQL tests in `packages/core-go` are skipped unless `GRAPH_TEST_MYSQL_*`
is set. From the repository root (or any worktree):

```sh
./dev/mysql/test.sh                 # all three TLS modes
./dev/mysql/test.sh verify-full     # just one
```

The script starts the container if needed, creates a throwaway database for
this run (the tests delete every row before each test, so parallel runs must
not share one), runs `internal/store`, `internal/httpserver` and
`cmd/graph-engine` (the CLI's per-backend tests, e.g. delete-ticket and
update-ticket) once per TLS mode, one package at a time, and drops the
database on exit. The CLI tests only touch projects they create themselves
and delete them afterwards.

### The `mysql` job in CI mirrors this script -- keep the two in sync

`.github/workflows/ci.yml` has a `mysql` job that runs the same three packages
against a MySQL service container on every pull request and every push to
`main`, so the rule in `CLAUDE.md` -- run this script for any change to the DB
layer -- is no longer carried by each developer remembering it alone.

The two cannot share code: this script drives `dev/mysql/compose.yaml`, while
a service container is started before the repository is checked out and so can
be handed nothing from the tree. What must stay identical is what is actually
exercised -- the three package paths, the `GRAPH_TEST_MYSQL_*` variable names,
and `-count=1 -p 1` (the three packages share one database and must not run
concurrently). **Change either side and change the other**; the job carries
the same note.

Two of the differences between them are forced by the service container and
are not worth trying to remove:

- **The throwaway database.** This script creates one per run and drops it on
  exit. The job lets the container create `graph_ops_ci` on startup and throws
  the whole container away with the job.
- **The CA certificate.** Here it comes from compose's `certs` service. In CI
  it is copied out of the running container
  (`docker cp <container>:/var/lib/mysql/ca.pem`), because MySQL 8.4 generates
  its own CA and server certificate into `/var/lib/mysql` on first start.

**CI covers two of the three TLS modes: `disabled` and `verify-ca`.**
`verify-full` -- the production default -- cannot run there: MySQL's
auto-generated server certificate carries no subjectAltName at all, so the
hostname check fails with "cannot validate certificate for 127.0.0.1 because
it doesn't contain any IP SANs". `dev/mysql/compose.yaml` avoids this by
generating a certificate that names `localhost` and `127.0.0.1`, which is why
all three modes work here. Getting `verify-full` onto CI is tracked as
DFLT-00125; until that lands, **this script is the only place `verify-full` is
ever exercised**, so run it locally before concluding a change to the DB layer
is green.

The job also fails if the tests only *looked* green: every MySQL-backed test
skips itself when `GRAPH_TEST_MYSQL_HOST` is unset, so a step there counts the
skips and the tests that really connected, and fails on a single skip or on
too few passes. If you add or remove MySQL-backed tests, or change the text
those tests skip with, check that step's counts in `ci.yml` along with them.

### How long it takes, and why

A full run (three TLS modes) takes **4 to 5 minutes** on a developer laptop,
most of it `internal/store` (40-75 seconds per mode, depending on the machine
and on what else is running). Four concurrency tests account for about half of
that package's time -- roughly a third of the whole run:

- `TestUpdateNode_ConcurrentDifferentColumnsBothSurvive`
- `TestUpdateProject_NilNamePatchDoesNotRollBackARename`
- `TestClaimNode_ConcurrentClaimsYieldExactlyOneWinner`
- `TestGetExecutableNodes_ConcurrentCallsNeverHandOutTheSameNode`

That cost is deliberate, because those four are what actually catch the bugs
they cover (lost updates across columns, and the same node being claimed
twice). The deterministic tests next to them -- which columns a patch puts in
the `SET` clause, which statuses a claim may move from -- pass against the
pre-fix code as well: they pin down the mechanism, not the regression.

Two things follow for anyone tempted to make them cheaper:

- **If you lower their round counts, redo the check that the pre-fix code
  fails.** The counts were picked to make the old read-modify-write behaviour
  fail every time, not to be comfortable; a smaller number can let it pass and
  the test then guards nothing. Redo that check against MySQL in particular:
  `TestUpdateProject_NilNamePatchDoesNotRollBackARename` can pass by luck on
  SQLite, where `SetMaxOpenConns(1)` serializes statements and narrows the
  window, so MySQL is effectively the only backend that detects it.
- **The TLS mode has nothing to do with the races**, so a single mode
  (`./dev/mysql/test.sh verify-full`) is enough while iterating on them; run
  all three before concluding the suite is green.

## Start / stop by hand

```sh
docker compose -f dev/mysql/compose.yaml up -d     # start
docker compose -f dev/mysql/compose.yaml down      # stop (data is kept)
docker compose -f dev/mysql/compose.yaml down -v   # stop and delete data and certificates
```

## Using it from GraphOps

| | Value |
| --- | --- |
| Host | `127.0.0.1` (or `localhost`) |
| Port | `13306` (set `GRAPH_DEV_MYSQL_PORT` to change it) |
| Database | `graph_ops` |
| User / password | `graphops` / `graphops` |
| Root password | `root` |

In the Web UI, open Settings (gear button) > App Settings, choose MySQL as
the storage and enter the values above. For TLS, `disabled` needs nothing
else. For `verify-ca` or `verify-full`, copy the CA certificate out of the
container and set its absolute path as the CA file:

```sh
docker compose -f dev/mysql/compose.yaml cp mysql:/etc/mysql/certs/ca.pem ~/.graph-ops/dev-mysql-ca.pem
```

The server certificate names `localhost` and `127.0.0.1`, so use one of those
as the host with `verify-full`. Recopy the file after `down -v`, which
regenerates the certificates. Storage changes take effect the next time the
UI server starts.
