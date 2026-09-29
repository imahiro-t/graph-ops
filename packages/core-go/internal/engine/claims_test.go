package engine

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/graph-ops/core-go/internal/config"
	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/identity"
	"github.com/graph-ops/core-go/internal/store"
)

// claimFixture is one ticket with a single automatic node, on SQLite, with
// the engine's clock under the test's control.
type claimFixture struct {
	e         *GraphEngine
	repo      store.GraphRepository
	projectID string
	ticketID  string
	nodeID    string
	now       time.Time
}

func newClaimFixture(t *testing.T) *claimFixture {
	t.Helper()
	e, repo, projectID := newTestEngine(t)
	f := &claimFixture{e: e, repo: repo, projectID: projectID, now: time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)}
	e.SetClock(func() time.Time { return f.now })
	e.SetLogf(func(string, ...any) {})
	tk, err := repo.CreateTicket(projectID, domain.Ticket{Title: "claims", Status: domain.TicketTODO, AutoExecutable: true})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	n, err := repo.CreateNode(domain.GraphNode{TicketID: tk.ID, Name: "impl", Type: domain.NodeTypeImplementation, Status: domain.NodeTODO, MaxIterations: 3})
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	f.ticketID, f.nodeID = tk.ID, n.ID
	return f
}

func (f *claimFixture) begin(t *testing.T, name, machine, run string) BeginSessionResult {
	t.Helper()
	res, err := f.e.BeginSession(f.ticketID, run, identity.Actor{Name: name, MachineID: machine})
	if err != nil {
		t.Fatalf("BeginSession(%s): %v", name, err)
	}
	return res
}

func (f *claimFixture) claim(t *testing.T, name, session string) domain.GraphNode {
	t.Helper()
	exec, err := f.e.GetExecutableNodesAs(f.ticketID, config.Catalog{}, Claimer{Name: name, SessionID: session})
	if err != nil || len(exec) != 1 {
		t.Fatalf("GetExecutableNodesAs = %+v, %v", exec, err)
	}
	return exec[0]
}

func (f *claimFixture) saveRun(t *testing.T, id, state string, heartbeat time.Time) {
	t.Helper()
	runs := f.repo.(store.AutopilotRunStore)
	ts := heartbeat.UTC().Format(time.RFC3339Nano)
	if err := runs.SaveAutopilotRun(domain.AutopilotRunRecord{
		ID: id, ProjectID: f.projectID, RootTicketID: f.ticketID, Mode: "ticket", State: state,
		Heartbeat: ts, CreatedAt: ts, UpdatedAt: ts, Revision: time.Now().UnixNano(), Snapshot: []byte("{}"),
	}); err != nil {
		t.Fatalf("SaveAutopilotRun: %v", err)
	}
}

func claimedByOther(err error) (*domain.APIError, bool) {
	var apiErr *domain.APIError
	if errors.As(err, &apiErr) && apiErr.Code == domain.ErrCodeNodeClaimedByOther {
		return apiErr, true
	}
	return nil, false
}

// The token is never part of a node's JSON: every output that prints a
// GraphNode leaves it out without having to remember to.
func TestGraphNodeJSONNeverCarriesTheClaimToken(t *testing.T) {
	tok, name := "secret-token", "Alice"
	raw, err := json.Marshal(domain.GraphNode{ID: "n", ClaimToken: &tok, ClaimedByName: &name})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "secret-token") || strings.Contains(string(raw), "claim_token") {
		t.Fatalf("GraphNode JSON carries the token: %s", raw)
	}
	if !strings.Contains(string(raw), `"claimed_by_name":"Alice"`) {
		t.Fatalf("GraphNode JSON lost the claimer: %s", raw)
	}
	// And unset claim fields do not appear at all.
	raw, _ = json.Marshal(domain.GraphNode{ID: "n"})
	for _, k := range []string{"claimed_by_name", "claimed_at", "claim_session_id", "claim_lease", "claim_heartbeat", "claimed_by_name_is_fallback"} {
		if strings.Contains(string(raw), k) {
			t.Errorf("an unclaimed node's JSON has %s: %s", k, raw)
		}
	}
}

func TestGetExecutableNodesAs_RecordsTheClaim(t *testing.T) {
	f := newClaimFixture(t)
	s := f.begin(t, "Alice", "m-1", "")
	n := f.claim(t, "Alice", s.SessionID)
	if n.ClaimToken == nil || n.ClaimedByName == nil || *n.ClaimedByName != "Alice" || n.ClaimSessionID == nil || *n.ClaimSessionID != s.SessionID ||
		n.ClaimedAt == nil || *n.ClaimedAt != f.now.Format(time.RFC3339Nano) {
		t.Fatalf("claim = %+v", n)
	}
	stored, _ := f.repo.GetNode(f.nodeID)
	if stored.ClaimToken == nil || *stored.ClaimToken != *n.ClaimToken {
		t.Fatalf("stored token %v != returned %v", stored.ClaimToken, *n.ClaimToken)
	}
}

func TestEvaluateLease(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	tok, sid, rsid := "t", "s-manual", "s-run"
	claimed := domain.GraphNode{Status: domain.NodeInProgress, ClaimToken: &tok, ClaimSessionID: &sid}
	byRun := domain.GraphNode{Status: domain.NodeInProgress, ClaimToken: &tok, ClaimSessionID: &rsid}
	at := func(d time.Duration) string { return sessionTimestamp(now.Add(-d)) }
	for _, tc := range []struct {
		name     string
		node     domain.GraphNode
		sessions map[string]domain.ProcessingSession
		runs     map[string]domain.AutopilotRunRecord
		want     string
	}{
		{"legacy: no claim record", domain.GraphNode{Status: domain.NodeInProgress}, nil, nil, domain.ClaimLeaseLegacy},
		{"unknown: no session recorded", domain.GraphNode{Status: domain.NodeInProgress, ClaimToken: &tok}, nil, nil, domain.ClaimLeaseUnknown},
		{"unknown: session not found", claimed, nil, nil, domain.ClaimLeaseUnknown},
		{"manual, 59 minutes", claimed, map[string]domain.ProcessingSession{sid: {ID: sid, Heartbeat: at(59 * time.Minute)}}, nil, domain.ClaimLeaseLive},
		{"manual, 61 minutes", claimed, map[string]domain.ProcessingSession{sid: {ID: sid, Heartbeat: at(61 * time.Minute)}}, nil, domain.ClaimLeaseExpired},
		{"run active, session silent for hours", byRun,
			map[string]domain.ProcessingSession{rsid: {ID: rsid, RunID: "run-1", Heartbeat: at(5 * time.Hour)}},
			map[string]domain.AutopilotRunRecord{"run-1": {ID: "run-1", State: "running", Heartbeat: now.Add(-9 * time.Minute).Format(time.RFC3339Nano)}},
			domain.ClaimLeaseLive},
		{"run heartbeat 11 minutes old", byRun,
			map[string]domain.ProcessingSession{rsid: {ID: rsid, RunID: "run-1", Heartbeat: at(time.Minute)}},
			map[string]domain.AutopilotRunRecord{"run-1": {ID: "run-1", State: "running", Heartbeat: now.Add(-11 * time.Minute).Format(time.RFC3339Nano)}},
			domain.ClaimLeaseExpired},
		{"run finished", byRun,
			map[string]domain.ProcessingSession{rsid: {ID: rsid, RunID: "run-1", Heartbeat: at(time.Minute)}},
			map[string]domain.AutopilotRunRecord{"run-1": {ID: "run-1", State: "finished", Heartbeat: now.Format(time.RFC3339Nano)}},
			domain.ClaimLeaseExpired},
		{"run record unreadable: the session's own heartbeat", byRun,
			map[string]domain.ProcessingSession{rsid: {ID: rsid, RunID: "run-1", Heartbeat: at(30 * time.Minute)}},
			nil, domain.ClaimLeaseLive},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := evaluateLease(tc.node, tc.sessions, tc.runs, now)
			if got != tc.want {
				t.Errorf("lease = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestUnstickNodeWith_Rules(t *testing.T) {
	t.Run("own session: released, no warning", func(t *testing.T) {
		f := newClaimFixture(t)
		s := f.begin(t, "Alice", "m-1", "")
		f.claim(t, "Alice", s.SessionID)
		res, err := f.e.UnstickNodeWith(f.nodeID, UnstickOptions{SessionID: s.SessionID})
		if err != nil || res.Node.Status != domain.NodeTODO || len(res.Warnings) != 0 {
			t.Fatalf("= %+v, %v", res, err)
		}
	})
	t.Run("another session of the same live run: released without --force", func(t *testing.T) {
		f := newClaimFixture(t)
		f.saveRun(t, "run-1", "running", f.now)
		old := f.begin(t, "Alice", "m-1", "run-1")
		f.claim(t, "Alice", old.SessionID)
		cur := f.begin(t, "Alice", "m-1", "run-1")
		if len(cur.Others) != 0 || len(cur.SameRun) != 1 {
			t.Fatalf("begin-session of the same run: others %+v same_run %+v", cur.Others, cur.SameRun)
		}
		res, err := f.e.UnstickNodeWith(f.nodeID, UnstickOptions{SessionID: cur.SessionID})
		if err != nil || res.Node.Status != domain.NodeTODO {
			t.Fatalf("= %+v, %v; want released", res, err)
		}
	})
	t.Run("another live run: refused", func(t *testing.T) {
		f := newClaimFixture(t)
		f.saveRun(t, "run-1", "running", f.now)
		f.saveRun(t, "run-2", "running", f.now)
		other := f.begin(t, "Bob", "m-2", "run-1")
		f.claim(t, "Bob", other.SessionID)
		me := f.begin(t, "Alice", "m-1", "run-2")
		if _, err := f.e.UnstickNodeWith(f.nodeID, UnstickOptions{SessionID: me.SessionID}); err == nil {
			t.Fatal("released another run's live claim")
		} else if _, ok := claimedByOther(err); !ok {
			t.Fatalf("err = %v, want NODE_CLAIMED_BY_OTHER", err)
		}
	})
	t.Run("another member's live manual session: refused with details, --force releases", func(t *testing.T) {
		f := newClaimFixture(t)
		other := f.begin(t, "Bob", "m-shared", "")
		f.claim(t, "Bob", other.SessionID)
		f.now = f.now.Add(30 * time.Minute)
		me := f.begin(t, "Alice", "m-shared", "")
		_, err := f.e.UnstickNodeWith(f.nodeID, UnstickOptions{SessionID: me.SessionID, MachineID: "m-shared"})
		apiErr, ok := claimedByOther(err)
		if !ok {
			t.Fatalf("err = %v, want NODE_CLAIMED_BY_OTHER", err)
		}
		if apiErr.Details["claimed_by_name"] != "Bob" || apiErr.Details["session_id"] != other.SessionID || apiErr.Details["same_machine"] != true {
			t.Errorf("details = %+v", apiErr.Details)
		}
		if !strings.Contains(apiErr.Message, "--force") {
			t.Errorf("message does not point at --force: %s", apiErr.Message)
		}
		n, _ := f.repo.GetNode(f.nodeID)
		if n.Status != domain.NodeInProgress || n.ClaimToken == nil {
			t.Fatalf("a refused unstick wrote: %+v", n)
		}
		res, err := f.e.UnstickNodeWith(f.nodeID, UnstickOptions{SessionID: me.SessionID, Force: true})
		if err != nil || res.Node.Status != domain.NodeTODO || len(res.Warnings) != 1 {
			t.Fatalf("--force = %+v, %v", res, err)
		}
	})
	t.Run("expired lease: released with a warning", func(t *testing.T) {
		f := newClaimFixture(t)
		other := f.begin(t, "Bob", "m-2", "")
		f.claim(t, "Bob", other.SessionID)
		f.now = f.now.Add(61 * time.Minute)
		res, err := f.e.UnstickNodeWith(f.nodeID, UnstickOptions{})
		if err != nil || res.Node.Status != domain.NodeTODO || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "older than its lease") {
			t.Fatalf("= %+v, %v", res, err)
		}
	})
	t.Run("claim without a session: released with a warning", func(t *testing.T) {
		f := newClaimFixture(t)
		f.claim(t, "Bob", "")
		res, err := f.e.UnstickNodeWith(f.nodeID, UnstickOptions{SessionID: "whatever"})
		if err != nil || res.Node.Status != domain.NodeTODO || len(res.Warnings) != 1 {
			t.Fatalf("= %+v, %v", res, err)
		}
	})
	t.Run("legacy claim: released with a warning", func(t *testing.T) {
		f := newClaimFixture(t)
		if _, err := f.repo.ClaimNode(f.nodeID, domain.NodeInProgress, claimableExclusions, nil); err != nil {
			t.Fatal(err)
		}
		res, err := f.e.UnstickNodeWith(f.nodeID, UnstickOptions{})
		if err != nil || res.Node.Status != domain.NodeTODO || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "without a record") {
			t.Fatalf("= %+v, %v", res, err)
		}
	})
	t.Run("plain UnstickNode also refuses a live claim", func(t *testing.T) {
		f := newClaimFixture(t)
		other := f.begin(t, "Bob", "m-2", "")
		f.claim(t, "Bob", other.SessionID)
		if _, err := f.e.UnstickNode(f.nodeID); err == nil {
			t.Fatal("UnstickNode released a live claim")
		}
	})
}

func TestBeginSession(t *testing.T) {
	t.Run("reports other live sessions, with and without claims", func(t *testing.T) {
		f := newClaimFixture(t)
		bob, err := f.e.BeginSession(f.ticketID, "", identity.Actor{Name: "bob@host", NameIsFallback: true, MachineID: "m-2"})
		if err != nil {
			t.Fatal(err)
		}
		if len(bob.Others) != 0 || !bob.SessionsSupported || bob.LeaseMinutes != 60 {
			t.Fatalf("first session = %+v", bob)
		}
		carol := f.begin(t, "Carol", "m-1", "")
		f.claim(t, "bob@host", bob.SessionID)
		me := f.begin(t, "Alice", "m-1", "")
		if len(me.Others) != 2 {
			t.Fatalf("others = %+v, want bob and carol", me.Others)
		}
		for _, o := range me.Others {
			switch o.SessionID {
			case bob.SessionID:
				if !o.NameIsFallback || len(o.NodeIDs) != 1 || o.SameMachine {
					t.Errorf("bob = %+v", o)
				}
			case carol.SessionID:
				if len(o.NodeIDs) != 0 || !o.SameMachine {
					t.Errorf("carol = %+v", o)
				}
			default:
				t.Errorf("unexpected peer %+v", o)
			}
		}
		st := f.repo.(store.ProcessingSessionStore)
		saved, _ := st.GetProcessingSession(bob.SessionID)
		if saved == nil || !saved.ActorNameIsFallback || saved.MachineID != "m-2" {
			t.Errorf("saved session = %+v", saved)
		}
	})
	t.Run("silent sessions are not reported, and week-old ones are deleted", func(t *testing.T) {
		f := newClaimFixture(t)
		old := f.begin(t, "Bob", "m-2", "")
		f.now = f.now.Add(8 * 24 * time.Hour)
		me := f.begin(t, "Alice", "m-1", "")
		if len(me.Others) != 0 {
			t.Fatalf("others = %+v", me.Others)
		}
		if s, _ := f.repo.(store.ProcessingSessionStore).GetProcessingSession(old.SessionID); s != nil {
			t.Errorf("a week-old session was kept: %+v", s)
		}
	})
	t.Run("a CLOSED ticket is refused", func(t *testing.T) {
		f := newClaimFixture(t)
		if _, err := f.e.CloseTicket(f.ticketID, "done elsewhere"); err != nil {
			t.Fatal(err)
		}
		if _, err := f.e.BeginSession(f.ticketID, "", identity.Actor{Name: "A"}); err == nil {
			t.Fatal("began a session on a CLOSED ticket")
		}
	})
	t.Run("heartbeats move forward only", func(t *testing.T) {
		f := newClaimFixture(t)
		s := f.begin(t, "Alice", "m-1", "")
		f.now = f.now.Add(10 * time.Minute)
		f.e.TouchSession(s.SessionID)
		f.now = f.now.Add(-5 * time.Minute)
		f.e.TouchSession(s.SessionID)
		got, _ := f.repo.(store.ProcessingSessionStore).GetProcessingSession(s.SessionID)
		if want := sessionTimestamp(f.now.Add(5 * time.Minute)); got.Heartbeat != want {
			t.Errorf("heartbeat = %s, want %s", got.Heartbeat, want)
		}
	})
}

func TestCompleteNodeWith_ClaimToken(t *testing.T) {
	art := func() []domain.Artifact {
		c := "notes"
		return []domain.Artifact{{Name: "notes", Type: domain.ArtifactText, Content: &c}}
	}
	t.Run("matching token completes", func(t *testing.T) {
		f := newClaimFixture(t)
		n := f.claim(t, "Alice", "")
		if _, err := f.e.CompleteNodeWith(f.nodeID, true, art(), CompleteNodeOptions{ClaimToken: *n.ClaimToken}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("a stale token is refused and writes nothing", func(t *testing.T) {
		f := newClaimFixture(t)
		first := f.claim(t, "Alice", "")
		if _, err := f.e.UnstickNode(f.nodeID); err != nil {
			t.Fatal(err)
		}
		f.claim(t, "Bob", "")
		_, err := f.e.CompleteNodeWith(f.nodeID, true, art(), CompleteNodeOptions{ClaimToken: *first.ClaimToken})
		var apiErr *domain.APIError
		if !errors.As(err, &apiErr) || apiErr.Code != domain.ErrCodeInvalidNodeState {
			t.Fatalf("err = %v, want INVALID_NODE_STATE", err)
		}
		n, _ := f.repo.GetNode(f.nodeID)
		arts, _ := f.repo.ListArtifactsByNode(f.nodeID)
		if n.Status != domain.NodeInProgress || len(arts) != 0 {
			t.Fatalf("a refused completion wrote: status %s, %d artifacts", n.Status, len(arts))
		}
	})
	t.Run("no token completes as before", func(t *testing.T) {
		f := newClaimFixture(t)
		f.claim(t, "Alice", "")
		if _, err := f.e.CompleteNodeWith(f.nodeID, true, nil, CompleteNodeOptions{}); err != nil {
			t.Fatal(err)
		}
	})
}

func TestAnnotateClaims(t *testing.T) {
	f := newClaimFixture(t)
	s := f.begin(t, "Alice", "m-1", "")
	f.claim(t, "Alice", s.SessionID)
	f.now = f.now.Add(3 * time.Minute)
	d, err := f.e.GetTicketDetailWithFamily(f.ticketID)
	if err != nil {
		t.Fatal(err)
	}
	n := d.Nodes[0]
	if n.ClaimLease != domain.ClaimLeaseLive || n.ClaimHeartbeat == nil {
		t.Fatalf("annotated node = %+v", n)
	}
	f.now = f.now.Add(2 * time.Hour)
	d, _ = f.e.GetTicketDetailWithFamily(f.ticketID)
	if d.Nodes[0].ClaimLease != domain.ClaimLeaseExpired {
		t.Fatalf("lease after 2h = %q, want expired", d.Nodes[0].ClaimLease)
	}
}
