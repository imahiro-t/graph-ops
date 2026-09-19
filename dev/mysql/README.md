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

In the Web UI, open Global Settings > App Settings, choose MySQL as the
storage and enter the values above. For TLS, `disabled` needs nothing else.
For `verify-ca` or `verify-full`, copy the CA certificate out of the container
and set its absolute path as the CA file:

```sh
docker compose -f dev/mysql/compose.yaml cp mysql:/etc/mysql/certs/ca.pem ~/.graph-ops/dev-mysql-ca.pem
```

The server certificate names `localhost` and `127.0.0.1`, so use one of those
as the host with `verify-full`. Recopy the file after `down -v`, which
regenerates the certificates. Storage changes take effect the next time the
UI server starts.
