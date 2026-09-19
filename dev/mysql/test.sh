#!/bin/sh
# Runs the Go tests that need a real MySQL (they skip without one) against the
# dev MySQL in compose.yaml, once per GraphOps TLS mode. Starts the container
# if it is not running. Each run gets its own throwaway database, dropped on
# exit, so runs from several worktrees at once do not wipe each other's
# tables (the tests delete every row before each test).
# Usage: dev/mysql/test.sh [disabled|verify-ca|verify-full ...]
set -eu

here="$(cd "$(dirname "$0")" && pwd)"
modes="${*:-disabled verify-ca verify-full}"
compose="docker compose -f $here/compose.yaml"

# Not `up --wait`: some Compose versions wait forever on the one-shot certs
# service, which exits instead of becoming healthy.
$compose up -d >/dev/null 2>&1
tries=0
until [ "$(docker inspect -f '{{.State.Health.Status}}' "$($compose ps -q mysql)")" = healthy ]; do
  tries=$((tries + 1))
  if [ "$tries" -ge 60 ]; then
    echo "dev MySQL did not become healthy; see: $compose logs mysql" >&2
    exit 1
  fi
  sleep 2
done

tmp="$(mktemp -d)"
db="graph_ops_test_$(date +%s)_$$"
cleanup() {
  $compose exec -T mysql mysql -uroot -proot -e "DROP DATABASE IF EXISTS \`$db\`" 2>/dev/null || true
  rm -rf "$tmp"
}
trap cleanup EXIT INT TERM

$compose cp mysql:/etc/mysql/certs/ca.pem "$tmp/ca.pem"
$compose exec -T mysql mysql -uroot -proot -e \
  "CREATE DATABASE \`$db\` CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci; GRANT ALL PRIVILEGES ON \`$db\`.* TO 'graphops'@'%';" 2>/dev/null

export GRAPH_TEST_MYSQL_HOST=127.0.0.1
export GRAPH_TEST_MYSQL_PORT="${GRAPH_DEV_MYSQL_PORT:-13306}"
export GRAPH_TEST_MYSQL_DATABASE="$db"
export GRAPH_TEST_MYSQL_USER=graphops
export GRAPH_TEST_MYSQL_PASSWORD=graphops
export GRAPH_TEST_MYSQL_TLS_CA="$tmp/ca.pem"

cd "$here/../../packages/core-go"
status=0
for mode in $modes; do
  echo "== TLS mode: $mode"
  # -p 1: the packages share one database and must not run concurrently.
  GRAPH_TEST_MYSQL_TLS="$mode" go test -count=1 -p 1 ./internal/store/ ./internal/httpserver/ ./cmd/graph-engine/ || status=1
done
exit $status
