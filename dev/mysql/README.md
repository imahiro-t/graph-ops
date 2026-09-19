# Local MySQL for development

A Docker Compose setup for trying GraphOps' MySQL backend and running the
MySQL store tests locally. It serves TLS with a throwaway CA, so all three
TLS modes (`verify-full`, `verify-ca`, `disabled`) can be tried. The
credentials are fixed dev values; do not use this setup for real data.

## Start / stop

Run these from the repository root.

```sh
./dev/mysql/gen-certs.sh                                  # once: writes dev/mysql/certs/
docker compose -f dev/mysql/compose.yaml up -d --wait     # start
docker compose -f dev/mysql/compose.yaml down             # stop (data is kept)
docker compose -f dev/mysql/compose.yaml down -v          # stop and delete all data
```

MySQL listens on `127.0.0.1:13306` (set `GRAPH_DEV_MYSQL_PORT` to change it).
The init script in `init/` runs only when the data volume is first created,
so after changing it, recreate the volume with `down -v`. If you regenerate
the certificates with `gen-certs.sh --force`, restart the container.

| | Value |
| --- | --- |
| Host | `127.0.0.1` (or `localhost`) |
| Port | `13306` |
| Database | `graph_ops` (for the app), `graph_ops_test` (for `go test`) |
| User / password | `graphops` / `graphops` |
| Root password | `root` |
| CA file | `dev/mysql/certs/ca.pem` (use its absolute path) |

## Using it from GraphOps

In the Web UI, open Global Settings > App Settings, choose MySQL as the
storage, and enter the values above. Every TLS mode works:

- `verify-full`: set the CA file to the absolute path of `certs/ca.pem`. The
  server certificate names `localhost` and `127.0.0.1`, so use one of those
  as the host.
- `verify-ca`: same CA file; the host name is not checked.
- `disabled`: no CA file needed; the connection is plaintext.

Storage changes take effect the next time the UI server starts. Instead of
the settings screen you can also use environment variables:
`GRAPH_DB_BACKEND=mysql`, `GRAPH_MYSQL_HOST`, `GRAPH_MYSQL_PORT`,
`GRAPH_MYSQL_DATABASE`, `GRAPH_MYSQL_USER`, `GRAPH_MYSQL_PASSWORD`,
`GRAPH_MYSQL_TLS` and `GRAPH_MYSQL_TLS_CA`.

## Running the MySQL tests

The MySQL tests in `packages/core-go` are skipped unless `GRAPH_TEST_MYSQL_*`
is set. `test.sh` sets these variables for this container and runs the tests
once per TLS mode, against `graph_ops_test`. The tests delete every row before
each test, so never point them at `graph_ops`.

```sh
./dev/mysql/test.sh                 # all three TLS modes
./dev/mysql/test.sh verify-full     # just one
```
