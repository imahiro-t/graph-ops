package main

import (
	"encoding/json"
	"testing"

	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/runtimeconfig"
)

// sandboxRC is a runtimeConfig whose graph-config.json lookup (WorkDir, then
// HomeDir) is confined to fresh temp dirs, for commands that write the local
// settings file (create-project's projectPaths).
func sandboxRC(t *testing.T) runtimeConfig {
	t.Helper()
	return runtimeConfig{WorkDir: t.TempDir(), HomeDir: t.TempDir()}
}

func savedProjectPaths(t *testing.T, rc runtimeConfig) map[string]string {
	t.Helper()
	cfg, _, err := runtimeconfig.Load(rc.WorkDir, rc.HomeDir)
	if err != nil {
		t.Fatalf("runtimeconfig.Load: %v", err)
	}
	return cfg.ProjectPaths
}

// TestCmdCreateProject_DefaultsWorkdirAndAutoPrefix covers create-project
// with no --workdir/--prefix: the local path defaults to the cwd
// (rc.WorkDir) and is saved to graph-config.json's projectPaths, not the DB,
// and prefix is auto-derived from the name.
func TestCmdCreateProject_DefaultsWorkdirAndAutoPrefix(t *testing.T) {
	repo := newTestRepo(t)
	rc := sandboxRC(t)

	out := captureStdout(t, func() {
		if err := cmdCreateProject(repo, rc, []string{"My Project"}); err != nil {
			t.Fatalf("cmdCreateProject: %v", err)
		}
	})
	if !contains(out, `"prefix": "MYPRO"`) {
		t.Fatalf("expected auto-derived prefix MYPRO in output, got %q", out)
	}
	if contains(out, "work_dir") {
		t.Errorf("output must not carry work_dir: %q", out)
	}

	projects, err := repo.ListProjects()
	if err != nil || len(projects) != 1 {
		t.Fatalf("expected 1 project, got %d (err=%v)", len(projects), err)
	}
	if got := savedProjectPaths(t, rc)[projects[0].ID]; got != rc.WorkDir {
		t.Errorf("projectPaths[%s] = %q, want the cwd %q", projects[0].ID, got, rc.WorkDir)
	}
}

func TestCmdCreateProject_ExplicitPrefixAndWorkdir(t *testing.T) {
	repo := newTestRepo(t)
	rc := sandboxRC(t)
	workDir := t.TempDir()

	out := captureStdout(t, func() {
		if err := cmdCreateProject(repo, rc, []string{"P", "--prefix", "ABCDE", "--workdir", workDir}); err != nil {
			t.Fatalf("cmdCreateProject: %v", err)
		}
	})
	projects, _ := repo.ListProjects()
	if len(projects) != 1 || projects[0].Prefix != "ABCDE" {
		t.Fatalf("unexpected project: %+v", projects)
	}
	if got := savedProjectPaths(t, rc)[projects[0].ID]; got != workDir {
		t.Errorf("projectPaths[%s] = %q, want %q", projects[0].ID, got, workDir)
	}
	var printed cliProject
	if err := json.Unmarshal([]byte(out), &printed); err != nil || printed.LocalPath != workDir {
		t.Errorf("output local_path = %q (err %v), want %q", printed.LocalPath, err, workDir)
	}
}

// A relative --workdir is resolved against the cwd and cleaned.
func TestCmdCreateProject_RelativeWorkdirResolvedAgainstCwd(t *testing.T) {
	skipOnWindows(t)
	repo := newTestRepo(t)
	rc := runtimeConfig{WorkDir: "/work", HomeDir: t.TempDir()}
	// WorkDir "/work" does not exist, so graph-config.json resolves under HomeDir.

	captureStdout(t, func() {
		if err := cmdCreateProject(repo, rc, []string{"Eta", "--workdir", "eta/../eta2"}); err != nil {
			t.Fatalf("cmdCreateProject: %v", err)
		}
	})
	projects, _ := repo.ListProjects()
	if len(projects) != 1 || projects[0].Name != "Eta" {
		t.Fatalf("unexpected projects: %+v", projects)
	}
	if got := savedProjectPaths(t, rc)[projects[0].ID]; got != "/work/eta2" {
		t.Errorf("projectPaths[%s] = %q, want /work/eta2", projects[0].ID, got)
	}
}

// TestCmdUseProject_SetsCurrentProject covers use-project switching the
// current project, and create-ticket subsequently defaulting to it without
// an explicit --project flag.
func TestCmdUseProject_SetsCurrentProject(t *testing.T) {
	repo := newTestRepo(t)
	eng := engine.New(repo)

	captureStdout(t, func() {
		if err := cmdCreateProject(repo, sandboxRC(t), []string{"P", "--prefix", "ABCDE", "--workdir", t.TempDir()}); err != nil {
			t.Fatalf("cmdCreateProject: %v", err)
		}
	})
	projects, _ := repo.ListProjects()
	proj := projects[0]

	if err := cmdUseProject(repo, []string{proj.ID}); err != nil {
		t.Fatalf("cmdUseProject: %v", err)
	}
	cur, err := repo.GetCurrentProjectID()
	if err != nil || cur != proj.ID {
		t.Fatalf("expected current project %q, got %q (err=%v)", proj.ID, cur, err)
	}

	if err := cmdCreateTicket(eng, repo, runtimeConfig{}, []string{"title"}); err != nil {
		t.Fatalf("cmdCreateTicket without --project should default to current project: %v", err)
	}
	tickets, _ := repo.ListTicketsByProject(proj.ID)
	if len(tickets) != 1 || tickets[0].ID != "ABCDE-00001" {
		t.Fatalf("expected ticket ABCDE-00001 under the current project, got %+v", tickets)
	}
}

// TestCmdCreateTicket_NoCurrentProjectFailsWithoutFlag covers the "no
// project selected" error path when neither --project nor a current project
// is available.
func TestCmdCreateTicket_NoCurrentProjectFailsWithoutFlag(t *testing.T) {
	repo := newTestRepo(t)
	eng := engine.New(repo)

	err := cmdCreateTicket(eng, repo, runtimeConfig{}, []string{"title"})
	if err == nil {
		t.Fatal("expected an error when no project is selected and none is passed via --project")
	}
}

// TestCmdCreateTicket_ExplicitProjectFlag covers --project overriding
// whatever the current project is (or isn't).
func TestCmdCreateTicket_ExplicitProjectFlag(t *testing.T) {
	repo := newTestRepo(t)
	eng := engine.New(repo)
	proj, err := repo.CreateProject("P", "ABCDE")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	if err := cmdCreateTicket(eng, repo, runtimeConfig{}, []string{"title", "desc", "--project", proj.ID}); err != nil {
		t.Fatalf("cmdCreateTicket: %v", err)
	}
	tickets, _ := repo.ListTicketsByProject(proj.ID)
	if len(tickets) != 1 {
		t.Fatalf("expected 1 ticket under the explicit project, got %d", len(tickets))
	}
}

func TestCmdListProjects(t *testing.T) {
	repo := newTestRepo(t)
	a, _ := repo.CreateProject("Project A", "")
	repo.CreateProject("Project B", "")
	rc := runtimeConfig{ProjectPaths: map[string]string{a.ID: "/work/alpha"}}

	out := captureStdout(t, func() {
		if err := cmdListProjects(repo, rc); err != nil {
			t.Fatalf("cmdListProjects: %v", err)
		}
	})
	if !contains(out, "Project A") || !contains(out, "Project B") {
		t.Fatalf("expected both projects listed, got %q", out)
	}
	if contains(out, "work_dir") {
		t.Errorf("output must not carry work_dir: %q", out)
	}
	var listed []map[string]any
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := map[string]string{"Project A": "/work/alpha", "Project B": ""}
	for _, p := range listed {
		name := p["name"].(string)
		if lp, ok := p["local_path"]; !ok || lp != want[name] {
			t.Errorf("%s: local_path = %v, want %q", name, lp, want[name])
		}
	}
}
