package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/sessionstatus"
	"github.com/graph-ops/core-go/internal/store"
)

// DFLT-00383: the status band's file, written by session-status and as a
// side effect of the orchestrator's commands.

const (
	testSID      = "11111111-2222-4333-8444-555555555555"
	otherTestSID = "99999999-8888-4777-8666-555555555555"
)

// statusCLIRun runs the CLI as a subprocess with HOME set to home and
// CLAUDE_CODE_SESSION_ID to sid ("" leaves it unset).
func statusCLIRun(t *testing.T, testName, dir, dbPath, home, sid string, args ...string) (string, string, int) {
	t.Helper()
	cmd := newCLISubprocessCmd(testName, dir, dbPath, args...)
	cmd.Env = append(cmd.Env, "HOME="+home)
	if sid != "" {
		cmd.Env = append(cmd.Env, sessionstatus.EnvSessionID+"="+sid)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), cliSubprocessExitCode(t, err, stderr.Bytes())
}

func readStatus(t *testing.T, home, sid string) *sessionstatus.Status {
	t.Helper()
	st, err := sessionstatus.Read(filepath.Join(home, ".graph-ops"), sid)
	if err != nil {
		t.Fatalf("reading status: %v", err)
	}
	return st
}

func statusFileBytes(t *testing.T, home, sid string) []byte {
	t.Helper()
	p, _ := sessionstatus.Path(filepath.Join(home, ".graph-ops"), sid)
	data, _ := os.ReadFile(p)
	return data
}

func TestSessionStatusCLI_NoClaudeSessionWritesNothing(t *testing.T) {
	runMainIfSubprocess()
	const name = "TestSessionStatusCLI_NoClaudeSessionWritesNothing"
	repo, dir, dbPath := newSubprocessSQLiteRepo(t)
	proj, _ := repo.CreateProject("P", "TEST")
	tk, _ := repo.CreateTicket(proj.ID, domain.Ticket{Title: "t", Status: domain.TicketTODO, AutoExecutable: true})
	home := filepath.Join(dir, "home")
	os.MkdirAll(home, 0o700)

	for _, args := range [][]string{
		{"session-status", "set", "--ticket", tk.ID, "--phase", "running"},
		{"session-status", "show"},
		{"begin-session", tk.ID},
		{"get-executable", tk.ID},
	} {
		out, errOut, code := statusCLIRun(t, name, dir, dbPath, home, "", args...)
		if code != 0 || errOut != "" {
			t.Fatalf("%v = %d, stderr %q", args, code, errOut)
		}
		if args[0] == "session-status" && strings.TrimSpace(out) != "null" {
			t.Fatalf("%v printed %q", args, out)
		}
	}
	out, _, code := statusCLIRun(t, name, dir, dbPath, home, "", "session-status", "clear")
	if code != 0 || strings.TrimSpace(out) != `{"cleared":false}` {
		t.Fatalf("clear = %d %q", code, out)
	}
	if _, err := os.Stat(filepath.Join(home, ".graph-ops", sessionstatus.DirName)); !os.IsNotExist(err) {
		t.Fatalf("a status directory was created: %v", err)
	}
}

func TestSessionStatusCLI_SetShowClear(t *testing.T) {
	runMainIfSubprocess()
	const name = "TestSessionStatusCLI_SetShowClear"
	repo, dir, dbPath := newSubprocessSQLiteRepo(t)
	proj, _ := repo.CreateProject("P", "TEST")
	tk, _ := repo.CreateTicket(proj.ID, domain.Ticket{Title: "\x1b[31m危険\nな\u202eタイトル", Status: domain.TicketTODO})
	home := filepath.Join(dir, "home")
	run := func(sid string, args ...string) (string, string, int) {
		t.Helper()
		return statusCLIRun(t, name, dir, dbPath, home, sid, args...)
	}

	out, errOut, code := run(testSID, "session-status", "set", "--ticket", tk.ID, "--phase", "refining", "--detail", "完了条件を\n整理中")
	if code != 0 || errOut != "" {
		t.Fatalf("set = %d, stderr %q", code, errOut)
	}
	var printed sessionstatus.Status
	if err := json.Unmarshal([]byte(out), &printed); err != nil {
		t.Fatalf("set printed %q: %v", out, err)
	}
	st := readStatus(t, home, testSID)
	if st == nil || st.Role != sessionstatus.RoleWorker || st.Phase != sessionstatus.PhaseRefining || st.Detail != "完了条件を 整理中" ||
		st.Ticket == nil || st.Ticket.ID != tk.ID || st.Ticket.Title != "[31m危険 なタイトル" || st.SessionID != testSID {
		t.Fatalf("status = %+v (ticket %+v)", st, st.Ticket)
	}
	if printed.Ticket == nil || printed.Ticket.Title != st.Ticket.Title {
		t.Fatalf("printed = %+v", printed)
	}

	// Another Claude Code session writes its own file only.
	if _, _, code := run(otherTestSID, "session-status", "set", "--phase", "awaiting_human", "--role", "controller"); code != 0 {
		t.Fatalf("other set = %d", code)
	}
	if other := readStatus(t, home, otherTestSID); other == nil || other.Role != sessionstatus.RoleController || other.Ticket != nil {
		t.Fatalf("other = %+v", other)
	}
	if st2 := readStatus(t, home, testSID); st2.Phase != sessionstatus.PhaseRefining {
		t.Fatalf("the first session's status changed: %+v", st2)
	}

	out, _, code = run(testSID, "session-status", "show")
	if code != 0 || !strings.Contains(out, `"phase": "refining"`) {
		t.Fatalf("show = %d %q", code, out)
	}

	// Usage errors change nothing.
	before := statusFileBytes(t, home, testSID)
	for _, args := range [][]string{
		{"session-status", "set", "--phase", "hacking"},
		{"session-status", "set", "--ticket", tk.ID},
		{"session-status", "set", "--phase", "running", "--role", "admin"},
		{"session-status", "set", "--phase", "running", "--ticket", "../x"},
		{"session-status", "frobnicate"},
	} {
		if _, _, code := run(testSID, args...); code != 1 {
			t.Errorf("%v exit = %d, want 1", args, code)
		}
	}
	if !bytes.Equal(before, statusFileBytes(t, home, testSID)) {
		t.Fatal("a refused set changed the status")
	}

	out, _, code = run(testSID, "session-status", "clear")
	if code != 0 || strings.TrimSpace(out) != `{"cleared":true}` || readStatus(t, home, testSID) != nil {
		t.Fatalf("clear = %d %q", code, out)
	}
	if readStatus(t, home, otherTestSID) == nil {
		t.Fatal("clear removed another session's status")
	}

	// A session ID that cannot name a file is refused with a warning.
	_, errOut, code = run("../../evil", "session-status", "set", "--phase", "running")
	if code != 0 || !strings.Contains(errOut, "session status not updated") {
		t.Fatalf("bad session ID: %d %q", code, errOut)
	}
}

func TestSessionStatusCLI_OrchestratorCommandsFollowTheTicket(t *testing.T) {
	runMainIfSubprocess()
	const name = "TestSessionStatusCLI_OrchestratorCommandsFollowTheTicket"
	repo, dir, dbPath := newSubprocessSQLiteRepo(t)
	proj, _ := repo.CreateProject("P", "TEST")
	tk, _ := repo.CreateTicket(proj.ID, domain.Ticket{Title: "表示のチケット", Status: domain.TicketTODO, AutoExecutable: true})
	mk := func(name string, typ domain.NodeType, status domain.NodeStatus, manual bool) domain.GraphNode {
		n, err := repo.CreateNode(domain.GraphNode{TicketID: tk.ID, Name: name, Type: typ, Status: status, IsManual: manual, MaxIterations: 3})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	plan := mk("計画作成", domain.NodeTypePlan, domain.NodeDone, false)
	gate := mk("計画承認", domain.NodeTypeApprovalGate, domain.NodeTODO, true)
	impl := mk("実装", domain.NodeTypeImplementation, domain.NodeTODO, false)
	release := mk("リリース", domain.NodeTypeRelease, domain.NodeTODO, true)
	for _, e := range [][2]string{{plan.ID, gate.ID}, {gate.ID, impl.ID}, {impl.ID, release.ID}} {
		if _, err := repo.CreateEdge(domain.GraphEdge{ID: "edge-" + engine.NewArtifactID(), TicketID: tk.ID, FromNodeID: e[0], ToNodeID: e[1], Condition: domain.EdgeSuccess}); err != nil {
			t.Fatal(err)
		}
	}
	home := filepath.Join(dir, "home")
	writeMyName(t, home, "Alice")
	run := func(args ...string) string {
		t.Helper()
		out, errOut, code := statusCLIRun(t, name, dir, dbPath, home, testSID, args...)
		if code != 0 || strings.Contains(errOut, "session status") {
			t.Fatalf("%v = %d, stderr %q", args, code, errOut)
		}
		return out
	}
	expect := func(step, phase, detail string, nodes ...string) {
		t.Helper()
		st := readStatus(t, home, testSID)
		if st == nil {
			t.Fatalf("%s: no status", step)
		}
		var got []string
		for _, n := range st.Nodes {
			got = append(got, n.Name+"("+n.Type+")")
		}
		if st.Phase != phase || st.Detail != detail || strings.Join(got, ",") != strings.Join(nodes, ",") ||
			st.Ticket == nil || st.Ticket.ID != tk.ID || st.Ticket.Title != "表示のチケット" || st.Role != sessionstatus.RoleWorker {
			t.Fatalf("%s: status = phase %q detail %q nodes %v ticket %+v role %q", step, st.Phase, st.Detail, got, st.Ticket, st.Role)
		}
	}

	var begun struct {
		SessionID string `json:"session_id"`
	}
	json.Unmarshal([]byte(run("begin-session", tk.ID)), &begun)
	expect("begin-session", "running", "")

	// Only the approval gate is left: awaiting approval, naming it.
	if out := run("get-executable", tk.ID, "--session", begun.SessionID); strings.TrimSpace(out) != "[]" {
		t.Fatalf("get-executable = %s", out)
	}
	expect("gate", "awaiting_approval", "計画承認")

	// The commands a node's subagent runs write nothing.
	before := statusFileBytes(t, home, testSID)
	run("get-ticket", tk.ID, "--session", begun.SessionID)
	run("add-artifact", tk.ID, plan.ID, "notes", "text", "x")
	run("complete-node", gate.ID, "true")
	if !bytes.Equal(before, statusFileBytes(t, home, testSID)) {
		t.Fatal("get-ticket / add-artifact / complete-node changed the status")
	}

	// Approved: the next node runs.
	run("get-executable", tk.ID, "--session", begun.SessionID)
	expect("impl", "running", "", "実装(implementation)")
	// A second call while it runs keeps showing it.
	run("get-executable", tk.ID, "--session", begun.SessionID)
	expect("impl again", "running", "", "実装(implementation)")

	run("complete-node", impl.ID, "true")
	run("get-executable", tk.ID, "--session", begun.SessionID)
	expect("release", "releasing", "リリース")

	run("complete-node", release.ID, "true")
	run("get-executable", tk.ID, "--session", begun.SessionID)
	expect("all done", "finishing", "")
}

func TestSessionStatusCLI_RefineTicketMarksPlanning(t *testing.T) {
	runMainIfSubprocess()
	const name = "TestSessionStatusCLI_RefineTicketMarksPlanning"
	repo, dir, dbPath := newSubprocessSQLiteRepo(t)
	proj, _ := repo.CreateProject("P", "TEST")
	tk, _ := repo.CreateTicket(proj.ID, domain.Ticket{Title: "改善", Status: domain.TicketTODO})
	home := filepath.Join(dir, "home")
	if _, _, code := statusCLIRun(t, name, dir, dbPath, home, testSID, "session-status", "set", "--ticket", tk.ID, "--phase", "refining"); code != 0 {
		t.Fatal("set failed")
	}
	if _, errOut, code := statusCLIRun(t, name, dir, dbPath, home, testSID, "refine-ticket", tk.ID, "新しい説明"); code != 0 || errOut != "" {
		t.Fatalf("refine-ticket = %d %q", code, errOut)
	}
	if st := readStatus(t, home, testSID); st == nil || st.Phase != sessionstatus.PhasePlanning || st.Ticket.Title != "改善" {
		t.Fatalf("status = %+v", st)
	}
}

// A status that cannot be written changes neither the command's output nor
// its exit code: one warning line on stderr.
func TestSessionStatusCLI_UnwritableStatusOnlyWarns(t *testing.T) {
	runMainIfSubprocess()
	const name = "TestSessionStatusCLI_UnwritableStatusOnlyWarns"
	repo, dir, dbPath := newSubprocessSQLiteRepo(t)
	proj, _ := repo.CreateProject("P", "TEST")
	tk, _ := repo.CreateTicket(proj.ID, domain.Ticket{Title: "t", Status: domain.TicketTODO, AutoExecutable: true})
	home := filepath.Join(dir, "home")
	os.MkdirAll(filepath.Join(home, ".graph-ops"), 0o700)
	// The status directory's place is taken by a file.
	os.WriteFile(filepath.Join(home, ".graph-ops", sessionstatus.DirName), []byte("x"), 0o600)

	for _, args := range [][]string{{"begin-session", tk.ID}, {"get-executable", tk.ID}} {
		out, errOut, code := statusCLIRun(t, name, dir, dbPath, home, testSID, args...)
		if code != 0 {
			t.Fatalf("%v exit = %d (%s)", args, code, errOut)
		}
		var v any
		if err := json.Unmarshal([]byte(out), &v); err != nil {
			t.Fatalf("%v stdout is not JSON: %q", args, out)
		}
		if lines := strings.Split(strings.TrimSpace(errOut), "\n"); len(lines) != 1 || !strings.HasPrefix(lines[0], "graph-engine: warning: session status not updated: ") {
			t.Fatalf("%v stderr = %q", args, errOut)
		}
	}
}

// --- wait-node, in process

func waitNodeStatusSetup(t *testing.T) (store.GraphRepository, runtimeConfig, domain.GraphNode) {
	t.Helper()
	withFastPoll(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(sessionstatus.EnvSessionID, testSID)
	repo, projectID := newTestRepoWithProject(t)
	tk, err := repo.CreateTicket(projectID, domain.Ticket{Title: "待ちのチケット", Status: domain.TicketTODO})
	if err != nil {
		t.Fatal(err)
	}
	gate, err := repo.CreateNode(domain.GraphNode{TicketID: tk.ID, Name: "リリース承認", Type: domain.NodeTypeApprovalGate, Status: domain.NodeTODO, IsManual: true, MaxIterations: 3})
	if err != nil {
		t.Fatal(err)
	}
	rc := runtimeConfig{HomeDir: home}
	updateSessionStatus(rc, func(st *sessionstatus.Status) {
		st.Role, st.Phase, st.Detail = sessionstatus.RoleWorker, sessionstatus.PhaseRunning, "前の作業"
		st.Ticket = &sessionstatus.Ticket{ID: tk.ID, Title: "待ちのチケット"}
	})
	return repo, rc, gate
}

// whileWaiting runs wait-node with args and, once the band shows the wait,
// act; it returns what the band showed during the wait and wait-node's error.
func whileWaiting(t *testing.T, repo store.GraphRepository, rc runtimeConfig, act func(), args ...string) (*sessionstatus.Status, error) {
	t.Helper()
	seen := make(chan *sessionstatus.Status, 1)
	go func() {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if st := readStatus(t, rc.HomeDir, testSID); st != nil && st.Phase == sessionstatus.PhaseAwaitingApproval {
				seen <- st
				if act != nil {
					act()
				}
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
		seen <- nil
	}()
	var err error
	captureStdout(t, func() { err = waitNode(engine.New(repo), repo, rc, args) })
	return <-seen, err
}

func TestWaitNodeStatus_BackToRunningWhenTheGateIsDecided(t *testing.T) {
	repo, rc, gate := waitNodeStatusSetup(t)
	during, err := whileWaiting(t, repo, rc, func() {
		done := domain.NodeDone
		repo.UpdateNode(gate.ID, store.NodePatch{Status: &done})
	}, gate.ID)
	if err != nil {
		t.Fatalf("wait-node: %v", err)
	}
	if during == nil || during.Detail != "リリース承認" {
		t.Fatalf("during the wait = %+v", during)
	}
	if st := readStatus(t, rc.HomeDir, testSID); st.Phase != sessionstatus.PhaseRunning || st.Detail != "" {
		t.Fatalf("after = %+v", st)
	}
}

func TestWaitNodeStatus_TimeoutKeepsTheWait(t *testing.T) {
	repo, rc, gate := waitNodeStatusSetup(t)
	_, err := whileWaiting(t, repo, rc, nil, gate.ID, "--timeout", "100ms")
	var ec exitCodeError
	if !errors.As(err, &ec) || ec.code != waitNodeTimeoutExitCode {
		t.Fatalf("wait-node err = %v", err)
	}
	if st := readStatus(t, rc.HomeDir, testSID); st.Phase != sessionstatus.PhaseAwaitingApproval || st.Detail != "リリース承認" {
		t.Fatalf("after = %+v", st)
	}
}

func TestWaitNodeStatus_ErrorRestoresWhatWasThere(t *testing.T) {
	repo, rc, gate := waitNodeStatusSetup(t)
	_, err := whileWaiting(t, repo, rc, func() { repo.DeleteNode(gate.ID) }, gate.ID)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("wait-node err = %v", err)
	}
	if st := readStatus(t, rc.HomeDir, testSID); st.Phase != sessionstatus.PhaseRunning || st.Detail != "前の作業" {
		t.Fatalf("after = %+v", st)
	}
}

func TestWaitNodeStatus_LeavesAStatusThatMovedOnAlone(t *testing.T) {
	repo, rc, gate := waitNodeStatusSetup(t)
	_, err := whileWaiting(t, repo, rc, func() {
		// The person answered in the terminal; get-executable moved on.
		updateSessionStatus(rc, func(st *sessionstatus.Status) {
			st.Phase, st.Detail = sessionstatus.PhaseRunning, ""
			st.Nodes = []sessionstatus.Node{{ID: "N-1", Name: "実装", Type: "implementation"}}
		})
		done := domain.NodeDone
		repo.UpdateNode(gate.ID, store.NodePatch{Status: &done})
	}, gate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if st := readStatus(t, rc.HomeDir, testSID); st.Phase != sessionstatus.PhaseRunning || len(st.Nodes) != 1 {
		t.Fatalf("after = %+v", st)
	}
}

// --- autopilot, in process

func TestAutopilotStatus_ControllerAndWorkerKeepTheirOwnBands(t *testing.T) {
	repo, rc, projectID, gitRepo, _ := autopilotCLISetup(t)
	t.Setenv("HOME", rc.HomeDir)
	const controllerSID, workerSID = testSID, otherTestSID
	eng := engine.New(repo)
	root, _ := eng.CreateTicket(projectID, "ルート\x1b[2J", "")
	as := func(sid string) { t.Setenv(sessionstatus.EnvSessionID, sid) }

	as(controllerSID)
	runID, _ := mustAutopilot(t, repo, rc, "", "start", root.ID, "--mode", "ticket")["run_id"].(string)
	st := readStatus(t, rc.HomeDir, controllerSID)
	if st == nil || st.Role != sessionstatus.RoleController || st.Autopilot == nil || st.Autopilot.RunID != runID ||
		st.Ticket == nil || st.Ticket.ID != root.ID || st.Ticket.Title != "ルート[2J" {
		t.Fatalf("after start = %+v", st)
	}
	mustAutopilot(t, repo, rc, "", "next", runID)
	mustAutopilot(t, repo, rc, "", "launch", runID, root.ID)
	st = readStatus(t, rc.HomeDir, controllerSID)
	if st.Autopilot.Current == nil || st.Autopilot.Current.TicketID != root.ID || st.Autopilot.Counts.InProgress != 1 || st.Phase != sessionstatus.PhaseRunning {
		t.Fatalf("after launch = %+v (%+v)", st.Autopilot, st.Autopilot.Current)
	}
	controllerBefore := statusFileBytes(t, rc.HomeDir, controllerSID)

	// The worker, in its own Claude Code session.
	as(workerSID)
	mustAutopilot(t, repo, rc, "", "worker-context", runID, root.ID)
	w := readStatus(t, rc.HomeDir, workerSID)
	if w == nil || w.Role != sessionstatus.RoleWorker || w.Phase != sessionstatus.PhaseRefining || w.Ticket.ID != root.ID || w.Autopilot != nil {
		t.Fatalf("worker after worker-context = %+v", w)
	}
	mustAutopilot(t, repo, rc, "", "touch", runID, root.ID, "--awaiting-human", "計画承認: approve or reject")
	if w = readStatus(t, rc.HomeDir, workerSID); w.Phase != sessionstatus.PhaseAwaitingHuman || w.Detail != "計画承認: approve or reject" {
		t.Fatalf("worker after touch --awaiting-human = %+v", w)
	}
	mustAutopilot(t, repo, rc, "", "touch", runID, root.ID)
	if w = readStatus(t, rc.HomeDir, workerSID); w.Phase != sessionstatus.PhaseRunning {
		t.Fatalf("worker after touch = %+v", w)
	}
	wt := filepath.Join(gitRepo, ".claude", "worktrees", root.ID)
	os.WriteFile(filepath.Join(wt, "r.txt"), []byte("r"), 0o644)
	autopilotGit(t, wt, "add", "r.txt")
	autopilotGit(t, wt, "commit", "-q", "-m", "r")
	mustAutopilot(t, repo, rc, "", "report", runID, root.ID, "--result", "done", "--summary", "ok")
	if w = readStatus(t, rc.HomeDir, workerSID); w != nil {
		t.Fatalf("the worker's report left its status: %+v", w)
	}
	if !bytes.Equal(controllerBefore, statusFileBytes(t, rc.HomeDir, controllerSID)) {
		t.Fatal("the worker's commands changed the controller's status")
	}

	// Back in the controller: the wait sees the report.
	as(controllerSID)
	mustAutopilot(t, repo, rc, "", "wait", runID, root.ID)
	st = readStatus(t, rc.HomeDir, controllerSID)
	if st.Autopilot.Current != nil || st.Autopilot.Last == nil || st.Autopilot.Last.TicketID != root.ID || st.Autopilot.Last.Outcome != "done" || st.Autopilot.Counts.Done != 1 {
		t.Fatalf("after wait = %+v (last %+v)", st.Autopilot, st.Autopilot.Last)
	}
}
