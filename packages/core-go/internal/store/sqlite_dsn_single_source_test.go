package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSQLiteDSNIsBuiltInExactlyOnePlace pins DFLT-00100's completion
// criterion 1b: "the DSN must be assembled in one place in sqlite.go -- no
// separate DSN for tests and production".
//
// This is a constraint that erodes silently. A second sql.Open("sqlite", ...)
// added for a test helper or a one-off migration tool would compile, pass
// every other test, and quietly run without busy_timeout -- exactly the bug
// this ticket fixed. Checking it by hand with grep (as the first iteration
// did) does not survive the next change; this does.
func TestSQLiteDSNIsBuiltInExactlyOnePlace(t *testing.T) {
	// dsnFile is the one file allowed to spell out a DSN.
	const dsnFile = "internal/store/sqlite.go"
	// schemaProbeFile opens the driver with no pragmas at all, purely to
	// read table definitions back. It is not a second DSN, but it must stay
	// the only exception: anything that writes has to go through
	// NewSQLiteRepository so it inherits busy_timeout.
	const schemaProbeFile = "internal/httpserver/project_local_path_isolation_test.go"

	// scannerFile is this file. It necessarily spells out both patterns it
	// looks for, so it has to exclude itself.
	const scannerFile = "internal/store/sqlite_dsn_single_source_test.go"

	// The module root: internal/store -> internal -> packages/core-go.
	const root = "../.."

	var pragmaSites, openSites []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == "vendor" || info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(filepath.ToSlash(path), root+"/")
		if rel == scannerFile {
			return nil
		}
		for i, line := range strings.Split(string(data), "\n") {
			// A DSN is recognizable by the pragma syntax
			// modernc.org/sqlite uses.
			if strings.Contains(line, "_pragma=") {
				pragmaSites = append(pragmaSites, fmt.Sprintf("%s:%d", rel, i+1))
			}
			if strings.Contains(line, `sql.Open("sqlite"`) {
				openSites = append(openSites, fmt.Sprintf("%s:%d", rel, i+1))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}

	if len(pragmaSites) != 1 || !strings.HasPrefix(pragmaSites[0], dsnFile+":") {
		t.Errorf("the SQLite DSN's pragmas appear in %d place(s): %v\nwant exactly one, in %s -- a second DSN would not carry busy_timeout (DFLT-00100 / BUG-01)",
			len(pragmaSites), pragmaSites, dsnFile)
	}

	for _, site := range openSites {
		file := site[:strings.LastIndex(site, ":")]
		if file != dsnFile && file != schemaProbeFile {
			t.Errorf("%s opens the sqlite driver directly; every writer must go through NewSQLiteRepository so it inherits busy_timeout (DFLT-00100 / BUG-01)", site)
		}
	}
}
