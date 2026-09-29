package store

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// TestSQLiteConcurrentFirstInitOnNewFile is DFLT-00354's regression test:
// several stores opening and initializing a brand-new SQLite file at once
// all succeed. Before the fix, the first connection's journal_mode(WAL)
// pragma raced between them and the losers failed Init with SQLITE_BUSY
// ("reading sqlite schema version: database is locked"), about 5-11 times in
// 240 Inits on the machine this was measured on. Each SQLiteRepository has
// its own sql.DB and so its own connection, which is the same contention
// separate graph-engine processes have.
func TestSQLiteConcurrentFirstInitOnNewFile(t *testing.T) {
	const parallel = 6
	rounds := 40
	if testing.Short() {
		rounds = 10
	}

	var lastPath string
	for round := 0; round < rounds; round++ {
		path := filepath.Join(t.TempDir(), "graph.db")
		lastPath = path
		errs := make([]error, parallel)
		var wg sync.WaitGroup
		for i := 0; i < parallel; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				repo, err := NewSQLiteRepository(path)
				if err != nil {
					errs[i] = err
					return
				}
				defer repo.db.Close()
				errs[i] = repo.Init()
			}(i)
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Errorf("round %d, store %d: Init on a new file = %v, want nil", round, i, err)
			}
		}
	}

	// The DSN's settings are all in effect on a file created this way.
	repo, err := NewSQLiteRepository(lastPath)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.db.Close()
	if err := repo.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	var journal string
	if err := repo.db.QueryRow(`PRAGMA journal_mode`).Scan(&journal); err != nil {
		t.Fatalf("reading PRAGMA journal_mode: %v", err)
	}
	if journal != "wal" {
		t.Errorf("PRAGMA journal_mode = %q, want wal", journal)
	}
	var foreignKeys, busyTimeout int
	if err := repo.db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatalf("reading PRAGMA foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Errorf("PRAGMA foreign_keys = %d, want 1", foreignKeys)
	}
	if err := repo.db.QueryRow(`PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
		t.Fatalf("reading PRAGMA busy_timeout: %v", err)
	}
	if busyTimeout != 5000 {
		t.Errorf("PRAGMA busy_timeout = %d, want 5000", busyTimeout)
	}
}

// busyError returns a real SQLITE_BUSY-coded error from the driver, the kind
// connectWithBusyRetry retries on, by holding a write lock on one connection
// and writing from another with no busy timeout.
func busyError(t *testing.T) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "graph.db")
	holder, err := NewSQLiteRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.db.Close()
	if err := holder.Init(); err != nil {
		t.Fatal(err)
	}
	tx, err := holder.db.Begin() // _txlock=immediate: takes the write lock now
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	other, err := NewSQLiteRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.db.Close()
	if _, err := other.db.Exec(`PRAGMA busy_timeout = 0`); err != nil {
		t.Fatal(err)
	}
	_, err = other.db.Exec(`INSERT INTO projects (id, name, created_at) VALUES ('p-busy', 'busy', '2026-01-01T00:00:00Z')`)
	if err == nil {
		t.Fatal("write under another connection's write lock succeeded, want SQLITE_BUSY")
	}
	return err
}

func TestIsSQLiteBusy(t *testing.T) {
	busy := busyError(t)
	if !isSQLiteBusy(busy) {
		t.Fatalf("isSQLiteBusy(%v) = false, want true", busy)
	}
	if !isSQLiteBusy(fmt.Errorf("wrapped: %w", busy)) {
		t.Error("isSQLiteBusy on a wrapped SQLITE_BUSY = false, want true")
	}
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"nil", nil},
		{"plain error mentioning locked", errors.New("database is locked (5) (SQLITE_BUSY)")},
	} {
		if isSQLiteBusy(tc.err) {
			t.Errorf("isSQLiteBusy(%s) = true, want false", tc.name)
		}
	}
	// The driver error carries SQLITE_BUSY in its primary (low 8 bit) code.
	var sqliteErr *sqlite.Error
	if !errors.As(busy, &sqliteErr) {
		t.Fatalf("SQLITE_BUSY error %T is not a *sqlite.Error", busy)
	}
	if got := sqliteErr.Code() & 0xff; got != sqlite3.SQLITE_BUSY {
		t.Fatalf("busy error's primary code = %d, want %d", got, sqlite3.SQLITE_BUSY)
	}
}

func TestRetryOnSQLiteBusy(t *testing.T) {
	busy := busyError(t)

	t.Run("retries SQLITE_BUSY until the call succeeds", func(t *testing.T) {
		calls := 0
		err := retryOnSQLiteBusy(func() error {
			calls++
			if calls < 3 {
				return busy
			}
			return nil
		}, time.Second)
		if err != nil || calls != 3 {
			t.Errorf("retryOnSQLiteBusy = %v after %d calls, want nil after 3", err, calls)
		}
	})

	t.Run("returns any other error at once", func(t *testing.T) {
		other := errors.New("unable to open database file")
		calls := 0
		err := retryOnSQLiteBusy(func() error {
			calls++
			return other
		}, time.Second)
		if !errors.Is(err, other) || calls != 1 {
			t.Errorf("retryOnSQLiteBusy = %v after %d calls, want %v after 1", err, calls, other)
		}
	})

	t.Run("gives up with the last SQLITE_BUSY after the limit", func(t *testing.T) {
		start := time.Now()
		calls := 0
		err := retryOnSQLiteBusy(func() error {
			calls++
			return busy
		}, 50*time.Millisecond)
		elapsed := time.Since(start)
		if !isSQLiteBusy(err) {
			t.Errorf("retryOnSQLiteBusy = %v, want the SQLITE_BUSY error", err)
		}
		if calls < 2 {
			t.Errorf("op called %d times, want it retried", calls)
		}
		if elapsed < 50*time.Millisecond || elapsed > 2*time.Second {
			t.Errorf("gave up after %v, want about the 50ms limit", elapsed)
		}
	})
}
