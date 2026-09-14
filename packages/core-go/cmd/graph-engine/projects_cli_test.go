package main

import (
	"testing"

	"github.com/graph-ops/core-go/internal/engine"
)

// TestCmdCreateProject_DefaultsWorkdirAndAutoPrefix covers create-project
// with no --workdir/--prefix: workdir defaults to something non-empty
// (cwd), and prefix is auto-derived from the name.
func TestCmdCreateProject_DefaultsWorkdirAndAutoPrefix(t *testing.T) {
	repo := newTestRepo(t)

	out := captureStdout(t, func() {
		if err := cmdCreateProject(repo, []string{"My Project"}); err != nil {
			t.Fatalf("cmdCreateProject: %v", err)
		}
	})
	if !contains(out, `"prefix": "MYPRO"`) {
		t.Fatalf("expected auto-derived prefix MYPRO in output, got %q", out)
	}

	projects, err := repo.ListProjects()
	if err != nil || len(projects) != 1 {
		t.Fatalf("expected 1 project, got %d (err=%v)", len(projects), err)
	}
	if projects[0].WorkDir == "" {
		t.Errorf("expected a non-empty default work_dir, got %+v", projects[0])
	}
}

func TestCmdCreateProject_ExplicitPrefixAndWorkdir(t *testing.T) {
	repo := newTestRepo(t)
	workDir := t.TempDir()

	if err := cmdCreateProject(repo, []string{"P", "--prefix", "ABCDE", "--workdir", workDir}); err != nil {
		t.Fatalf("cmdCreateProject: %v", err)
	}
	projects, _ := repo.ListProjects()
	if len(projects) != 1 || projects[0].Prefix != "ABCDE" || projects[0].WorkDir != workDir {
		t.Fatalf("unexpected project: %+v", projects)
	}
}

// TestCmdUseProject_SetsCurrentProject covers use-project switching the
// current project, and create-ticket subsequently defaulting to it without
// an explicit --project flag.
func TestCmdUseProject_SetsCurrentProject(t *testing.T) {
	repo := newTestRepo(t)
	eng := engine.New(repo)

	if err := cmdCreateProject(repo, []string{"P", "--prefix", "ABCDE", "--workdir", t.TempDir()}); err != nil {
		t.Fatalf("cmdCreateProject: %v", err)
	}
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
	proj, err := repo.CreateProject("P", "ABCDE", t.TempDir())
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
	repo.CreateProject("Project A", "", t.TempDir())
	repo.CreateProject("Project B", "", t.TempDir())

	out := captureStdout(t, func() {
		if err := cmdListProjects(repo); err != nil {
			t.Fatalf("cmdListProjects: %v", err)
		}
	})
	if !contains(out, "Project A") || !contains(out, "Project B") {
		t.Fatalf("expected both projects listed, got %q", out)
	}
}
