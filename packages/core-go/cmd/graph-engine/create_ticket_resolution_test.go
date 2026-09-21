package main

// Tests for DFLT-00025: create-ticket resolving its target project from the
// CLI's current directory before falling back to the current project. Each
// test names the Gherkin rule/scenario (art-0a57c869) it covers.
//
// Since DFLT-00080 a project's directory is its local path in this
// environment's home config file (projectPaths), not a DB column, so the
// tests keep the paths in standardProjects.paths and hand that map to
// create-ticket as runtimeConfig.ProjectPaths.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/currentproject"
	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/store"
)

// resolutionStubRepo wraps a real repository, overriding ListProjects -- for
// a project list in a given order, or a failure the real store can't be made
// to produce (ListProjects erroring).
//
// Since DFLT-00106 it no longer needs to override GetCurrentProjectID: the
// current project is this environment's home config setting, so a
// state the DB refuses to hold (a dangling id) is now simply written to the
// sandboxed settings file -- see standardProjects.setCurrentID.
type resolutionStubRepo struct {
	store.GraphRepository
	listProjects       func() ([]domain.Project, error)
	listProjectsCalled int
}

func (r *resolutionStubRepo) ListProjects() ([]domain.Project, error) {
	r.listProjectsCalled++
	if r.listProjects != nil {
		return r.listProjects()
	}
	return r.GraphRepository.ListProjects()
}

// standardProjects is the "標準のプロジェクト構成": the spec's logical ids
// (proj-ops/proj-blg) mapped to the real, store-generated project ids.
type standardProjects struct {
	repo store.GraphRepository
	ids  map[string]string
	// paths is the environment's projectPaths: real project ID -> local path.
	paths map[string]string
	// home is this environment's sandboxed home directory, i.e. where its
	// home config file (and with it currentProjectId, DFLT-00106) lives.
	// Every runCreateTicket call passes it as rc.HomeDir, so no test can
	// reach the real one.
	home string
}

func (sp standardProjects) id(t *testing.T, logical string) string {
	t.Helper()
	id, ok := sp.ids[logical]
	if !ok {
		t.Fatalf("unknown logical project %q", logical)
	}
	return id
}

func (sp standardProjects) add(t *testing.T, logical, name, localPath string) domain.Project {
	t.Helper()
	p, err := sp.repo.CreateProject(name, "")
	if err != nil {
		t.Fatalf("CreateProject(%s, %q): %v", logical, localPath, err)
	}
	sp.ids[logical] = p.ID
	sp.paths[p.ID] = localPath
	return p
}

// setCurrent selects a project for this environment the way `use-project`
// does: in the home config file, not in the DB (DFLT-00106).
func (sp standardProjects) setCurrent(t *testing.T, logical string) {
	t.Helper()
	sp.setCurrentID(t, sp.id(t, logical))
}

// setCurrentID is setCurrent for an id that need not name a real project --
// a dangling currentProjectId, which is exactly what a project deleted by
// somebody else on a shared data source leaves behind here.
func (sp standardProjects) setCurrentID(t *testing.T, id string) {
	t.Helper()
	if err := currentproject.Set(sp.home, id); err != nil {
		t.Fatalf("currentproject.Set(%q): %v", id, err)
	}
}

// currentProjectID is what cmdCreateTicket hands resolveCreateTicketProject:
// this environment's own current project.
func (sp standardProjects) currentProjectID() func() (string, error) {
	return func() (string, error) { return currentproject.Get(sp.home, sp.repo, nil) }
}

func (sp standardProjects) ticketCount(t *testing.T, logical string) int {
	t.Helper()
	tickets, err := sp.repo.ListTicketsByProject(sp.id(t, logical))
	if err != nil {
		t.Fatalf("ListTicketsByProject: %v", err)
	}
	return len(tickets)
}

// skipOnWindows skips tests that use virtual Unix-style absolute paths
// (/work/graph-ops), which filepath.IsAbs rejects on Windows.
func skipOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("virtual Unix-style absolute paths are not absolute on Windows")
	}
}

func newBareResolutionEnv(t *testing.T) standardProjects {
	t.Helper()
	skipOnWindows(t)
	return standardProjects{repo: newTestRepo(t), ids: map[string]string{}, paths: map[string]string{}, home: t.TempDir()}
}

func newStandardResolutionEnv(t *testing.T) standardProjects {
	t.Helper()
	sp := newBareResolutionEnv(t)
	sp.add(t, "proj-ops", "GraphOps", "/work/graph-ops")
	sp.add(t, "proj-blg", "TECH BLOG", "/work/tech-blog")
	return sp
}

type cliResult struct {
	stdout string
	stderr string
	err    error
}

// runCreateTicket runs cmdCreateTicket with rc.WorkDir = cwd,
// rc.ProjectPaths = paths and rc.HomeDir = home against repo, capturing
// stdout and stderr separately. home is always the environment's sandboxed
// one (standardProjects.home), so the fallback to the current project reads
// and writes a temp home config file rather than the developer's own.
func runCreateTicket(t *testing.T, repo store.GraphRepository, paths map[string]string, home, cwd string, args ...string) cliResult {
	t.Helper()
	eng := engine.New(repo)
	var res cliResult
	res.stderr = captureStderr(t, func() {
		res.stdout = captureStdout(t, func() {
			res.err = cmdCreateTicket(eng, repo, runtimeConfig{WorkDir: cwd, HomeDir: home, ProjectPaths: paths}, args)
		})
	})
	return res
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close pipe writer: %v", err)
	}
	var sb strings.Builder
	tmp := make([]byte, 4096)
	for {
		n, err := r.Read(tmp)
		sb.Write(tmp[:n])
		if err != nil {
			break
		}
	}
	return sb.String()
}

// mustSucceed asserts success and that stdout is exactly one ticket JSON
// document, returning the parsed ticket.
func mustSucceed(t *testing.T, res cliResult) domain.Ticket {
	t.Helper()
	if res.err != nil {
		t.Fatalf("expected create-ticket to succeed, got error: %v (stderr=%q)", res.err, res.stderr)
	}
	var ticket domain.Ticket
	dec := json.NewDecoder(strings.NewReader(res.stdout))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&ticket); err != nil {
		t.Fatalf("stdout is not a ticket JSON document: %v\nstdout=%q", err, res.stdout)
	}
	if rest := strings.TrimSpace(res.stdout[dec.InputOffset():]); rest != "" {
		t.Fatalf("stdout has trailing output after the ticket JSON: %q", rest)
	}
	return ticket
}

func mustFail(t *testing.T, res cliResult) {
	t.Helper()
	if res.err == nil {
		t.Fatalf("expected create-ticket to fail, it succeeded (stdout=%q)", res.stdout)
	}
	if res.stdout != "" {
		t.Errorf("expected empty stdout on failure, got %q", res.stdout)
	}
}

func assertProject(t *testing.T, sp standardProjects, ticket domain.Ticket, logical string) {
	t.Helper()
	if want := sp.id(t, logical); ticket.ProjectID != want {
		t.Fatalf("expected ticket in %s (%s), got project_id %q", logical, want, ticket.ProjectID)
	}
}

func assertNoTicketsAnywhere(t *testing.T, repo store.GraphRepository) {
	t.Helper()
	tickets, err := repo.ListTickets()
	if err != nil {
		t.Fatalf("ListTickets: %v", err)
	}
	if len(tickets) != 0 {
		t.Fatalf("expected no tickets in any project, got %+v", tickets)
	}
}

func assertNoCurrentProjectMessage(t *testing.T, err error) {
	t.Helper()
	msg := err.Error()
	for _, want := range []string{"no current project selected", "create-project", "use-project", "--project", "projectPaths"} {
		if !strings.Contains(msg, want) {
			t.Errorf("expected error message to mention %q, got %q", want, msg)
		}
	}
	if strings.Contains(msg, "work_dir") {
		t.Errorf("error message must not mention the removed work_dir, got %q", msg)
	}
}

func stringPtr(s string) *string { return &s }

// ---------------------------------------------------------------------------
// Rule 1: a cwd match beats the current project.
// ---------------------------------------------------------------------------

func TestCreateTicketResolution_CwdExactMatchBeatsCurrentProject(t *testing.T) {
	sp := newStandardResolutionEnv(t)
	sp.setCurrent(t, "proj-blg")

	ticket := mustSucceed(t, runCreateTicket(t, sp.repo, sp.paths, sp.home, "/work/graph-ops", "タイトル", "説明"))
	assertProject(t, sp, ticket, "proj-ops")
	if n := sp.ticketCount(t, "proj-blg"); n != 0 {
		t.Fatalf("expected no ticket in proj-blg, got %d", n)
	}
}

func TestCreateTicketResolution_CwdSubdirectoryMatches(t *testing.T) {
	sp := newStandardResolutionEnv(t)
	sp.setCurrent(t, "proj-blg")

	ticket := mustSucceed(t, runCreateTicket(t, sp.repo, sp.paths, sp.home, "/work/graph-ops/packages/core-go/cmd", "タイトル"))
	assertProject(t, sp, ticket, "proj-ops")
}

func TestCreateTicketResolution_CwdMatchWithoutCurrentProject(t *testing.T) {
	sp := newStandardResolutionEnv(t)

	ticket := mustSucceed(t, runCreateTicket(t, sp.repo, sp.paths, sp.home, "/work/tech-blog", "タイトル"))
	assertProject(t, sp, ticket, "proj-blg")
}

func TestCreateTicketResolution_DoesNotChangeCurrentProject(t *testing.T) {
	sp := newStandardResolutionEnv(t)
	sp.setCurrent(t, "proj-blg")

	ticket := mustSucceed(t, runCreateTicket(t, sp.repo, sp.paths, sp.home, "/work/graph-ops", "タイトル"))
	assertProject(t, sp, ticket, "proj-ops")
	cur, err := sp.currentProjectID()()
	if err != nil || cur != sp.id(t, "proj-blg") {
		t.Fatalf("expected current project to stay proj-blg, got %q (err=%v)", cur, err)
	}
}

// ---------------------------------------------------------------------------
// Rule 2: nested local paths -> the deepest match wins.
// ---------------------------------------------------------------------------

func TestCreateTicketResolution_NestedLocalPathDeepestWins(t *testing.T) {
	cases := []struct {
		cwd  string
		want string
	}{
		{"/work/graph-ops/packages/sub", "proj-sub"},
		{"/work/graph-ops/packages/sub/src/lib", "proj-sub"},
		{"/work/graph-ops/packages", "proj-ops"},
		{"/work/graph-ops", "proj-ops"},
	}
	for _, tc := range cases {
		t.Run(tc.cwd, func(t *testing.T) {
			sp := newStandardResolutionEnv(t)
			sp.add(t, "proj-sub", "サブ", "/work/graph-ops/packages/sub")
			sp.setCurrent(t, "proj-blg")

			ticket := mustSucceed(t, runCreateTicket(t, sp.repo, sp.paths, sp.home, tc.cwd, "タイトル"))
			assertProject(t, sp, ticket, tc.want)
		})
	}
}

func TestCreateTicketResolution_DeepestWinsRegardlessOfListOrder(t *testing.T) {
	for _, shallowFirst := range []bool{true, false} {
		name := "shallow-first"
		if !shallowFirst {
			name = "deep-first"
		}
		t.Run(name, func(t *testing.T) {
			sp := newStandardResolutionEnv(t)
			sub := sp.add(t, "proj-sub", "サブ", "/work/graph-ops/packages/sub")
			ops, err := sp.repo.GetProject(sp.id(t, "proj-ops"))
			if err != nil || ops == nil {
				t.Fatalf("GetProject(proj-ops): %v", err)
			}
			ordered := []domain.Project{*ops, sub}
			if !shallowFirst {
				ordered = []domain.Project{sub, *ops}
			}
			stub := &resolutionStubRepo{
				GraphRepository: sp.repo,
				listProjects:    func() ([]domain.Project, error) { return ordered, nil },
			}

			ticket := mustSucceed(t, runCreateTicket(t, stub, sp.paths, sp.home, "/work/graph-ops/packages/sub", "タイトル"))
			assertProject(t, sp, ticket, "proj-sub")
		})
	}
}

// ---------------------------------------------------------------------------
// Rule 3: separator-boundary matching and Clean normalization.
// ---------------------------------------------------------------------------

func TestCreateTicketResolution_SeparatorBoundary(t *testing.T) {
	cases := []struct {
		localPath string
		cwd       string
		want      string
	}{
		{"/a/foo", "/a/foobar", "proj-blg"},
		{"/a/foo", "/a/foobar/baz", "proj-blg"},
		{"/a/foo", "/a/foo", "proj-foo"},
		{"/a/foo", "/a/foo/bar", "proj-foo"},
		{"/a/foo/bar", "/a/foo", "proj-blg"},
	}
	for _, tc := range cases {
		t.Run(tc.localPath+"@"+tc.cwd, func(t *testing.T) {
			sp := newStandardResolutionEnv(t)
			sp.add(t, "proj-foo", "Foo", tc.localPath)
			sp.setCurrent(t, "proj-blg")

			ticket := mustSucceed(t, runCreateTicket(t, sp.repo, sp.paths, sp.home, tc.cwd, "タイトル"))
			assertProject(t, sp, ticket, tc.want)
		})
	}
}

func TestCreateTicketResolution_LocalPathIsCleaned(t *testing.T) {
	cases := []struct {
		localPath string
		cwd       string
	}{
		{"/a/foo/", "/a/foo"},
		{"/a/foo//", "/a/foo/bar"},
		{"/a/x/../foo", "/a/foo"},
		{"/a/./foo", "/a/foo/bar"},
	}
	for _, tc := range cases {
		t.Run(tc.localPath+"@"+tc.cwd, func(t *testing.T) {
			sp := newStandardResolutionEnv(t)
			sp.add(t, "proj-foo", "Foo", tc.localPath)
			sp.setCurrent(t, "proj-blg")

			ticket := mustSucceed(t, runCreateTicket(t, sp.repo, sp.paths, sp.home, tc.cwd, "タイトル"))
			assertProject(t, sp, ticket, "proj-foo")
		})
	}
}

// ---------------------------------------------------------------------------
// Rule 4: empty / relative local paths are never candidates. SetProjectPath
// refuses to persist a relative one, so they are injected straight into the
// projectPaths map (as a hand-edited home config would look), with the
// projects themselves added through a ListProjects stub.
// ---------------------------------------------------------------------------

type extraProject struct {
	ID, Name, Path string
}

func withExtraProjects(sp standardProjects, extras ...extraProject) *resolutionStubRepo {
	var extra []domain.Project
	for _, e := range extras {
		extra = append(extra, domain.Project{ID: e.ID, Name: e.Name})
		sp.paths[e.ID] = e.Path
	}
	return &resolutionStubRepo{
		GraphRepository: sp.repo,
		listProjects: func() ([]domain.Project, error) {
			real, err := sp.repo.ListProjects()
			if err != nil {
				return nil, err
			}
			// Put the invalid ones first so a buggy "first match wins" would
			// pick them.
			return append(append([]domain.Project{}, extra...), real...), nil
		},
	}
}

func TestCreateTicketResolution_EmptyLocalPathNeverMatches(t *testing.T) {
	sp := newStandardResolutionEnv(t)
	sp.setCurrent(t, "proj-blg")
	stub := withExtraProjects(sp, extraProject{ID: "proj-empty", Name: "Empty", Path: ""})

	ticket := mustSucceed(t, runCreateTicket(t, stub, sp.paths, sp.home, "/somewhere/else", "タイトル"))
	assertProject(t, sp, ticket, "proj-blg")
}

func TestCreateTicketResolution_RelativeLocalPathExcluded(t *testing.T) {
	cases := []struct {
		localPath string
		cwd       string
	}{
		{".", "/somewhere/else"},
		{"graph-ops", "/somewhere/graph-ops"},
		{"./tech-blog2", "/somewhere"},
	}
	for _, tc := range cases {
		t.Run(tc.localPath+"@"+tc.cwd, func(t *testing.T) {
			sp := newStandardResolutionEnv(t)
			sp.setCurrent(t, "proj-blg")
			stub := withExtraProjects(sp, extraProject{ID: "proj-rel", Name: "Rel", Path: tc.localPath})

			ticket := mustSucceed(t, runCreateTicket(t, stub, sp.paths, sp.home, tc.cwd, "タイトル"))
			assertProject(t, sp, ticket, "proj-blg")
		})
	}
}

// In the real CLI rc.WorkDir is os.Getwd(), so a relative local path resolved
// with filepath.Abs would land exactly on the cwd and match. The virtual
// cwd paths above can't expose that (Abs resolves against the test
// process's real cwd), so this chdirs to a symlink-resolved D and passes D
// as rc.WorkDir too.
func TestCreateTicketResolution_RelativeLocalPathExcludedEvenWhenItResolvesToCwd(t *testing.T) {
	sp := newStandardResolutionEnv(t)
	sp.setCurrent(t, "proj-blg")
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if err := os.Mkdir(filepath.Join(d, "sub"), 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	t.Chdir(d)
	if got, err := os.Getwd(); err != nil || got != d {
		t.Fatalf("precondition: expected os.Getwd() == %q after chdir, got %q (err=%v)", d, got, err)
	}

	for _, wd := range []string{".", "sub"} {
		t.Run(wd, func(t *testing.T) {
			cwd := d
			if wd == "sub" {
				cwd = filepath.Join(d, "sub")
			}
			stub := withExtraProjects(sp, extraProject{ID: "proj-rel", Name: "Rel", Path: wd})
			ticket := mustSucceed(t, runCreateTicket(t, stub, sp.paths, sp.home, cwd, "タイトル"))
			assertProject(t, sp, ticket, "proj-blg")
		})
	}
}

func TestCreateTicketResolution_ExcludedProjectsDoNotBlockAbsoluteMatch(t *testing.T) {
	sp := newStandardResolutionEnv(t)
	sp.setCurrent(t, "proj-blg")
	stub := withExtraProjects(sp,
		extraProject{ID: "proj-rel", Name: "Rel", Path: "."},
		extraProject{ID: "proj-empty", Name: "Empty", Path: ""},
	)

	ticket := mustSucceed(t, runCreateTicket(t, stub, sp.paths, sp.home, "/work/graph-ops", "タイトル"))
	assertProject(t, sp, ticket, "proj-ops")
}

// ---------------------------------------------------------------------------
// Rule 5: fallback and errors (projects registered).
// ---------------------------------------------------------------------------

func TestCreateTicketResolution_NoCwdMatchFallsBackToCurrentProject(t *testing.T) {
	sp := newStandardResolutionEnv(t)
	sp.setCurrent(t, "proj-blg")

	ticket := mustSucceed(t, runCreateTicket(t, sp.repo, sp.paths, sp.home, "/tmp/unrelated", "タイトル"))
	assertProject(t, sp, ticket, "proj-blg")
}

func TestCreateTicketResolution_NoCwdMatchNoCurrentProjectFails(t *testing.T) {
	sp := newStandardResolutionEnv(t)

	res := runCreateTicket(t, sp.repo, sp.paths, sp.home, "/tmp/unrelated", "タイトル")
	mustFail(t, res)
	assertNoCurrentProjectMessage(t, res.err)
	assertNoTicketsAnywhere(t, sp.repo)
}

func TestCreateTicketResolution_ListProjectsErrorDoesNotFallBack(t *testing.T) {
	sp := newStandardResolutionEnv(t)
	sp.setCurrent(t, "proj-blg")
	stub := &resolutionStubRepo{
		GraphRepository: sp.repo,
		listProjects:    func() ([]domain.Project, error) { return nil, errors.New("boom: projects table unreadable") },
	}

	res := runCreateTicket(t, stub, sp.paths, sp.home, "/work/graph-ops", "タイトル")
	mustFail(t, res)
	if !strings.Contains(res.err.Error(), "boom: projects table unreadable") {
		t.Errorf("expected the ListProjects error to be surfaced, got %q", res.err)
	}
	if n := sp.ticketCount(t, "proj-blg"); n != 0 {
		t.Fatalf("expected no ticket in proj-blg, got %d", n)
	}
}

// A dangling currentProjectId is what a project another member deleted from
// the shared data source leaves behind in this environment's
// home config file (DFLT-00106) -- nothing stops the file from naming a
// project that no longer exists, so it is written directly here.
func TestCreateTicketResolution_DanglingCurrentProjectFails(t *testing.T) {
	sp := newStandardResolutionEnv(t)
	sp.setCurrentID(t, "proj-gone")

	res := runCreateTicket(t, sp.repo, sp.paths, sp.home, "/tmp/unrelated", "タイトル")
	mustFail(t, res)
	if !strings.Contains(res.err.Error(), "proj-gone") {
		t.Errorf("expected error to mention proj-gone, got %q", res.err)
	}
	assertNoTicketsAnywhere(t, sp.repo)
}

// With rc.WorkDir empty, the process's real cwd must not be used implicitly.
// D is symlink-resolved and the precondition os.Getwd() == D is asserted: on
// macOS t.TempDir() lives under /var -> /private/var, and without this a
// buggy os.Getwd()-based implementation would miss the match and pass anyway.
func TestCreateTicketResolution_EmptyCwdIgnoresProcessCwd(t *testing.T) {
	sp := newStandardResolutionEnv(t)
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	sp.add(t, "proj-tmp", "Tmp", d)
	t.Chdir(d)
	if got, err := os.Getwd(); err != nil || got != d {
		t.Fatalf("precondition: expected os.Getwd() == %q after chdir, got %q (err=%v)", d, got, err)
	}
	if m := findProjectForDir(mustListProjects(t, sp.repo), sp.paths, d); m == nil || m.ID != sp.id(t, "proj-tmp") {
		t.Fatalf("precondition: D should match proj-tmp if it were used as the cwd, got %+v", m)
	}
	sp.setCurrent(t, "proj-blg")

	ticket := mustSucceed(t, runCreateTicket(t, sp.repo, sp.paths, sp.home, "", "タイトル"))
	assertProject(t, sp, ticket, "proj-blg")
	if n := sp.ticketCount(t, "proj-tmp"); n != 0 {
		t.Fatalf("expected no ticket in proj-tmp, got %d", n)
	}
}

func mustListProjects(t *testing.T, repo store.GraphRepository) []domain.Project {
	t.Helper()
	projects, err := repo.ListProjects()
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	return projects
}

// ---------------------------------------------------------------------------
// Rule 6: no projects registered at all.
// ---------------------------------------------------------------------------

func TestCreateTicketResolution_NoProjectsFails(t *testing.T) {
	sp := newBareResolutionEnv(t)

	res := runCreateTicket(t, sp.repo, sp.paths, sp.home, "/work/graph-ops", "タイトル")
	mustFail(t, res)
	assertNoCurrentProjectMessage(t, res.err)
	assertNoTicketsAnywhere(t, sp.repo)
}

// ---------------------------------------------------------------------------
// Rule 7: explicit --project wins, with no cwd lookup and no stderr output.
// ---------------------------------------------------------------------------

func TestCreateTicketResolution_ExplicitProjectBeatsCwdMatch(t *testing.T) {
	sp := newStandardResolutionEnv(t)
	sp.setCurrent(t, "proj-ops")

	ticket := mustSucceed(t, runCreateTicket(t, sp.repo, sp.paths, sp.home, "/work/graph-ops", "タイトル", "説明", "--project", sp.id(t, "proj-blg")))
	assertProject(t, sp, ticket, "proj-blg")
}

func TestCreateTicketResolution_ExplicitProjectSkipsListProjects(t *testing.T) {
	sp := newStandardResolutionEnv(t)
	stub := &resolutionStubRepo{
		GraphRepository: sp.repo,
		listProjects:    func() ([]domain.Project, error) { return nil, errors.New("must not be called") },
	}

	ticket := mustSucceed(t, runCreateTicket(t, stub, sp.paths, sp.home, "/work/graph-ops", "タイトル", "--project", sp.id(t, "proj-blg")))
	assertProject(t, sp, ticket, "proj-blg")
	if stub.listProjectsCalled != 0 {
		t.Fatalf("expected ListProjects not to be called with --project, called %d time(s)", stub.listProjectsCalled)
	}
}

func TestCreateTicketResolution_ExplicitProjectWritesNothingToStderr(t *testing.T) {
	sp := newStandardResolutionEnv(t)
	sp.setCurrent(t, "proj-ops")

	res := runCreateTicket(t, sp.repo, sp.paths, sp.home, "/work/graph-ops", "タイトル", "--project", sp.id(t, "proj-blg"))
	ticket := mustSucceed(t, res)
	assertProject(t, sp, ticket, "proj-blg")
	if res.stderr != "" {
		t.Fatalf("expected no stderr output with --project, got %q", res.stderr)
	}
}

// ---------------------------------------------------------------------------
// Rule 8: with --project omitted, a success prints one resolution line on
// stderr while stdout stays the ticket JSON alone.
// ---------------------------------------------------------------------------

func singleStderrLine(t *testing.T, stderr string) string {
	t.Helper()
	lines := strings.Split(strings.TrimRight(stderr, "\n"), "\n")
	if stderr == "" || len(lines) != 1 {
		t.Fatalf("expected exactly one stderr line, got %q", stderr)
	}
	return lines[0]
}

func TestCreateTicketResolution_StderrReportsCwdResolution(t *testing.T) {
	sp := newStandardResolutionEnv(t)
	sp.setCurrent(t, "proj-blg")

	res := runCreateTicket(t, sp.repo, sp.paths, sp.home, "/work/graph-ops", "タイトル")
	ticket := mustSucceed(t, res)
	assertProject(t, sp, ticket, "proj-ops")
	line := singleStderrLine(t, res.stderr)
	for _, want := range []string{"GraphOps", sp.id(t, "proj-ops"), string(resolvedFromCurrentDirectory)} {
		if !strings.Contains(line, want) {
			t.Errorf("expected stderr line to contain %q, got %q", want, line)
		}
	}
}

func TestCreateTicketResolution_StderrReportsCurrentProjectFallback(t *testing.T) {
	sp := newStandardResolutionEnv(t)
	sp.setCurrent(t, "proj-blg")

	res := runCreateTicket(t, sp.repo, sp.paths, sp.home, "/tmp/unrelated", "タイトル")
	ticket := mustSucceed(t, res)
	assertProject(t, sp, ticket, "proj-blg")
	line := singleStderrLine(t, res.stderr)
	for _, want := range []string{"TECH BLOG", sp.id(t, "proj-blg"), string(resolvedFromCurrentProject)} {
		if !strings.Contains(line, want) {
			t.Errorf("expected stderr line to contain %q, got %q", want, line)
		}
	}
	if strings.Contains(line, string(resolvedFromCurrentDirectory)) {
		t.Errorf("fallback line must not claim a current-directory match: %q", line)
	}
}

// ---------------------------------------------------------------------------
// findProjectForDir unit checks not reachable through the CLI scenarios.
// ---------------------------------------------------------------------------

func TestFindProjectForDir_EdgeCases(t *testing.T) {
	skipOnWindows(t)
	projects := []domain.Project{{ID: "root"}, {ID: "a"}}
	paths := map[string]string{"root": "/", "a": "/a"}
	if got := findProjectForDir(projects, paths, "/b/c"); got == nil || got.ID != "root" {
		t.Errorf("expected a root local path to contain /b/c, got %+v", got)
	}
	if got := findProjectForDir(projects, paths, "/a/b"); got == nil || got.ID != "a" {
		t.Errorf("expected /a to beat / for /a/b, got %+v", got)
	}
	if got := findProjectForDir(projects, paths, "a/b"); got != nil {
		t.Errorf("expected a relative dir to match nothing, got %+v", got)
	}
	if got := findProjectForDir(projects, paths, ""); got != nil {
		t.Errorf("expected an empty dir to match nothing, got %+v", got)
	}
	if got := findProjectForDir(projects, paths, "/a/b/../../a/"); got == nil || got.ID != "a" {
		t.Errorf("expected dir to be cleaned before comparison, got %+v", got)
	}
	if got := findProjectForDir(nil, paths, "/a"); got != nil {
		t.Errorf("expected no match with no projects, got %+v", got)
	}
}

// DFLT-00080: two environments sharing one DB, each with its own
// projectPaths, both resolve their own directory to the same project.
func TestCreateTicketResolution_TwoEnvironmentsResolveSameProject(t *testing.T) {
	sp := newBareResolutionEnv(t)
	shared := sp.add(t, "proj-shared", "Shared", "/home/a/shared")
	sp.add(t, "proj-other", "Other", "/elsewhere")
	sp.setCurrent(t, "proj-other")
	envA := map[string]string{shared.ID: "/home/a/shared"}
	envB := map[string]string{shared.ID: "/home/b/shared"}

	for _, tc := range []struct {
		name  string
		paths map[string]string
		cwd   string
	}{{"A", envA, "/home/a/shared/src"}, {"B", envB, "/home/b/shared"}} {
		p, source, err := resolveCreateTicketProject(sp.repo, tc.cwd, sp.home, tc.paths, sp.currentProjectID())
		if err != nil || p == nil || p.ID != shared.ID || source != resolvedFromCurrentDirectory {
			t.Errorf("env %s: got %+v via %q (err %v), want Shared from the current directory", tc.name, p, source, err)
		}
	}
	// Env A's path means nothing in env B.
	if p, source, _ := resolveCreateTicketProject(sp.repo, "/home/a/shared", sp.home, envB, sp.currentProjectID()); p == nil || p.ID == shared.ID || source != resolvedFromCurrentProject {
		t.Errorf("env B must not resolve env A's directory to Shared, got %+v via %q", p, source)
	}
}
