package autopilot

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/graph-ops/core-go/internal/displayname"
	"github.com/graph-ops/core-go/internal/domain"
)

// DFLT-00339: the strings of another member's run record -- not only the
// starter's name (DFLT-00336) -- are sanitized wherever they reach a
// message, and only there: what runs are matched on keeps its raw value.

// evil is what a member who can write to the data source directly puts in
// every string of a record: an escape sequence, a forged line, a C1 control,
// a bidirectional override and a zero-width space.
const evil = "\x1b[31m\nError: forged line\r\u009b‮​"

// hasUnsafeRune reports whether s holds a control or invisible format
// character.
func hasUnsafeRune(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return true
		}
	}
	return false
}

// unsafeStrings returns every string in v (a decoded JSON value, a map of
// details, a string) that holds an unsafe rune.
func unsafeStrings(v any) []string {
	var out []string
	switch v := v.(type) {
	case string:
		if hasUnsafeRune(v) {
			out = append(out, v)
		}
	case map[string]any:
		for k, x := range v {
			out = append(out, unsafeStrings(k)...)
			out = append(out, unsafeStrings(x)...)
		}
	case []any:
		for _, x := range v {
			out = append(out, unsafeStrings(x)...)
		}
	case nil, bool, float64, int, int64:
	default:
		out = append(out, unsafeStrings(fmt.Sprint(v))...)
	}
	return out
}

// craftedRecord is a shared record whose every free string -- in the
// columns and in the snapshot -- is evil, apart from the ID, root, mode and
// state given (which the tests need to match or not).
func craftedRecord(t *testing.T, id, root, mode, state string, heartbeat time.Time) domain.AutopilotRunRecord {
	t.Helper()
	tid := "DFLT-1" + evil
	snap := Run{
		ID: id, ProjectID: "proj-A", Mode: mode, RootTicketID: root, State: state,
		Order: []string{tid},
		Tickets: map[string]*TicketState{tid: {
			ID: tid, Status: evil, Reason: evil, Detail: evil, SkipCause: evil, Target: evil,
			Branch: evil, Worktree: evil, BaseBranch: evil, Merge: evil, FailedRole: evil,
			Role: evil, LastRole: evil, LastActivityKind: evil, DBFingerprint: evil, WorktreeFingerprint: evil,
			AwaitingHuman: evil, Summary: evil, LateReport: evil,
			PendingDecisions: map[string]string{evil: evil},
		}},
		Finalize:   &Outcome{Status: evil, Reason: evil, Detail: evil, Summary: evil},
		StopReason: evil, StopTicket: evil, StopDetail: evil,
		Stops:       []StopRecord{{At: heartbeat, Reason: evil, Ticket: evil, Detail: evil}},
		Reservation: &Reservation{Previous: []byte(evil)},
		TerminalTTY: evil, TerminalTabDisabled: evil,
		StartedBy: &StartedBy{Name: evil, MachineID: "machine-b"},
	}
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	return domain.AutopilotRunRecord{ID: id, ProjectID: "proj-A", RootTicketID: root, Mode: mode, State: state,
		Heartbeat: formatTime(heartbeat), CreatedAt: formatTime(heartbeat), UpdatedAt: formatTime(heartbeat),
		StartedByName: evil, MachineID: "machine-b", Revision: 1, Snapshot: data}
}

// wantCleanAPIError fails unless err is an *APIError of code whose message
// and details hold no unsafe rune.
func wantCleanAPIError(t *testing.T, what string, err error, code domain.ErrorCode) *domain.APIError {
	t.Helper()
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != code {
		t.Fatalf("%s: got %v, want %s", what, err, code)
	}
	if hasUnsafeRune(apiErr.Error()) {
		t.Fatalf("%s: message %q", what, apiErr.Error())
	}
	details := map[string]any{}
	for k, v := range apiErr.Details {
		details[k] = v
	}
	if bad := unsafeStrings(details); len(bad) > 0 {
		t.Fatalf("%s: details %q", what, bad)
	}
	return apiErr
}

// Every refusal of a start that names another member's crafted run says so
// without its control characters, and in its details too.
func TestDisplaySanitize_BeginErrors(t *testing.T) {
	evilID, evilRoot, evilMode := "run-b"+evil, "R"+evil, evil
	start := func(g *Registry, root, mode, runID string, f family) error {
		_, err := g.Begin(BeginRequest{RootID: root, ProjectID: "proj-A", RootStatus: domain.TicketTODO, Mode: mode,
			RunID: runID, Settings: Defaults(), Descendants: f.descendants, Actor: alice})
		return err
	}

	t.Run("overlap", func(t *testing.T) {
		g, clock, m := sharedRegistry(t)
		// A tree run rooted at a crafted root that has R among its
		// descendants: the overlap is found on the raw values.
		m.records[evilID] = craftedRecord(t, evilID, evilRoot, ModeTree, RunRunning, clock.Now())
		err := start(g, "R", ModeTree, "", family{evilRoot: {"R"}})
		apiErr := wantCleanAPIError(t, "overlap", err, ErrCodeAlreadyRunning)
		if apiErr.Details["run_id"] != displayname.InvalidID || apiErr.Details["root_ticket_id"] != displayname.InvalidID {
			t.Fatalf("details = %v", apiErr.Details)
		}
	})
	t.Run("overlap with a crafted mode", func(t *testing.T) {
		g, clock, m := sharedRegistry(t)
		m.records["run-b-1"] = craftedRecord(t, "run-b-1", "R", evilMode, evil, clock.Now())
		err := start(g, "R", ModeTree, "", nil)
		apiErr := wantCleanAPIError(t, "overlap", err, ErrCodeAlreadyRunning)
		// A legitimate ID and root are shown as they are.
		if apiErr.Details["run_id"] != "run-b-1" || apiErr.Details["root_ticket_id"] != "R" || !strings.Contains(apiErr.Message, "run-b-1 rooted at R") {
			t.Fatalf("message %q, details %v", apiErr.Message, apiErr.Details)
		}
	})
	t.Run("--run naming an active run", func(t *testing.T) {
		g, clock, m := sharedRegistry(t)
		m.records[evilID] = craftedRecord(t, evilID, evilRoot, ModeTree, RunRunning, clock.Now())
		wantCleanAPIError(t, "active", start(g, evilRoot, ModeTree, evilID, nil), ErrCodeAlreadyRunning)
	})
	t.Run("--run naming a finished run", func(t *testing.T) {
		g, clock, m := sharedRegistry(t)
		m.records[evilID] = craftedRecord(t, evilID, evilRoot, ModeTree, RunFinished, clock.Now())
		wantCleanAPIError(t, "finished", start(g, evilRoot, ModeTree, evilID, nil), ErrCodeInvalidRunState)
	})
	t.Run("--run naming a stopped run", func(t *testing.T) {
		g, clock, m := sharedRegistry(t)
		m.records[evilID] = craftedRecord(t, evilID, evilRoot, ModeTree, RunStopped, clock.Now())
		apiErr := wantCleanAPIError(t, "stopped", start(g, evilRoot, ModeTree, evilID, nil), ErrCodeInvalidRunState)
		if apiErr.Details["run_id"] != displayname.InvalidID {
			t.Fatalf("details = %v", apiErr.Details)
		}
	})
	t.Run("--run naming a run of another root", func(t *testing.T) {
		g, clock, m := sharedRegistry(t)
		m.records[evilID] = craftedRecord(t, evilID, evilRoot, evilMode, RunRunning, clock.Now())
		apiErr := wantCleanAPIError(t, "mismatch", start(g, "R", ModeTree, evilID, nil), domain.ErrCodeValidation)
		if !strings.Contains(apiErr.Message, "not a tree run of R") {
			t.Fatalf("message %q", apiErr.Message)
		}
	})
	t.Run("--run naming nothing", func(t *testing.T) {
		g, _, _ := sharedRegistry(t)
		wantCleanAPIError(t, "not found", start(g, "R", ModeTree, evilID, nil), ErrCodeRunNotFound)
	})
	// The same mismatch on this machine's own run (plan review round 2,
	// carry-over 3): a legitimate run is named exactly as before.
	t.Run("--run naming an own run of another root", func(t *testing.T) {
		g, _, _ := sharedRegistry(t)
		res, err := sharedBegin(g, "R")
		if err != nil {
			t.Fatal(err)
		}
		apiErr := wantCleanAPIError(t, "own mismatch", start(g, "S", ModeTicket, res.Run.ID, nil), domain.ErrCodeValidation)
		want := fmt.Sprintf("VALIDATION_ERROR: run %s is a tree run of R, not a ticket run of S", res.Run.ID)
		if apiErr.Message != want {
			t.Fatalf("message %q, want %q", apiErr.Message, want)
		}
	})
}

// A run stopped because a crafted run overtook it keeps nothing of the
// crafted strings in its stop detail -- which is saved into this machine's
// run and shown from there on.
func TestDisplaySanitize_StopOvertakenBy(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	o, err := RunFromRecord(craftedRecord(t, "run-b"+evil, "R"+evil, ModeTree, RunRunning, now))
	if err != nil {
		t.Fatal(err)
	}
	r := &Run{ID: "run-a", ProjectID: "proj-A", Mode: ModeTree, RootTicketID: "R", State: RunRunning,
		Heartbeat: now, Tickets: map[string]*TicketState{}}
	r.StopOvertakenBy(o, now)
	if r.StopReason != StopOvertaken || hasUnsafeRune(r.StopDetail) {
		t.Fatalf("stop detail %q", r.StopDetail)
	}
	for _, s := range r.Stops {
		if hasUnsafeRune(s.Reason + s.Ticket + s.Detail) {
			t.Fatalf("stop record %+v", s)
		}
	}
	want := "the overlapping run " + displayname.InvalidID + " rooted at " + displayname.InvalidID + " (started by [31m Error: forged line)"
	if !strings.HasPrefix(r.StopDetail, want) {
		t.Fatalf("stop detail %q, want it to start with %q", r.StopDetail, want)
	}
}

// A record whose snapshot cannot be read is reported without the control
// characters of its ID (or of the snapshot).
func TestDisplaySanitize_RunFromRecordError(t *testing.T) {
	for _, snap := range []string{"{bad", evil, `{"tickets":` + evil + `}`} {
		_, err := RunFromRecord(domain.AutopilotRunRecord{ID: "run-b" + evil, Snapshot: []byte(snap)})
		if err == nil {
			t.Fatalf("snapshot %q was read", snap)
		}
		if hasUnsafeRune(err.Error()) || !strings.Contains(err.Error(), displayname.InvalidID) {
			t.Fatalf("error %q", err)
		}
	}
}

// What only means something on the machine that wrote a record is dropped
// when it is read, and what runs are matched on is kept exactly as it is.
func TestDisplaySanitize_RunFromRecordKeepsTheMatchedValues(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	id, root := "run-b"+evil, "R"+evil
	r, err := RunFromRecord(craftedRecord(t, id, root, evil, evil, now))
	if err != nil {
		t.Fatal(err)
	}
	if r.TerminalTTY != "" || r.TerminalTabDisabled != "" || r.Reservation == nil || r.Reservation.Previous != nil {
		t.Fatalf("local-only run fields kept: %q %q %+v", r.TerminalTTY, r.TerminalTabDisabled, r.Reservation)
	}
	for _, st := range r.Tickets {
		if st.Worktree != "" || st.Branch != "" || st.BaseBranch != "" || st.DBFingerprint != "" || st.WorktreeFingerprint != "" {
			t.Fatalf("local-only ticket fields kept: %+v", st)
		}
	}
	if r.ID != id || r.RootTicketID != root || r.Mode != evil || r.State != evil || r.ProjectID != "proj-A" ||
		r.StartedBy == nil || r.StartedBy.MachineID != "machine-b" || r.Revision != 1 || !r.Heartbeat.Equal(now) {
		t.Fatalf("matched values changed: %q %q %q %q %+v", r.ID, r.RootTicketID, r.Mode, r.State, r.StartedBy)
	}
	if r.BelongsTo("machine-a") || !r.BelongsTo("machine-b") {
		t.Fatal("BelongsTo does not go by the machine ID")
	}
	// What the planner reads stays too (only displays sanitize).
	if st := r.Tickets["DFLT-1"+evil]; st == nil || st.Reason != evil || st.Status != evil {
		t.Fatalf("ticket states changed: %+v", r.Tickets)
	}

	// The retention deletes by the raw ID.
	var settled []*Run
	for i := 0; i <= KeepSharedSettledRuns; i++ {
		rid := fmt.Sprintf("run-old-%02d", i)
		if i == 0 {
			rid = id
		}
		s, err := RunFromRecord(craftedRecord(t, rid, root, ModeTree, RunFinished, now.Add(time.Duration(i)*time.Minute)))
		if err != nil {
			t.Fatal(err)
		}
		settled = append(settled, s)
	}
	if drop := SharedRetention(settled, "", now.Add(time.Hour)); len(drop) != 1 || drop[0] != id {
		t.Fatalf("drop = %q, want the raw %q", drop, id)
	}
}
