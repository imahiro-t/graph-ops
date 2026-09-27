// Package mysqltestenv reads the GRAPH_TEST_MYSQL_* environment variables
// that the MySQL-backed Go tests run against, and refuses to hand them a
// database they must never touch.
//
// It is shared by every test that opens a real MySQL server --
// internal/store's own tests (package store), its external tests (package
// store_test) and cmd/graph-engine's backend tests -- so the database-name
// check lives in exactly one place. It is a leaf package on purpose: it
// imports nothing from this module, so internal/store's in-package tests can
// import it without an import cycle. Callers turn Settings into a
// store.Config themselves.
//
// Only test code imports this package; it is not linked into graph-engine.
package mysqltestenv

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
)

// WorkingDatabase is the database name dev/mysql/compose.yaml creates for
// day-to-day GraphOps use. The throwaway test databases live on the same
// server, so the database name is the only thing telling them apart.
const WorkingDatabase = "graph_ops"

// CheckDatabase rejects database names the MySQL tests must never run
// against: internal/store's tests delete every row of every table before each
// test and some alter the schema, and the other tests write rows of their
// own, so pointing GRAPH_TEST_MYSQL_DATABASE at the working database would
// wipe or pollute it. An empty name is rejected too, so the target is always
// stated explicitly (dev/mysql/test.sh and CI always set one). This is a deny
// list rather than an allow list so that the names test.sh
// (graph_ops_test_<epoch>_<pid>) and CI (graph_ops_ci) use keep working
// without a shared naming rule.
func CheckDatabase(name string) error {
	if name == "" {
		return fmt.Errorf("GRAPH_TEST_MYSQL_DATABASE is empty; set it to a throwaway database (dev/mysql/test.sh creates one per run)")
	}
	if strings.EqualFold(name, WorkingDatabase) {
		return fmt.Errorf("GRAPH_TEST_MYSQL_DATABASE is %q, the working database; the tests delete or write rows, so use a throwaway database (dev/mysql/test.sh creates one per run)", name)
	}
	return nil
}

// Settings are the connection settings read from GRAPH_TEST_MYSQL_*.
type Settings struct {
	Host     string
	Port     int
	Database string
	User     string
	Password string
	// TLSMode is passed through as-is: there is deliberately no test-only
	// lenient default, so an unset GRAPH_TEST_MYSQL_TLS normalizes to
	// verify-full -- the same secure default a real deployment gets.
	TLSMode   string
	TLSCAFile string
}

// Load reads the GRAPH_TEST_MYSQL_* variables for the tests described by
// what (used in the skip and failure messages, e.g. "MySQLRepository
// tests"). It skips tb when GRAPH_TEST_MYSQL_HOST is unset -- MySQL can only
// be exercised against a real server, which dev/mysql/test.sh and CI
// provide -- and fails tb, before anything connects, when
// GRAPH_TEST_MYSQL_DATABASE does not pass CheckDatabase or
// GRAPH_TEST_MYSQL_PORT is not a valid port. GRAPH_TEST_MYSQL_PORT defaults
// to 3306.
func Load(tb testing.TB, what string) Settings {
	tb.Helper()
	host := os.Getenv("GRAPH_TEST_MYSQL_HOST")
	if host == "" {
		tb.Skipf("GRAPH_TEST_MYSQL_HOST not set; skipping %s (run ./dev/mysql/test.sh)", what)
	}
	database := os.Getenv("GRAPH_TEST_MYSQL_DATABASE")
	if err := CheckDatabase(database); err != nil {
		tb.Fatalf("refusing to run %s: %v", what, err)
	}
	port := 3306
	if p := os.Getenv("GRAPH_TEST_MYSQL_PORT"); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			tb.Fatalf("invalid GRAPH_TEST_MYSQL_PORT %q: want a port number between 1 and 65535", p)
		}
		port = n
	}
	return Settings{
		Host:      host,
		Port:      port,
		Database:  database,
		User:      os.Getenv("GRAPH_TEST_MYSQL_USER"),
		Password:  os.Getenv("GRAPH_TEST_MYSQL_PASSWORD"),
		TLSMode:   os.Getenv("GRAPH_TEST_MYSQL_TLS"),
		TLSCAFile: os.Getenv("GRAPH_TEST_MYSQL_TLS_CA"),
	}
}
