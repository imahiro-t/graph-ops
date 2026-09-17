package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/store"
)

// waitNodePollInterval is how often wait-node re-reads the watched nodes. A
// package variable (not a constant) so tests can shorten it.
var waitNodePollInterval = 2 * time.Second

// waitNodeTimeoutExitCode is wait-node's exit code on timeout, distinct from
// 0 (a node changed) and 1 (any error), so a caller can tell "nothing
// happened yet" apart from "something went wrong" without parsing output.
const waitNodeTimeoutExitCode = 2

// exitCodeError asks main() to exit with code without printing an "Error:"
// line. The command that returns it has already written everything the caller
// needs to stdout. Only wait-node's timeout uses it; every other error still
// exits 1 with "Error: ..." on stderr.
type exitCodeError struct {
	code int
}

func (e exitCodeError) Error() string {
	return fmt.Sprintf("exit status %d", e.code)
}

// exitCodeFor maps run()'s error to main()'s process exit code, and reports
// whether the error should be printed to stderr.
func exitCodeFor(err error) (code int, printErr bool) {
	if err == nil {
		return 0, false
	}
	var ec exitCodeError
	if errors.As(err, &ec) {
		return ec.code, false
	}
	return 1, true
}

type waitNodeChange struct {
	ID              string            `json:"id"`
	Status          domain.NodeStatus `json:"status"`
	RejectionReason string            `json:"rejection_reason,omitempty"`
}

type waitNodeResult struct {
	Result string           `json:"result"` // "changed" or "timeout"
	Nodes  []waitNodeChange `json:"nodes"`
}

// cmdWaitNode implements `wait-node <nodeId...> [--timeout <duration>]`: it
// blocks until at least one of the given nodes leaves TODO (any other status
// counts, not only DONE/REJECTED), then prints the changed nodes as JSON. On
// timeout it prints {"result":"timeout","nodes":[]} and returns an
// exitCodeError so the process exits 2. process-ticket runs it in the
// background at an approval_gate so a decision made in the Web UI resumes the
// waiting session.
func cmdWaitNode(repo store.GraphRepository, args []string) error {
	const usage = `usage: graph-engine wait-node <nodeId> [<nodeId> ...] [--timeout <duration>]`
	var ids []string
	seen := map[string]bool{}
	var timeout time.Duration
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--timeout" {
			if i+1 >= len(args) {
				return fmt.Errorf("%s: --timeout requires a value", usage)
			}
			d, err := time.ParseDuration(args[i+1])
			if err != nil || d <= 0 {
				return fmt.Errorf("%s: invalid --timeout %q (want a positive Go duration such as 30s, 10m, 12h)", usage, args[i+1])
			}
			timeout = d
			i++
			continue
		}
		if strings.HasPrefix(a, "-") {
			return fmt.Errorf("%s: unrecognized argument %q", usage, a)
		}
		if !seen[a] {
			seen[a] = true
			ids = append(ids, a)
		}
	}
	if len(ids) == 0 {
		return fmt.Errorf("%s: at least one nodeId is required", usage)
	}

	// Validate every id before waiting, so a typo fails immediately instead
	// of hanging until the timeout (or forever).
	for _, id := range ids {
		node, err := repo.GetNode(id)
		if err != nil {
			return err
		}
		if node == nil {
			return fmt.Errorf("node %s not found", id)
		}
	}

	result, err := waitForNodes(repo, ids, waitNodePollInterval, timeout)
	if err != nil {
		return err
	}
	if err := printJSON(result); err != nil {
		return err
	}
	if result.Result == "timeout" {
		return exitCodeError{code: waitNodeTimeoutExitCode}
	}
	return nil
}

// waitForNodes polls ids every interval until one or more of them is no
// longer TODO, returning every changed node (in ids order) at that moment. A
// timeout of 0 waits forever. Nodes already non-TODO on entry return
// immediately. A node that disappears mid-wait, or any DB error, ends the wait
// with an error rather than a retry: the caller (process-ticket) re-checks the
// gate with get-ticket and restarts the wait if needed.
func waitForNodes(repo store.GraphRepository, ids []string, interval, timeout time.Duration) (waitNodeResult, error) {
	var timerC <-chan time.Time
	if timeout > 0 {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		timerC = timer.C
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		changed, err := collectChangedNodes(repo, ids)
		if err != nil {
			return waitNodeResult{}, err
		}
		if len(changed) > 0 {
			return waitNodeResult{Result: "changed", Nodes: changed}, nil
		}
		select {
		case <-timerC:
			return waitNodeResult{Result: "timeout", Nodes: []waitNodeChange{}}, nil
		case <-ticker.C:
		}
	}
}

func collectChangedNodes(repo store.GraphRepository, ids []string) ([]waitNodeChange, error) {
	var changed []waitNodeChange
	for _, id := range ids {
		node, err := repo.GetNode(id)
		if err != nil {
			return nil, err
		}
		if node == nil {
			return nil, fmt.Errorf("node %s not found (deleted while waiting)", id)
		}
		if node.Status == domain.NodeTODO {
			continue
		}
		c := waitNodeChange{ID: node.ID, Status: node.Status}
		if node.Status == domain.NodeRejected {
			reason, err := latestRejectionReason(repo, node.ID)
			if err != nil {
				return nil, err
			}
			c.RejectionReason = reason
		}
		changed = append(changed, c)
	}
	return changed, nil
}

// latestRejectionReason returns the content of the node's newest
// "rejection_reason" artifact, or "" if there is none. A gate rejected,
// reopened and rejected again carries several, so "newest" matters.
//
// created_at is compared as a parsed time, not as the string the store sorts
// by: RFC3339Nano drops trailing zeros, so "...05.1Z" sorts after
// "...05.12Z" as text even though it is earlier. On an equal (or
// unparseable) timestamp the later row in the store's order wins.
func latestRejectionReason(repo store.GraphRepository, nodeID string) (string, error) {
	artifacts, err := repo.ListArtifactsByNode(nodeID)
	if err != nil {
		return "", err
	}
	var best *domain.Artifact
	var bestTime time.Time
	for i := range artifacts {
		a := &artifacts[i]
		if a.Name != "rejection_reason" || a.Content == nil {
			continue
		}
		t, err := time.Parse(time.RFC3339Nano, a.CreatedAt)
		if err != nil {
			// Unparseable: fall back to the store's own order.
			best, bestTime = a, time.Time{}
			continue
		}
		if best == nil || !t.Before(bestTime) {
			best, bestTime = a, t
		}
	}
	if best == nil {
		return "", nil
	}
	return *best.Content, nil
}
