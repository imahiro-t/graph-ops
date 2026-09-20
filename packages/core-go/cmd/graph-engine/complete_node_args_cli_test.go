package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/store"
)

// Tests for DFLT-00102 / BUG-03 + DOC-05 (the pass/fail positional argument
// and the help notation that described it wrongly) and for BUG-04 as seen from
// the CLI.
//
// What BUG-03 was: `passed = rest[0] != "false"`. Every value but that one
// literal string -- "False", "0", "no", a typo, and "passed:false", which is
// what this command's own help told people to write -- recorded a PASS. A
// reviewer writing down a failure got an approval on the record and the graph
// moved on as though the work had been signed off.

// newCompletableNode creates a ticket with one automatic node already at IN
// PROGRESS, i.e. a node get-executable has handed out: the state a node has to
// be in for complete-node to accept it.
func newCompletableNode(t *testing.T, repo store.GraphRepository, projectID string) (ticketID, nodeID string) {
	t.Helper()
	ticket, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	node, err := repo.CreateNode(domain.GraphNode{
		TicketID: ticket.ID, Name: "impl", Type: domain.NodeTypeImplementation,
		Status: domain.NodeInProgress, MaxIterations: 3,
	})
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	return ticket.ID, node.ID
}

// TestCmdCompleteNode_PassFailArgumentTable is the table completion criterion
// 1 asks for: `true` and `false` (and omitting the argument) are the only
// things accepted, and everything else is a usage error that leaves the node
// exactly as it was.
func TestCmdCompleteNode_PassFailArgumentTable(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string // the arguments after the node id
		want domain.NodeStatus
	}{
		{"true", []string{"true"}, domain.NodeDone},
		{"false", []string{"false"}, ""}, // no loop edge: the ticket blocks, the node stays put
		{"omitted defaults to pass", nil, domain.NodeDone},
		{"passed:true", []string{"passed:true"}, ""},
		{"passed:false", []string{"passed:false"}, ""},
		{"True", []string{"True"}, ""},
		{"False", []string{"False"}, ""},
		{"TRUE", []string{"TRUE"}, ""},
		{"zero", []string{"0"}, ""},
		{"one", []string{"1"}, ""},
		{"no", []string{"no"}, ""},
		{"yes", []string{"yes"}, ""},
		{"empty string", []string{""}, ""},
		{"tru", []string{"tru"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, projectID := newTestRepoWithProject(t)
			eng := engine.New(repo)
			_, nodeID := newCompletableNode(t, repo, projectID)

			var err error
			captureStdout(t, func() {
				err = cmdCompleteNode(eng, repo, append([]string{nodeID}, tc.args...))
			})

			accepted := tc.name == "true" || tc.name == "false" || tc.name == "omitted defaults to pass"
			if accepted {
				if err != nil {
					t.Fatalf("cmdCompleteNode(%q) = %v, want success", tc.args, err)
				}
			} else {
				if err == nil {
					t.Fatalf("cmdCompleteNode(%q) succeeded; an unrecognized pass/fail value must be a usage error", tc.args)
				}
				// The message has to say what is accepted, or the
				// person who just typed "passed:false" has no way to
				// learn what to type instead.
				for _, want := range []string{"true", "false"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q does not name the accepted value %q", err, want)
					}
				}
			}

			node, getErr := repo.GetNode(nodeID)
			if getErr != nil {
				t.Fatalf("GetNode: %v", getErr)
			}
			wantStatus := tc.want
			if wantStatus == "" {
				// Either a refused call or a fail with no loop edge:
				// the node itself is left where it was.
				wantStatus = domain.NodeInProgress
			}
			if node.Status != wantStatus {
				t.Errorf("node status = %s, want %s", node.Status, wantStatus)
			}
		})
	}
}

// TestCmdCompleteNode_InvalidPassFailArgumentWritesNothing checks the whole
// blast radius of a rejected argument, not just the node's status: the verdict
// is refused before the engine is called at all, so no artifact appears and
// the ticket is untouched.
func TestCmdCompleteNode_InvalidPassFailArgumentWritesNothing(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	eng := engine.New(repo)
	ticketID, nodeID := newCompletableNode(t, repo, projectID)

	before, err := repo.GetTicket(ticketID)
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}

	if err := cmdCompleteNode(eng, repo, []string{nodeID, "False"}); err == nil {
		t.Fatal(`cmdCompleteNode(..., "False") succeeded; it must be a usage error`)
	}

	node, err := repo.GetNode(nodeID)
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if node.Status != domain.NodeInProgress {
		t.Errorf("node status = %s, want IN PROGRESS", node.Status)
	}
	arts, err := repo.ListArtifactsByNode(nodeID)
	if err != nil {
		t.Fatalf("ListArtifactsByNode: %v", err)
	}
	if len(arts) != 0 {
		t.Errorf("a refused call wrote %d artifact(s): %+v", len(arts), arts)
	}
	after, err := repo.GetTicket(ticketID)
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if after.Status != before.Status || after.Blocked != before.Blocked {
		t.Errorf("ticket changed from %s/blocked=%v to %s/blocked=%v", before.Status, before.Blocked, after.Status, after.Blocked)
	}
}

// TestCmdCompleteNode_InvalidPassFailArgumentBeatsReason: the pass/fail value
// is validated before --reason is resolved, so a typo does not get as far as
// writing a rejection_reason artifact onto the gate.
func TestCmdCompleteNode_InvalidPassFailArgumentBeatsReason(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	eng := engine.New(repo)
	_, gateID := mustCreateTicketAndNode(t, repo, projectID, domain.NodeTypeApprovalGate)

	err := cmdCompleteNode(eng, repo, []string{gateID, "passed:false", "--reason", "理由"})
	if err == nil {
		t.Fatal(`cmdCompleteNode(..., "passed:false", "--reason", ...) succeeded; it must be a usage error`)
	}
	for _, want := range []string{"true", "false"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name the accepted value %q", err, want)
		}
	}
	arts, _ := repo.ListArtifactsByNode(gateID)
	if len(arts) != 0 {
		t.Errorf("expected no rejection_reason artifact, got %+v", arts)
	}
	node, _ := repo.GetNode(gateID)
	if node.Status != domain.NodeTODO {
		t.Errorf("gate status = %s, want TODO", node.Status)
	}
}

// TestCmdCompleteNode_MissingNodeWithReasonIsNodeNotFound: --reason takes its
// own lookup path through repo.GetNode, which used to report a missing node
// with its own fmt.Errorf while the engine reported the same thing as an
// APIError. One command describing one situation two ways, depending on which
// flags happened to be passed, is the sort of mismatch this ticket closes.
func TestCmdCompleteNode_MissingNodeWithReasonIsNodeNotFound(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	eng := engine.New(repo)
	if _, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Status: domain.TicketTODO}); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	withReason := cmdCompleteNode(eng, repo, []string{"NO-SUCH-NODE-99", "false", "--reason", "理由"})
	withoutReason := cmdCompleteNode(eng, repo, []string{"NO-SUCH-NODE-99", "false"})

	for name, err := range map[string]error{"--reason": withReason, "no --reason": withoutReason} {
		var apiErr *domain.APIError
		if !errors.As(err, &apiErr) || apiErr.Code != domain.ErrCodeNodeNotFound {
			t.Errorf("%s: err = %v, want a NODE_NOT_FOUND APIError", name, err)
		}
	}
	if withReason.Error() != withoutReason.Error() {
		t.Errorf("the two paths word it differently:\n  with --reason: %v\n  without:       %v", withReason, withoutReason)
	}
}

// TestCmdCompleteNode_StatusTableViaCLI is the CLI half of completion
// criterion 5's "check both the CLI and the HTTP route"; the engine-level
// table lives in internal/engine.
func TestCmdCompleteNode_StatusTableViaCLI(t *testing.T) {
	for _, tc := range []struct {
		name    string
		manual  bool
		status  domain.NodeStatus
		allowed bool
	}{
		{"automatic/IN PROGRESS", false, domain.NodeInProgress, true},
		{"automatic/IN REVIEW", false, domain.NodeInReview, true},
		{"automatic/TODO", false, domain.NodeTODO, false},
		{"automatic/DONE", false, domain.NodeDone, false},
		{"automatic/REJECTED", false, domain.NodeRejected, false},
		{"automatic/AWAITING FIX", false, domain.NodeAwaitingFix, false},
		{"manual/TODO", true, domain.NodeTODO, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, projectID := newTestRepoWithProject(t)
			eng := engine.New(repo)
			ticket, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Status: domain.TicketTODO})
			if err != nil {
				t.Fatalf("CreateTicket: %v", err)
			}
			node, err := repo.CreateNode(domain.GraphNode{
				TicketID: ticket.ID, Name: "n", Type: domain.NodeTypeImplementation,
				Status: tc.status, IsManual: tc.manual, MaxIterations: 3,
			})
			if err != nil {
				t.Fatalf("CreateNode: %v", err)
			}

			var cmdErr error
			captureStdout(t, func() {
				cmdErr = cmdCompleteNode(eng, repo, []string{node.ID, "true"})
			})

			got, _ := repo.GetNode(node.ID)
			if tc.allowed {
				if cmdErr != nil {
					t.Fatalf("complete-node from %s (is_manual=%v) = %v, want success", tc.status, tc.manual, cmdErr)
				}
				if got.Status != domain.NodeDone {
					t.Fatalf("node status = %s, want DONE", got.Status)
				}
				return
			}
			var apiErr *domain.APIError
			if !errors.As(cmdErr, &apiErr) || apiErr.Code != domain.ErrCodeInvalidNodeState {
				t.Fatalf("complete-node from %s = %v, want an INVALID_NODE_STATE APIError", tc.status, cmdErr)
			}
			if got.Status != tc.status {
				t.Errorf("a refused completion changed the node to %s; it must stay %s", got.Status, tc.status)
			}
		})
	}
}

// TestCmdCompleteNode_ClosedTicketRefusedViaCLI: a closed ticket takes no more
// work, whatever state its nodes are in.
func TestCmdCompleteNode_ClosedTicketRefusedViaCLI(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	eng := engine.New(repo)
	ticketID, nodeID := newCompletableNode(t, repo, projectID)
	closed := domain.TicketClosed
	if _, err := repo.UpdateTicket(ticketID, store.TicketPatch{Status: &closed}); err != nil {
		t.Fatalf("closing the ticket: %v", err)
	}

	err := cmdCompleteNode(eng, repo, []string{nodeID, "true"})
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != domain.ErrCodeInvalidNodeState {
		t.Fatalf("complete-node on a CLOSED ticket = %v, want an INVALID_NODE_STATE APIError", err)
	}
	node, _ := repo.GetNode(nodeID)
	if node.Status != domain.NodeInProgress {
		t.Errorf("node status = %s, want IN PROGRESS", node.Status)
	}
}

// TestCompleteNodeHelpNotationMatchesWhatIsAccepted is DOC-05: the help used
// to show `[passed:true|false]`, a notation the command has never accepted and
// which -- under the old parsing -- silently recorded a pass. Both the help
// listing and the command's own usage line have to show what is really taken.
func TestCompleteNodeHelpNotationMatchesWhatIsAccepted(t *testing.T) {
	const want = `complete-node <nodeId> [true|false] [--reason "<text>"]`

	help := captureStdout(t, printUsage)
	if !strings.Contains(help, "  "+want) {
		t.Errorf("the help listing does not show %q", want)
	}

	repo, _ := newTestRepoWithProject(t)
	usageErr := cmdCompleteNode(engine.New(repo), repo, nil)
	if usageErr == nil || !strings.Contains(usageErr.Error(), want) {
		t.Errorf("the usage line is %v, want it to show %q", usageErr, want)
	}
}

// TestNoPassedColonNotationInRepository backs the help fix up with a sweep of
// the shipped text: `passed:true` / `passed:false` must not survive anywhere
// as a *notation* a reader could copy. Test inputs are exempt -- the argument
// table above deliberately feeds both strings in as things that must be
// rejected -- and so is prose explaining the old mistake, so the sweep skips
// _test.go files and this repository's own Go comments.
func TestNoPassedColonNotationInRepository(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Skipf("cannot locate the repository root: %v", err)
	}
	var found []string
	for _, dir := range []string{"packages", "docs"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if name := d.Name(); name == "node_modules" || name == "dist" || name == ".git" {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(path, "_test.go") {
				return nil
			}
			body, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil
			}
			// Built binaries sit untracked next to the sources
			// (packages/core-go/graph-engine, packages/plugin/libexec/),
			// and a stale one still carries the old help string in its
			// string table. Only shipped text is in scope here.
			if bytes.IndexByte(body, 0) >= 0 {
				return nil
			}
			for i, line := range strings.Split(string(body), "\n") {
				if !strings.Contains(line, "passed:true") && !strings.Contains(line, "passed:false") {
					continue
				}
				// A Go comment explaining what the notation used to
				// be is not a notation anyone can copy into a shell.
				if strings.HasSuffix(path, ".go") && strings.HasPrefix(strings.TrimSpace(line), "//") {
					continue
				}
				rel, _ := filepath.Rel(root, path)
				found = append(found, rel+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(line))
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", dir, err)
		}
	}
	if len(found) > 0 {
		t.Errorf("the passed:true/passed:false notation is still documented in:\n  %s", strings.Join(found, "\n  "))
	}
}

// repositoryRoot walks up from the working directory until it finds the
// directory holding both packages/ and docs/.
func repositoryRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "packages")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "docs")); err == nil {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}
