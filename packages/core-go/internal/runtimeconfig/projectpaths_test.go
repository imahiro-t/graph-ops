package runtimeconfig

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
)

func readRawConfig(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decoding %s: %v", path, err)
	}
	return m
}

// TestSetProjectPath_WritesAndReadsBackTheHomeConfig is completion criterion
// 5: the read side and the write side must name the same file, or a saved
// local path silently fails to take effect. A leftover graph-config.json in
// the working directory is present throughout, to show it is neither written
// nor consulted.
func TestSetProjectPath_WritesAndReadsBackTheHomeConfig(t *testing.T) {
	cwd, home := t.TempDir(), t.TempDir()
	writeWorkDirConfig(t, cwd, `{"projectPaths": {"proj-aaa": "/work/from-the-repo"}}`)

	path, err := SetProjectPath(home, "proj-aaa", "/home/a/alpha")
	if err != nil {
		t.Fatalf("SetProjectPath: %v", err)
	}
	if want := HomeConfigPath(home); path != want {
		t.Errorf("wrote %q, want the home config %q", path, want)
	}
	m := readRawConfig(t, path)
	pp, _ := m["projectPaths"].(map[string]any)
	if pp["proj-aaa"] != "/home/a/alpha" {
		t.Fatalf("projectPaths on disk = %v, want proj-aaa -> /home/a/alpha", m["projectPaths"])
	}
	cfg := LoadEffective(cwd, home).Config
	if got := cfg.ProjectPath("proj-aaa"); got != "/home/a/alpha" {
		t.Errorf("read back ProjectPath = %q, want /home/a/alpha", got)
	}
}

func TestSetProjectPath_NormalizesPath(t *testing.T) {
	cwd, home := t.TempDir(), t.TempDir()
	if _, err := SetProjectPath(home, "proj-aaa", "/home/a/alpha/../alpha/"); err != nil {
		t.Fatalf("SetProjectPath: %v", err)
	}
	cfg := LoadEffective(cwd, home).Config
	if got := cfg.ProjectPaths["proj-aaa"]; got != "/home/a/alpha" {
		t.Errorf("stored %q, want /home/a/alpha", got)
	}
}

func TestSetProjectPath_OverwritesAndClears(t *testing.T) {
	cwd, home := t.TempDir(), t.TempDir()
	if _, err := SetProjectPath(home, "proj-aaa", "/home/a/alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := SetProjectPath(home, "proj-aaa", "/home/a/alpha2"); err != nil {
		t.Fatal(err)
	}
	cfg := LoadEffective(cwd, home).Config
	if len(cfg.ProjectPaths) != 1 || cfg.ProjectPaths["proj-aaa"] != "/home/a/alpha2" {
		t.Fatalf("after overwrite projectPaths = %v", cfg.ProjectPaths)
	}

	path, err := SetProjectPath(home, "proj-aaa", "")
	if err != nil {
		t.Fatal(err)
	}
	cfg = LoadEffective(cwd, home).Config
	if _, ok := cfg.ProjectPaths["proj-aaa"]; ok {
		t.Fatalf("entry should be removed, got %v", cfg.ProjectPaths)
	}
	if got := cfg.ProjectPath("proj-aaa"); got != "" {
		t.Errorf("ProjectPath after clear = %q, want \"\"", got)
	}
	if _, present := readRawConfig(t, path)["projectPaths"]; present {
		t.Error("an emptied projectPaths should be omitted from the file")
	}
}

func TestSetProjectPath_RejectsRelativePathWithoutWriting(t *testing.T) {
	home := t.TempDir()
	_, err := SetProjectPath(home, "proj-aaa", "relative/path")
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != domain.ErrCodeValidation {
		t.Fatalf("expected VALIDATION_ERROR, got %v", err)
	}
	if _, statErr := os.Stat(HomeConfigPath(home)); statErr == nil {
		t.Error("the home config should not have been written")
	}
}

func TestSetProjectPath_PreservesOtherFields(t *testing.T) {
	cwd, home := t.TempDir(), t.TempDir()
	orig := FileConfig{DBBackend: "mysql", MySQLHost: "db.example", MySQLUser: "u", MySQLPassword: "${PW}", MyName: "me", PaginationPageSize: 30}
	if _, _, err := UpdateHome(home, func(cfg *FileConfig) error {
		*cfg = orig
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := SetProjectPath(home, "proj-aaa", "/home/a/alpha"); err != nil {
		t.Fatal(err)
	}
	cfg := LoadEffective(cwd, home).Config
	if cfg.DBBackend != "mysql" || cfg.MySQLHost != "db.example" || cfg.MySQLUser != "u" || cfg.MySQLPassword != "${PW}" || cfg.MyName != "me" || cfg.PaginationPageSize != 30 {
		t.Errorf("other fields changed: %+v", cfg)
	}
}

// TestUpdateHome_ConcurrentWritersDoNotLoseUpdates is the unit-level
// guarantee fileMu exists for: many goroutines each adding a different
// projectPaths entry through SetProjectPath must all survive, since each call
// is a load, an in-memory edit and a save of the same file.
func TestUpdateHome_ConcurrentWritersDoNotLoseUpdates(t *testing.T) {
	cwd, home := t.TempDir(), t.TempDir()
	const n = 40
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "proj-" + string(rune('a'+i%26)) + string(rune('a'+i/26))
			if _, err := SetProjectPath(home, id, filepath.Join("/work", id)); err != nil {
				t.Errorf("SetProjectPath: %v", err)
			}
		}(i)
	}
	wg.Wait()
	cfg := LoadEffective(cwd, home).Config
	if len(cfg.ProjectPaths) != n {
		t.Errorf("expected %d entries after concurrent writes, got %d", n, len(cfg.ProjectPaths))
	}
}

func TestFindProjectIDForDir(t *testing.T) {
	known := []string{"proj-parent", "proj-child"}
	paths := map[string]string{
		"proj-parent": "/work/repo",
		"proj-child":  "/work/repo/sub",
	}
	tests := []struct {
		name  string
		paths map[string]string
		dir   string
		want  string
	}{
		{"exact match", paths, "/work/repo", "proj-parent"},
		{"subdirectory (worktree) matches parent", paths, "/work/repo/.claude/worktrees/DFLT-00001", "proj-parent"},
		{"deepest wins", paths, "/work/repo/sub/pkg", "proj-child"},
		{"no match across separator boundary", paths, "/work/repository", ""},
		{"trailing separator and dots are cleaned", paths, "/work/repo/sub/./x/../", "proj-child"},
		{"relative dir never matches", paths, "work/repo", ""},
		{"empty dir never matches", paths, "", ""},
		{"same depth: first known ID wins", map[string]string{"proj-parent": "/work/repo", "proj-child": "/work/repo"}, "/work/repo", "proj-parent"},
		{"unknown ID ignored", map[string]string{"proj-parent": "/work/repo", "proj-child": "/work/repo/sub", "proj-deleted": "/work/repo/sub/deep"}, "/work/repo/sub/deep", "proj-child"},
		{"unset project never matches", map[string]string{}, "/other/place", ""},
		{"relative stored path ignored", map[string]string{"proj-parent": "rel"}, "/work/repo", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FindProjectIDForDir(tt.paths, known, tt.dir); got != tt.want {
				t.Errorf("FindProjectIDForDir(%q) = %q, want %q", tt.dir, got, tt.want)
			}
		})
	}
}
