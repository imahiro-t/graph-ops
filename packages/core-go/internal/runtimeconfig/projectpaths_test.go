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

func TestSetProjectPath_SavesAndLoadsByProjectID(t *testing.T) {
	cwd, home := t.TempDir(), t.TempDir()
	path, err := SetProjectPath(cwd, home, "proj-aaa", "/home/a/alpha")
	if err != nil {
		t.Fatalf("SetProjectPath: %v", err)
	}
	m := readRawConfig(t, path)
	pp, _ := m["projectPaths"].(map[string]any)
	if pp["proj-aaa"] != "/home/a/alpha" {
		t.Fatalf("projectPaths on disk = %v, want proj-aaa -> /home/a/alpha", m["projectPaths"])
	}
	cfg, _, err := Load(cwd, home)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.ProjectPath("proj-aaa"); got != "/home/a/alpha" {
		t.Errorf("ProjectPath = %q, want /home/a/alpha", got)
	}
}

func TestSetProjectPath_NormalizesPath(t *testing.T) {
	cwd, home := t.TempDir(), t.TempDir()
	if _, err := SetProjectPath(cwd, home, "proj-aaa", "/home/a/alpha/../alpha/"); err != nil {
		t.Fatalf("SetProjectPath: %v", err)
	}
	cfg, _, _ := Load(cwd, home)
	if got := cfg.ProjectPaths["proj-aaa"]; got != "/home/a/alpha" {
		t.Errorf("stored %q, want /home/a/alpha", got)
	}
}

func TestSetProjectPath_OverwritesAndClears(t *testing.T) {
	cwd, home := t.TempDir(), t.TempDir()
	if _, err := SetProjectPath(cwd, home, "proj-aaa", "/home/a/alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := SetProjectPath(cwd, home, "proj-aaa", "/home/a/alpha2"); err != nil {
		t.Fatal(err)
	}
	cfg, _, _ := Load(cwd, home)
	if len(cfg.ProjectPaths) != 1 || cfg.ProjectPaths["proj-aaa"] != "/home/a/alpha2" {
		t.Fatalf("after overwrite projectPaths = %v", cfg.ProjectPaths)
	}

	path, err := SetProjectPath(cwd, home, "proj-aaa", "")
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, _ = Load(cwd, home)
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
	cwd, home := t.TempDir(), t.TempDir()
	_, err := SetProjectPath(cwd, home, "proj-aaa", "relative/path")
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != domain.ErrCodeValidation {
		t.Fatalf("expected VALIDATION_ERROR, got %v", err)
	}
	for _, p := range CandidatePaths(cwd, home) {
		if _, statErr := os.Stat(p); statErr == nil {
			t.Errorf("%s should not have been written", p)
		}
	}
}

func TestSetProjectPath_PreservesOtherFields(t *testing.T) {
	cwd, home := t.TempDir(), t.TempDir()
	orig := FileConfig{DBBackend: "mysql", MySQLHost: "db.example", MySQLUser: "u", MySQLPassword: "${PW}", MyName: "me", PaginationPageSize: 30}
	if _, err := Save(cwd, home, orig); err != nil {
		t.Fatal(err)
	}
	if _, err := SetProjectPath(cwd, home, "proj-aaa", "/home/a/alpha"); err != nil {
		t.Fatal(err)
	}
	cfg, _, _ := Load(cwd, home)
	if cfg.DBBackend != "mysql" || cfg.MySQLHost != "db.example" || cfg.MySQLUser != "u" || cfg.MySQLPassword != "${PW}" || cfg.MyName != "me" || cfg.PaginationPageSize != 30 {
		t.Errorf("other fields changed: %+v", cfg)
	}
}

func TestUpdate_FnErrorWritesNothing(t *testing.T) {
	cwd, home := t.TempDir(), t.TempDir()
	sentinel := errors.New("stop")
	_, _, err := Update(cwd, home, func(cfg *FileConfig) error {
		cfg.MyName = "should not be saved"
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Update err = %v, want sentinel", err)
	}
	for _, p := range CandidatePaths(cwd, home) {
		if _, statErr := os.Stat(p); statErr == nil {
			t.Errorf("%s should not have been written", p)
		}
	}
}

// TestUpdate_ConcurrentWritersDoNotLoseUpdates is the unit-level guarantee
// behind the plan review's condition 2: many goroutines each editing a
// different field through Update must all survive.
func TestUpdate_ConcurrentWritersDoNotLoseUpdates(t *testing.T) {
	cwd, home := t.TempDir(), t.TempDir()
	const n = 40
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "proj-" + string(rune('a'+i%26)) + string(rune('a'+i/26))
			if _, err := SetProjectPath(cwd, home, id, filepath.Join("/work", id)); err != nil {
				t.Errorf("SetProjectPath: %v", err)
			}
		}(i)
	}
	wg.Wait()
	cfg, _, err := Load(cwd, home)
	if err != nil {
		t.Fatal(err)
	}
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
