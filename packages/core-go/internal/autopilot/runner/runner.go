// Package runner is the autopilot's execution layer (DFLT-00142, plan phase
// 3): it ties the run registry, the planner, git and the terminal launcher to
// the DB, and is what every `graph-engine autopilot` run subcommand (and,
// from phase 5, the Web UI's launch API) calls.
//
// It is a separate package from internal/autopilot because it needs the
// store, and the store (through runtimeconfig) already depends on
// internal/autopilot for the settings types; internal/autopilot itself stays
// free of DB and terminal I/O so its rules can be tested in isolation.
//
// Every dependency with a side effect outside the DB is injectable -- the
// terminal launcher, git, the clock, the registry root, the project's local
// path and settings -- so tests (including phase 6's 20-ticket scale test)
// can run whole trees with a fake launcher and fake workers.
package runner

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/graph-ops/core-go/internal/autopilot"
	"github.com/graph-ops/core-go/internal/config"
	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/runtimeconfig"
	"github.com/graph-ops/core-go/internal/store"
	"github.com/graph-ops/core-go/internal/terminal"
)

// Launcher opens a terminal running claude with extraArgs and prompt in
// workDir. The real one is terminal.LaunchWithArgs; tests pass a fake.
type Launcher interface {
	Launch(workDir string, extraArgs []string, prompt string) error
}

// LauncherFunc adapts a function to Launcher.
type LauncherFunc func(workDir string, extraArgs []string, prompt string) error

// Launch calls f.
func (f LauncherFunc) Launch(workDir string, extraArgs []string, prompt string) error {
	return f(workDir, extraArgs, prompt)
}

// TerminalLauncher launches through internal/terminal.
type TerminalLauncher struct {
	Config    terminal.Config
	ClaudeBin string
}

// Launch implements Launcher.
func (l TerminalLauncher) Launch(workDir string, extraArgs []string, prompt string) error {
	return terminal.LaunchWithArgs(l.Config, workDir, l.ClaudeBin, extraArgs, prompt)
}

// Service runs autopilot runs.
type Service struct {
	Repo     store.GraphRepository
	Registry *autopilot.Registry
	Git      autopilot.Git
	Launcher Launcher
	// Settings returns a project's effective settings.
	Settings func(projectID string) (autopilot.Settings, error)
	// LocalPath returns a project's local path, "" when none is set.
	LocalPath func(projectID string) string
	// Now is the clock; nil means time.Now. The registry uses the same one.
	Now func() time.Time
	// PollInterval is how often Wait re-checks; 0 means 30 seconds.
	PollInterval time.Duration
	// Sleep waits between polls; nil means time.Sleep. Tests replace it to
	// advance a fake clock instead of sleeping.
	Sleep func(time.Duration)
}

// Options are what New needs to build a Service from the runtime config.
type Options struct {
	Repo              store.GraphRepository
	HomeDir           string
	UserExtensionsDir string
	TeamExtensionsDir string
	TerminalCommand   string
	ClaudeBinary      string
}

// RegistryRoot is where a home directory's autopilot registry lives:
// $HOME/.graph-ops/autopilot (plan decision D4).
func RegistryRoot(homeDir string) string {
	if homeDir == "" {
		return ""
	}
	return filepath.Join(homeDir, ".graph-ops", "autopilot")
}

// New builds the Service the CLI uses: the registry under the home
// directory, settings and local paths read fresh from the home config (and
// the team autopilot.yaml) on every call, and the real terminal launcher.
func New(o Options) *Service {
	s := &Service{
		Repo:     o.Repo,
		Registry: &autopilot.Registry{Root: RegistryRoot(o.HomeDir)},
		Launcher: TerminalLauncher{Config: terminal.Config{TerminalCommand: o.TerminalCommand}, ClaudeBin: o.ClaudeBinary},
	}
	s.Settings = func(projectID string) (autopilot.Settings, error) {
		fileCfg, _ := runtimeconfig.LoadHomeConfig(o.HomeDir)
		roots := config.ResolveRoots(o.UserExtensionsDir, o.TeamExtensionsDir)
		return autopilot.ResolveProject(projectID, fileCfg.AutopilotLocal(projectID), roots.TeamDir).Settings, nil
	}
	s.LocalPath = func(projectID string) string {
		fileCfg, _ := runtimeconfig.LoadHomeConfig(o.HomeDir)
		return fileCfg.ProjectPath(projectID)
	}
	return s
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) registry() *autopilot.Registry {
	if s.Registry.Now == nil && s.Now != nil {
		s.Registry.Now = s.Now
	}
	return s.Registry
}

func (s *Service) settings(projectID string) (autopilot.Settings, error) {
	if s.Settings == nil {
		return autopilot.Defaults(), nil
	}
	return s.Settings(projectID)
}

func (s *Service) localPath(projectID string) (string, error) {
	p := ""
	if s.LocalPath != nil {
		p = s.LocalPath(projectID)
	}
	if p == "" {
		return "", domain.NewAPIError(autopilot.ErrCodeLocalPathNotSet,
			"PROJECT_LOCAL_PATH_NOT_SET: project %s has no local path in this environment; set it (Web UI project settings, or create-project --workdir) so the autopilot knows which repository to work in", projectID)
	}
	return p, nil
}

// ---------------------------------------------------------------------------
// Tickets and the tree

func (s *Service) ticket(id string) (*domain.Ticket, error) {
	t, err := s.Repo.GetTicket(id)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, domain.NewAPIError(domain.ErrCodeTicketNotFound, "TICKET_NOT_FOUND: ticket not found: %s", id)
	}
	return t, nil
}

// projectIndex is a project's tickets indexed by ID and by parent, children
// in creation order. One listing per call, whatever the backend.
type projectIndex struct {
	byID     map[string]domain.Ticket
	children map[string][]string
}

func (s *Service) index(projectID string) (*projectIndex, error) {
	all, err := s.Repo.ListTicketsByProject(projectID)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].CreatedAt != all[j].CreatedAt {
			return all[i].CreatedAt < all[j].CreatedAt
		}
		return all[i].ID < all[j].ID
	})
	idx := &projectIndex{byID: map[string]domain.Ticket{}, children: map[string][]string{}}
	for _, t := range all {
		idx.byID[t.ID] = t
		if t.ParentTicketID != nil && *t.ParentTicketID != "" {
			idx.children[*t.ParentTicketID] = append(idx.children[*t.ParentTicketID], t.ID)
		}
	}
	return idx, nil
}

func (idx *projectIndex) descendants(id string) []string {
	var out []string
	seen := map[string]bool{id: true}
	stack := append([]string(nil), idx.children[id]...)
	for len(stack) > 0 {
		c := stack[0]
		stack = stack[1:]
		if seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
		stack = append(stack, idx.children[c]...)
	}
	return out
}

func (idx *projectIndex) tree(root string, mode string) autopilot.Tree {
	tree := autopilot.Tree{}
	var add func(id string)
	add = func(id string) {
		if _, ok := tree[id]; ok {
			return
		}
		t := idx.byID[id]
		tt := &autopilot.TreeTicket{ID: id, Status: t.Status}
		if t.ParentTicketID != nil {
			tt.ParentID = *t.ParentTicketID
		}
		tree[id] = tt
		if mode != autopilot.ModeTree {
			return
		}
		tt.Children = append([]string(nil), idx.children[id]...)
		for _, c := range tt.Children {
			add(c)
		}
	}
	add(root)
	return tree
}

// foreignLaunched returns the tickets launched by this project's inactive
// runs other than self (S6).
func foreignLaunched(runs []*autopilot.Run, self string, now time.Time) map[string]bool {
	out := map[string]bool{}
	for _, r := range runs {
		if r.ID == self || r.IsActive(now) {
			continue
		}
		for _, id := range r.LaunchedTicketIDs() {
			out[id] = true
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Runs

// findRun locates runID's project.
func (s *Service) findRun(runID string) (string, error) {
	projectID, err := s.registry().FindProject(runID)
	if err != nil {
		return "", err
	}
	if projectID == "" {
		return "", domain.NewAPIError(autopilot.ErrCodeRunNotFound, "AUTOPILOT_RUN_NOT_FOUND: run %s not found", runID)
	}
	return projectID, nil
}

// withRun loads runID under its project's lock, runs fn, and saves the run
// when fn succeeds.
func (s *Service) withRun(runID string, fn func(tx *autopilot.Tx, run *autopilot.Run) error) error {
	projectID, err := s.findRun(runID)
	if err != nil {
		return err
	}
	return s.registry().WithLock(projectID, func(tx *autopilot.Tx) error {
		run, err := tx.Load(runID)
		if err != nil {
			return err
		}
		if run == nil {
			return domain.NewAPIError(autopilot.ErrCodeRunNotFound, "AUTOPILOT_RUN_NOT_FOUND: run %s not found", runID)
		}
		if err := fn(tx, run); err != nil {
			return err
		}
		return tx.Save(run)
	})
}

func ticketState(run *autopilot.Run, ticketID string) (*autopilot.TicketState, error) {
	st := run.Ticket(ticketID)
	if st == nil {
		return nil, domain.NewAPIError(domain.ErrCodeValidation, "VALIDATION_ERROR: ticket %s is not part of run %s", ticketID, run.ID)
	}
	return st, nil
}

func invalidState(format string, args ...any) error {
	return domain.NewAPIError(autopilot.ErrCodeInvalidRunState, "AUTOPILOT_INVALID_STATE: "+format, args...)
}

// ---------------------------------------------------------------------------
// start

// StartResult is `autopilot start`'s output.
type StartResult struct {
	RunID     string `json:"run_id"`
	ProjectID string `json:"project_id"`
	Mode      string `json:"mode"`
	Root      string `json:"root"`
	State     string `json:"state"`
	Created   bool   `json:"created"`
	Resumed   bool   `json:"resumed"`
	Adopted   bool   `json:"adopted"`
	Next      string `json:"next"`
}

// Start starts a run from ticketID (autopilot start), or with reserve, only
// reserves it for an orchestrator to adopt with runID (the Web UI's launch,
// phase 5). The decision itself is autopilot.Registry.Begin's, the single
// place the CLI and the API share.
func (s *Service) Start(ticketID, mode, runID string, reserve bool) (StartResult, error) {
	root, err := s.ticket(ticketID)
	if err != nil {
		return StartResult{}, err
	}
	settings, err := s.settings(root.ProjectID)
	if err != nil {
		return StartResult{}, err
	}
	var idx *projectIndex
	descendants := func(id string) ([]string, error) {
		if idx == nil {
			var err error
			if idx, err = s.index(root.ProjectID); err != nil {
				return nil, err
			}
		}
		return idx.descendants(id), nil
	}
	res, err := s.registry().Begin(autopilot.BeginRequest{
		RootID: root.ID, ProjectID: root.ProjectID, RootStatus: root.Status,
		Mode: mode, RunID: runID, Reserve: reserve, Settings: settings, Descendants: descendants,
	})
	if err != nil {
		return StartResult{}, err
	}
	return StartResult{
		RunID: res.Run.ID, ProjectID: res.Run.ProjectID, Mode: res.Run.Mode, Root: res.Run.RootTicketID,
		State: res.Run.State, Created: res.Created, Resumed: res.TookOver, Adopted: res.Adopted,
		Next: "graph-engine autopilot next " + res.Run.ID,
	}, nil
}

// CancelReservation undoes Start(..., reserve=true) (phase 5: the terminal
// failed to open).
func (s *Service) CancelReservation(runID string) error {
	projectID, err := s.findRun(runID)
	if err != nil {
		return err
	}
	return s.registry().CancelReservation(projectID, runID)
}

// ---------------------------------------------------------------------------
// next

// NextResult is `autopilot next`'s output: the planner's action plus where
// and how to carry it out.
//
// RunID is not printed: the orchestrator already holds it (it passed it to
// next), and command carries it. The orchestrator reads a next line two or
// three times per ticket, so the ~45 bytes add up over a 20-ticket tree
// (completion criterion 4, the scale test's 1KB-a-ticket budget).
type NextResult struct {
	RunID string `json:"-"`
	autopilot.Action
	Worktree string `json:"worktree,omitempty"`
	Command  string `json:"command,omitempty"`
}

// Next decides the run's next action (autopilot next).
func (s *Service) Next(runID string) (NextResult, error) {
	var out NextResult
	err := s.withRun(runID, func(tx *autopilot.Tx, run *autopilot.Run) error {
		if run.State == autopilot.RunStarting {
			return invalidState("run %s is reserved and not adopted yet; run `graph-engine autopilot start %s --mode %s --run %s` first", run.ID, run.RootTicketID, run.Mode, run.ID)
		}
		now := s.now()
		if !run.IsFinal() {
			run.Heartbeat = now
			s.observe(run, now)
		}
		idx, err := s.index(run.ProjectID)
		if err != nil {
			return err
		}
		runs, err := tx.List()
		if err != nil {
			return err
		}
		action := autopilot.Next(autopilot.PlanInput{
			Run: run, Tree: idx.tree(run.RootTicketID, run.Mode),
			ForeignLaunched: foreignLaunched(runs, run.ID, now), Now: now,
		})
		out = NextResult{RunID: run.ID, Action: action}
		out.Worktree, out.Command = s.describe(run, action)
		return nil
	})
	return out, err
}

// describe returns the worktree an action happens in and the command that
// carries it out. A launch action carries no worktree: the launch itself
// prints the one it prepared a moment later, and the path -- often the
// longest thing in the line -- would otherwise be read twice for every
// ticket (the scale test's 1KB-a-ticket budget). A wait keeps it: that is
// where a resumed orchestrator's person can look at the running session.
func (s *Service) describe(run *autopilot.Run, a autopilot.Action) (worktree, command string) {
	switch a.Action {
	case autopilot.ActionLaunch:
		command = fmt.Sprintf("graph-engine autopilot launch %s %s --role %s", run.ID, a.Ticket, a.Role)
	case autopilot.ActionWait:
		command = fmt.Sprintf("graph-engine autopilot wait %s %s", run.ID, a.Ticket)
		worktree = s.sessionWorktree(run, a.Ticket, a.Role)
	case autopilot.ActionMergeUp:
		command = fmt.Sprintf("graph-engine autopilot merge-up %s %s", run.ID, a.Ticket)
	case autopilot.ActionDone, autopilot.ActionStopped:
		command = "graph-engine autopilot summary " + run.ID
	}
	return worktree, command
}

// sessionWorktree is where a session of role for ticketID runs: its own
// worktree for work and finalize (the root's), its merge target's for
// merge-up. "" when the local path is not set.
func (s *Service) sessionWorktree(run *autopilot.Run, ticketID, role string) string {
	repo := ""
	if s.LocalPath != nil {
		repo = s.LocalPath(run.ProjectID)
	}
	if repo == "" {
		return ""
	}
	if role == autopilot.RoleMergeUp {
		if st := run.Ticket(ticketID); st != nil && st.Target != "" {
			ticketID = st.Target
		}
	}
	if st := run.Ticket(ticketID); st != nil && st.Worktree != "" {
		return st.Worktree
	}
	return autopilot.WorktreePath(repo, ticketID)
}

// observe refreshes the activity of the run's running session from the DB
// and its worktree (D4-2).
func (s *Service) observe(run *autopilot.Run, now time.Time) {
	st := run.ActiveSession()
	if st == nil {
		return
	}
	dbFP, wtFP := s.fingerprints(run, st)
	autopilot.ObserveActivity(st, dbFP, wtFP, now)
}

func (s *Service) fingerprints(run *autopilot.Run, st *autopilot.TicketState) (dbFP, wtFP string) {
	if d, err := s.Repo.GetTicketDetail(st.ID); err == nil && d != nil {
		dbFP = autopilot.DBFingerprint(d)
	}
	role := st.Role
	if role == "" {
		role = st.LastRole
	}
	wt := s.sessionWorktree(run, st.ID, role)
	if wt != "" {
		if _, err := os.Stat(wt); err == nil {
			wtFP = s.Git.WorktreeFingerprint(wt)
		}
	}
	return dbFP, wtFP
}

// ---------------------------------------------------------------------------
// launch

// LaunchResult is `autopilot launch`'s output.
type LaunchResult struct {
	Launched   string `json:"launched"`
	Role       string `json:"role"`
	Worktree   string `json:"worktree"`
	Branch     string `json:"branch,omitempty"`
	BaseBranch string `json:"base_branch,omitempty"`
	Next       string `json:"next"`
}

// WorkerPrompt is the prompt a child session starts with.
func WorkerPrompt(runID, ticketID, role string) string {
	return fmt.Sprintf("/graph-ops:autopilot-worker %s %s --role %s", runID, ticketID, role)
}

// Launch prepares ticketID's worktree and opens a child session for role
// in it (autopilot launch). The session is recorded as running before the
// terminal opens -- git and the terminal run outside the registry lock --
// and the record is put back if either fails.
func (s *Service) Launch(runID, ticketID, role string) (LaunchResult, error) {
	if role == "" {
		role = autopilot.RoleWork
	}
	if role != autopilot.RoleWork && role != autopilot.RoleMergeUp && role != autopilot.RoleFinalize {
		return LaunchResult{}, domain.NewAPIError(domain.ErrCodeValidation, "VALIDATION_ERROR: --role must be work, merge-up or finalize, got %q", role)
	}
	projectID, err := s.findRun(runID)
	if err != nil {
		return LaunchResult{}, err
	}
	repo, err := s.localPath(projectID)
	if err != nil {
		return LaunchResult{}, err
	}

	var (
		snapshot                    autopilot.Run
		prevTicket                  autopilot.TicketState
		workDir, base, gitTicket    string
		gitBase                     string
		permissionMode, sessionRole = "", role
	)
	err = s.withRun(runID, func(tx *autopilot.Tx, run *autopilot.Run) error {
		if run.IsFinal() || run.State == autopilot.RunStarting {
			return invalidState("run %s is %s", run.ID, run.State)
		}
		st, err := ticketState(run, ticketID)
		if err != nil {
			return err
		}
		if active := run.ActiveSession(); active != nil {
			return invalidState("run %s already has a %s session running for %s; wait for it (autopilot wait) first", run.ID, active.Role, active.ID)
		}
		snapshot = *run
		prevTicket = *st
		now := s.now()
		switch role {
		case autopilot.RoleWork:
			retry := st.RetryPending && (st.Status == autopilot.TicketFailed || st.Status == autopilot.TicketBlocked)
			if st.Status != autopilot.TicketQueued && !retry {
				return invalidState("ticket %s is %s in run %s, not waiting to be launched", ticketID, st.Status, run.ID)
			}
			if st.Target != "" {
				t := run.Ticket(st.Target)
				if t == nil || t.Branch == "" {
					return invalidState("the merge target %s of %s has no branch", st.Target, ticketID)
				}
				base = t.Branch
			} else {
				base = s.Git.DefaultBranch(repo)
			}
			st.Status, st.Reason, st.Detail, st.FailedRole = autopilot.TicketLaunched, "", "", ""
			st.RetryPending = false
			if st.Merge == autopilot.NotMerged {
				st.Merge = ""
			}
			st.LaunchedGeneration = run.Generation
			if !st.Counted {
				st.Counted = true
				run.WorkLaunches++
			}
			st.Branch = autopilot.BranchFor(ticketID)
			st.Worktree = autopilot.WorktreePath(repo, ticketID)
			st.BaseBranch = base
			gitTicket, gitBase, workDir = ticketID, base, st.Worktree
		case autopilot.RoleMergeUp:
			if st.Status != autopilot.TicketDone || !st.MergeNeedsSession || st.Target == "" {
				return invalidState("ticket %s does not need a merge-up session", ticketID)
			}
			t := run.Ticket(st.Target)
			if t == nil || t.Branch == "" {
				return invalidState("the merge target %s of %s has no branch", st.Target, ticketID)
			}
			gitTicket, workDir = t.ID, t.Worktree
			if workDir == "" {
				workDir = autopilot.WorktreePath(repo, t.ID)
			}
		case autopilot.RoleFinalize:
			if ticketID != run.RootTicketID || run.Mode != autopilot.ModeTree || st.Status != autopilot.TicketDone {
				return invalidState("finalize runs only for the done root of a tree run")
			}
			if run.Finalize != nil && run.Finalize.Status != "" {
				if run.Finalize.Status == autopilot.TicketDone || run.Finalize.Status == autopilot.TicketLaunched {
					return invalidState("finalize is already %s", run.Finalize.Status)
				}
			}
			run.Finalize = &autopilot.Outcome{Status: autopilot.TicketLaunched}
			run.State = autopilot.RunFinalizing
			gitTicket, workDir = ticketID, st.Worktree
			if workDir == "" {
				workDir = autopilot.WorktreePath(repo, ticketID)
			}
		}
		st.Role = sessionRole
		st.LaunchedAt = now
		st.DBFingerprint, st.WorktreeFingerprint = "", ""
		autopilot.RecordActivity(st, autopilot.ActivityLaunch, now)
		run.Heartbeat = now
		permissionMode = run.Settings.PermissionMode
		return nil
	})
	if err != nil {
		return LaunchResult{}, err
	}

	// Outside the lock: git and the terminal can take seconds.
	launchErr := func() error {
		path, _, _, err := s.Git.EnsureWorktree(repo, gitTicket, gitBase)
		if err != nil {
			return err
		}
		workDir = path
		if s.Launcher == nil {
			return errors.New("no terminal launcher configured")
		}
		return s.Launcher.Launch(workDir, []string{"--permission-mode", permissionMode}, WorkerPrompt(runID, ticketID, role))
	}()
	if launchErr != nil {
		_ = s.withRun(runID, func(tx *autopilot.Tx, run *autopilot.Run) error {
			restored := prevTicket
			run.Tickets[ticketID] = &restored
			run.WorkLaunches = snapshot.WorkLaunches
			run.Finalize = snapshot.Finalize
			run.State = snapshot.State
			return nil
		})
		return LaunchResult{}, fmt.Errorf("launching the %s session for %s: %w", role, ticketID, launchErr)
	}

	var out LaunchResult
	err = s.withRun(runID, func(tx *autopilot.Tx, run *autopilot.Run) error {
		st, err := ticketState(run, ticketID)
		if err != nil {
			return err
		}
		if role == autopilot.RoleWork {
			st.Worktree = workDir
		}
		// The baseline the next observation compares against.
		dbFP, wtFP := s.fingerprints(run, st)
		autopilot.ObserveActivity(st, dbFP, wtFP, s.now())
		out = LaunchResult{Launched: ticketID, Role: role, Worktree: workDir, Next: fmt.Sprintf("graph-engine autopilot wait %s %s", runID, ticketID)}
		if role == autopilot.RoleWork {
			out.Branch, out.BaseBranch = st.Branch, st.BaseBranch
		}
		return nil
	})
	return out, err
}

// ---------------------------------------------------------------------------
// wait

// WaitResult is `autopilot wait`'s one line.
type WaitResult struct {
	State               string `json:"state"` // reported / waiting / awaiting_human
	Ticket              string `json:"ticket"`
	Role                string `json:"role,omitempty"`
	Result              string `json:"result,omitempty"`
	Reason              string `json:"reason,omitempty"`
	Detail              string `json:"detail,omitempty"`
	Summary             string `json:"summary,omitempty"`
	Awaiting            string `json:"awaiting,omitempty"`
	IdleMinutes         *int   `json:"idle_minutes,omitempty"`
	StallTimeoutMinutes *int   `json:"stall_timeout_minutes,omitempty"`
}

// Reported reports whether the session ended (the caller exits 0); false
// means the wait timed out (exit 2).
func (w WaitResult) Reported() bool { return w.State == "reported" }

// Wait blocks until ticketID's session reports, fails as unresponsive, or
// timeout (real time) passes (autopilot wait). Every poll refreshes the run's
// heartbeat and the session's activity (D4-2).
func (s *Service) Wait(runID, ticketID string, timeout time.Duration) (WaitResult, error) {
	interval := s.PollInterval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	sleep := s.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	started := time.Now()
	for {
		var res WaitResult
		err := s.withRun(runID, func(tx *autopilot.Tx, run *autopilot.Run) error {
			st, err := ticketState(run, ticketID)
			if err != nil {
				return err
			}
			if st.Role == "" && st.LastRole == "" {
				return invalidState("ticket %s has no session in run %s; launch it first", ticketID, run.ID)
			}
			now := s.now()
			if st.Role != "" {
				if !run.IsFinal() {
					run.Heartbeat = now
				}
				dbFP, wtFP := s.fingerprints(run, st)
				autopilot.ObserveActivity(st, dbFP, wtFP, now)
				autopilot.CheckStall(run, now)
			}
			if st.Role == "" {
				res = reportedResult(run, st)
				return nil
			}
			idle := autopilot.IdleMinutes(st, now)
			stall := run.Settings.StallTimeoutMinutes
			if st.AwaitingHuman != "" {
				res = WaitResult{State: "awaiting_human", Ticket: st.ID, Role: st.Role, Awaiting: st.AwaitingHuman}
			} else {
				res = WaitResult{State: "waiting", Ticket: st.ID, Role: st.Role, IdleMinutes: &idle, StallTimeoutMinutes: &stall}
			}
			return nil
		})
		if err != nil {
			return WaitResult{}, err
		}
		if res.Reported() || time.Since(started) >= timeout {
			return res, nil
		}
		sleep(interval)
	}
}

func reportedResult(run *autopilot.Run, st *autopilot.TicketState) WaitResult {
	res := WaitResult{State: "reported", Ticket: st.ID, Role: st.LastRole}
	switch st.LastRole {
	case autopilot.RoleFinalize:
		if run.Finalize != nil {
			res.Result, res.Reason, res.Detail, res.Summary = run.Finalize.Status, run.Finalize.Reason, run.Finalize.Detail, run.Finalize.Summary
		}
	case autopilot.RoleMergeUp:
		res.Result, res.Reason, res.Detail, res.Summary = st.Status, st.Reason, st.Detail, st.Summary
		if st.Status == autopilot.TicketDone {
			res.Result = "merged"
		}
	default:
		res.Result, res.Reason, res.Detail, res.Summary = st.Status, st.Reason, st.Detail, st.Summary
	}
	return res
}

// ---------------------------------------------------------------------------
// merge-up / merge-into-parent

// MergeResult is merge-up's and merge-into-parent's output.
type MergeResult struct {
	Result       string `json:"result"` // merged / up_to_date / needs_merge_session
	Ticket       string `json:"ticket"`
	Branch       string `json:"branch"`
	TargetBranch string `json:"target_branch"`
	Reason       string `json:"reason,omitempty"`
	// No "next" hint: after merge-up the orchestrator always runs next
	// (the skill says so), and every byte here is read once per merged
	// ticket.
}

func (s *Service) mergeBranches(run *autopilot.Run, st *autopilot.TicketState) (repo, source, target string, err error) {
	if st.Target == "" {
		return "", "", "", domain.NewAPIError(domain.ErrCodeValidation,
			"VALIDATION_ERROR: ticket %s has no parent branch in run %s (a single-mode run or the tree's root is not merged into a parent)", st.ID, run.ID)
	}
	t := run.Ticket(st.Target)
	if t == nil || t.Branch == "" || st.Branch == "" {
		return "", "", "", invalidState("ticket %s or its merge target %s has no branch", st.ID, st.Target)
	}
	repo, err = s.localPath(run.ProjectID)
	return repo, st.Branch, t.Branch, err
}

// MergeUp carries a done ticket's branch -- with everything its subtree
// merged into it -- up into its merge target's branch, fast-forward only
// (autopilot merge-up, D6). When that is not possible (not a fast-forward, or
// the target's worktree is dirty) nothing is changed and the result is
// needs_merge_session: the next `next` launches a merge-up session.
func (s *Service) MergeUp(runID, ticketID string) (MergeResult, error) {
	var out MergeResult
	err := s.withRun(runID, func(tx *autopilot.Tx, run *autopilot.Run) error {
		st, err := ticketState(run, ticketID)
		if err != nil {
			return err
		}
		if st.Status != autopilot.TicketDone || st.Role != "" {
			return invalidState("ticket %s is %s; only a done ticket with no session running is merged up", ticketID, st.Status)
		}
		repo, source, target, err := s.mergeBranches(run, st)
		if err != nil {
			return err
		}
		run.Heartbeat = s.now()
		out = MergeResult{Ticket: ticketID, Branch: source, TargetBranch: target}
		moved, err := s.Git.FastForward(repo, source, target)
		if err != nil {
			var apiErr *domain.APIError
			if errors.As(err, &apiErr) && (apiErr.Code == autopilot.ErrCodeNotFastForward || apiErr.Code == autopilot.ErrCodeParentDirty) {
				st.MergeNeedsSession = true
				out.Result, out.Reason = "needs_merge_session", string(apiErr.Code)
				return nil
			}
			return err
		}
		autopilot.MarkMergedUp(run, st)
		out.Result = "merged"
		if !moved {
			out.Result = "up_to_date"
		}
		return nil
	})
	return out, err
}

// MergeIntoParent fast-forwards the worker's branch into its merge target's
// branch (autopilot merge-into-parent, the tree child's release step). It
// fails -- changing nothing -- with NOT_FAST_FORWARD or
// PARENT_WORKTREE_DIRTY (S9); the worker then reports blocked
// (merge_conflict).
func (s *Service) MergeIntoParent(runID, ticketID string) (MergeResult, error) {
	var out MergeResult
	err := s.withRun(runID, func(tx *autopilot.Tx, run *autopilot.Run) error {
		st, err := ticketState(run, ticketID)
		if err != nil {
			return err
		}
		repo, source, target, err := s.mergeBranches(run, st)
		if err != nil {
			return err
		}
		autopilot.RecordActivity(st, "merge-into-parent", s.now())
		moved, err := s.Git.FastForward(repo, source, target)
		if err != nil {
			return err
		}
		autopilot.MarkMergedSelf(run, st)
		out = MergeResult{Result: "merged", Ticket: ticketID, Branch: source, TargetBranch: target}
		if !moved {
			out.Result = "up_to_date"
		}
		return nil
	})
	return out, err
}

// ---------------------------------------------------------------------------
// Worker-side commands

// WorkerContext is `autopilot worker-context`'s output: what a child session
// needs to know about its place in the run.
type WorkerContext struct {
	RunID    string `json:"run_id"`
	Ticket   string `json:"ticket"`
	Mode     string `json:"mode"`
	RunState string `json:"run_state"`
	// Position: single (ticket mode), tree_root, or tree_child.
	Position string `json:"position"`
	Role     string `json:"role"`
	Branch   string `json:"branch,omitempty"`
	Worktree string `json:"worktree,omitempty"`
	// BaseBranch is what the branch was cut from; TargetBranch is where
	// merge-into-parent (tree child) or a merge-up session puts it. Both
	// are the nearest ancestor this run worked on (S3).
	BaseBranch   string `json:"base_branch,omitempty"`
	TargetBranch string `json:"target_branch,omitempty"`
	TargetTicket string `json:"target_ticket,omitempty"`
	// DefaultBranch is the repository's main branch: where a single run's
	// release and a tree's finalize reflect the work (mainReflection).
	DefaultBranch string `json:"default_branch,omitempty"`
	// MergeSourceBranch / MergeWorktree: for a merge-up session, the branch
	// to merge and the worktree (the target's) to merge it in.
	MergeSourceBranch string             `json:"merge_source_branch,omitempty"`
	MergeWorktree     string             `json:"merge_worktree,omitempty"`
	Settings          autopilot.Settings `json:"settings"`
	PendingDecisions  []string           `json:"pending_decisions"`
}

// WorkerContext returns the child session's context and records the call
// as activity.
func (s *Service) WorkerContext(runID, ticketID string) (WorkerContext, error) {
	var out WorkerContext
	err := s.withRun(runID, func(tx *autopilot.Tx, run *autopilot.Run) error {
		st, err := ticketState(run, ticketID)
		if err != nil {
			return err
		}
		autopilot.RecordActivity(st, "worker-context", s.now())
		role := st.Role
		if role == "" {
			role = st.LastRole
		}
		out = WorkerContext{
			RunID: run.ID, Ticket: st.ID, Mode: run.Mode, RunState: run.State, Role: role,
			Branch: st.Branch, Worktree: st.Worktree, BaseBranch: st.BaseBranch,
			Settings: run.Settings, PendingDecisions: []string{},
		}
		switch {
		case run.Mode == autopilot.ModeTicket:
			out.Position = "single"
		case st.ID == run.RootTicketID:
			out.Position = "tree_root"
		default:
			out.Position = "tree_child"
		}
		if t := run.Ticket(st.Target); t != nil {
			out.TargetTicket, out.TargetBranch = t.ID, t.Branch
			if role == autopilot.RoleMergeUp {
				out.MergeSourceBranch, out.MergeWorktree = st.Branch, t.Worktree
			}
		}
		if out.Position != "tree_child" {
			if repo, err := s.localPath(run.ProjectID); err == nil {
				out.DefaultBranch = s.Git.DefaultBranch(repo)
			}
		}
		for k := range st.PendingDecisions {
			out.PendingDecisions = append(out.PendingDecisions, k)
		}
		sort.Strings(out.PendingDecisions)
		return nil
	})
	return out, err
}

var decisionKindPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,39}$`)

// DecisionArtifactName is the artifact name of an automatic decision of kind
// (D7).
func DecisionArtifactName(kind string) string { return "autopilot-decision-" + kind }

// RecordDecision keeps an automatic decision made before the ticket has a
// node to hold it (the refine decision, D7) in the run until
// AttachDecisions saves it.
func (s *Service) RecordDecision(runID, ticketID, kind, content string) error {
	if !decisionKindPattern.MatchString(kind) {
		return domain.NewAPIError(domain.ErrCodeValidation, "VALIDATION_ERROR: invalid decision kind %q (lowercase letters, digits, '-' and '_')", kind)
	}
	if strings.TrimSpace(content) == "" {
		return domain.NewAPIError(domain.ErrCodeValidation, "VALIDATION_ERROR: the decision content is empty")
	}
	return s.withRun(runID, func(tx *autopilot.Tx, run *autopilot.Run) error {
		st, err := ticketState(run, ticketID)
		if err != nil {
			return err
		}
		if st.PendingDecisions == nil {
			st.PendingDecisions = map[string]string{}
		}
		st.PendingDecisions[kind] = content
		autopilot.RecordActivity(st, "record-decision", s.now())
		return nil
	})
}

// AttachResult is attach-decisions' output.
type AttachResult struct {
	Attached []string `json:"attached"`
	Skipped  []string `json:"skipped"`
}

// AttachDecisions saves the ticket's pending decisions as text artifacts of
// nodeID and clears them. A decision whose artifact name is already on the
// ticket is not saved again, so a call repeated after an interruption does
// not duplicate it (D7).
func (s *Service) AttachDecisions(runID, ticketID, nodeID string) (AttachResult, error) {
	node, err := s.Repo.GetNode(nodeID)
	if err != nil {
		return AttachResult{}, err
	}
	if node == nil {
		return AttachResult{}, domain.NewAPIError(domain.ErrCodeNodeNotFound, "NODE_NOT_FOUND: node not found: %s", nodeID)
	}
	if node.TicketID != ticketID {
		return AttachResult{}, domain.NewAPIError(domain.ErrCodeValidation, "VALIDATION_ERROR: node %s belongs to ticket %s, not %s", nodeID, node.TicketID, ticketID)
	}
	out := AttachResult{Attached: []string{}, Skipped: []string{}}
	err = s.withRun(runID, func(tx *autopilot.Tx, run *autopilot.Run) error {
		st, err := ticketState(run, ticketID)
		if err != nil {
			return err
		}
		autopilot.RecordActivity(st, "attach-decisions", s.now())
		if len(st.PendingDecisions) == 0 {
			return nil
		}
		existing, err := s.Repo.ListArtifactsByTicket(ticketID)
		if err != nil {
			return err
		}
		have := map[string]bool{}
		for _, a := range existing {
			have[a.Name] = true
		}
		kinds := make([]string, 0, len(st.PendingDecisions))
		for k := range st.PendingDecisions {
			kinds = append(kinds, k)
		}
		sort.Strings(kinds)
		for _, k := range kinds {
			name := DecisionArtifactName(k)
			if have[name] {
				out.Skipped = append(out.Skipped, name)
				continue
			}
			content := st.PendingDecisions[k]
			if _, err := s.Repo.CreateArtifact(domain.Artifact{
				ID: engine.NewArtifactID(), TicketID: ticketID, NodeID: nodeID, Name: name,
				Type: domain.ArtifactText, Content: &content,
			}); err != nil {
				return err
			}
			out.Attached = append(out.Attached, name)
		}
		st.PendingDecisions = nil
		return nil
	})
	return out, err
}

// Touch records a sign of activity from the worker (autopilot touch). With
// awaiting non-empty the session enters "waiting for a person" and is exempt
// from the stall check until its next activity (D4-2).
func (s *Service) Touch(runID, ticketID, awaiting string) error {
	return s.withRun(runID, func(tx *autopilot.Tx, run *autopilot.Run) error {
		st, err := ticketState(run, ticketID)
		if err != nil {
			return err
		}
		now := s.now()
		autopilot.RecordActivity(st, "touch", now)
		// Re-baseline the fingerprints, so a change the worker made just
		// before touching does not count as a later activity (which would
		// end the wait for a person at the next poll).
		if st.Role != "" {
			st.DBFingerprint, st.WorktreeFingerprint = s.fingerprints(run, st)
		}
		st.AwaitingHuman = strings.TrimSpace(awaiting)
		return nil
	})
}

var reasonPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

// ReportResult is report's output.
type ReportResult struct {
	Recorded   bool   `json:"recorded"`
	LateReport bool   `json:"late_report,omitempty"`
	Ticket     string `json:"ticket"`
	Result     string `json:"result"`
}

// Report records the result of ticketID's session (autopilot report). The
// summary is cut to 3 lines / 500 characters. A second report for the same
// session overwrites the first (the last one wins), except for a session
// already failed as unresponsive, whose late report is only kept as
// late_report (D4-2).
func (s *Service) Report(runID, ticketID, result, reason, summary string) (ReportResult, error) {
	switch result {
	case autopilot.TicketDone, autopilot.TicketFailed, autopilot.TicketBlocked:
	default:
		return ReportResult{}, domain.NewAPIError(domain.ErrCodeValidation, "VALIDATION_ERROR: --result must be done, failed or blocked, got %q", result)
	}
	if reason != "" && !reasonPattern.MatchString(reason) {
		return ReportResult{}, domain.NewAPIError(domain.ErrCodeValidation, "VALIDATION_ERROR: invalid --reason %q (lowercase letters, digits and '_')", reason)
	}
	if result == autopilot.TicketDone {
		reason = ""
	}
	summary = autopilot.TruncateSummary(summary)
	var out ReportResult
	err := s.withRun(runID, func(tx *autopilot.Tx, run *autopilot.Run) error {
		st, err := ticketState(run, ticketID)
		if err != nil {
			return err
		}
		if st.Role == "" && st.LastRole == "" {
			return invalidState("ticket %s has not been launched in run %s", ticketID, run.ID)
		}
		accepted := autopilot.ApplyOutcome(run, st, result, reason, "", summary)
		out = ReportResult{Recorded: accepted, LateReport: !accepted, Ticket: ticketID, Result: result}
		return nil
	})
	return out, err
}

// ---------------------------------------------------------------------------
// status

// RunStatus is one run in `autopilot status` (and, from phase 5, the runs
// API).
type RunStatus struct {
	RunID     string    `json:"run_id"`
	ProjectID string    `json:"project_id"`
	Mode      string    `json:"mode"`
	Root      string    `json:"root"`
	State     string    `json:"state"`
	Active    bool      `json:"active"`
	Heartbeat time.Time `json:"heartbeat"`
	// Current is the ticket whose session is running, with its role and
	// whether it is waiting for a person.
	Current       string            `json:"current,omitempty"`
	CurrentRole   string            `json:"current_role,omitempty"`
	AwaitingHuman string            `json:"awaiting_human,omitempty"`
	StopReason    string            `json:"stop_reason,omitempty"`
	Tickets       map[string]string `json:"tickets"`
}

// Status lists a project's runs, newest first.
func (s *Service) Status(projectID string) ([]RunStatus, error) {
	runs, err := s.registry().List(projectID)
	if err != nil {
		return nil, err
	}
	now := s.now()
	out := make([]RunStatus, 0, len(runs))
	for i := len(runs) - 1; i >= 0; i-- {
		r := runs[i]
		rs := RunStatus{RunID: r.ID, ProjectID: r.ProjectID, Mode: r.Mode, Root: r.RootTicketID, State: r.State,
			Active: r.IsActive(now), Heartbeat: r.Heartbeat, StopReason: r.StopReason, Tickets: map[string]string{}}
		for id, st := range r.Tickets {
			rs.Tickets[id] = st.Status
		}
		if st := r.ActiveSession(); st != nil {
			rs.Current, rs.CurrentRole, rs.AwaitingHuman = st.ID, st.Role, st.AwaitingHuman
		}
		out = append(out, rs)
	}
	return out, nil
}
