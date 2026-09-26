package autopilot

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/graph-ops/core-go/internal/domain"
)

// This file is the liveness side of plan decision D4-2: a child session runs
// in a detached terminal whose process cannot be tracked, so "is it still
// working?" is answered by watching for signs of activity. There are three:
//
//  1. the worker's own `graph-engine autopilot` calls (RecordActivity),
//  2. the ticket changing in the DB -- the ticket, its nodes, its artifacts
//     (DBFingerprint),
//  3. its worktree changing -- HEAD, status, the listed files
//     (Git.WorktreeFingerprint).
//
// 2 and 3 are fingerprints compared against the previous observation
// (ObserveActivity); any change moves LastActivityAt to now. CheckStall
// (planner.go) fails a session whose LastActivityAt is older than
// stallTimeoutMinutes.

// Activity kinds, as they appear in "last activity" details.
const (
	ActivityLaunch   = "launch"
	ActivityDB       = "db"
	ActivityWorktree = "worktree"
)

// DBFingerprint hashes the parts of a ticket that move while its graph is
// being worked on: the ticket's and every node's updated_at, and the number
// and newest created_at of its artifacts (newest as a time, not as a string:
// see domain.TimestampKey).
func DBFingerprint(d *domain.TicketDetail) string {
	if d == nil {
		return ""
	}
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%s", d.ID, d.UpdatedAt, d.Status)
	for _, n := range d.Nodes {
		fmt.Fprintf(h, "|n:%s:%s:%s", n.ID, n.Status, n.UpdatedAt)
	}
	latest := ""
	for _, a := range d.Artifacts {
		if latest == "" || domain.CompareTimestamps(a.CreatedAt, latest) > 0 {
			latest = a.CreatedAt
		}
	}
	fmt.Fprintf(h, "|a:%d:%s", len(d.Artifacts), latest)
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// ObserveActivity compares freshly computed fingerprints with st's and, if
// either changed, records activity of that kind at now. An empty fingerprint
// (could not be computed) never counts as a change. It returns whether
// activity was seen.
func ObserveActivity(st *TicketState, dbFP, worktreeFP string, now time.Time) bool {
	seen := ""
	if dbFP != "" && dbFP != st.DBFingerprint {
		if st.DBFingerprint != "" {
			seen = ActivityDB
		}
		st.DBFingerprint = dbFP
	}
	if worktreeFP != "" && worktreeFP != st.WorktreeFingerprint {
		if st.WorktreeFingerprint != "" && seen == "" {
			seen = ActivityWorktree
		}
		st.WorktreeFingerprint = worktreeFP
	}
	if seen == "" {
		return false
	}
	RecordActivity(st, seen, now)
	return true
}

// RecordActivity records a sign of life of the given kind at now. Any
// activity ends a wait for a person (touch --awaiting-human): from then on
// the stall check applies again.
func RecordActivity(st *TicketState, kind string, now time.Time) {
	st.LastActivityAt = now
	st.LastActivityKind = kind
	st.AwaitingHuman = ""
}

// IdleMinutes is how long st's session has shown no activity.
func IdleMinutes(st *TicketState, now time.Time) int {
	if st.LastActivityAt.IsZero() {
		return 0
	}
	return int(now.Sub(st.LastActivityAt) / time.Minute)
}
