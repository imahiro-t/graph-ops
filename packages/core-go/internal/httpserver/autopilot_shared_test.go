package httpserver

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/graph-ops/core-go/internal/autopilot"
	"github.com/graph-ops/core-go/internal/autopilot/runner"
	"github.com/graph-ops/core-go/internal/runtimeconfig"
	"github.com/graph-ops/core-go/internal/store"
)

// DFLT-00326: two members' Web UI servers on one DB.

func setMyName(t *testing.T, home, name string) {
	t.Helper()
	if _, _, err := runtimeconfig.UpdateHome(home, func(c *runtimeconfig.FileConfig) error {
		c.MyName = name
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// secondMember is another member's server on e's DB: its own HOME (machine
// ID, registry, myName) and local path.
func secondMember(t *testing.T, e *autopilotEnv, name string) (*Server, *fakeAutopilotLauncher) {
	t.Helper()
	cfg := Config{ArtifactsDir: t.TempDir(), HomeDir: t.TempDir()}
	s := New(e.repo, e.s.engine, cfg)
	if _, err := runtimeconfig.SetProjectPath(cfg.HomeDir, e.pid, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	setMyName(t, cfg.HomeDir, name)
	l := &fakeAutopilotLauncher{}
	s.cfg.AutopilotLauncher = l
	return s, l
}

func sharedRunIDs(t *testing.T, repo store.GraphRepository, pid string) []string {
	t.Helper()
	recs, err := repo.(store.AutopilotRunStore).ListAutopilotRuns(pid)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, r := range recs {
		ids = append(ids, r.ID)
	}
	return ids
}

func TestAutopilotShared_OtherMembersRunBlocksTheWebUILaunch(t *testing.T) {
	e := newAutopilotEnv(t)
	setMyName(t, e.s.cfg.HomeDir, "Alice")
	r := e.ticket(t, "R", "")
	x := e.ticket(t, "X", r)
	resA, err := e.svc().Start(r, autopilot.ModeTree, "", false)
	if err != nil {
		t.Fatal(err)
	}
	b, launcherB := secondMember(t, e, "Bob")
	rec := doJSON(t, b, http.MethodPost, startAutopilotPath(x), map[string]any{"mode": "ticket"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	apiErr := decodeError(t, rec)
	if apiErr.Code != autopilot.ErrCodeAlreadyRunning || apiErr.Details["started_by"] != "Alice" {
		t.Fatalf("error = %+v", apiErr)
	}
	if launcherB.count() != 0 {
		t.Fatal("B opened a terminal")
	}
	runsB, err := b.autopilotService().Registry.List(e.pid)
	if err != nil || len(runsB) != 0 {
		t.Fatalf("B's registry = %+v, %v", runsB, err)
	}
	if ids := sharedRunIDs(t, e.repo, e.pid); len(ids) != 1 || ids[0] != resA.RunID {
		t.Fatalf("DB runs = %v", ids)
	}

	// B's runs listing shows A's run as someone else's.
	listRec := doJSON(t, b, http.MethodGet, "/api/autopilot/runs?project_id="+e.pid, nil)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", listRec.Code, listRec.Body.String())
	}
	var views []runner.RunView
	if err := json.Unmarshal(listRec.Body.Bytes(), &views); err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0].RunID != resA.RunID || views[0].Mine || views[0].StartedBy == nil || views[0].StartedBy.Name != "Alice" {
		t.Fatalf("B's listing = %s", listRec.Body.String())
	}
	// A's own listing marks it mine.
	var own []runner.RunView
	if err := json.Unmarshal(doJSON(t, e.s, http.MethodGet, "/api/autopilot/runs?project_id="+e.pid, nil).Body.Bytes(), &own); err != nil {
		t.Fatal(err)
	}
	if len(own) != 1 || !own[0].Mine {
		t.Fatalf("A's listing = %+v", own)
	}
}

func TestAutopilotShared_BrokenMachineIDListsButDoesNotLaunch(t *testing.T) {
	e := newAutopilotEnv(t)
	r := e.ticket(t, "R", "")
	b, launcherB := secondMember(t, e, "Bob")
	path := filepath.Join(b.cfg.HomeDir, ".graph-ops", "machine-id")
	if err := os.WriteFile(path, []byte("broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if rec := doJSON(t, b, http.MethodGet, "/api/autopilot/runs?project_id="+e.pid, nil); rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	rec := doJSON(t, b, http.MethodPost, startAutopilotPath(r), map[string]any{"mode": "tree"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("launch with a broken machine id: %d %s", rec.Code, rec.Body.String())
	}
	if apiErr := decodeError(t, rec); apiErr.Code != autopilot.ErrCodeMachineIDUnreadable || apiErr.Details["path"] != path {
		t.Fatalf("error = %+v", apiErr)
	}
	if launcherB.count() != 0 || len(sharedRunIDs(t, e.repo, e.pid)) != 0 {
		t.Fatal("something was launched or recorded")
	}
}
