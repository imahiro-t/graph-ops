package store

import (
	"path/filepath"
	"testing"
	"time"
)

// DFLT-00100 / BUG-01: parallel graph-engine processes share one SQLite
// file, so a write lock held by another process has to be waited for rather
// than failed on. These tests pin the DSN pragma that does the waiting, and
// -- just as importantly -- the one case it cannot cover.

// newBusyTestDB opens an independent connection pool on dbPath, as a second
// graph-engine process would. Each pool is capped at one connection by
// NewSQLiteRepository, so the contention here is genuinely between pools.
func newBusyTestDB(t *testing.T, dbPath string) *SQLiteRepository {
	t.Helper()
	repo, err := NewSQLiteRepository(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteRepository(%s): %v", dbPath, err)
	}
	t.Cleanup(func() { repo.db.Close() })
	return repo
}

func TestSQLiteDSNSetsBusyTimeout(t *testing.T) {
	repo := newTestRepo(t)

	var timeout int
	if err := repo.db.QueryRow(`PRAGMA busy_timeout`).Scan(&timeout); err != nil {
		t.Fatalf("reading PRAGMA busy_timeout: %v", err)
	}
	if timeout != 5000 {
		t.Errorf("PRAGMA busy_timeout = %d, want 5000 -- without it a write lock held by another process fails immediately", timeout)
	}
	// The pragmas the DSN already carried must survive alongside it.
	var journal string
	if err := repo.db.QueryRow(`PRAGMA journal_mode`).Scan(&journal); err != nil {
		t.Fatalf("reading PRAGMA journal_mode: %v", err)
	}
	if journal != "wal" {
		t.Errorf("PRAGMA journal_mode = %q, want wal", journal)
	}
	var foreignKeys int
	if err := repo.db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatalf("reading PRAGMA foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Errorf("PRAGMA foreign_keys = %d, want 1", foreignKeys)
	}
}

// TestSQLiteBusyTimeoutWaitsOutAnotherConnectionsWriteLock is the guarantee
// the fix buys: a plain statement whose write lock is held elsewhere waits
// for it instead of returning "database is locked" straight away. This is
// the shape of every write the parallel-execution path makes -- the artifact
// INSERT behind add-artifact, the node UPDATE behind complete-node, the
// migration UPDATE in Init.
func TestSQLiteBusyTimeoutWaitsOutAnotherConnectionsWriteLock(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "busy.db")
	writer := newBusyTestDB(t, dbPath)
	if _, err := writer.db.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("creating the test table: %v", err)
	}
	blocker := newBusyTestDB(t, dbPath)

	// database/sql's Begin issues a plain (deferred) BEGIN, which takes no
	// lock at all; it is the INSERT right after it that takes the write lock,
	// and the transaction then holds it until Commit.
	tx, err := blocker.db.Begin()
	if err != nil {
		t.Fatalf("Begin on the blocking connection: %v", err)
	}
	if _, err := tx.Exec(`INSERT INTO t (id) VALUES (1)`); err != nil {
		t.Fatalf("taking the write lock on the blocking connection: %v", err)
	}

	const hold = 300 * time.Millisecond
	released := make(chan error, 1)
	go func() {
		time.Sleep(hold)
		released <- tx.Commit()
	}()

	start := time.Now()
	_, err = writer.db.Exec(`INSERT INTO t (id) VALUES (2)`)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("a write blocked by another connection must wait for busy_timeout, not fail after %s: %v", elapsed, err)
	}
	if err := <-released; err != nil {
		t.Fatalf("committing on the blocking connection: %v", err)
	}
	// It really waited rather than slipping in before the lock was taken.
	if elapsed < hold/2 {
		t.Errorf("the blocked write returned after %s, before the lock was released at %s -- the test is not creating contention", elapsed, hold)
	}
}

// TestSQLiteBusyTimeoutDoesNotCoverDeferredTransactionUpgrade pins the limit
// of the fix, because it is not obvious and it still bites in production.
//
// SQLite deliberately does NOT run the busy handler when a transaction that
// has already read tries to become a writer: waiting there could deadlock,
// so it returns SQLITE_BUSY (5) -- or SQLITE_BUSY_SNAPSHOT (517) if another
// connection wrote since the read -- immediately, whatever busy_timeout
// says. Every db.Begin() in this package is deferred, so any read-then-write
// transaction keeps failing under concurrent processes: updateTicket
// (labels.go), which syncTicketStatus drives from complete-node and
// get-executable, and the ID allocation in CreateTicket/CreateNode.
//
// DFLT-00100 removed most of the exposure rather than the failure mode:
// syncTicketStatus now skips UpdateTicket entirely when the derived status
// already matches the stored one, so complete-node/get-executable only enter
// this transaction on a real status transition. The transaction itself is
// still deferred, and still fails this way when two processes do transition
// the same ticket at the same moment.
//
// Fixing that needs BEGIN IMMEDIATE (DSN _txlock=immediate) or a retry, both
// of which DFLT-00100 puts out of scope. If a later ticket takes it on, this
// test is expected to fail and should be replaced with its mirror image.
func TestSQLiteBusyTimeoutDoesNotCoverDeferredTransactionUpgrade(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "upgrade.db")
	reader := newBusyTestDB(t, dbPath)
	if _, err := reader.db.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("creating the test table: %v", err)
	}
	other := newBusyTestDB(t, dbPath)

	tx, err := reader.db.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer tx.Rollback() //nolint:errcheck
	var n int
	if err := tx.QueryRow(`SELECT count(*) FROM t`).Scan(&n); err != nil {
		t.Fatalf("reading inside the deferred transaction: %v", err)
	}

	// Another connection writes while this transaction holds its snapshot.
	if _, err := other.db.Exec(`INSERT INTO t (id) VALUES (1)`); err != nil {
		t.Fatalf("writing from the other connection: %v", err)
	}

	start := time.Now()
	_, err = tx.Exec(`INSERT INTO t (id) VALUES (2)`)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("promoting a deferred read transaction to a writer unexpectedly succeeded; if BEGIN IMMEDIATE or a retry was introduced, replace this test")
	}
	// The point is that it gave up at once: busy_timeout bought nothing.
	if elapsed > time.Second {
		t.Errorf("the failing upgrade took %s, so the busy handler did run after all -- this test no longer describes the behaviour", elapsed)
	}
	t.Logf("deferred read-then-write upgrade failed after %s with: %v", elapsed, err)
}
