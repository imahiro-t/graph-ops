package store

import (
	"path/filepath"
	"testing"
	"time"
)

// DFLT-00100 / BUG-01: parallel graph-engine processes share one SQLite
// file, so a write lock held by another process has to be waited for rather
// than failed on. These tests pin the DSN pragma that does the waiting
// (busy_timeout), and the DSN parameter that makes it reach transactions too
// (_txlock=immediate, DFLT-00136): busy_timeout on its own cannot cover a
// deferred transaction that reads and then tries to upgrade to a writer.

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

	// With _txlock=immediate, Begin itself issues BEGIN IMMEDIATE and so
	// already takes the write lock (TestSQLiteBeginTakesTheWriteLockImmediately);
	// the INSERT is kept so the transaction would hold the lock even under a
	// deferred BEGIN. Either way it is held until Commit.
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

// TestSQLiteBeginTakesTheWriteLockImmediately pins _txlock=immediate
// directly, so a DSN edit that drops it fails here with an obvious cause
// rather than only as a flaky lock error in the concurrency tests. The
// observer runs with busy_timeout off so a held lock shows up as an
// immediate error instead of a wait.
func TestSQLiteBeginTakesTheWriteLockImmediately(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "immediate.db")
	repo := newBusyTestDB(t, dbPath)
	if _, err := repo.db.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("creating the test table: %v", err)
	}
	// The observer goes through NewSQLiteRepository like every other
	// connection (TestSQLiteDSNIsBuiltInExactlyOnePlace) and then turns its
	// own busy_timeout off; the pool is capped at one connection, so the
	// pragma sticks to every statement below.
	observer := newBusyTestDB(t, dbPath).db
	if _, err := observer.Exec(`PRAGMA busy_timeout = 0`); err != nil {
		t.Fatalf("disabling busy_timeout on the observer: %v", err)
	}

	tx, err := repo.db.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	// No statement has run inside tx: under a deferred BEGIN the observer's
	// write would go straight through.
	if _, err := observer.Exec(`INSERT INTO t (id) VALUES (1)`); err == nil {
		t.Error("another connection wrote while a freshly begun transaction was open -- Begin did not take the write lock, so _txlock=immediate is not in effect")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if _, err := observer.Exec(`INSERT INTO t (id) VALUES (1)`); err != nil {
		t.Fatalf("writing after the transaction ended: %v", err)
	}
}

// TestSQLiteImmediateTransactionWaitsInsteadOfFailingUpgrade is the mirror
// image of the test DFLT-00100 left here
// (TestSQLiteBusyTimeoutDoesNotCoverDeferredTransactionUpgrade), replaced by
// DFLT-00136.
//
// SQLite deliberately does NOT run the busy handler when a transaction that
// has already read tries to become a writer -- waiting there could deadlock
// -- so with a deferred BEGIN a read-then-write transaction failed at once
// with SQLITE_BUSY whenever another process wrote concurrently, whatever
// busy_timeout said. That is the shape of updateTicket (labels.go), which
// syncTicketStatus drives from get-executable and complete-node, and of the
// ID allocation in CreateTicket/CreateNode. With _txlock=immediate the write
// lock is taken at BEGIN, where busy_timeout applies, so both sides of the
// conflict now wait instead of failing.
func TestSQLiteImmediateTransactionWaitsInsteadOfFailingUpgrade(t *testing.T) {
	const hold = 300 * time.Millisecond // far below the 5000ms busy_timeout

	// A read-then-write transaction is open when another connection wants
	// to write: the other connection waits for it, and the transaction's
	// own write -- the step that used to fail -- succeeds.
	t.Run("another write waits for the open transaction", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "upgrade.db")
		txConn := newBusyTestDB(t, dbPath)
		if _, err := txConn.db.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY)`); err != nil {
			t.Fatalf("creating the test table: %v", err)
		}
		other := newBusyTestDB(t, dbPath)

		tx, err := txConn.db.Begin()
		if err != nil {
			t.Fatalf("Begin: %v", err)
		}
		defer tx.Rollback() //nolint:errcheck
		var n int
		if err := tx.QueryRow(`SELECT count(*) FROM t`).Scan(&n); err != nil {
			t.Fatalf("reading inside the transaction: %v", err)
		}

		otherDone := make(chan error, 1)
		go func() {
			_, err := other.db.Exec(`INSERT INTO t (id) VALUES (1)`)
			otherDone <- err
		}()
		// Give the other write time to reach the lock and start waiting.
		select {
		case err := <-otherDone:
			t.Fatalf("the other connection's write finished (err=%v) while the transaction still held the write lock", err)
		case <-time.After(hold):
		}

		if _, err := tx.Exec(`INSERT INTO t (id) VALUES (2)`); err != nil {
			t.Fatalf("the transaction's own write after its read failed -- the deferred-upgrade failure is back: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("Commit: %v", err)
		}
		select {
		case err := <-otherDone:
			if err != nil {
				t.Fatalf("the waiting write failed instead of going through after the commit: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the waiting write never completed after the transaction committed")
		}
	})

	// The case the fix exists for: another connection already holds the
	// write lock when a read-then-write transaction begins. Under a deferred
	// BEGIN the read went ahead on a snapshot and the upgrade then failed
	// immediately; now the BEGIN waits for the lock and everything after it
	// succeeds.
	t.Run("the transaction waits for another connection's write lock", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "upgrade.db")
		txConn := newBusyTestDB(t, dbPath)
		if _, err := txConn.db.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY)`); err != nil {
			t.Fatalf("creating the test table: %v", err)
		}
		blocker := newBusyTestDB(t, dbPath)

		blockTx, err := blocker.db.Begin()
		if err != nil {
			t.Fatalf("Begin on the blocking connection: %v", err)
		}
		if _, err := blockTx.Exec(`INSERT INTO t (id) VALUES (1)`); err != nil {
			t.Fatalf("writing on the blocking connection: %v", err)
		}
		released := make(chan error, 1)
		go func() {
			time.Sleep(hold)
			released <- blockTx.Commit()
		}()

		start := time.Now()
		tx, err := txConn.db.Begin()
		if err != nil {
			t.Fatalf("Begin while another connection holds the write lock must wait, not fail after %s: %v", time.Since(start), err)
		}
		defer tx.Rollback() //nolint:errcheck
		var n int
		if err := tx.QueryRow(`SELECT count(*) FROM t`).Scan(&n); err != nil {
			t.Fatalf("reading inside the transaction: %v", err)
		}
		if _, err := tx.Exec(`INSERT INTO t (id) VALUES (2)`); err != nil {
			t.Fatalf("read-then-write inside the transaction failed -- the deferred-upgrade failure is back: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("Commit: %v", err)
		}
		elapsed := time.Since(start)
		if err := <-released; err != nil {
			t.Fatalf("committing on the blocking connection: %v", err)
		}
		if n != 1 {
			t.Errorf("the transaction read %d rows, want 1 -- it should have started after the blocker committed", n)
		}
		if elapsed < hold/2 {
			t.Errorf("the transaction finished after %s, before the lock was released at %s -- the test is not creating contention", elapsed, hold)
		}
	})
}
