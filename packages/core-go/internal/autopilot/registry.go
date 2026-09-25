package autopilot

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/graph-ops/core-go/internal/domain"
)

// Registry is the local file registry of autopilot runs (plan decision D4):
//
//	<Root>/<projectId>/runs/<runId>.json   one file per run
//	<Root>/<projectId>/.lock               the project's exclusive lock
//
// Root is normally $HOME/.graph-ops/autopilot. Every project has its own
// directory and its own lock, so runs of different projects never touch each
// other's files (completion criterion 8).
//
// The lock is a file created with O_EXCL, retried until it is free: it works
// the same on every OS and needs no flock. A lock whose file is older than
// StaleLockAge is taken to be left by a crashed process and removed. Writes of
// a run file go to a temp file in the same directory and are renamed over the
// target, so a reader never sees half a file -- which is what lets read-only
// callers (Load) skip the lock.
type Registry struct {
	Root string
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// LockTimeout bounds how long WithLock waits; 0 means 60 seconds.
	LockTimeout time.Duration
}

// StaleLockAge is how old a lock file has to be before it is presumed
// abandoned. Nothing holds the lock for long (no terminal launch or network
// call happens under it), so a minute is ample.
const StaleLockAge = time.Minute

var (
	runIDPattern     = regexp.MustCompile(`^run-[a-z0-9-]{1,64}$`)
	projectIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)
)

// ValidRunID reports whether id has the shape NewRunID produces; anything
// else is refused before it gets near a file path.
func ValidRunID(id string) bool { return runIDPattern.MatchString(id) }

// NewRunID returns a fresh run ID: run-<UTC timestamp>-<random hex>.
func NewRunID(now time.Time) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return "run-" + now.UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
}

// ErrCodeAutopilotRunNotFound and friends are the error codes of the run
// commands.
const (
	ErrCodeRunNotFound       domain.ErrorCode = "AUTOPILOT_RUN_NOT_FOUND"
	ErrCodeAlreadyRunning    domain.ErrorCode = "AUTOPILOT_ALREADY_RUNNING"
	ErrCodeRootFinished      domain.ErrorCode = "AUTOPILOT_ROOT_FINISHED"
	ErrCodeLocalPathNotSet   domain.ErrorCode = "PROJECT_LOCAL_PATH_NOT_SET"
	ErrCodeNotFastForward    domain.ErrorCode = "NOT_FAST_FORWARD"
	ErrCodeParentDirty       domain.ErrorCode = "PARENT_WORKTREE_DIRTY"
	ErrCodeInvalidRunState   domain.ErrorCode = "AUTOPILOT_INVALID_STATE"
	ErrCodeRegistryLockTimed domain.ErrorCode = "AUTOPILOT_REGISTRY_LOCKED"
)

func (g *Registry) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

func (g *Registry) projectDir(projectID string) (string, error) {
	if g.Root == "" {
		return "", errors.New("autopilot registry has no root directory (the home directory could not be resolved)")
	}
	if !projectIDPattern.MatchString(projectID) || projectID == "." || projectID == ".." {
		return "", fmt.Errorf("invalid project id for the autopilot registry: %q", projectID)
	}
	return filepath.Join(g.Root, projectID), nil
}

func (g *Registry) runPath(projectID, runID string) (string, error) {
	if !ValidRunID(runID) {
		return "", domain.NewAPIError(domain.ErrCodeValidation, "VALIDATION_ERROR: invalid run id %q", runID)
	}
	dir, err := g.projectDir(projectID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "runs", runID+".json"), nil
}

// LockPath returns the project's lock file path.
func (g *Registry) LockPath(projectID string) (string, error) {
	dir, err := g.projectDir(projectID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ".lock"), nil
}

// Tx is the view of one project's registry while its lock is held.
type Tx struct {
	g         *Registry
	projectID string
}

// WithLock runs fn holding projectID's lock.
func (g *Registry) WithLock(projectID string, fn func(tx *Tx) error) error {
	lockPath, err := g.LockPath(projectID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(filepath.Dir(lockPath), "runs"), 0o700); err != nil {
		return err
	}
	timeout := g.LockTimeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			fmt.Fprintf(f, "%d %s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339Nano))
			f.Close()
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) > StaleLockAge {
			_ = os.Remove(lockPath)
			continue
		}
		if time.Now().After(deadline) {
			return domain.NewAPIError(ErrCodeRegistryLockTimed,
				"AUTOPILOT_REGISTRY_LOCKED: timed out waiting for the autopilot registry lock %s (remove it if no graph-engine autopilot command is running)", lockPath)
		}
		time.Sleep(15 * time.Millisecond)
	}
	defer os.Remove(lockPath)
	return fn(&Tx{g: g, projectID: projectID})
}

// ProjectID is the project this transaction's lock belongs to.
func (tx *Tx) ProjectID() string { return tx.projectID }

// Load reads one run of the transaction's project; nil if it does not exist.
func (tx *Tx) Load(runID string) (*Run, error) { return tx.g.Load(tx.projectID, runID) }

// List reads every run of the transaction's project, oldest first.
func (tx *Tx) List() ([]*Run, error) { return tx.g.List(tx.projectID) }

// Save writes run (stamping UpdatedAt) atomically.
func (tx *Tx) Save(run *Run) error {
	if run.ProjectID != tx.projectID {
		return fmt.Errorf("run %s belongs to project %s, not %s", run.ID, run.ProjectID, tx.projectID)
	}
	path, err := tx.g.runPath(run.ProjectID, run.ID)
	if err != nil {
		return err
	}
	run.UpdatedAt = tx.g.now()
	data, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data)
}

// Delete removes a run's file.
func (tx *Tx) Delete(runID string) error {
	path, err := tx.g.runPath(tx.projectID, runID)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Load reads one run without the lock (writes are atomic renames); nil if it
// does not exist.
func (g *Registry) Load(projectID, runID string) (*Run, error) {
	path, err := g.runPath(projectID, runID)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var run Run
	if err := json.Unmarshal(data, &run); err != nil {
		return nil, fmt.Errorf("reading autopilot run %s: %w", path, err)
	}
	return &run, nil
}

// List reads every run of projectID, oldest (by creation) first. A file that
// cannot be parsed is skipped rather than failing the listing.
func (g *Registry) List(projectID string) ([]*Run, error) {
	dir, err := g.projectDir(projectID)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(dir, "runs"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var runs []*Run
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") || !ValidRunID(strings.TrimSuffix(name, ".json")) {
			continue
		}
		run, err := g.Load(projectID, strings.TrimSuffix(name, ".json"))
		if err != nil || run == nil {
			continue
		}
		runs = append(runs, run)
	}
	sort.SliceStable(runs, func(i, j int) bool {
		if !runs[i].CreatedAt.Equal(runs[j].CreatedAt) {
			return runs[i].CreatedAt.Before(runs[j].CreatedAt)
		}
		return runs[i].ID < runs[j].ID
	})
	return runs, nil
}

// FindProject returns the project whose registry holds runID, "" if none
// does. Run IDs are unique, so the CLI's run commands need only the run ID.
func (g *Registry) FindProject(runID string) (string, error) {
	if !ValidRunID(runID) {
		return "", domain.NewAPIError(domain.ErrCodeValidation, "VALIDATION_ERROR: invalid run id %q", runID)
	}
	if g.Root == "" {
		return "", errors.New("autopilot registry has no root directory (the home directory could not be resolved)")
	}
	entries, err := os.ReadDir(g.Root)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if !e.IsDir() || !projectIDPattern.MatchString(e.Name()) {
			continue
		}
		if _, err := os.Stat(filepath.Join(g.Root, e.Name(), "runs", runID+".json")); err == nil {
			return e.Name(), nil
		}
	}
	return "", nil
}

func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
