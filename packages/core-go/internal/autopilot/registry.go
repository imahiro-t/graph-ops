package autopilot

import (
	"bytes"
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
// the same on every OS and needs no flock. The file holds a token unique to
// its holder, and the holder refreshes the file's modification time every
// lockRefreshInterval for as long as it holds it, so a lock is only ever
// "stale" -- older than StaleLockAge -- when its holder is gone (crashed,
// killed, or suspended for minutes). A waiter removes a stale lock only
// after checking that the file still holds the token it judged stale, and a
// holder only ever removes (or saves under) the lock while the file still
// holds its own token: a holder whose lock was taken from it fails its save
// with AUTOPILOT_REGISTRY_LOCKED instead of overwriting the new holder's
// work.
//
// What happens under the lock is kept to reading, deciding and writing run
// files: the git, DB and network work of a command (fingerprints,
// fast-forwards, the ticket tree, artifacts) is done before or after it (see
// runner), so the lock is held for milliseconds and a waiter's LockTimeout
// is never spent behind a slow git hook or a remote backend.
//
// Writes of a run file go to a temp file in the same directory and are
// renamed over the target, so a reader never sees half a file -- which is
// what lets read-only callers (Load, List) skip the lock.
type Registry struct {
	Root string
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// LockTimeout bounds how long WithLock waits; 0 means 60 seconds.
	LockTimeout time.Duration
	// Logf reports what an operator should be able to trace afterwards --
	// a stale lock removed, a lock lost, a run file that cannot be read.
	// nil discards it. The CLI writes it to stderr, the HTTP server to its
	// logger.
	Logf func(format string, args ...any)
}

// StaleLockAge is how long a lock file may go unrefreshed before it is
// presumed abandoned. Its holder refreshes it every lockRefreshInterval, so
// only a holder that has stopped running for this long loses it.
var StaleLockAge = 2 * time.Minute

// lockRefreshInterval is how often a holder refreshes its lock file.
var lockRefreshInterval = 10 * time.Second

// ErrCodeRegistryCorrupt: a run file of the project cannot be read, so a
// start cannot tell whether it would duplicate an active run.
const ErrCodeRegistryCorrupt domain.ErrorCode = "AUTOPILOT_REGISTRY_CORRUPT"

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
	lock      *heldLock
}

func (g *Registry) logf(format string, args ...any) {
	if g.Logf != nil {
		g.Logf(format, args...)
	}
}

// heldLock is a lock file this process created, identified by its token.
type heldLock struct {
	path  string
	token []byte
	stop  chan struct{}
	done  chan struct{}
}

// owned reports whether the lock file still holds this holder's token.
func (l *heldLock) owned() bool {
	cur, err := os.ReadFile(l.path)
	return err == nil && bytes.Equal(cur, l.token)
}

// refresh keeps the lock file's modification time recent while it is held,
// so no waiter takes it for abandoned however long fn runs.
func (l *heldLock) refresh() {
	defer close(l.done)
	t := time.NewTicker(lockRefreshInterval)
	defer t.Stop()
	for {
		select {
		case <-l.stop:
			return
		case <-t.C:
			if l.owned() {
				now := time.Now()
				_ = os.Chtimes(l.path, now, now)
			}
		}
	}
}

// release stops the refresh and removes the lock file -- only while it is
// still this holder's.
func (g *Registry) release(l *heldLock) {
	close(l.stop)
	<-l.done
	if l.owned() {
		_ = os.Remove(l.path)
		return
	}
	g.logf("autopilot registry lock %s was taken over by another process while this one held it (token %s); leaving it in place",
		l.path, strings.TrimSpace(string(l.token)))
}

// removeIfStale removes the lock file at path if it has gone unrefreshed for
// StaleLockAge and still holds the content it held when judged stale (so a
// lock a faster waiter has just re-created is never the one removed). It
// reports whether it removed it.
func (g *Registry) removeIfStale(path string) bool {
	info, err := os.Stat(path)
	if err != nil || time.Since(info.ModTime()) <= StaleLockAge {
		return false
	}
	seen, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	// Re-check right before removing: same content, still unrefreshed.
	cur, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(cur, seen) {
		return false
	}
	if info, err := os.Stat(path); err != nil || time.Since(info.ModTime()) <= StaleLockAge {
		return false
	}
	if err := os.Remove(path); err != nil {
		return false
	}
	g.logf("removed a stale autopilot registry lock %s (held by %q, not refreshed since %s): its holder is presumed gone",
		path, strings.TrimSpace(string(seen)), info.ModTime().UTC().Format(time.RFC3339))
	return true
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
	var nonce [8]byte
	_, _ = rand.Read(nonce[:])
	token := []byte(fmt.Sprintf("%d %s %s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339Nano), hex.EncodeToString(nonce[:])))
	deadline := time.Now().Add(timeout)
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, werr := f.Write(token)
			cerr := f.Close()
			if werr != nil || cerr != nil {
				_ = os.Remove(lockPath)
				if werr == nil {
					werr = cerr
				}
				return werr
			}
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		if g.removeIfStale(lockPath) {
			continue
		}
		if time.Now().After(deadline) {
			return domain.NewAPIError(ErrCodeRegistryLockTimed,
				"AUTOPILOT_REGISTRY_LOCKED: timed out waiting for the autopilot registry lock %s (remove it if no graph-engine autopilot command is running)", lockPath)
		}
		time.Sleep(15 * time.Millisecond)
	}
	l := &heldLock{path: lockPath, token: token, stop: make(chan struct{}), done: make(chan struct{})}
	go l.refresh()
	defer g.release(l)
	return fn(&Tx{g: g, projectID: projectID, lock: l})
}

// checkOwned fails when this transaction's lock has been taken from it, so
// nothing is written without the lock.
func (tx *Tx) checkOwned() error {
	if tx.lock == nil || tx.lock.owned() {
		return nil
	}
	return domain.NewAPIError(ErrCodeRegistryLockTimed,
		"AUTOPILOT_REGISTRY_LOCKED: the autopilot registry lock %s was taken over by another process while this command held it; nothing was saved -- run the command again", tx.lock.path)
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
	if err := tx.checkOwned(); err != nil {
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
	if err := tx.checkOwned(); err != nil {
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

// UnreadableRun is a run file List could not parse.
type UnreadableRun struct {
	Path string `json:"path"`
	Err  string `json:"error"`
}

// List reads every run of projectID, oldest (by creation) first. A file that
// cannot be parsed is left out of the listing and reported through Logf;
// ListChecked also returns it, for a caller that must not decide without it.
func (g *Registry) List(projectID string) ([]*Run, error) {
	runs, _, err := g.ListChecked(projectID)
	return runs, err
}

// ListChecked is List that also returns the run files it could not parse.
func (g *Registry) ListChecked(projectID string) ([]*Run, []UnreadableRun, error) {
	dir, err := g.projectDir(projectID)
	if err != nil {
		return nil, nil, err
	}
	entries, err := os.ReadDir(filepath.Join(dir, "runs"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var runs []*Run
	var bad []UnreadableRun
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") || !ValidRunID(strings.TrimSuffix(name, ".json")) {
			continue
		}
		run, err := g.Load(projectID, strings.TrimSuffix(name, ".json"))
		if err != nil {
			path := filepath.Join(dir, "runs", name)
			bad = append(bad, UnreadableRun{Path: path, Err: err.Error()})
			g.logf("skipping an unreadable autopilot run file %s: %v (fix or remove it; until then the run it holds is invisible)", path, err)
			continue
		}
		if run == nil {
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
	return runs, bad, nil
}

// KeepSettledRuns is how many settled runs (see Prune) a project keeps.
const KeepSettledRuns = 20

// Prune deletes a project's oldest settled runs beyond KeepSettledRuns, so
// the registry -- which the runs API reads on every Web UI poll and next
// reads on every call -- does not grow with the project's history. A run is
// settled when it is not active and no start would take it over again:
// finished, or superseded by a newer run of the same root and mode (a start
// only ever takes over the newest one). A stopped or interrupted run that is
// the newest of its root and mode is kept however old, since running the
// same command again resumes it. runs is the project's listing, oldest
// first; it returns the IDs it deleted.
func (tx *Tx) Prune(runs []*Run, now time.Time) ([]string, error) {
	type key struct{ root, mode string }
	newest := map[key]*Run{}
	for _, r := range runs {
		newest[key{r.RootTicketID, r.Mode}] = r
	}
	var settled []*Run
	for _, r := range runs {
		if r.IsActive(now) || r.State == RunStarting {
			continue
		}
		if r.State == RunFinished || newest[key{r.RootTicketID, r.Mode}] != r {
			settled = append(settled, r)
		}
	}
	var deleted []string
	for i := 0; i < len(settled)-KeepSettledRuns; i++ {
		if err := tx.Delete(settled[i].ID); err != nil {
			return deleted, err
		}
		deleted = append(deleted, settled[i].ID)
	}
	return deleted, nil
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
