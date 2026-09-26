package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/autopilot"
	"github.com/graph-ops/core-go/internal/autopilot/runner"
	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/runtimeconfig"
	"github.com/graph-ops/core-go/internal/store"
	"github.com/graph-ops/core-go/internal/terminal"
)

// DFLT-00142 phase 3: the `graph-engine autopilot` run subcommands end to
// end, with a fake launcher standing in for the terminal (Gherkin: "偽ランチャー
// で start から summary までを通す").

type fakeLaunch struct {
	workDir string
	args    []string
	prompt  string
}

func autopilotGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// autopilotCLISetup gives a project whose local path is a throwaway git
// repository, and routes launches to a recorder.
func autopilotCLISetup(t *testing.T) (store.GraphRepository, runtimeConfig, string, string, *[]fakeLaunch) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo, projectID := newTestRepoWithProject(t)
	rc := sandboxRC(t)
	gitRepo := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(gitRepo); err == nil {
		gitRepo = resolved
	}
	autopilotGit(t, gitRepo, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(gitRepo, "README.md"), []byte("x\n"), 0o644)
	autopilotGit(t, gitRepo, "add", "README.md")
	autopilotGit(t, gitRepo, "commit", "-q", "-m", "initial")
	if _, err := runtimeconfig.SetProjectPath(rc.HomeDir, projectID, gitRepo); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runtimeconfig.UpdateHome(rc.HomeDir, func(cfg *runtimeconfig.FileConfig) error {
		cfg.AutopilotSettings = map[string]autopilot.LocalSettings{projectID: {"permissionMode": "acceptEdits"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var launches []fakeLaunch
	orig := newAutopilotService
	newAutopilotService = func(repo store.GraphRepository, rc runtimeConfig) *runner.Service {
		svc := orig(repo, rc)
		// No real ps: the orchestrator's Terminal.app tty is faked.
		svc.TerminalTTY = func() string { return "" }
		svc.Launcher = runner.LauncherFunc(func(req runner.LaunchRequest) (terminal.LaunchOutcome, error) {
			launches = append(launches, fakeLaunch{req.WorkDir, append([]string(nil), req.ExtraArgs...), req.Prompt})
			return terminal.LaunchOutcome{}, nil
		})
		return svc
	}
	t.Cleanup(func() { newAutopilotService = orig })
	return repo, rc, projectID, gitRepo, &launches
}

// runAutopilot runs `graph-engine autopilot <args>` and returns its stdout.
func runAutopilot(t *testing.T, repo store.GraphRepository, rc runtimeConfig, stdin string, args ...string) (string, error) {
	t.Helper()
	if stdin != "" {
		orig := autopilotStdin
		autopilotStdin = strings.NewReader(stdin)
		defer func() { autopilotStdin = orig }()
	}
	var err error
	out := captureStdout(t, func() { err = cmdAutopilot(repo, rc, args) })
	return out, err
}

// smallJSON asserts out is one line of JSON under 1KB and decodes it.
func smallJSON(t *testing.T, out string) map[string]any {
	t.Helper()
	trimmed := strings.TrimSpace(out)
	if strings.Contains(trimmed, "\n") || len(out) >= 1024 {
		t.Fatalf("output is not one small line (%d bytes): %s", len(out), out)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(trimmed), &m); err != nil {
		t.Fatalf("not JSON: %v: %s", err, out)
	}
	return m
}

func mustAutopilot(t *testing.T, repo store.GraphRepository, rc runtimeConfig, stdin string, args ...string) map[string]any {
	t.Helper()
	out, err := runAutopilot(t, repo, rc, stdin, args...)
	if err != nil {
		t.Fatalf("autopilot %v: %v", args, err)
	}
	return smallJSON(t, out)
}

func TestAutopilotCLI_StartToSummaryWithAFakeLauncher(t *testing.T) {
	repo, rc, projectID, gitRepo, launches := autopilotCLISetup(t)
	eng := engine.New(repo)
	root, _ := eng.CreateTicket(projectID, "Root", "")
	parent := root.ID
	child, _ := eng.CreateTicketWithOptions(projectID, "Child", "", engine.CreateTicketOptions{ParentTicketID: &parent})

	start := mustAutopilot(t, repo, rc, "", "start", root.ID, "--mode", "tree")
	runID, _ := start["run_id"].(string)
	if runID == "" || start["mode"] != "tree" || start["created"] != true {
		t.Fatalf("start = %v", start)
	}
	if _, err := os.Stat(filepath.Join(rc.HomeDir, ".graph-ops", "autopilot", projectID, "runs", runID+".json")); err != nil {
		t.Fatalf("run file: %v", err)
	}

	next := mustAutopilot(t, repo, rc, "", "next", runID)
	if next["action"] != "launch" || next["ticket"] != root.ID || next["role"] != "work" ||
		next["command"] != "graph-engine autopilot launch "+runID+" "+root.ID+" --role work" {
		t.Fatalf("next = %v", next)
	}
	launch := mustAutopilot(t, repo, rc, "", "launch", runID, root.ID)
	rootWT := filepath.Join(gitRepo, ".claude", "worktrees", root.ID)
	if launch["worktree"] != rootWT || launch["branch"] != "worktree-"+root.ID || launch["base_branch"] != "main" {
		t.Fatalf("launch = %v", launch)
	}
	if len(*launches) != 1 {
		t.Fatalf("launches = %v", *launches)
	}
	l := (*launches)[0]
	if l.workDir != rootWT || strings.Join(l.args, " ") != "--permission-mode acceptEdits" ||
		l.prompt != "/graph-ops:autopilot-worker "+runID+" "+root.ID+" --role work" {
		t.Fatalf("launch call = %+v", l)
	}

	// Not reported yet: wait times out with exit code 2 and one line.
	out, err := runAutopilot(t, repo, rc, "", "wait", runID, root.ID, "--timeout", "1ms")
	var ec exitCodeError
	if !errors.As(err, &ec) || ec.code != 2 {
		t.Fatalf("wait err = %v", err)
	}
	if w := smallJSON(t, out); w["state"] != "waiting" || w["stall_timeout_minutes"] == nil || w["idle_minutes"] == nil {
		t.Fatalf("wait = %v", w)
	}

	// The worker: context, commit, report (summary from stdin).
	ctx := mustAutopilot(t, repo, rc, "", "worker-context", runID, root.ID)
	if ctx["position"] != "tree_root" || ctx["default_branch"] != "main" {
		t.Fatalf("worker-context = %v", ctx)
	}
	os.WriteFile(filepath.Join(rootWT, "root.txt"), []byte("r"), 0o644)
	autopilotGit(t, rootWT, "add", "root.txt")
	autopilotGit(t, rootWT, "commit", "-q", "-m", "root")
	mustAutopilot(t, repo, rc, "line 1\nline 2\nline 3\nline 4\n", "report", runID, root.ID, "--result", "done", "--summary", "-")
	w := mustAutopilot(t, repo, rc, "", "wait", runID, root.ID)
	if w["state"] != "reported" || w["result"] != "done" || w["summary"] != "line 1\nline 2\nline 3" {
		t.Fatalf("wait = %v", w)
	}

	next = mustAutopilot(t, repo, rc, "", "next", runID)
	if next["action"] != "launch" || next["ticket"] != child.ID {
		t.Fatalf("next = %v", next)
	}
	mustAutopilot(t, repo, rc, "", "launch", runID, child.ID, "--role", "work")
	childWT := filepath.Join(gitRepo, ".claude", "worktrees", child.ID)
	os.WriteFile(filepath.Join(childWT, "child.txt"), []byte("c"), 0o644)
	autopilotGit(t, childWT, "add", "child.txt")
	autopilotGit(t, childWT, "commit", "-q", "-m", "child")
	m := mustAutopilot(t, repo, rc, "", "merge-into-parent", runID, child.ID)
	if m["result"] != "merged" || m["target_branch"] != "worktree-"+root.ID {
		t.Fatalf("merge-into-parent = %v", m)
	}
	mustAutopilot(t, repo, rc, "", "touch", runID, child.ID)
	mustAutopilot(t, repo, rc, "", "report", runID, child.ID, "--result", "done", "--summary", "child done")
	mustAutopilot(t, repo, rc, "", "wait", runID, child.ID)

	next = mustAutopilot(t, repo, rc, "", "next", runID)
	if next["action"] != "merge-up" || next["ticket"] != child.ID {
		t.Fatalf("next = %v", next)
	}
	if mu := mustAutopilot(t, repo, rc, "", "merge-up", runID, child.ID); mu["result"] != "up_to_date" && mu["result"] != "merged" {
		t.Fatalf("merge-up = %v", mu)
	}
	next = mustAutopilot(t, repo, rc, "", "next", runID)
	if next["action"] != "done" {
		t.Fatalf("next = %v", next)
	}

	summary, err := runAutopilot(t, repo, rc, "", "summary", runID)
	if err != nil || !strings.HasPrefix(summary, "# Autopilot summary") || !strings.Contains(summary, child.ID) || !strings.Contains(summary, "child done") {
		t.Fatalf("summary: %v\n%s", err, summary)
	}

	status, err := runAutopilot(t, repo, rc, "", "status", "--project", projectID)
	if err != nil || !strings.Contains(status, runID) || !strings.Contains(status, `"finished"`) {
		t.Fatalf("status: %v\n%s", err, status)
	}
}

func TestAutopilotCLI_RefusesADoubleStart(t *testing.T) {
	repo, rc, projectID, _, _ := autopilotCLISetup(t)
	eng := engine.New(repo)
	root, _ := eng.CreateTicket(projectID, "Root", "")
	parent := root.ID
	child, _ := eng.CreateTicketWithOptions(projectID, "Child", "", engine.CreateTicketOptions{ParentTicketID: &parent})
	mustAutopilot(t, repo, rc, "", "start", root.ID, "--mode", "tree")
	_, err := runAutopilot(t, repo, rc, "", "start", child.ID, "--mode", "ticket")
	assertAPIErrorCode(t, err, autopilot.ErrCodeAlreadyRunning)
}

func TestAutopilotCLI_LaunchWithoutLocalPath(t *testing.T) {
	repo, rc, projectID, _, launches := autopilotCLISetup(t)
	if _, err := runtimeconfig.SetProjectPath(rc.HomeDir, projectID, ""); err != nil {
		t.Fatal(err)
	}
	root, _ := engine.New(repo).CreateTicket(projectID, "Root", "")
	start := mustAutopilot(t, repo, rc, "", "start", root.ID, "--mode", "ticket")
	runID := start["run_id"].(string)
	mustAutopilot(t, repo, rc, "", "next", runID)
	_, err := runAutopilot(t, repo, rc, "", "launch", runID, root.ID)
	assertAPIErrorCode(t, err, autopilot.ErrCodeLocalPathNotSet)
	if len(*launches) != 0 {
		t.Fatal("launched")
	}
}

func TestAutopilotCLI_UsageErrors(t *testing.T) {
	repo, _ := newTestRepoWithProject(t)
	rc := sandboxRC(t)
	for name, args := range map[string][]string{
		"start without mode":   {"start", "T-1"},
		"start bad mode":       {"start", "T-1", "--mode", "forest"},
		"next without run":     {"next"},
		"launch extra":         {"launch", "run-x", "T-1", "extra"},
		"launch unknown flag":  {"launch", "run-x", "T-1", "--force", "x"},
		"wait bad timeout":     {"wait", "run-x", "T-1", "--timeout", "soon"},
		"report no result":     {"report", "run-x", "T-1", "--summary", "x"},
		"report no summary":    {"report", "run-x", "T-1", "--result", "done"},
		"touch empty awaiting": {"touch", "run-x", "T-1", "--awaiting-human", " "},
		"record too few":       {"record-decision", "run-x", "T-1", "refine"},
		"unknown":              {"frobnicate"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := runAutopilot(t, repo, rc, "", args...); err == nil ||
				!strings.Contains(err.Error(), "usage") && !strings.Contains(err.Error(), "unknown autopilot subcommand") {
				t.Fatalf("err = %v", err)
			}
		})
	}
	// A run id that does not exist, or is not a run id at all.
	_, err := runAutopilot(t, repo, rc, "", "next", "run-20260101-000000-deadbeef")
	assertAPIErrorCode(t, err, autopilot.ErrCodeRunNotFound)
	_, err = runAutopilot(t, repo, rc, "", "next", "../../etc")
	assertAPIErrorCode(t, err, domain.ErrCodeValidation)
}

// DFLT-00154: a Terminal.app tab that falls back to a new window warns on
// stderr only; launch's stdout stays the one JSON line the orchestrator
// reads, and the start's tty reaches the launcher.
func TestAutopilotCLI_TabFallbackWarnsOnStderrOnly(t *testing.T) {
	repo, rc, projectID, _, _ := autopilotCLISetup(t)
	var reqs []runner.LaunchRequest
	withFakes := newAutopilotService
	newAutopilotService = func(repo store.GraphRepository, rc runtimeConfig) *runner.Service {
		svc := withFakes(repo, rc)
		svc.TerminalTTY = func() string { return "/dev/ttys003" }
		svc.Launcher = runner.LauncherFunc(func(req runner.LaunchRequest) (terminal.LaunchOutcome, error) {
			reqs = append(reqs, req)
			return terminal.LaunchOutcome{TabError: "osascript: not authorized (-1743)", DisableTab: true}, nil
		})
		return svc
	}
	t.Cleanup(func() { newAutopilotService = withFakes })

	root, _ := engine.New(repo).CreateTicket(projectID, "Root", "")
	start := mustAutopilot(t, repo, rc, "", "start", root.ID, "--mode", "ticket")
	runID := start["run_id"].(string)
	mustAutopilot(t, repo, rc, "", "next", runID)
	var out string
	var err error
	stderr := captureStderr(t, func() { out, err = runAutopilot(t, repo, rc, "", "launch", runID, root.ID) })
	if err != nil {
		t.Fatal(err)
	}
	launch := smallJSON(t, out)
	if launch["launched"] != root.ID || strings.Contains(out, "osascript") {
		t.Fatalf("launch stdout = %q", out)
	}
	if !strings.Contains(stderr, "graph-engine: warning: ") || !strings.Contains(stderr, "-1743") {
		t.Fatalf("stderr = %q", stderr)
	}
	if len(reqs) != 1 || reqs[0].TerminalTTY != "/dev/ttys003" || reqs[0].SkipTab {
		t.Fatalf("launch requests = %+v", reqs)
	}
}

// DFLT-00182: start and launch print untrusted_folder only when the fake
// home's ~/.claude.json shows the folder is not trusted; the run goes on
// the same either way. The home is the sandbox's temp directory, never the
// real one.
func TestAutopilotCLI_UntrustedFolderNotice(t *testing.T) {
	cases := []struct {
		name      string
		trusted   func(gitRepo string) string // the trusted key; "" writes no file
		untrusted bool
	}{
		{name: "untrusted", trusted: func(string) string { return "/some/other/project" }, untrusted: true},
		{name: "trusted", trusted: func(gitRepo string) string { return gitRepo }},
		{name: "only a parent folder above the repository is trusted", trusted: func(gitRepo string) string { return filepath.Dir(gitRepo) }, untrusted: true},
		{name: "no file", trusted: func(string) string { return "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CLAUDE_CONFIG_DIR", "")
			repo, rc, projectID, gitRepo, launches := autopilotCLISetup(t)
			if key := tc.trusted(gitRepo); key != "" {
				b, _ := json.Marshal(map[string]any{"projects": map[string]any{key: map[string]any{"hasTrustDialogAccepted": true}}})
				if err := os.WriteFile(filepath.Join(rc.HomeDir, ".claude.json"), b, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			root, _ := engine.New(repo).CreateTicket(projectID, "Root", "")

			start := mustAutopilot(t, repo, rc, "", "start", root.ID, "--mode", "ticket")
			runID, _ := start["run_id"].(string)
			if runID == "" || start["created"] != true {
				t.Fatalf("start = %v", start)
			}
			got, present := start["untrusted_folder"]
			if present != tc.untrusted || (tc.untrusted && got != gitRepo) {
				t.Fatalf("start untrusted_folder = %v (present %v), want present %v", got, present, tc.untrusted)
			}

			mustAutopilot(t, repo, rc, "", "next", runID)
			launch := mustAutopilot(t, repo, rc, "", "launch", runID, root.ID)
			rootWT := filepath.Join(gitRepo, ".claude", "worktrees", root.ID)
			if launch["worktree"] != rootWT || len(*launches) != 1 {
				t.Fatalf("launch = %v, launches = %v", launch, *launches)
			}
			got, present = launch["untrusted_folder"]
			if present != tc.untrusted || (tc.untrusted && got != rootWT) {
				t.Fatalf("launch untrusted_folder = %v (present %v), want present %v", got, present, tc.untrusted)
			}
		})
	}
}
