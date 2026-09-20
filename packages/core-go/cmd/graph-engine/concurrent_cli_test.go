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
//
// Two tests live here, covering the two shapes the contention takes:
//
//   - TestConcurrentCLIProcessesShareOneSQLiteDB is the steady state -- the
//     ticket's derived status does not change while the nodes work -- which
//     is what process-ticket spends nearly all of its time in. Nothing may
//     fail here at all.
//   - TestConcurrentCompleteNodeProcessesContendOnOneTicketRow is the worst
//     case -- several processes deriving the *same new* status for the *same
//     ticket row* at the same moment, so they genuinely have to write it.
//     That is where the residual deferred read-then-write defect still
//     bites, and the test bounds it rather than ignoring it.

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
// "SQLITE_BUSY" is matched as a substring, so it also covers
// SQLITE_BUSY_SNAPSHOT (517) and SQLITE_BUSY_RECOVERY (261).
var lockErrorFragments = []string{"database is locked", "SQLITE_BUSY"}

// deferredUpgradeMarker identifies the one lock failure busy_timeout cannot
// remove: updateTicket (internal/store/labels.go) runs a deferred
// transaction that reads the ticket and then writes it, and SQLite refuses to
// run the busy handler when a reading transaction tries to become a writer.
// The CLI reports it as "updating ticket <id>: database is locked".
//
// TestSQLiteBusyTimeoutDoesNotCoverDeferredTransactionUpgrade
// (internal/store) pins that behaviour deterministically. DFLT-00100 removed
// nearly all exposure to it by making syncTicketStatus skip UpdateTicket when
// the derived status already matches the stored one, so the transaction is
// now entered only on a real status transition. It is a KNOWN REMAINING
// DEFECT, not an accepted one -- closing it needs BEGIN IMMEDIATE (DSN
// _txlock=immediate) or a retry, both out of this ticket's scope.
const deferredUpgradeMarker = "updating ticket "

// processOutcome classifies what one CLI subprocess did.
type processOutcome int

const (
	// outcomeOK: exited 0 with no lock error in sight.
	outcomeOK processOutcome = iota
	// outcomeKnownDeferredUpgrade: failed only on the residual
	// deferred-upgrade defect, in a command that actually goes through
	// syncTicketStatus.
	outcomeKnownDeferredUpgrade
	// outcomeUnexpected: anything else -- a lock failure busy_timeout was
	// supposed to absorb, a deferred-upgrade failure from a command that
	// cannot legitimately produce one, or a plain non-zero exit.
	outcomeUnexpected
)

// commandGoesThroughSyncTicketStatus reports whether the command behind label
// can legitimately hit the deferred-upgrade defect. Only complete-node and
// get-executable call syncTicketStatus -> UpdateTicket; add-artifact writes
// through the repository directly (cmdAddArtifact in main.go) and therefore
// never enters that transaction, so a lock failure from add-artifact is
// always a real regression even if the text happens to mention a ticket.
func commandGoesThroughSyncTicketStatus(label string) bool {
	return strings.HasPrefix(label, "complete-node") || strings.HasPrefix(label, "get-executable")
}

// classifyProcessOutcome inspects one process's combined output line by line.
// The known-failure allowance is deliberately narrow: the marker has to sit
// on the very line that carries the lock error (not merely somewhere in the
// output), and the command has to be one that reaches updateTicket at all.
// Any other lock error anywhere in the output wins and makes the outcome
// unexpected.
func classifyProcessOutcome(label, out string, code int) (processOutcome, string) {
	sawKnown := false
	for _, line := range strings.Split(out, "\n") {
		hasLockError := false
		for _, fragment := range lockErrorFragments {
			if strings.Contains(line, fragment) {
				hasLockError = true
				break
			}
		}
		if !hasLockError {
			continue
		}
		if commandGoesThroughSyncTicketStatus(label) && strings.Contains(line, deferredUpgradeMarker) {
			sawKnown = true
			continue
		}
		return outcomeUnexpected, fmt.Sprintf(
			"%s hit a lock failure that busy_timeout must absorb -- concurrent graph-engine processes have to wait for the write lock, not fail:\n%s",
			label, out)
	}
	if sawKnown {
		return outcomeKnownDeferredUpgrade, ""
	}
	if code != 0 {
		return outcomeUnexpected, fmt.Sprintf("%s exited with code %d, want 0:\n%s", label, code, out)
	}
	return outcomeOK, ""
}

// outcomeTally is the per-run breakdown every concurrency test asserts on,
// rather than eyeballing a log line.
type outcomeTally struct {
	total      int
	ok         int
	known      int
	unexpected int
}

func (t outcomeTally) String() string {
	return fmt.Sprintf("%d process(es): %d ok, %d known deferred-upgrade failures, %d unexpected", t.total, t.ok, t.known, t.unexpected)
}

// classifyAll tallies every process, reporting each unexpected outcome as a
// test failure as it goes. matching, when non-empty, restricts the tally to
// processes whose label starts with it.
func classifyAll(t *testing.T, procs []*cliProcess, outputs []string, codes []int, matching string) outcomeTally {
	t.Helper()
	var tally outcomeTally
	for i, p := range procs {
		if matching != "" && !strings.HasPrefix(p.label, matching) {
			continue
		}
		tally.total++
		outcome, reason := classifyProcessOutcome(p.label, outputs[i], codes[i])
		switch outcome {
		case outcomeOK:
			tally.ok++
		case outcomeKnownDeferredUpgrade:
			tally.known++
		case outcomeUnexpected:
			tally.unexpected++
			t.Error(reason)
		}
	}
	return tally
}

// assertTicketStatus pins the ticket row itself (not just the nodes) after a
// concurrent run: a sync that silently gets skipped or lost would otherwise
// leave a user-visible inconsistency no assertion here would catch.
func assertTicketStatus(t *testing.T, repo interface {
	GetTicket(string) (*domain.Ticket, error)
}, ticketID string, want domain.TicketStatus) {
	t.Helper()
	ticket, err := repo.GetTicket(ticketID)
	if err != nil {
		t.Fatalf("GetTicket(%s): %v", ticketID, err)
	}
	if ticket == nil {
		t.Fatalf("ticket %s disappeared", ticketID)
	}
	if ticket.Status != want {
		t.Errorf("ticket %s: status = %q after the concurrent run, want %q", ticketID, ticket.Status, want)
	}
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
//
// The ticket's derived status stays IN PROGRESS throughout (nodes are still
// working and the graph was never expanded, so deriveTicketStatus cannot
// reach DONE), which is the steady state process-ticket runs in. Since
// DFLT-00100 made syncTicketStatus skip the no-op write, that means *no
// process writes the ticket row at all* -- so the run must be completely
// clean, with zero known-failure allowance.
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
	// process can fail. The same-row race is covered separately by
	// TestConcurrentCompleteNodeProcessesContendOnOneTicketRow.
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

	tally := classifyAll(t, procs, outputs, codes, "")
	t.Logf("steady state, %s in %s", tally, elapsed)
	// No process needs to write the ticket row in this shape, so there is
	// nothing for the deferred-upgrade defect to bite. Anything above zero
	// means either the syncTicketStatus guard regressed or a new write
	// appeared on a path that must stay read-only.
	if tally.known != 0 {
		t.Errorf("%d process(es) hit the deferred read-then-write failure in updateTicket, want 0: in the steady state the derived status never changes, so no command may write the ticket row", tally.known)
	}
	if tally.ok != tally.total {
		t.Errorf("only %d of %d processes finished cleanly; every concurrent graph-engine call has to succeed", tally.ok, tally.total)
	}

	// Exiting 0 is not enough: the writes have to have landed. A process
	// that silently did nothing would otherwise pass.
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

	// Every get-executable, not just one of them, has to have produced a
	// usable answer -- an empty body from a swallowed error must not pass.
	execTally := classifyAll(t, procs, outputs, codes, "get-executable")
	var readable int
	for i, p := range procs {
		if !strings.HasPrefix(p.label, "get-executable") || codes[i] != 0 {
			continue
		}
		var nodes []map[string]any
		if err := json.Unmarshal([]byte(outputs[i]), &nodes); err != nil {
			t.Errorf("%s: output is not a JSON list of executable nodes (%v):\n%s", p.label, err, outputs[i])
			continue
		}
		readable++
	}
	if execTally.total != concurrentExecutableProcs {
		t.Fatalf("tallied %d get-executable processes, want %d", execTally.total, concurrentExecutableProcs)
	}
	if readable != concurrentExecutableProcs {
		t.Errorf("%d of %d concurrent get-executable calls returned a readable node list (%s); every one of them has to answer",
			readable, concurrentExecutableProcs, execTally)
	}

	// The ticket row itself: still IN PROGRESS, because the artifact nodes
	// are unfinished and the graph was never expanded.
	assertTicketStatus(t, repo, ticket.ID, domain.TicketInProgress)
}

// contendingCompleteProcs is how many complete-node processes race for the
// same ticket row in the transition test.
const contendingCompleteProcs = 6

// maxContendingDeferredUpgradeFailures bounds the residual defect. Measured
// over 25 runs of this test after the syncTicketStatus guard landed: 22 runs
// were completely clean and 3 runs had exactly 1 of the 6 processes fail, so
// the observed maximum is 1. The bound is set at half the processes, which
// leaves 3x headroom for a loaded CI machine while still failing the test if
// the contention ever returns to the pre-guard level (where most of the
// processes failed). It is a ceiling on a known defect, not a budget: if a
// change makes this trip, the defect got worse and needs looking at.
const maxContendingDeferredUpgradeFailures = contendingCompleteProcs / 2

// TestConcurrentCompleteNodeProcessesContendOnOneTicketRow covers QA-5: the
// path that actually breaks in production, where several complete-node
// processes all derive a *new* status for the *same* ticket and therefore all
// have to write the same row through updateTicket's deferred read-then-write
// transaction.
//
// This is the residual defect's home. The guard added in DFLT-00100 removes
// the steady-state exposure but not this one, so the test does not pretend it
// is clean. What it does pin is that the defect stays bounded and the data
// converges anyway:
//
//   - the failures stay under an explicit ceiling
//     (maxContendingDeferredUpgradeFailures), so the allowance cannot silently
//     widen as the contention gets worse;
//   - every node still reaches DONE, because complete-node writes the node
//     before it syncs the ticket;
//   - the ticket row converges to the derived status -- either straight away,
//     or on the next get-executable, which is what the engine does on every
//     poll.
func TestConcurrentCompleteNodeProcessesContendOnOneTicketRow(t *testing.T) {
	runMainIfSubprocess()

	const testName = "TestConcurrentCompleteNodeProcessesContendOnOneTicketRow"
	repo, dir, dbPath := newSubprocessSQLiteRepo(t)
	proj, err := repo.CreateProject("Contention", "CONT")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	// Stored status TODO but derived status IN PROGRESS: every one of the
	// concurrent complete-node calls therefore has real work for
	// syncTicketStatus, and they all aim at the same row at the same moment.
	ticket, err := repo.CreateTicket(proj.ID, domain.Ticket{
		Title: "concurrent ticket-row transition", Status: domain.TicketTODO, AutoExecutable: true,
	})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	nodeIDs := make([]string, contendingCompleteProcs)
	for i := range nodeIDs {
		node, err := repo.CreateNode(domain.GraphNode{
			TicketID: ticket.ID, Name: fmt.Sprintf("impl-%d", i), Type: domain.NodeTypeImplementation,
			Status: domain.NodeInProgress, MaxIterations: 3,
		})
		if err != nil {
			t.Fatalf("CreateNode(impl-%d): %v", i, err)
		}
		nodeIDs[i] = node.ID
	}

	barrierPath := filepath.Join(dir, "start-barrier")
	procs := make([]*cliProcess, 0, contendingCompleteProcs)
	for i, nodeID := range nodeIDs {
		procs = append(procs, startCLISubprocess(t, testName, dir, dbPath, barrierPath,
			fmt.Sprintf("complete-node #%d", i), "complete-node", nodeID, "true"))
	}

	start := time.Now()
	releaseCLISubprocessBarrier(t, barrierPath)
	outputs := make([]string, len(procs))
	codes := make([]int, len(procs))
	for i, p := range procs {
		outputs[i], codes[i] = p.wait(t)
	}
	elapsed := time.Since(start)

	tally := classifyAll(t, procs, outputs, codes, "")
	t.Logf("same-row transition, %s in %s", tally, elapsed)
	// The bound. Without it the allowance is open-ended and the test would
	// stay green even if every single process failed.
	if tally.known > maxContendingDeferredUpgradeFailures {
		t.Errorf("%d of %d processes hit the deferred read-then-write failure in updateTicket (%s), above the ceiling of %d; the residual defect got worse",
			tally.known, contendingCompleteProcs, tally, maxContendingDeferredUpgradeFailures)
	}

	// The node writes happen before syncTicketStatus, so they must all have
	// landed regardless of how many syncs failed.
	for _, nodeID := range nodeIDs {
		node, err := repo.GetNode(nodeID)
		if err != nil {
			t.Fatalf("GetNode(%s): %v", nodeID, err)
		}
		if node == nil || node.Status != domain.NodeDone {
			t.Errorf("node %s: status = %v after complete-node, want DONE", nodeID, node)
		}
	}

	// The ticket row converged. IN PROGRESS (not DONE) because the graph was
	// never expanded -- see deriveTicketStatus.
	assertTicketStatus(t, repo, ticket.ID, domain.TicketInProgress)

	// And it stays converged: a following poll must not reopen the race,
	// because the derived status now matches what is stored and
	// syncTicketStatus skips the write.
	follow := startCLISubprocess(t, testName, dir, dbPath, barrierPath,
		"get-executable #follow", "get-executable", ticket.ID)
	followOut, followCode := follow.wait(t)
	if outcome, reason := classifyProcessOutcome(follow.label, followOut, followCode); outcome != outcomeOK {
		t.Errorf("the follow-up get-executable did not run cleanly once the status had settled: %s", reason)
	}
	assertTicketStatus(t, repo, ticket.ID, domain.TicketInProgress)
}
