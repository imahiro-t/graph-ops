package main

// DFLT-00138: `graph-engine list-labels` and `graph-engine create-label`,
// exercised against every storage backend (forEachBackend: sqlite, MySQL via
// ./dev/mysql/test.sh, HTTP data source), plus their project resolution
// (shared with create-ticket), usage errors and help text.

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/store"
)

// runLabelCmd runs list-labels (cmd == "list-labels") or create-label
// against repo with the given runtime config, capturing stdout and stderr.
func runLabelCmd(t *testing.T, repo store.GraphRepository, rc runtimeConfig, cmd string, args ...string) cliResult {
	t.Helper()
	var res cliResult
	res.stderr = captureStderr(t, func() {
		res.stdout = captureStdout(t, func() {
			switch cmd {
			case "list-labels":
				res.err = cmdListLabels(repo, rc, args)
			case "create-label":
				res.err = cmdCreateLabel(engine.New(repo), repo, rc, args)
			default:
				t.Fatalf("unknown command %q", cmd)
			}
		})
	})
	return res
}

// mustLabelOK asserts success and returns stdout. stderr is left to the
// caller, since whether it should be empty depends on --project.
func mustLabelOK(t *testing.T, res cliResult) string {
	t.Helper()
	if res.err != nil {
		t.Fatalf("expected success, got %v (stderr=%q)", res.err, res.stderr)
	}
	return res.stdout
}

func decodeLabelUsages(t *testing.T, out string) []domain.LabelUsage {
	t.Helper()
	var got []domain.LabelUsage
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout is not a JSON label array: %v\n%s", err, out)
	}
	return got
}

func decodeCreatedLabel(t *testing.T, out string) domain.Label {
	t.Helper()
	var got domain.Label
	dec := json.NewDecoder(strings.NewReader(out))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("stdout is not a label JSON document: %v\n%s", err, out)
	}
	return got
}

func mustCreateColoredLabel(t *testing.T, repo store.GraphRepository, projectID, name string, color domain.LabelColor) domain.Label {
	t.Helper()
	l, err := repo.CreateLabel(projectID, name, string(color))
	if err != nil {
		t.Fatalf("CreateLabel(%q, %q): %v", name, color, err)
	}
	return l
}

func projectLabels(t *testing.T, repo store.GraphRepository, projectID string) []domain.LabelUsage {
	t.Helper()
	labels, err := repo.ListLabelsByProject(projectID)
	if err != nil {
		t.Fatalf("ListLabelsByProject: %v", err)
	}
	return labels
}

func assertAPIErrorCode(t *testing.T, err error, code domain.ErrorCode) {
	t.Helper()
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != code {
		t.Fatalf("err = %v (%T), want an APIError with code %s", err, err, code)
	}
}

// --- list-labels ------------------------------------------------------------

func TestListLabels_AllBackends(t *testing.T) {
	forEachBackend(t, func(t *testing.T, repo store.GraphRepository, projectID string) {
		rc := runtimeConfig{HomeDir: t.TempDir()}

		// No labels: exactly "[]".
		out := mustLabelOK(t, runLabelCmd(t, repo, rc, "list-labels", "--project", projectID))
		if strings.TrimSpace(out) != "[]" {
			t.Fatalf("empty project: stdout = %q, want []", out)
		}

		bug := mustCreateColoredLabel(t, repo, projectID, "bug", domain.LabelColorRed)
		mustCreateColoredLabel(t, repo, projectID, "Feature", domain.LabelColorBlue)
		mustCreateColoredLabel(t, repo, projectID, "docs", domain.LabelColorGray)
		for i := 0; i < 2; i++ {
			if _, err := repo.CreateTicket(projectID, domain.Ticket{
				Title: fmt.Sprintf("T%d", i), Status: domain.TicketTODO, Priority: domain.TicketPriorityMedium,
				Labels: []domain.Label{{ID: bug.ID}},
			}); err != nil {
				t.Fatalf("CreateTicket: %v", err)
			}
		}
		other, err := repo.CreateProject(fmt.Sprintf("Other %s", projectID), "")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		t.Cleanup(func() { _ = repo.DeleteProject(other.ID) })
		mustCreateColoredLabel(t, repo, other.ID, "other", domain.LabelColorGray)

		res := runLabelCmd(t, repo, rc, "list-labels", "--project", projectID)
		out = mustLabelOK(t, res)
		if res.stderr != "" {
			t.Errorf("--project given: stderr = %q, want nothing", res.stderr)
		}
		var raw []map[string]any
		if err := json.Unmarshal([]byte(out), &raw); err != nil {
			t.Fatalf("stdout is not JSON: %v", err)
		}
		for _, item := range raw {
			for _, key := range []string{"id", "project_id", "name", "color", "ticket_count"} {
				if _, ok := item[key]; !ok {
					t.Errorf("label %v lacks %q", item, key)
				}
			}
		}
		got := decodeLabelUsages(t, out)
		var names []string
		for _, l := range got {
			names = append(names, l.Name)
			want := 0
			if l.Name == "bug" {
				want = 2
			}
			if l.TicketCount != want {
				t.Errorf("%s ticket_count = %d, want %d", l.Name, l.TicketCount, want)
			}
			if l.ProjectID != projectID {
				t.Errorf("%s belongs to %s, want %s", l.Name, l.ProjectID, projectID)
			}
		}
		if strings.Join(names, ",") != "bug,docs,Feature" {
			t.Errorf("order = %v, want bug,docs,Feature", names)
		}
	})
}

func TestListLabels_UnknownProject(t *testing.T) {
	forEachBackend(t, func(t *testing.T, repo store.GraphRepository, projectID string) {
		res := runLabelCmd(t, repo, runtimeConfig{HomeDir: t.TempDir()}, "list-labels", "--project", "no-such-project")
		assertAPIErrorCode(t, res.err, domain.ErrCodeProjectNotFound)
		if res.stdout != "" {
			t.Errorf("stdout = %q, want nothing", res.stdout)
		}
	})
}

func TestListLabels_UsageErrors(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	rc := runtimeConfig{HomeDir: t.TempDir()}
	for _, args := range [][]string{
		{"--project"},
		{"--project", ""},
		{"--unknown"},
		{"extra-arg", "--project", projectID},
		{"--color", "red", "--project", projectID},
		{"--project", projectID, "--project", projectID},
	} {
		res := runLabelCmd(t, repo, rc, "list-labels", args...)
		if res.err == nil || !strings.Contains(res.err.Error(), listLabelsUsageLine) {
			t.Errorf("list-labels %q: err = %v, want a usage error", args, res.err)
		}
		if res.stdout != "" {
			t.Errorf("list-labels %q: stdout = %q, want nothing", args, res.stdout)
		}
	}
}

// --- create-label -----------------------------------------------------------

func TestCreateLabel_AllBackends(t *testing.T) {
	forEachBackend(t, func(t *testing.T, repo store.GraphRepository, projectID string) {
		rc := runtimeConfig{HomeDir: t.TempDir()}

		// Explicit color.
		res := runLabelCmd(t, repo, rc, "create-label", "bug", "--color", "red", "--project", projectID)
		created := decodeCreatedLabel(t, mustLabelOK(t, res))
		if res.stderr != "" {
			t.Errorf("--project given: stderr = %q, want nothing", res.stderr)
		}
		if created.ID == "" || created.ProjectID != projectID || created.Name != "bug" || created.Color != domain.LabelColorRed {
			t.Fatalf("unexpected created label %+v", created)
		}
		listed := projectLabels(t, repo, projectID)
		if len(listed) != 1 || listed[0].ID != created.ID || listed[0].Color != domain.LabelColorRed {
			t.Fatalf("ListLabelsByProject = %+v, want just the created label", listed)
		}

		// Surrounding whitespace is trimmed; 50 characters is fine.
		l := decodeCreatedLabel(t, mustLabelOK(t, runLabelCmd(t, repo, rc, "create-label", "  needs review  ", "--color", "teal", "--project", projectID)))
		if l.Name != "needs review" {
			t.Errorf("name = %q, want %q", l.Name, "needs review")
		}
		mustLabelOK(t, runLabelCmd(t, repo, rc, "create-label", strings.Repeat("x", 50), "--color", "gray", "--project", projectID))
	})
}

func TestCreateLabel_OmittedColor_AllBackends(t *testing.T) {
	forEachBackend(t, func(t *testing.T, repo store.GraphRepository, projectID string) {
		rc := runtimeConfig{HomeDir: t.TempDir()}
		create := func(name string) domain.Label {
			t.Helper()
			return decodeCreatedLabel(t, mustLabelOK(t, runLabelCmd(t, repo, rc, "create-label", name, "--project", projectID)))
		}

		// Another project's colors don't count.
		other, err := repo.CreateProject(fmt.Sprintf("Other %s", projectID), "")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		t.Cleanup(func() { _ = repo.DeleteProject(other.ID) })
		mustCreateColoredLabel(t, repo, other.ID, "x", domain.LabelColorGray)

		if l := create("first"); l.Color != domain.LabelColorGray {
			t.Errorf("empty project: color = %q, want gray", l.Color)
		}
		mustCreateColoredLabel(t, repo, projectID, "b", domain.LabelColorRed)
		if l := create("c"); l.Color != domain.LabelColorOrange {
			t.Errorf("gray+red used: color = %q, want orange", l.Color)
		}
	})
}

func TestCreateLabel_PaletteExhausted_AllBackends(t *testing.T) {
	forEachBackend(t, func(t *testing.T, repo store.GraphRepository, projectID string) {
		for i, c := range domain.LabelColors {
			mustCreateColoredLabel(t, repo, projectID, fmt.Sprintf("c%d", i), c)
		}
		mustCreateColoredLabel(t, repo, projectID, "gray2", domain.LabelColorGray)

		l := decodeCreatedLabel(t, mustLabelOK(t, runLabelCmd(t, repo, runtimeConfig{HomeDir: t.TempDir()}, "create-label", "extra", "--project", projectID)))
		if l.Color != domain.LabelColorRed {
			t.Errorf("color = %q, want red (least used, earliest in the palette)", l.Color)
		}
		if n := len(projectLabels(t, repo, projectID)); n != 12 {
			t.Errorf("label count = %d, want 12", n)
		}
	})
}

func TestCreateLabel_ValidationCreatesNothing_AllBackends(t *testing.T) {
	forEachBackend(t, func(t *testing.T, repo store.GraphRepository, projectID string) {
		rc := runtimeConfig{HomeDir: t.TempDir()}
		mustCreateColoredLabel(t, repo, projectID, "Bug", domain.LabelColorRed)
		cases := []struct {
			args []string
			code domain.ErrorCode
		}{
			{[]string{"", "--color", "red"}, domain.ErrCodeInvalidLabelName},
			{[]string{"   ", "--color", "red"}, domain.ErrCodeInvalidLabelName},
			{[]string{"   "}, domain.ErrCodeInvalidLabelName},
			{[]string{strings.Repeat("x", 51), "--color", "red"}, domain.ErrCodeInvalidLabelName},
			{[]string{"ok", "--color", "crimson"}, domain.ErrCodeInvalidLabelColor},
			{[]string{"ok", "--color", "Red"}, domain.ErrCodeInvalidLabelColor},
			{[]string{"bug", "--color", "blue"}, domain.ErrCodeLabelNameTaken},
			{[]string{"bug"}, domain.ErrCodeLabelNameTaken},
		}
		for _, tc := range cases {
			res := runLabelCmd(t, repo, rc, "create-label", append(tc.args, "--project", projectID)...)
			assertAPIErrorCode(t, res.err, tc.code)
			if res.stdout != "" {
				t.Errorf("%q: stdout = %q, want nothing", tc.args, res.stdout)
			}
		}
		if n := len(projectLabels(t, repo, projectID)); n != 1 {
			t.Errorf("label count = %d, want 1 (nothing created)", n)
		}

		for _, args := range [][]string{
			{"ok", "--color", "red", "--project", "no-such-project"},
			{"ok", "--project", "no-such-project"},
		} {
			res := runLabelCmd(t, repo, rc, "create-label", args...)
			assertAPIErrorCode(t, res.err, domain.ErrCodeProjectNotFound)
			if res.stdout != "" {
				t.Errorf("%q: stdout = %q, want nothing", args, res.stdout)
			}
		}
		for _, l := range projectLabels(t, repo, projectID) {
			if l.Name == "ok" {
				t.Errorf("label %q must not have been created", l.Name)
			}
		}
	})
}

func TestCreateLabel_UsageErrorsCreateNothing(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	rc := runtimeConfig{HomeDir: t.TempDir()}
	for _, args := range [][]string{
		{"--project", projectID},
		{"a", "b", "--project", projectID},
		{"a", "--project", projectID, "--color"},
		{"a", "--project"},
		{"a", "--color", "", "--project", projectID},
		{"a", "--unknown", "x", "--project", projectID},
		{"a", "--color", "red", "--color", "blue", "--project", projectID},
	} {
		res := runLabelCmd(t, repo, rc, "create-label", args...)
		if res.err == nil || !strings.Contains(res.err.Error(), createLabelUsageLine) {
			t.Errorf("create-label %q: err = %v, want a usage error", args, res.err)
		}
		if res.stdout != "" {
			t.Errorf("create-label %q: stdout = %q, want nothing", args, res.stdout)
		}
	}
	if n := len(projectLabels(t, repo, projectID)); n != 0 {
		t.Errorf("label count = %d, want 0", n)
	}
}

func TestCreateLabel_HelpFlagIsHelpRequest(t *testing.T) {
	for _, args := range [][]string{{"create-label", "--help"}, {"create-label", "x", "-h"}, {"list-labels", "--help"}} {
		if !isHelpRequest(args) {
			t.Errorf("isHelpRequest(%q) = false, want true (so nothing is created)", args)
		}
	}
}

// --- project resolution (same rules as create-ticket) -----------------------

func labelRC(sp standardProjects, cwd string) runtimeConfig {
	return runtimeConfig{WorkDir: cwd, HomeDir: sp.home, ProjectPaths: sp.paths}
}

func TestLabelCommands_ResolveFromCwd(t *testing.T) {
	sp := newStandardResolutionEnv(t)
	sp.setCurrent(t, "proj-blg")
	ops := sp.id(t, "proj-ops")

	res := runLabelCmd(t, sp.repo, labelRC(sp, "/work/graph-ops/sub"), "create-label", "new1")
	l := decodeCreatedLabel(t, mustLabelOK(t, res))
	if l.ProjectID != ops {
		t.Errorf("created in %s, want %s", l.ProjectID, ops)
	}
	want := fmt.Sprintf("resolved project: GraphOps (%s) from %s", ops, resolvedFromCurrentDirectory)
	if line := singleStderrLine(t, res.stderr); line != want {
		t.Errorf("stderr = %q, want %q", line, want)
	}

	res = runLabelCmd(t, sp.repo, labelRC(sp, "/work/graph-ops/sub"), "list-labels")
	got := decodeLabelUsages(t, mustLabelOK(t, res))
	if len(got) != 1 || got[0].Name != "new1" {
		t.Errorf("list-labels = %+v, want [new1]", got)
	}
	if line := singleStderrLine(t, res.stderr); line != want {
		t.Errorf("stderr = %q, want %q", line, want)
	}
}

func TestLabelCommands_FallBackToCurrentProject(t *testing.T) {
	sp := newStandardResolutionEnv(t)
	sp.setCurrent(t, "proj-blg")
	blg := sp.id(t, "proj-blg")
	want := fmt.Sprintf("resolved project: TECH BLOG (%s) from %s", blg, resolvedFromCurrentProject)

	res := runLabelCmd(t, sp.repo, labelRC(sp, "/tmp/unrelated"), "create-label", "new2")
	if l := decodeCreatedLabel(t, mustLabelOK(t, res)); l.ProjectID != blg {
		t.Errorf("created in %s, want %s", l.ProjectID, blg)
	}
	if line := singleStderrLine(t, res.stderr); line != want {
		t.Errorf("stderr = %q, want %q", line, want)
	}

	res = runLabelCmd(t, sp.repo, labelRC(sp, "/tmp/unrelated"), "list-labels")
	if got := decodeLabelUsages(t, mustLabelOK(t, res)); len(got) != 1 || got[0].Name != "new2" {
		t.Errorf("list-labels = %+v, want [new2]", got)
	}
	if line := singleStderrLine(t, res.stderr); line != want {
		t.Errorf("stderr = %q, want %q", line, want)
	}
}

func TestLabelCommands_ExplicitProjectBeatsCwdAndIsQuiet(t *testing.T) {
	sp := newStandardResolutionEnv(t)
	blg := sp.id(t, "proj-blg")

	res := runLabelCmd(t, sp.repo, labelRC(sp, "/work/graph-ops"), "create-label", "new3", "--project", blg)
	if l := decodeCreatedLabel(t, mustLabelOK(t, res)); l.ProjectID != blg {
		t.Errorf("created in %s, want %s", l.ProjectID, blg)
	}
	if res.stderr != "" {
		t.Errorf("stderr = %q, want nothing", res.stderr)
	}
	res = runLabelCmd(t, sp.repo, labelRC(sp, "/work/graph-ops"), "list-labels", "--project", blg)
	if got := decodeLabelUsages(t, mustLabelOK(t, res)); len(got) != 1 || got[0].Name != "new3" {
		t.Errorf("list-labels = %+v, want [new3]", got)
	}
	if res.stderr != "" {
		t.Errorf("stderr = %q, want nothing", res.stderr)
	}
}

func TestLabelCommands_NoProjectResolvedFailsLikeCreateTicket(t *testing.T) {
	sp := newStandardResolutionEnv(t)
	for _, tc := range []struct {
		cmd  string
		args []string
	}{{"list-labels", nil}, {"create-label", []string{"new4"}}} {
		res := runLabelCmd(t, sp.repo, labelRC(sp, "/tmp/unrelated"), tc.cmd, tc.args...)
		if res.err == nil {
			t.Fatalf("%s: expected an error, got stdout %q", tc.cmd, res.stdout)
		}
		assertNoCurrentProjectMessage(t, res.err)
		if res.stdout != "" || res.stderr != "" {
			t.Errorf("%s: stdout=%q stderr=%q, want both empty", tc.cmd, res.stdout, res.stderr)
		}
	}
	for _, logical := range []string{"proj-ops", "proj-blg"} {
		if n := len(projectLabels(t, sp.repo, sp.id(t, logical))); n != 0 {
			t.Errorf("%s has %d labels, want 0", logical, n)
		}
	}
}

// --- LABEL_NOT_FOUND points at create-label ----------------------------------

func TestCreateTicket_UnknownLabelMessageMentionsCreateLabel(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	res := runCreateTicket(t, repo, nil, t.TempDir(), "", "T", "--label", "unknown", "--project", projectID)
	assertAPIErrorCode(t, res.err, domain.ErrCodeLabelNotFound)
	if !strings.Contains(res.err.Error(), `"graph-engine create-label"`) {
		t.Errorf("error should point at graph-engine create-label: %v", res.err)
	}
	if n := countProjectTickets(t, repo, projectID); n != 0 {
		t.Errorf("ticket count = %d, want 0", n)
	}
}

// --- help -------------------------------------------------------------------

func TestPrintUsage_DocumentsLabelCommands(t *testing.T) {
	out := captureStdout(t, printUsage)
	for _, want := range []string{
		"list-labels [--project <id>]",
		"create-label <name> [--color <color>] [--project <id>]",
		"gray red orange amber green teal blue indigo purple pink",
		"--color omitted",
		"Renaming and deleting labels are Web UI only",
		"See the registered\n                                           ones with list-labels",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("usage should contain %q", want)
		}
	}
	if strings.Contains(out, "registered, renamed and deleted only in the Web UI's settings") {
		t.Error("usage must no longer say labels are registered only in the Web UI")
	}
	// refine-ticket's --label text points at both commands too.
	refine := out[strings.Index(out, "  refine-ticket "):strings.Index(out, "  close-ticket ")]
	for _, want := range []string{"list-labels", "create-label"} {
		if !strings.Contains(refine, want) {
			t.Errorf("refine-ticket's help should mention %s", want)
		}
	}
}
