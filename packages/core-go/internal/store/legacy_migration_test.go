package store

import (
	"errors"
	"path/filepath"
	"testing"
)

// setLegacyWorkDirDropHook installs testHookBeforeLegacyWorkDirDrop for one
// test. The hook runs at most once, so an Init it triggers on another
// connection does not re-enter it. (No test in this package calls
// t.Parallel, so the package-level hook is not shared across tests.)
func setLegacyWorkDirDropHook(t *testing.T, fn func()) {
	t.Helper()
	fired := false
	testHookBeforeLegacyWorkDirDrop = func() {
		if fired {
			return
		}
		fired = true
		fn()
	}
	t.Cleanup(func() { testHookBeforeLegacyWorkDirDrop = nil })
}

func TestDropLegacyProjectsWorkDirColumn_Cases(t *testing.T) {
	dropErr := errors.New("drop failed")
	checkErr := errors.New("check failed")
	tests := []struct {
		name      string
		checks    []bool  // successive columnExists results
		checkErrs []error // successive columnExists errors
		drop      error
		wantErr   error // nil: success; otherwise errors.Is target
		wantDrops int
	}{
		{name: "column absent: nothing to do", checks: []bool{false}, wantDrops: 0},
		{name: "column present: dropped", checks: []bool{true}, wantDrops: 1},
		{name: "drop fails but column already gone (lost the race): success", checks: []bool{true, false}, drop: dropErr, wantDrops: 1},
		{name: "drop fails and column still there: error", checks: []bool{true, true}, drop: dropErr, wantErr: dropErr, wantDrops: 1},
		{name: "drop fails and re-check fails: original drop error", checks: []bool{true, false}, checkErrs: []error{nil, checkErr}, drop: dropErr, wantErr: dropErr, wantDrops: 1},
		{name: "initial check fails: error, no drop", checks: []bool{false}, checkErrs: []error{checkErr}, wantErr: checkErr, wantDrops: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			calls, drops := 0, 0
			exists := func() (bool, error) {
				i := calls
				calls++
				var err error
				if i < len(tc.checkErrs) {
					err = tc.checkErrs[i]
				}
				return tc.checks[i], err
			}
			drop := func() error { drops++; return tc.drop }
			err := dropLegacyProjectsWorkDirColumn("test", exists, drop)
			if tc.wantErr == nil && err != nil {
				t.Fatalf("expected success, got %v", err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected error wrapping %v, got %v", tc.wantErr, err)
			}
			if drops != tc.wantDrops {
				t.Errorf("drop called %d times, want %d", drops, tc.wantDrops)
			}
		})
	}
}

// Two processes Init the same pre-DFLT-00080 SQLite DB right after an
// upgrade: both see work_dir, the other one drops it first, and this Init's
// DROP fails with "no such column". Init must still succeed, since the
// column is gone either way.
func TestSQLiteInit_LegacyWorkDirDroppedConcurrentlyIsNotAnError(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	legacy, err := NewSQLiteRepository(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteRepository: %v", err)
	}
	if _, err := legacy.db.Exec(legacySchemaDDL(t)); err != nil {
		t.Fatalf("applying legacy schema: %v", err)
	}
	legacy.db.Close()

	first, err := NewSQLiteRepository(dbPath)
	if err != nil {
		t.Fatalf("opening first: %v", err)
	}
	t.Cleanup(func() { first.db.Close() })
	second, err := NewSQLiteRepository(dbPath)
	if err != nil {
		t.Fatalf("opening second: %v", err)
	}
	t.Cleanup(func() { second.db.Close() })

	secondDropped := false
	setLegacyWorkDirDropHook(t, func() {
		// "Another process" completes its whole Init (including the DROP)
		// between first's column check and first's DROP.
		if err := second.Init(); err != nil {
			t.Errorf("second Init: %v", err)
		}
		has, err := second.projectsHasWorkDir()
		secondDropped = err == nil && !has
	})

	if err := first.Init(); err != nil {
		t.Fatalf("first Init should treat the already-dropped column as success, got %v", err)
	}
	if !secondDropped {
		t.Fatal("test setup: the concurrent Init should have dropped work_dir before first's DROP")
	}
	if sqliteColumnNames(t, first.db, "projects")["work_dir"] {
		t.Fatal("projects.work_dir should be gone")
	}
	if _, err := first.CreateProject("Alpha", ""); err != nil {
		t.Errorf("CreateProject after migration: %v", err)
	}
}
