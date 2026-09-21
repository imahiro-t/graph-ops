package main

import (
	"encoding/json"
	"testing"

	"github.com/graph-ops/core-go/internal/currentproject"
	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/runtimeconfig"
)

// sandboxRC is a runtimeConfig whose home config file is confined to a fresh
// temp dir, for commands that write it (create-project's projectPaths,
// use-project's currentProjectId). WorkDir is a temp dir too, so a command
// that looks at the working directory (create-ticket's project resolution,
// the leftover graph-config.json warning) never sees the repository.
func sandboxRC(t *testing.T) runtimeConfig {
	t.Helper()
	return runtimeConfig{WorkDir: t.TempDir(), HomeDir: t.TempDir()}
}

func savedProjectPaths(t *testing.T, rc runtimeConfig) map[string]string {
	t.Helper()
	cfg, err := runtimeconfig.LoadHomeConfig(rc.HomeDir)
	if err != nil {
		t.Fatalf("runtimeconfig.LoadHomeConfig: %v", err)
	}
	return cfg.ProjectPaths
}

// savedCurrentProjectID returns the home config file's currentProjectId as
// stored: nil when the key is absent ("never set here, inherit from the data
// source once"), otherwise a pointer to the value -- including "", which
// means "deliberately deselected" (DFLT-00106).
func savedCurrentProjectID(t *testing.T, rc runtimeConfig) *string {
	t.Helper()
	cfg, err := runtimeconfig.LoadHomeConfig(rc.HomeDir)
	if err != nil {
		t.Fatalf("runtimeconfig.LoadHomeConfig: %v", err)
	}
	return cfg.CurrentProjectID
}

// TestCmdCreateProject_DefaultsWorkdirAndAutoPrefix covers create-project
// with no --workdir/--prefix: the local path defaults to the cwd
// (rc.WorkDir) and is saved to the home config's projectPaths, not the DB,
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
	// WorkDir "/work" does not exist; the config file is under HomeDir either way.

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
//
// Since DFLT-00106 the selection goes to this environment's
// home config file rather than the DB's app_state, so the assertion is on
// the settings file -- and on the DB row NOT moving, which is what keeps a
// colleague on the same shared data source out of it.
func TestCmdUseProject_SetsCurrentProject(t *testing.T) {
	repo := newTestRepo(t)
	eng := engine.New(repo)
	rc := sandboxRC(t)

	captureStdout(t, func() {
		if err := cmdCreateProject(repo, rc, []string{"P", "--prefix", "ABCDE", "--workdir", t.TempDir()}); err != nil {
			t.Fatalf("cmdCreateProject: %v", err)
		}
	})
	projects, _ := repo.ListProjects()
	proj := projects[0]

	captureStdout(t, func() {
		if err := cmdUseProject(repo, rc, []string{proj.ID}); err != nil {
			t.Fatalf("cmdUseProject: %v", err)
		}
	})
	if cur := savedCurrentProjectID(t, rc); cur == nil || *cur != proj.ID {
		t.Fatalf("expected the home config's currentProjectId to be %q, got %v", proj.ID, cur)
	}
	if cur, err := repo.GetCurrentProjectID(); err != nil || cur != "" {
		t.Fatalf("use-project must not write the shared data source, got %q (err=%v)", cur, err)
	}

	if err := cmdCreateTicket(eng, repo, rc, []string{"title"}); err != nil {
		t.Fatalf("cmdCreateTicket without --project should default to current project: %v", err)
	}
	tickets, _ := repo.ListTicketsByProject(proj.ID)
	if len(tickets) != 1 || tickets[0].ID != "ABCDE-00001" {
		t.Fatalf("expected ticket ABCDE-00001 under the current project, got %+v", tickets)
	}
}

// TestCmdUseProject_UnknownProjectKeepsSelection covers a typo'd id failing
// before anything is written, leaving the previous selection intact.
func TestCmdUseProject_UnknownProjectKeepsSelection(t *testing.T) {
	repo := newTestRepo(t)
	rc := sandboxRC(t)
	proj, err := repo.CreateProject("P", "ABCDE")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := currentproject.Set(rc.HomeDir, proj.ID); err != nil {
		t.Fatalf("currentproject.Set: %v", err)
	}

	if err := cmdUseProject(repo, rc, []string{"proj-missing"}); err == nil {
		t.Fatal("expected use-project with an unknown project id to fail")
	}
	if cur := savedCurrentProjectID(t, rc); cur == nil || *cur != proj.ID {
		t.Fatalf("expected currentProjectId to stay %q, got %v", proj.ID, cur)
	}
}

// TestCmdUseProject_DoesNotAffectAnotherEnvironment is DFLT-00106's headline
// completion criterion at the CLI level: two environments (two
// home config files) on ONE data source, one of them running
// use-project, and the other's `create-ticket` without --project still
// landing in its own project. Before this, both read the same app_state row.
func TestCmdUseProject_DoesNotAffectAnotherEnvironment(t *testing.T) {
	repo := newTestRepo(t)
	eng := engine.New(repo)
	envA, envB := sandboxRC(t), sandboxRC(t)

	alpha, err := repo.CreateProject("Alpha", "ALPHA")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	beta, err := repo.CreateProject("Beta", "BETA0")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := currentproject.Set(envB.HomeDir, beta.ID); err != nil {
		t.Fatalf("currentproject.Set: %v", err)
	}

	captureStdout(t, func() {
		if err := cmdUseProject(repo, envA, []string{alpha.ID}); err != nil {
			t.Fatalf("cmdUseProject: %v", err)
		}
	})

	// Env B's cwd (a fresh temp dir) matches no project's local path, so
	// resolution falls through to env B's own current project.
	captureStderr(t, func() {
		captureStdout(t, func() {
			if err := cmdCreateTicket(eng, repo, envB, []string{"タイトル"}); err != nil {
				t.Fatalf("cmdCreateTicket: %v", err)
			}
		})
	})

	if tickets, err := repo.ListTicketsByProject(beta.ID); err != nil || len(tickets) != 1 {
		t.Errorf("expected env B's ticket in Beta, got %+v (err=%v)", tickets, err)
	}
	if tickets, err := repo.ListTicketsByProject(alpha.ID); err != nil || len(tickets) != 0 {
		t.Errorf("env A's use-project must not have redirected env B, got %+v (err=%v)", tickets, err)
	}
	if got := savedCurrentProjectID(t, envB); got == nil || *got != beta.ID {
		t.Errorf("env B's currentProjectId = %v, want it unchanged at %q", got, beta.ID)
	}
}

// TestCmdCreateTicket_NoCurrentProjectFailsWithoutFlag covers the "no
// project selected" error path when neither --project nor a current project
// is available.
func TestCmdCreateTicket_NoCurrentProjectFailsWithoutFlag(t *testing.T) {
	repo := newTestRepo(t)
	eng := engine.New(repo)

	err := cmdCreateTicket(eng, repo, sandboxRC(t), []string{"title"})
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

	if err := cmdCreateTicket(eng, repo, sandboxRC(t), []string{"title", "desc", "--project", proj.ID}); err != nil {
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
