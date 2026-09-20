package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/graph-ops/core-go/internal/domain"
)

// DFLT-00100 (BUG-01 / QA-03): process-ticket runs a ticket's node
// subagents in parallel, and each of them is a separate graph-engine
// process calling add-artifact / complete-node / get-executable against the
// same SQLite file. Without a busy timeout in the DSN, modernc.org/sqlite
// waits 0ms for a write lock and those calls fail outright with "database is
// locked" -- artifacts are lost and nodes never complete.
//
// The regression has to be exercised across processes. Goroutines cannot
// reproduce it: SQLiteRepository.Open sets SetMaxOpenConns(1), which
// serializes everything inside one process, so a goroutine-only test passes
// even with the bug present.

// cliSubprocessBarrierEnv names a file that a CLI subprocess waits for
// before it starts doing any work. Process startup (re-exec of the test
// binary, Go runtime init, flag parsing) costs far more than the few
// milliseconds a CLI call spends holding the DB, so without a barrier the
// processes would arrive at the DB one after another and never contend.
const cliSubprocessBarrierEnv = "GRAPH_ENGINE_TEST_START_BARRIER"

// cliSubprocessBarrierTimeout bounds the wait so a subprocess can never
// outlive a parent that died before releasing the barrier.
const cliSubprocessBarrierTimeout = 30 * time.Second

// waitForCLISubprocessStartBarrier blocks until the parent creates the
// barrier file, if it asked for one. Called from runMainIfSubprocess, so it
// runs before main() opens the DB. A no-op for every other subprocess test.
func waitForCLISubprocessStartBarrier() {
	path := os.Getenv(cliSubprocessBarrierEnv)
	if path == "" {
		return
	}
	deadline := time.Now().Add(cliSubprocessBarrierTimeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			fmt.Fprintf(os.Stderr, "start barrier %s never appeared\n", path)
			os.Exit(1)
		}
		// Busy-ish wait: the barrier is released within milliseconds, and
		// sleeping longer would blunt the simultaneity the test needs.
		time.Sleep(200 * time.Microsecond)
	}
}

// cliProcess is one CLI subprocess started but not yet waited for.
type cliProcess struct {
	label string
	cmd   *exec.Cmd
	out   *bytes.Buffer
}

// startCLISubprocess starts the CLI (this test binary re-entering testName)
// against the SQLite DB at dbPath without waiting for it. The process blocks
// on the barrier file until releaseCLISubprocessBarrier is called, so a
// caller can start every process first and have them all hit the DB
// together. label identifies the process in failure messages.
func startCLISubprocess(t *testing.T, testName, dir, dbPath, barrierPath, label string, args ...string) *cliProcess {
	t.Helper()
	cmd := newCLISubprocessCmd(testName, dir, dbPath, args...)
	cmd.Env = append(cmd.Env, cliSubprocessBarrierEnv+"="+barrierPath)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting CLI subprocess %s: %v", label, err)
	}
	return &cliProcess{label: label, cmd: cmd, out: &buf}
}

// wait collects the subprocess's combined output and exit code.
func (p *cliProcess) wait(t *testing.T) (string, int) {
	t.Helper()
	err := p.cmd.Wait()
	out := p.out.Bytes()
	return string(out), cliSubprocessExitCode(t, err, out)
}

// releaseCLISubprocessBarrier creates the barrier file, letting every waiting
// subprocess run at once.
func releaseCLISubprocessBarrier(t *testing.T, barrierPath string) {
	t.Helper()
	if err := os.WriteFile(barrierPath, []byte("go"), 0o600); err != nil {
		t.Fatalf("releasing the start barrier: %v", err)
	}
}

// lockErrorFragments are the shapes BUG-01 takes in a CLI process's output.
var lockErrorFragments = []string{"database is locked", "SQLITE_BUSY", "SQLITE_BUSY_SNAPSHOT"}

// deferredUpgradeMarker identifies the one lock failure busy_timeout cannot
// remove, and which DFLT-00100 therefore does not claim to fix: updateTicket
// (internal/store/labels.go) runs a deferred transaction that reads the
// ticket and then writes it, and SQLite refuses to run the busy handler when
// a reading transaction tries to become a writer. syncTicketStatus drives it
// from both complete-node and get-executable, so it surfaces here.
//
// TestSQLiteBusyTimeoutDoesNotCoverDeferredTransactionUpgrade
// (internal/store) pins that behaviour deterministically; this constant only
// keeps the concurrency test from reporting it as a regression of the fix.
// It is a KNOWN REMAINING DEFECT, not an accepted one -- fixing it needs
// BEGIN IMMEDIATE (DSN _txlock=immediate) or a retry, both out of this
// ticket's scope.
const deferredUpgradeMarker = "updating ticket "

// checkLockFailure reports whether out shows a lock failure the fix was
// supposed to remove (failing the test), and separately counts the known
// deferred-upgrade failures so the run can report how often they happened.
func checkLockFailure(t *testing.T, label, out string, code int) (knownDeferredUpgrade bool) {
	t.Helper()
	var sawLockError bool
	for _, fragment := range lockErrorFragments {
		if strings.Contains(out, fragment) {
			sawLockError = true
			break
		}
	}
	if sawLockError && strings.Contains(out, deferredUpgradeMarker) {
		return true
	}
	if sawLockError {
		t.Errorf("%s hit a lock failure that busy_timeout must absorb -- concurrent graph-engine processes have to wait for the write lock, not fail:\n%s", label, out)
		return false
	}
	if code != 0 {
		t.Errorf("%s exited with code %d, want 0:\n%s", label, code, out)
	}
	return false
}

// Sizing of the concurrent run. 14 processes is enough to lose the race
// reliably without the fix (measured: every run fails on the unfixed DSN)
// while staying far below the 5000ms busy timeout once the fix is in, and
// keeps the test's share of `go test ./...` to roughly a second.
const (
	concurrentArtifactProcs   = 6
	concurrentCompleteProcs   = 4
	concurrentExecutableProcs = 4
)

// TestConcurrentCLIProcessesShareOneSQLiteDB is the BUG-01 / QA-03
// regression test: several graph-engine processes run add-artifact,
// complete-node and get-executable against one SQLite file at the same
// moment, and none of them may fail. Before the busy_timeout fix this test
// fails with "database is locked".
func TestConcurrentCLIProcessesShareOneSQLiteDB(t *testing.T) {
	runMainIfSubprocess()

	const testName = "TestConcurrentCLIProcessesShareOneSQLiteDB"
	repo, dir, dbPath := newSubprocessSQLiteRepo(t)
	proj, err := repo.CreateProject("Concurrency", "CONC")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	ticket, err := repo.CreateTicket(proj.ID, domain.Ticket{
		Title: "concurrent CLI access", Status: domain.TicketInProgress, AutoExecutable: true,
	})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	// One node per writing process, so the processes race on the DB rather
	// than on each other's rows: a lock failure is then the only reason a
	// process can fail.
	artifactNodes := make([]string, concurrentArtifactProcs)
	for i := range artifactNodes {
		node, err := repo.CreateNode(domain.GraphNode{
			TicketID: ticket.ID, Name: fmt.Sprintf("impl-%d", i), Type: domain.NodeTypeImplementation,
			Status: domain.NodeInProgress, MaxIterations: 3,
		})
		if err != nil {
			t.Fatalf("CreateNode(impl-%d): %v", i, err)
		}
		artifactNodes[i] = node.ID
	}
	completeNodes := make([]string, concurrentCompleteProcs)
	for i := range completeNodes {
		node, err := repo.CreateNode(domain.GraphNode{
			TicketID: ticket.ID, Name: fmt.Sprintf("inv-%d", i), Type: domain.NodeTypeInvestigation,
			Status: domain.NodeInProgress, MaxIterations: 3,
		})
		if err != nil {
			t.Fatalf("CreateNode(inv-%d): %v", i, err)
		}
		completeNodes[i] = node.ID
	}

	barrierPath := filepath.Join(dir, "start-barrier")
	procs := make([]*cliProcess, 0, concurrentArtifactProcs+concurrentCompleteProcs+concurrentExecutableProcs)
	// Interleaved on purpose: the three kinds of call then overlap instead
	// of running as three separate waves.
	for i := 0; i < concurrentArtifactProcs; i++ {
		procs = append(procs, startCLISubprocess(t, testName, dir, dbPath, barrierPath,
			fmt.Sprintf("add-artifact #%d", i),
			"add-artifact", ticket.ID, artifactNodes[i], fmt.Sprintf("note-%d", i), "text", fmt.Sprintf("content %d", i)))
		if i < concurrentExecutableProcs {
			procs = append(procs, startCLISubprocess(t, testName, dir, dbPath, barrierPath,
				fmt.Sprintf("get-executable #%d", i), "get-executable", ticket.ID))
		}
		if i < concurrentCompleteProcs {
			procs = append(procs, startCLISubprocess(t, testName, dir, dbPath, barrierPath,
				fmt.Sprintf("complete-node #%d", i), "complete-node", completeNodes[i], "true"))
		}
	}

	start := time.Now()
	releaseCLISubprocessBarrier(t, barrierPath)
	outputs := make([]string, len(procs))
	codes := make([]int, len(procs))
	for i, p := range procs {
		outputs[i], codes[i] = p.wait(t)
	}
	elapsed := time.Since(start)
	var deferredUpgradeFailures int
	for i, p := range procs {
		if checkLockFailure(t, p.label, outputs[i], codes[i]) {
			deferredUpgradeFailures++
		}
	}
	t.Logf("%d concurrent graph-engine processes finished in %s (%d hit the known deferred read-then-write failure in updateTicket)",
		len(procs), elapsed, deferredUpgradeFailures)

	// Exiting 0 is not enough: the writes have to have landed. A process
	// that silently did nothing would otherwise pass. These assertions stay
	// strict even for a process that hit the deferred-upgrade failure: both
	// commands do their own write before syncTicketStatus runs, so the
	// artifact and the node's DONE status must be there either way.
	for i, nodeID := range artifactNodes {
		arts, err := repo.ListArtifactsByNode(nodeID)
		if err != nil {
			t.Fatalf("ListArtifactsByNode(%s): %v", nodeID, err)
		}
		if len(arts) != 1 || arts[0].Name != fmt.Sprintf("note-%d", i) {
			t.Errorf("node %s: artifacts = %+v, want exactly the one note-%d add-artifact saved", nodeID, arts, i)
		}
	}
	for _, nodeID := range completeNodes {
		node, err := repo.GetNode(nodeID)
		if err != nil {
			t.Fatalf("GetNode(%s): %v", nodeID, err)
		}
		if node == nil || node.Status != domain.NodeDone {
			t.Errorf("node %s: status = %v after complete-node, want DONE", nodeID, node)
		}
	}
	// A get-executable that did finish has to have produced a usable
	// answer, not an empty body from a swallowed error.
	var answered int
	for i, p := range procs {
		if !strings.HasPrefix(p.label, "get-executable") || codes[i] != 0 {
			continue
		}
		var nodes []map[string]any
		if err := json.Unmarshal([]byte(outputs[i]), &nodes); err != nil {
			t.Errorf("%s: output is not a JSON list of executable nodes (%v):\n%s", p.label, err, outputs[i])
			continue
		}
		answered++
	}
	if answered == 0 {
		t.Errorf("not one of the %d concurrent get-executable calls returned a result", concurrentExecutableProcs)
	}
}
