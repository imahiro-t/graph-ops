#!/bin/sh
# Runs the Go tests that need a real MySQL (they skip without one) against the
# dev MySQL in compose.yaml, once per GraphOps TLS mode. Uses the
# graph_ops_test database, which the tests wipe before each test.
# Usage: dev/mysql/test.sh [disabled|verify-ca|verify-full ...]
set -eu

here="$(cd "$(dirname "$0")" && pwd)"
modes="${*:-disabled verify-ca verify-full}"

export GRAPH_TEST_MYSQL_HOST=127.0.0.1
export GRAPH_TEST_MYSQL_PORT="${GRAPH_DEV_MYSQL_PORT:-13306}"
export GRAPH_TEST_MYSQL_DATABASE=graph_ops_test
export GRAPH_TEST_MYSQL_USER=graphops
export GRAPH_TEST_MYSQL_PASSWORD=graphops
export GRAPH_TEST_MYSQL_TLS_CA="$here/certs/ca.pem"

cd "$here/../../packages/core-go"
for mode in $modes; do
  echo "== TLS mode: $mode"
  # -p 1: the packages share graph_ops_test and must not run concurrently.
  GRAPH_TEST_MYSQL_TLS="$mode" go test -count=1 -p 1 ./internal/store/ ./internal/httpserver/
done
