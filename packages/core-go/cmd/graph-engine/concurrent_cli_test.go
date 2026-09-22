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
// Three tests live here, covering the shapes the contention takes. Nothing
// may fail in any of them:
//
//   - TestConcurrentCLIProcessesShareOneSQLiteDB is the steady state -- the
//     ticket's derived status does not change while the nodes work -- which
//     is what process-ticket spends nearly all of its time in.
//   - TestConcurrentCompleteNodeProcessesContendOnOneTicketRow is the worst
//     case -- several processes deriving the *same new* status for the *same
//     ticket row* at the same moment, so they genuinely have to write it.
//     Until DFLT-00136 made SQLite transactions begin IMMEDIATE, this is
//     where the deferred read-then-write failure still bit.
//   - TestConcurrentGetExecutableAndCompleteNodeLeaveNoOrphanClaims mixes
//     get-executable into that worst case and checks that no node is left
//     claimed without having been handed to anyone (DFLT-00136).

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
//
// None of them is tolerated anywhere in this file. Until DFLT-00136 one was:
// SQLite transactions began deferred, and a transaction that read and then
// wrote (updateTicket, driven by syncTicketStatus from complete-node and
// get-executable) failed at once when another process held the write lock,
// because SQLite does not run the busy handler for that upgrade. The DSN now
// sets _txlock=immediate, so the write lock is taken at BEGIN, where
// busy_timeout waits for it (internal/store's
// TestSQLiteImmediateTransactionWaitsInsteadOfFailingUpgrade pins that
// deterministically), and the allowance these tests used to carry for the
// "known remaining defect" is gone.
var lockErrorFragments = []string{"database is locked", "SQLITE_BUSY"}

// classifyProcessOutcome reports whether one process finished cleanly --
// exit 0 and no lock error anywhere in its output -- and, if not, why.
func classifyProcessOutcome(label, out string, code int) (bool, string) {
	for _, fragment := range lockErrorFragments {
		if strings.Contains(out, fragment) {
			return false, fmt.Sprintf(
				"%s hit a lock failure -- concurrent graph-engine processes have to wait for the write lock (busy_timeout, BEGIN IMMEDIATE), not fail:\n%s",
				label, out)
		}
	}
	if code != 0 {
		return false, fmt.Sprintf("%s exited with code %d, want 0:\n%s", label, code, out)
	}
	return true, ""
}

// outcomeTally is the per-run breakdown every concurrency test asserts on,
// rather than eyeballing a log line.
type outcomeTally struct {
	total  int
	ok     int
	failed int
}

func (t outcomeTally) String() string {
	return fmt.Sprintf("%d process(es): %d ok, %d failed", t.total, t.ok, t.failed)
}

// classifyAll tallies every process, reporting each failure as a test
// failure as it goes. matching, when non-empty, restricts the tally to
// processes whose label starts with it.
func classifyAll(t *testing.T, procs []*cliProcess, outputs []string, codes []int, matching string) outcomeTally {
	t.Helper()
	var tally outcomeTally
	for i, p := range procs {
		if matching != "" && !strings.HasPrefix(p.label, matching) {
			continue
		}
		tally.total++
		if ok, reason := classifyProcessOutcome(p.label, outputs[i], codes[i]); ok {
			tally.ok++
		} else {
			tally.failed++
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
// process writes the ticket row at all*.
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

// TestConcurrentCompleteNodeProcessesContendOnOneTicketRow covers QA-5: the
// path that actually broke in production, where several complete-node
// processes all derive a *new* status for the *same* ticket and therefore all
// have to write the same row through updateTicket's read-then-write
// transaction.
//
// Until DFLT-00136 this was the home of a known remaining defect: the
// transaction began deferred, SQLite refused to wait when it upgraded to a
// writer, and the test had to allow up to five of the six processes to fail
// (the measured distribution over 600 runs was 538 / 59 / 0 / 3 runs with
// 0 / 1 / 2 / 3 failures). With _txlock=immediate the transaction takes the
// write lock at BEGIN and waits for it under busy_timeout, so every process
// has to succeed now:
//
//   - no process fails, lock error or otherwise;
//   - every node reaches DONE;
//   - the ticket row reaches the derived status, and a following poll leaves
//     it there.
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

	for _, nodeID := range nodeIDs {
		node, err := repo.GetNode(nodeID)
		if err != nil {
			t.Fatalf("GetNode(%s): %v", nodeID, err)
		}
		if node == nil || node.Status != domain.NodeDone {
			t.Errorf("node %s: status = %v after complete-node, want DONE", nodeID, node)
		}
	}

	// IN PROGRESS (not DONE) because the graph was never expanded -- see
	// deriveTicketStatus.
	assertTicketStatus(t, repo, ticket.ID, domain.TicketInProgress)

	// And it stays there: a following poll finds the derived status already
	// stored, so syncTicketStatus skips the write.
	follow := startCLISubprocess(t, testName, dir, dbPath, barrierPath,
		"get-executable #follow", "get-executable", ticket.ID)
	followOut, followCode := follow.wait(t)
	if ok, reason := classifyProcessOutcome(follow.label, followOut, followCode); !ok {
		t.Errorf("the follow-up get-executable did not run cleanly once the status had settled: %s", reason)
	}
	assertTicketStatus(t, repo, ticket.ID, domain.TicketInProgress)
}

// Sizing of the orphan-claim test: DFLT-00100's contention shape -- six
// processes, get-executable and complete-node, on one ticket -- repeated over
// several fresh tickets.
const (
	orphanClaimRounds        = 30
	orphanClaimExecProcs     = 3
	orphanClaimCompleteProcs = 3
	orphanClaimTodoNodes     = 4
)

// TestConcurrentGetExecutableAndCompleteNodeLeaveNoOrphanClaims is
// DFLT-00136's end-to-end check. get-executable claims nodes and then syncs
// the ticket's status; before the fix, a sync that lost the deferred-upgrade
// race returned an error without handing out the nodes it had just claimed,
// leaving them IN PROGRESS with no worker (an "orphan claim") until someone
// ran unstick-node. In DFLT-00100's measurements that happened to 1.9-3.1% of
// processes under six-way contention.
//
// Each round puts a fresh ticket at stored status TODO, so the first sync is
// a real transition and the ticket-row write is contended -- exactly the
// failing path -- and runs three get-executable and three complete-node
// processes against it at once. Every round must show:
//
//	(a) no process failing, and no lock error in any output;
//	(b) every get-executable answering with a JSON node list, and no node
//	    handed to two of them;
//	(c) no orphan claim: every node at IN PROGRESS/IN REVIEW, other than the
//	    complete-node targets, was handed to some get-executable -- and so
//	    every TODO node was handed out;
//	(d) every complete-node target DONE;
//	(e) the ticket at IN PROGRESS.
//
// This test is probabilistic: at the pre-fix failure rate a single round
// would usually pass even with the bug present, hence the repetition. The
// deterministic guarantees live in internal/store's
// TestSQLiteImmediateTransactionWaitsInsteadOfFailingUpgrade (the lock
// error) and internal/engine's get_executable_rollback_test.go (the release
// of claims when get-executable fails anyway).
//
// Measured on an Apple silicon laptop (10 cores): 30 rounds take about 0.95s.
// With _txlock=immediate removed from the DSN, 10 rounds caught the lock
// failure in 11 of 20 runs; 30 rounds with both fixes removed failed 10 of
// 10 runs, one of which also showed the orphan claims themselves. With both
// fixes in place, 20 consecutive runs were clean.
func TestConcurrentGetExecutableAndCompleteNodeLeaveNoOrphanClaims(t *testing.T) {
	runMainIfSubprocess()

	const testName = "TestConcurrentGetExecutableAndCompleteNodeLeaveNoOrphanClaims"
	repo, dir, dbPath := newSubprocessSQLiteRepo(t)
	proj, err := repo.CreateProject("Orphans", "ORPH")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	start := time.Now()
	for round := 0; round < orphanClaimRounds; round++ {
		ticket, err := repo.CreateTicket(proj.ID, domain.Ticket{
			Title: fmt.Sprintf("orphan claims, round %d", round), Status: domain.TicketTODO, AutoExecutable: true,
		})
		if err != nil {
			t.Fatalf("round %d: CreateTicket: %v", round, err)
		}
		todoNodes := map[string]bool{}
		for i := 0; i < orphanClaimTodoNodes; i++ {
			node, err := repo.CreateNode(domain.GraphNode{
				TicketID: ticket.ID, Name: fmt.Sprintf("impl-%d", i), Type: domain.NodeTypeImplementation,
				Status: domain.NodeTODO, MaxIterations: 3,
			})
			if err != nil {
				t.Fatalf("round %d: CreateNode(impl-%d): %v", round, i, err)
			}
			todoNodes[node.ID] = true
		}
		completeNodes := map[string]bool{}
		completeOrder := make([]string, 0, orphanClaimCompleteProcs)
		for i := 0; i < orphanClaimCompleteProcs; i++ {
			node, err := repo.CreateNode(domain.GraphNode{
				TicketID: ticket.ID, Name: fmt.Sprintf("inv-%d", i), Type: domain.NodeTypeInvestigation,
				Status: domain.NodeInProgress, MaxIterations: 3,
			})
			if err != nil {
				t.Fatalf("round %d: CreateNode(inv-%d): %v", round, i, err)
			}
			completeNodes[node.ID] = true
			completeOrder = append(completeOrder, node.ID)
		}

		barrierPath := filepath.Join(dir, fmt.Sprintf("start-barrier-%d", round))
		procs := make([]*cliProcess, 0, orphanClaimExecProcs+orphanClaimCompleteProcs)
		for i := 0; i < orphanClaimExecProcs || i < orphanClaimCompleteProcs; i++ {
			if i < orphanClaimExecProcs {
				procs = append(procs, startCLISubprocess(t, testName, dir, dbPath, barrierPath,
					fmt.Sprintf("round %d get-executable #%d", round, i), "get-executable", ticket.ID))
			}
			if i < orphanClaimCompleteProcs {
				procs = append(procs, startCLISubprocess(t, testName, dir, dbPath, barrierPath,
					fmt.Sprintf("round %d complete-node #%d", round, i), "complete-node", completeOrder[i], "true"))
			}
		}
		releaseCLISubprocessBarrier(t, barrierPath)
		outputs := make([]string, len(procs))
		codes := make([]int, len(procs))
		for i, p := range procs {
			outputs[i], codes[i] = p.wait(t)
		}

		// (a)
		classifyAll(t, procs, outputs, codes, "")

		// (b)
		handedTo := map[string]string{}
		for i, p := range procs {
			if !strings.Contains(p.label, "get-executable") || codes[i] != 0 {
				continue
			}
			var nodes []struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal([]byte(outputs[i]), &nodes); err != nil {
				t.Errorf("%s: output is not a JSON list of executable nodes (%v):\n%s", p.label, err, outputs[i])
				continue
			}
			for _, n := range nodes {
				if other, dup := handedTo[n.ID]; dup {
					t.Errorf("round %d: node %s was handed to both %s and %s", round, n.ID, other, p.label)
				}
				handedTo[n.ID] = p.label
			}
		}

		detail, err := repo.GetTicketDetail(ticket.ID)
		if err != nil || detail == nil {
			t.Fatalf("round %d: GetTicketDetail: %v", round, err)
		}
		var orphans, unfinished []string
		for _, n := range detail.Nodes {
			if completeNodes[n.ID] {
				// (d) -- checked apart from (c): a failed complete-node
				// leaves its target IN PROGRESS too, and must not be
				// mistaken for an orphan claim.
				if n.Status != domain.NodeDone {
					unfinished = append(unfinished, fmt.Sprintf("%s (%s)", n.ID, n.Status))
				}
				continue
			}
			// (c)
			if _, handed := handedTo[n.ID]; !handed {
				if n.Status == domain.NodeInProgress || n.Status == domain.NodeInReview {
					orphans = append(orphans, fmt.Sprintf("%s (%s)", n.ID, n.Status))
				} else if todoNodes[n.ID] {
					unfinished = append(unfinished, fmt.Sprintf("%s (%s, never handed out)", n.ID, n.Status))
				}
			}
		}
		if len(orphans) != 0 {
			t.Errorf("round %d: %d orphan claim(s) -- IN PROGRESS/IN REVIEW but returned by no get-executable: %v", round, len(orphans), orphans)
		}
		if len(unfinished) != 0 {
			t.Errorf("round %d: node(s) not where the run should have left them: %v", round, unfinished)
		}

		// (e)
		assertTicketStatus(t, repo, ticket.ID, domain.TicketInProgress)
		if t.Failed() {
			t.Fatalf("stopping after round %d", round)
		}
	}
	t.Logf("%d rounds of %d processes in %s", orphanClaimRounds, orphanClaimExecProcs+orphanClaimCompleteProcs, time.Since(start))
}
