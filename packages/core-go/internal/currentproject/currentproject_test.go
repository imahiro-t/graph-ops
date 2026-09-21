package currentproject

// Tests for DFLT-00106: the current project is a per-environment setting in
// graph-config.json, inherited once from the data source's app_state row and
// never read from it again.

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/runtimeconfig"
	"github.com/graph-ops/core-go/internal/store"
)

// stubRepo answers GetCurrentProjectID and counts the calls, so a test can
// assert that the DB is consulted exactly once (or not at all). Every other
// method of store.GraphRepository panics through the embedded nil interface
// -- reaching one would be the bug.
type stubRepo struct {
	store.GraphRepository
	id    string
	err   error
	calls int
}

func (r *stubRepo) GetCurrentProjectID() (string, error) {
	r.calls++
	return r.id, r.err
}

// env is one environment's graph-config.json location. Both candidate paths
// (cwd first, then home) are sandboxed temp dirs, so nothing here can reach
// the developer's real settings file.
type env struct {
	cwd  string
	home string
}

func newEnv(t *testing.T) env {
	t.Helper()
	return env{cwd: t.TempDir(), home: t.TempDir()}
}

func (e env) stored(t *testing.T) *string {
	t.Helper()
	cfg, _, err := runtimeconfig.Load(e.cwd, e.home)
	if err != nil {
		t.Fatalf("runtimeconfig.Load: %v", err)
	}
	return cfg.CurrentProjectID
}

func (e env) configExists() bool {
	for _, p := range runtimeconfig.CandidatePaths(e.cwd, e.home) {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

func mustGet(t *testing.T, e env, repo store.GraphRepository) string {
	t.Helper()
	id, err := Get(e.cwd, e.home, repo, discardLogger())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	return id
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil))
}

func TestSetAndGet_RoundTrip(t *testing.T) {
	e := newEnv(t)
	repo := &stubRepo{id: "proj-from-db"}

	if err := Set(e.cwd, e.home, "proj-alpha"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got := mustGet(t, e, repo); got != "proj-alpha" {
		t.Errorf("Get = %q, want proj-alpha", got)
	}
	if repo.calls != 0 {
		t.Errorf("the data source must not be consulted once the file has a value, got %d calls", repo.calls)
	}
}

// Set must not disturb the rest of graph-config.json -- it goes through
// runtimeconfig.Update, which is a read-modify-write of the whole file.
func TestSet_PreservesOtherSettings(t *testing.T) {
	e := newEnv(t)
	if _, err := runtimeconfig.SetProjectPath(e.cwd, e.home, "proj-alpha", filepath.Join(t.TempDir(), "alpha")); err != nil {
		t.Fatalf("SetProjectPath: %v", err)
	}
	if _, _, err := runtimeconfig.Update(e.cwd, e.home, func(cfg *runtimeconfig.FileConfig) error {
		cfg.MyName = "山田"
		cfg.PaginationPageSize = 42
		return nil
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	if err := Set(e.cwd, e.home, "proj-beta"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	cfg, _, err := runtimeconfig.Load(e.cwd, e.home)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MyName != "山田" || cfg.PaginationPageSize != 42 || len(cfg.ProjectPaths) != 1 {
		t.Errorf("Set clobbered other settings: %+v", cfg)
	}
}

// The three states the *string exists for: absent, explicit "", and an id.
func TestClear_StoresExplicitEmptyRatherThanRemovingTheKey(t *testing.T) {
	e := newEnv(t)

	if got := e.stored(t); got != nil {
		t.Fatalf("precondition: expected no key, got %v", got)
	}
	if err := Set(e.cwd, e.home, "proj-alpha"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := Clear(e.cwd, e.home); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	got := e.stored(t)
	if got == nil {
		t.Fatal("Clear must leave an explicit \"\", not remove the key (an absent key re-triggers the inheritance)")
	}
	if *got != "" {
		t.Errorf("stored currentProjectId = %q, want \"\"", *got)
	}
	// And it survives a round trip through the JSON file as a present key.
	raw, err := os.ReadFile(runtimeconfig.ResolvePath(e.cwd, e.home))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(raw), `"currentProjectId": ""`) {
		t.Errorf("expected an explicit empty currentProjectId in the file, got: %s", raw)
	}
}

func TestGet_InheritsDataSourceValueExactlyOnce(t *testing.T) {
	e := newEnv(t)
	repo := &stubRepo{id: "proj-alpha"}

	if got := mustGet(t, e, repo); got != "proj-alpha" {
		t.Errorf("Get = %q, want the inherited proj-alpha", got)
	}
	if got := e.stored(t); got == nil || *got != "proj-alpha" {
		t.Fatalf("expected the inherited id to be written to graph-config.json, got %v", got)
	}
	if repo.calls != 1 {
		t.Fatalf("expected exactly 1 data source read, got %d", repo.calls)
	}

	// Somebody else moves the shared row: this environment keeps its own.
	repo.id = "proj-beta"
	if got := mustGet(t, e, repo); got != "proj-alpha" {
		t.Errorf("Get = %q after the shared row moved, want proj-alpha", got)
	}
	if repo.calls != 1 {
		t.Errorf("the data source must not be read again, got %d calls", repo.calls)
	}
}

// A brand-new install: nothing to inherit means nothing is written, so a
// mere read never creates a settings file.
func TestGet_EmptyDataSourceValueWritesNothing(t *testing.T) {
	e := newEnv(t)
	repo := &stubRepo{id: ""}

	if got := mustGet(t, e, repo); got != "" {
		t.Errorf("Get = %q, want \"\"", got)
	}
	if e.configExists() {
		t.Error("no graph-config.json should have been created")
	}
	if got := e.stored(t); got != nil {
		t.Errorf("currentProjectId should still be absent, got %v", got)
	}

	// Until something is selected, each read asks the data source again --
	// the accepted cost of not writing an empty value.
	repo.id = "proj-alpha"
	if got := mustGet(t, e, repo); got != "proj-alpha" {
		t.Errorf("Get = %q, want proj-alpha once the data source has one", got)
	}
	if got := e.stored(t); got == nil || *got != "proj-alpha" {
		t.Errorf("expected it to be inherited at that point, got %v", got)
	}
}

// An environment that deliberately deselected its project must not have the
// stale shared value pushed back onto it.
func TestGet_ExplicitEmptyBlocksInheritance(t *testing.T) {
	e := newEnv(t)
	if err := Clear(e.cwd, e.home); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	repo := &stubRepo{id: "proj-alpha"}

	if got := mustGet(t, e, repo); got != "" {
		t.Errorf("Get = %q, want \"\"", got)
	}
	if repo.calls != 0 {
		t.Errorf("the data source must not be read at all, got %d calls", repo.calls)
	}
	if got := e.stored(t); got == nil || *got != "" {
		t.Errorf("currentProjectId = %v, want it to stay an explicit \"\"", got)
	}
}

func TestGet_DataSourceErrorIsReturned(t *testing.T) {
	e := newEnv(t)
	repo := &stubRepo{err: errors.New("boom: app_state unreadable")}

	if _, err := Get(e.cwd, e.home, repo, discardLogger()); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected the data source error to be surfaced, got %v", err)
	}
}

// Failing to save the inherited value is a warning, not an error: the
// caller's question ("which project?") has an answer, and the inheritance is
// simply retried next time.
func TestGet_InheritanceSaveFailureWarnsAndStillAnswers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions do not block writes the same way on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	e := newEnv(t)
	// Make the resolved config path unwritable by taking write permission
	// off the directory it would be created in.
	dir := filepath.Dir(runtimeconfig.ResolvePath(e.cwd, e.home))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	var logged bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelWarn}))
	repo := &stubRepo{id: "proj-alpha"}

	id, err := Get(e.cwd, e.home, repo, logger)
	if err != nil {
		t.Fatalf("a failed save must not fail the read: %v", err)
	}
	if id != "proj-alpha" {
		t.Errorf("Get = %q, want proj-alpha", id)
	}
	if !strings.Contains(logged.String(), "current_project_inherit_save_failed") {
		t.Errorf("expected a warning about the failed save, got: %s", logged.String())
	}
}

// A nil logger must not panic -- cmd/graph-engine has no logger to hand in.
func TestGet_NilLoggerIsAccepted(t *testing.T) {
	e := newEnv(t)
	if _, err := Get(e.cwd, e.home, &stubRepo{id: "proj-alpha"}, nil); err != nil {
		t.Fatalf("Get: %v", err)
	}
}

func TestSetIn_AlwaysStoresANonNilPointer(t *testing.T) {
	var cfg runtimeconfig.FileConfig
	SetIn(&cfg, "")
	if cfg.CurrentProjectID == nil || *cfg.CurrentProjectID != "" {
		t.Errorf("SetIn(\"\") = %v, want a pointer to \"\"", cfg.CurrentProjectID)
	}
	SetIn(&cfg, "proj-alpha")
	if cfg.CurrentProjectID == nil || *cfg.CurrentProjectID != "proj-alpha" {
		t.Errorf("SetIn = %v, want a pointer to proj-alpha", cfg.CurrentProjectID)
	}
}

func TestIsCurrent(t *testing.T) {
	var absent runtimeconfig.FileConfig
	if IsCurrent(absent, "proj-alpha") || IsCurrent(absent, "") {
		t.Error("an absent key must never match, not even \"\"")
	}
	var cfg runtimeconfig.FileConfig
	SetIn(&cfg, "proj-alpha")
	if !IsCurrent(cfg, "proj-alpha") || IsCurrent(cfg, "proj-beta") {
		t.Error("IsCurrent must match exactly the stored id")
	}
}
