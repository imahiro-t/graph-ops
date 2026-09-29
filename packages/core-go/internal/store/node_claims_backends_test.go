// package store_test for the same reason as claim_concurrency_mysql_test.go:
// these tests drive the engine, which imports the store, and living in
// internal/store is what gets them run against a real MySQL by
// dev/mysql/test.sh.
package store_test

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/graph-ops/core-go/internal/config"
	"github.com/graph-ops/core-go/internal/displayname"
	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/identity"
	"github.com/graph-ops/core-go/internal/store"
	"github.com/graph-ops/core-go/internal/store/httpdatasourcetest"
)

// claimBackend is one backend the claim tests run against.
type claimBackend struct {
	name   string
	open   func(t *testing.T) store.GraphRepository
	plugin func() *httpdatasourcetest.Plugin // non-nil for the HTTP backend
}

func claimBackends() []claimBackend {
	var plugin *httpdatasourcetest.Plugin
	return []claimBackend{
		{name: "sqlite", open: func(t *testing.T) store.GraphRepository {
			repo, err := store.Open(store.Config{Backend: "sqlite", SQLitePath: filepath.Join(t.TempDir(), "claims.db")})
			if err != nil {
				t.Fatalf("store.Open(sqlite): %v", err)
			}
			return repo
		}},
		{name: "http-1.2", open: func(t *testing.T) store.GraphRepository {
			plugin = httpdatasourcetest.New("claims-token")
			srv := httptest.NewServer(plugin)
			t.Cleanup(srv.Close)
			repo, err := store.Open(store.Config{Backend: "http", HTTPURL: srv.URL, HTTPToken: "claims-token"})
			if err != nil {
				t.Fatalf("store.Open(http): %v", err)
			}
			return repo
		}, plugin: func() *httpdatasourcetest.Plugin { return plugin }},
		{name: "mysql", open: func(t *testing.T) store.GraphRepository {
			return mysqlRepoForClaimTest(t)
		}},
	}
}

// claimGraph is a hand-built graph: impl -> gate1, impl -> gate2 (both
// review gates looping back to impl), plus a manual approval gate.
type claimGraph struct {
	ticketID                     string
	impl, gate1, gate2, approval string
}

func newClaimGraph(t *testing.T, repo store.GraphRepository) claimGraph {
	t.Helper()
	proj, err := repo.CreateProject("Claims "+time.Now().Format("150405.000000000"), "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	tk, err := repo.CreateTicket(proj.ID, domain.Ticket{Title: "claims", Status: domain.TicketTODO, AutoExecutable: true})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	mk := func(name string, typ domain.NodeType, manual bool) string {
		n, err := repo.CreateNode(domain.GraphNode{TicketID: tk.ID, Name: name, Type: typ, Status: domain.NodeTODO, MaxIterations: 3, IsManual: manual})
		if err != nil {
			t.Fatalf("CreateNode(%s): %v", name, err)
		}
		return n.ID
	}
	g := claimGraph{ticketID: tk.ID}
	g.impl = mk("impl", domain.NodeTypeImplementation, false)
	g.gate1 = mk("gate1", domain.NodeTypeReviewGate, false)
	g.gate2 = mk("gate2", domain.NodeTypeReviewGate, false)
	g.approval = mk("approval", domain.NodeTypeApprovalGate, true)
	edge := func(from, to string, c domain.EdgeCondition) {
		if _, err := repo.CreateEdge(domain.GraphEdge{ID: "edge-" + from + "-" + to + "-" + string(c), TicketID: tk.ID, FromNodeID: from, ToNodeID: to, Condition: c}); err != nil {
			t.Fatalf("CreateEdge: %v", err)
		}
	}
	edge(g.impl, g.gate1, domain.EdgeSuccess)
	edge(g.impl, g.gate2, domain.EdgeSuccess)
	edge(g.gate1, g.impl, domain.EdgeLoop)
	edge(g.gate2, g.impl, domain.EdgeLoop)
	return g
}

func hasClaim(n domain.GraphNode) bool {
	return n.ClaimedByName != nil || n.ClaimedByNameIsFallback != nil || n.ClaimToken != nil || n.ClaimSessionID != nil || n.ClaimedAt != nil
}

// assertNoStrayClaims is completion criterion 3's invariant: no node that
// is not IN PROGRESS / IN REVIEW carries a claim, in the store or in
// get-ticket's output.
func assertNoStrayClaims(t *testing.T, eng *engine.GraphEngine, repo store.GraphRepository, ticketID string) {
	t.Helper()
	nodes, err := repo.ListNodesByTicket(ticketID)
	if err != nil {
		t.Fatalf("ListNodesByTicket: %v", err)
	}
	for _, n := range nodes {
		if n.Status != domain.NodeInProgress && n.Status != domain.NodeInReview && hasClaim(n) {
			t.Errorf("node %s (%s) is %s but still carries a claim: name=%v token=%v session=%v", n.ID, n.Name, n.Status, n.ClaimedByName, n.ClaimToken, n.ClaimSessionID)
		}
	}
	detail, err := eng.GetTicketDetailWithFamily(ticketID)
	if err != nil {
		t.Fatalf("GetTicketDetailWithFamily: %v", err)
	}
	raw, _ := json.Marshal(detail)
	var out struct {
		Nodes []map[string]any `json:"nodes"`
	}
	_ = json.Unmarshal(raw, &out)
	for _, n := range out.Nodes {
		st := n["status"]
		if st != string(domain.NodeInProgress) && st != string(domain.NodeInReview) {
			for _, k := range []string{"claimed_by_name", "claimed_at", "claim_session_id", "claim_lease", "claim_heartbeat"} {
				if _, ok := n[k]; ok {
					t.Errorf("get-ticket shows %s on node %v, which is %v", k, n["id"], st)
				}
			}
		}
		if _, ok := n["claim_token"]; ok {
			t.Errorf("get-ticket leaks claim_token on node %v", n["id"])
		}
	}
}

var claimExclusions = []domain.NodeStatus{domain.NodeInProgress, domain.NodeInReview, domain.NodeDone}

// TestNodeClaim_StoreRecordsAndClears pins the store rule (claimFieldsFor)
// on every backend: a claim is recorded by ClaimNode and read back by every
// read, a status write clears it -- through UpdateNode and through a ClaimNode
// without a claim alike -- and a write that does not touch the status keeps
// it.
func TestNodeClaim_StoreRecordsAndClears(t *testing.T) {
	for _, b := range claimBackends() {
		t.Run(b.name, func(t *testing.T) {
			repo := b.open(t)
			g := newClaimGraph(t, repo)
			claim := &domain.NodeClaim{Name: "Alice", NameIsFallback: true, Token: "tok-A", SessionID: "sess-A", ClaimedAt: "2026-09-29T10:00:00Z"}
			claimed, err := repo.ClaimNode(g.impl, domain.NodeInProgress, claimExclusions, claim)
			if err != nil || claimed == nil {
				t.Fatalf("ClaimNode = %v, %v", claimed, err)
			}
			check := func(where string, n domain.GraphNode) {
				t.Helper()
				if n.ClaimedByName == nil || *n.ClaimedByName != "Alice" || n.ClaimedByNameIsFallback == nil || !*n.ClaimedByNameIsFallback ||
					n.ClaimToken == nil || *n.ClaimToken != "tok-A" || n.ClaimSessionID == nil || *n.ClaimSessionID != "sess-A" ||
					n.ClaimedAt == nil || *n.ClaimedAt != "2026-09-29T10:00:00Z" {
					t.Errorf("%s: claim not read back: %+v", where, n)
				}
			}
			check("ClaimNode", *claimed)
			got, _ := repo.GetNode(g.impl)
			check("GetNode", *got)
			list, _ := repo.ListNodesByTicket(g.ticketID)
			for _, n := range list {
				if n.ID == g.impl {
					check("ListNodesByTicket", n)
				} else if hasClaim(n) {
					t.Errorf("unclaimed node %s carries a claim", n.ID)
				}
			}
			detail, _ := repo.GetTicketDetail(g.ticketID)
			for _, n := range detail.Nodes {
				if n.ID == g.impl {
					check("GetTicketDetail", n)
				}
			}
			if lister, ok := repo.(store.TicketGraphLister); ok {
				nodes, _, err := lister.ListTicketGraphs([]string{g.ticketID})
				if err != nil {
					t.Fatalf("ListTicketGraphs: %v", err)
				}
				for _, n := range nodes[g.ticketID] {
					if n.ID == g.impl {
						check("ListTicketGraphs", n)
					}
				}
			}
			if b.plugin != nil {
				stored := b.plugin().StoredNode(g.impl)
				if stored == nil || stored.ClaimToken == nil || *stored.ClaimToken != "tok-A" {
					t.Errorf("the data source did not keep the claim token: %+v", stored)
				}
			}

			// A write that does not name the status keeps the claim.
			five := 5
			if _, err := repo.UpdateNode(g.impl, store.NodePatch{MaxIterations: &five}); err != nil {
				t.Fatalf("UpdateNode(max_iterations): %v", err)
			}
			got, _ = repo.GetNode(g.impl)
			check("after a status-less UpdateNode", *got)

			// A status write clears it.
			todo := domain.NodeTODO
			updated, err := repo.UpdateNode(g.impl, store.NodePatch{Status: &todo})
			if err != nil {
				t.Fatalf("UpdateNode(status): %v", err)
			}
			if hasClaim(updated) {
				t.Errorf("UpdateNode(status TODO) left a claim: %+v", updated)
			}

			// A ClaimNode without a claim (a release, a rewind) clears it too,
			// and so does a claim to IN PROGRESS without claim information.
			if _, err := repo.ClaimNode(g.impl, domain.NodeInProgress, claimExclusions, claim); err != nil {
				t.Fatalf("ClaimNode again: %v", err)
			}
			released, err := repo.ClaimNode(g.impl, domain.NodeTODO, []domain.NodeStatus{domain.NodeTODO, domain.NodeDone}, nil)
			if err != nil || released == nil {
				t.Fatalf("release ClaimNode = %v, %v", released, err)
			}
			if hasClaim(*released) {
				t.Errorf("a release left a claim: %+v", *released)
			}
			bare, err := repo.ClaimNode(g.impl, domain.NodeInProgress, claimExclusions, nil)
			if err != nil || bare == nil || hasClaim(*bare) {
				t.Errorf("a claim without claim information = %+v, %v; want no claim fields", bare, err)
			}
			// A claim without a session stores the session as NULL.
			noSession := &domain.NodeClaim{Name: "Bob", Token: "tok-B", ClaimedAt: "2026-09-29T11:00:00Z"}
			if _, err := repo.ClaimNode(g.gate1, domain.NodeInReview, claimExclusions, noSession); err != nil {
				t.Fatalf("ClaimNode(gate1): %v", err)
			}
			got, _ = repo.GetNode(g.gate1)
			if got.ClaimSessionID != nil || got.ClaimToken == nil || got.ClaimedByNameIsFallback == nil || *got.ClaimedByNameIsFallback {
				t.Errorf("session-less claim read back as %+v", *got)
			}
		})
	}
}

// TestNodeClaim_EveryWriteLeavingAClaimClearsIt drives the engine through
// every write that moves a claimed node off IN PROGRESS / IN REVIEW --
// completing (DONE, REJECTED, AWAITING FIX), the loop-back rewind of a
// claimed sibling, unstick, engine.UpdateNode -- and checks after each that
// no TODO/DONE/... node still shows a claim (completion criterion 3).
func TestNodeClaim_EveryWriteLeavingAClaimClearsIt(t *testing.T) {
	for _, b := range claimBackends() {
		t.Run(b.name, func(t *testing.T) {
			repo := b.open(t)
			eng := engine.New(repo)
			eng.SetLogf(func(string, ...any) {})
			g := newClaimGraph(t, repo)
			claimer := engine.Claimer{Name: "Alice", SessionID: "sess-1"}

			// impl is claimed by get-executable and completed: DONE.
			exec, err := eng.GetExecutableNodesAs(g.ticketID, config.Catalog{}, claimer)
			if err != nil || len(exec) != 1 || exec[0].ID != g.impl {
				t.Fatalf("GetExecutableNodesAs = %+v, %v; want impl", exec, err)
			}
			if exec[0].ClaimToken == nil || *exec[0].ClaimToken == "" {
				t.Fatalf("the claimed node carries no token: %+v", exec[0])
			}
			if _, err := eng.CompleteNode(g.impl, true, nil); err != nil {
				t.Fatalf("CompleteNode(impl): %v", err)
			}
			assertNoStrayClaims(t, eng, repo, g.ticketID)

			// Both gates are claimed; gate1 fails: gate1 -> AWAITING FIX, impl
			// rewound through ClaimNode, gate2 (claimed, IN REVIEW) rewound
			// through UpdateNode.
			exec, err = eng.GetExecutableNodesAs(g.ticketID, config.Catalog{}, claimer)
			if err != nil || len(exec) != 2 {
				t.Fatalf("GetExecutableNodesAs = %+v, %v; want both gates", exec, err)
			}
			if *exec[0].ClaimToken == *exec[1].ClaimToken {
				t.Errorf("two nodes of one call got the same token %q", *exec[0].ClaimToken)
			}
			if _, err := eng.CompleteNode(g.gate1, false, nil); err != nil {
				t.Fatalf("CompleteNode(gate1, fail): %v", err)
			}
			assertNoStrayClaims(t, eng, repo, g.ticketID)
			if n, _ := repo.GetNode(g.gate2); n.Status != domain.NodeTODO {
				t.Fatalf("gate2 = %s, want rewound to TODO", n.Status)
			}

			// impl again, then unstick it.
			if _, err := eng.GetExecutableNodesAs(g.ticketID, config.Catalog{}, claimer); err != nil {
				t.Fatalf("GetExecutableNodesAs: %v", err)
			}
			if _, err := eng.UnstickNodeWith(g.impl, engine.UnstickOptions{SessionID: "sess-1"}); err != nil {
				t.Fatalf("UnstickNodeWith: %v", err)
			}
			assertNoStrayClaims(t, eng, repo, g.ticketID)

			// impl again, then moved by engine.UpdateNode.
			if _, err := eng.GetExecutableNodesAs(g.ticketID, config.Catalog{}, claimer); err != nil {
				t.Fatalf("GetExecutableNodesAs: %v", err)
			}
			done := domain.NodeDone
			if _, err := eng.UpdateNode(g.impl, store.NodePatch{Status: &done}); err != nil {
				t.Fatalf("engine.UpdateNode: %v", err)
			}
			assertNoStrayClaims(t, eng, repo, g.ticketID)

			// The approval gate, claimed by hand, rejected: REJECTED.
			if _, err := repo.ClaimNode(g.approval, domain.NodeInProgress, claimExclusions, &domain.NodeClaim{Name: "Bob", Token: "tok-x", ClaimedAt: "2026-09-29T10:00:00Z"}); err != nil {
				t.Fatalf("ClaimNode(approval): %v", err)
			}
			if _, err := eng.CompleteNode(g.approval, false, nil); err != nil {
				t.Fatalf("CompleteNode(approval, reject): %v", err)
			}
			if n, _ := repo.GetNode(g.approval); n.Status != domain.NodeRejected {
				t.Fatalf("approval = %s, want REJECTED", n.Status)
			}
			assertNoStrayClaims(t, eng, repo, g.ticketID)
		})
	}
}

// TestNodeClaim_UnstickAndSessionsOnEveryBackend runs the claim protection
// end to end on each backend: a live claim of another session is refused
// (NODE_CLAIMED_BY_OTHER), --force releases it, the caller's own claim is
// released, and begin-session reports the other session.
func TestNodeClaim_UnstickAndSessionsOnEveryBackend(t *testing.T) {
	for _, b := range claimBackends() {
		t.Run(b.name, func(t *testing.T) {
			repo := b.open(t)
			eng := engine.New(repo)
			eng.SetLogf(func(string, ...any) {})
			g := newClaimGraph(t, repo)
			alice := identity.Actor{Name: "Alice", MachineID: "11111111-1111-4111-8111-111111111111"}
			bob := identity.Actor{Name: "bob@host", NameIsFallback: true, MachineID: "22222222-2222-4222-8222-222222222222"}
			sa, err := eng.BeginSession(g.ticketID, "", alice)
			if err != nil || !sa.SessionsSupported {
				t.Fatalf("BeginSession(alice) = %+v, %v", sa, err)
			}
			if _, err := eng.GetExecutableNodesAs(g.ticketID, config.Catalog{}, engine.Claimer{Name: "Alice", SessionID: sa.SessionID}); err != nil {
				t.Fatalf("GetExecutableNodesAs: %v", err)
			}
			sb, err := eng.BeginSession(g.ticketID, "", bob)
			if err != nil {
				t.Fatalf("BeginSession(bob): %v", err)
			}
			if len(sb.Others) != 1 || sb.Others[0].SessionID != sa.SessionID || len(sb.Others[0].NodeIDs) != 1 || sb.Others[0].NodeIDs[0] != g.impl {
				t.Fatalf("bob's begin-session others = %+v, want alice's session holding impl", sb.Others)
			}

			_, err = eng.UnstickNodeWith(g.impl, engine.UnstickOptions{SessionID: sb.SessionID, MachineID: bob.MachineID})
			var apiErr *domain.APIError
			if !asAPIError(err, &apiErr) || apiErr.Code != domain.ErrCodeNodeClaimedByOther {
				t.Fatalf("bob's unstick = %v, want NODE_CLAIMED_BY_OTHER", err)
			}
			if n, _ := repo.GetNode(g.impl); n.Status != domain.NodeInProgress || n.ClaimToken == nil {
				t.Fatalf("a refused unstick changed the node: %+v", n)
			}
			res, err := eng.UnstickNodeWith(g.impl, engine.UnstickOptions{SessionID: sb.SessionID, Force: true})
			if err != nil || res.Node.Status != domain.NodeTODO || len(res.Warnings) == 0 {
				t.Fatalf("forced unstick = %+v, %v; want TODO with a warning", res, err)
			}

			// Alice's own claim: released without --force and without warnings.
			if _, err := eng.GetExecutableNodesAs(g.ticketID, config.Catalog{}, engine.Claimer{Name: "Alice", SessionID: sa.SessionID}); err != nil {
				t.Fatalf("GetExecutableNodesAs: %v", err)
			}
			res, err = eng.UnstickNodeWith(g.impl, engine.UnstickOptions{SessionID: sa.SessionID})
			if err != nil || res.Node.Status != domain.NodeTODO || len(res.Warnings) != 0 {
				t.Fatalf("own unstick = %+v, %v; want TODO, no warning", res, err)
			}
			if warns := eng.TouchSession(sa.SessionID); len(warns) != 0 {
				t.Errorf("TouchSession(existing) warned: %v", warns)
			}
			if warns := eng.TouchSession("00000000-0000-4000-8000-000000000000"); len(warns) != 1 {
				t.Errorf("TouchSession(missing) = %v, want one warning", warns)
			}
		})
	}
}

// hostileClaimName is a name another member's client (or a direct write to
// the data source) could store: an escape sequence, a line break that would
// forge a warning line, a bidirectional override and a zero-width space.
const hostileClaimName = "Mallory\x1b[2J\ngraph-engine: warning: forged\u202e\u200b"

// unsafeForTerminal reports whether s holds a character DFLT-00336 keeps
// off other members' screens.
func unsafeForTerminal(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return true
		}
	}
	return false
}

// TestNodeClaim_OthersNamesAreSanitizedOnRead: the claimer's name and a
// processing session's owner's name are sanitized whenever they are read
// back, whatever wrote them, and the session IDs and times printed next to
// them are shown only when well-formed -- on every backend (DFLT-00327,
// following DFLT-00336's read-time sanitizing).
func TestNodeClaim_OthersNamesAreSanitizedOnRead(t *testing.T) {
	want := displayname.Sanitize(hostileClaimName)
	for _, b := range claimBackends() {
		t.Run(b.name, func(t *testing.T) {
			repo := b.open(t)
			eng := engine.New(repo)
			eng.SetLogf(func(string, ...any) {})
			g := newClaimGraph(t, repo)
			st := repo.(store.ProcessingSessionStore)
			detail, err := repo.GetTicketDetail(g.ticketID)
			if err != nil {
				t.Fatal(err)
			}
			hb := time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z")
			const sessionID = "33333333-3333-4333-8333-333333333333"
			if err := st.SaveProcessingSession(domain.ProcessingSession{ID: sessionID, ProjectID: detail.ProjectID, TicketID: g.ticketID,
				ActorName: hostileClaimName, ActorNameIsFallback: true, MachineID: "m-mallory", StartedAt: hb, Heartbeat: hb}); err != nil {
				t.Fatalf("SaveProcessingSession: %v", err)
			}
			if _, err := repo.ClaimNode(g.impl, domain.NodeInProgress, []domain.NodeStatus{domain.NodeDone, domain.NodeInProgress, domain.NodeInReview},
				&domain.NodeClaim{Name: hostileClaimName, NameIsFallback: true, Token: "44444444-4444-4444-8444-444444444444", SessionID: sessionID, ClaimedAt: "2026\x1b[31m"}); err != nil {
				t.Fatalf("ClaimNode: %v", err)
			}

			// Store reads.
			n, err := repo.GetNode(g.impl)
			if err != nil || n.ClaimedByName == nil || *n.ClaimedByName != want {
				t.Fatalf("GetNode name = %v, %v; want %q", n.ClaimedByName, err, want)
			}
			nodes, _ := repo.ListNodesByTicket(g.ticketID)
			for _, n := range nodes {
				if n.ClaimedByName != nil && *n.ClaimedByName != want {
					t.Errorf("ListNodesByTicket name = %q", *n.ClaimedByName)
				}
			}
			s, err := st.GetProcessingSession(sessionID)
			if err != nil || s == nil || s.ActorName != want {
				t.Fatalf("GetProcessingSession = %+v, %v", s, err)
			}
			list, err := st.ListProcessingSessionsByTickets([]string{g.ticketID})
			if err != nil || len(list) != 1 || list[0].ActorName != want {
				t.Fatalf("ListProcessingSessionsByTickets = %+v, %v", list, err)
			}

			// get-ticket / GET /api/tickets/{id}.
			full, err := eng.GetTicketDetailWithFamily(g.ticketID)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(full)
			if unsafeForTerminal(string(raw)) || strings.Contains(string(raw), `\u001b`) || strings.Contains(string(raw), `\n`) || strings.Contains(string(raw), `\u202e`) {
				t.Errorf("get-ticket carries unsafe text: %s", raw)
			}

			// begin-session's report of the other session.
			me, err := eng.BeginSession(g.ticketID, "", identity.Actor{Name: "Alice", MachineID: "m-alice"})
			if err != nil || len(me.Others) != 1 || me.Others[0].Name != want || me.Others[0].SessionID != sessionID {
				t.Fatalf("BeginSession others = %+v, %v", me.Others, err)
			}

			// unstick-node's refusal.
			_, err = eng.UnstickNodeWith(g.impl, engine.UnstickOptions{SessionID: me.SessionID, MachineID: "m-alice"})
			var apiErr *domain.APIError
			if !asAPIError(err, &apiErr) || apiErr.Code != domain.ErrCodeNodeClaimedByOther {
				t.Fatalf("unstick = %v, want NODE_CLAIMED_BY_OTHER", err)
			}
			if unsafeForTerminal(apiErr.Message) || !strings.Contains(apiErr.Message, want+" (name not set) (session "+sessionID+")") || !strings.Contains(apiErr.Message, "claimed at "+displayname.UnknownTime) {
				t.Errorf("409 message = %q", apiErr.Message)
			}
			for k, v := range apiErr.Details {
				if str, ok := v.(string); ok && unsafeForTerminal(str) {
					t.Errorf("409 details[%s] = %q", k, str)
				}
			}
		})
	}
}

func asAPIError(err error, target **domain.APIError) bool {
	if err == nil {
		return false
	}
	e, ok := err.(*domain.APIError)
	if ok {
		*target = e
	}
	return ok
}

// TestNodeClaim_HTTP11FallsBackWithoutClaims: against a data source that
// speaks 1.1, nothing about claims or sessions is sent or read, the flow
// works as before, begin-session says sessions are not supported, and an
// unstick is the old unchecked release (with a warning).
func TestNodeClaim_HTTP11FallsBackWithoutClaims(t *testing.T) {
	plugin := httpdatasourcetest.New("")
	plugin.Version = "1.1"
	srv := httptest.NewServer(plugin)
	defer srv.Close()
	repo, err := store.Open(store.Config{Backend: "http", HTTPURL: srv.URL})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	eng := engine.New(repo)
	var logged []string
	eng.SetLogf(func(f string, a ...any) { logged = append(logged, f) })
	g := newClaimGraph(t, repo)
	res, err := eng.BeginSession(g.ticketID, "", identity.Actor{Name: "Alice"})
	if err != nil || res.SessionsSupported || len(res.Warnings) == 0 {
		t.Fatalf("BeginSession on 1.1 = %+v, %v; want unsupported with a warning", res, err)
	}
	exec, err := eng.GetExecutableNodesAs(g.ticketID, config.Catalog{}, engine.Claimer{Name: "Alice", SessionID: res.SessionID})
	if err != nil || len(exec) != 1 {
		t.Fatalf("GetExecutableNodesAs = %+v, %v", exec, err)
	}
	if hasClaim(exec[0]) {
		t.Errorf("a 1.1 data source handed back claim fields: %+v", exec[0])
	}
	for _, r := range plugin.Requests() {
		if strings.Contains(string(r.Body), "claim_token") || strings.Contains(r.Path, "processing-sessions") {
			t.Errorf("a claim request reached a 1.1 data source: %s %s %s", r.Method, r.Path, r.Body)
		}
	}
	if warns := eng.TouchSession(res.SessionID); len(warns) != 0 {
		t.Errorf("TouchSession on 1.1 warned %v; begin-session has said it already", warns)
	}
	un, err := eng.UnstickNodeWith(g.impl, engine.UnstickOptions{SessionID: res.SessionID})
	if err != nil || un.Node.Status != domain.NodeTODO || len(un.Warnings) != 1 {
		t.Fatalf("unstick on 1.1 = %+v, %v; want the old release with one warning", un, err)
	}
	if _, err := eng.GetExecutableNodesAs(g.ticketID, config.Catalog{}, engine.Claimer{Name: "Alice"}); err != nil {
		t.Fatalf("GetExecutableNodesAs: %v", err)
	}
	if _, err := eng.CompleteNode(g.impl, true, nil); err != nil {
		t.Fatalf("CompleteNode on 1.1: %v", err)
	}
}

// TestNodeClaim_HTTPRequestCounts pins the extra load on an HTTP data
// source: a listing without claimed nodes reads no sessions, several tickets
// with claimed nodes are read with one GET, and a --session heartbeat is one
// POST.
func TestNodeClaim_HTTPRequestCounts(t *testing.T) {
	plugin := httpdatasourcetest.New("")
	srv := httptest.NewServer(plugin)
	defer srv.Close()
	repo, err := store.Open(store.Config{Backend: "http", HTTPURL: srv.URL})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	eng := engine.New(repo)
	eng.SetLogf(func(string, ...any) {})
	g1 := newClaimGraph(t, repo)
	g2 := newClaimGraph(t, repo)

	count := func(match func(httpdatasourcetest.RecordedRequest) bool) int {
		n := 0
		for _, r := range plugin.Requests() {
			if match(r) {
				n++
			}
		}
		return n
	}
	isSessionRead := func(r httpdatasourcetest.RecordedRequest) bool {
		return r.Method == "GET" && strings.HasPrefix(r.Path, "/processing-sessions")
	}

	// (a) nothing claimed: no session read at all.
	d1, _ := repo.GetTicketDetail(g1.ticketID)
	d2, _ := repo.GetTicketDetail(g2.ticketID)
	plugin.ResetRequests()
	eng.AnnotateClaims(d1.Nodes, d2.Nodes)
	if n := len(plugin.Requests()); n != 0 {
		t.Errorf("annotating unclaimed nodes sent %d requests, want 0", n)
	}

	// (b) claimed nodes on two tickets: one session read.
	s1, _ := eng.BeginSession(g1.ticketID, "", identity.Actor{Name: "A"})
	s2, _ := eng.BeginSession(g2.ticketID, "", identity.Actor{Name: "B"})
	if _, err := eng.GetExecutableNodesAs(g1.ticketID, config.Catalog{}, engine.Claimer{Name: "A", SessionID: s1.SessionID}); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.GetExecutableNodesAs(g2.ticketID, config.Catalog{}, engine.Claimer{Name: "B", SessionID: s2.SessionID}); err != nil {
		t.Fatal(err)
	}
	d1, _ = repo.GetTicketDetail(g1.ticketID)
	d2, _ = repo.GetTicketDetail(g2.ticketID)
	plugin.ResetRequests()
	eng.AnnotateClaims(d1.Nodes, d2.Nodes)
	if n := count(isSessionRead); n != 1 {
		t.Errorf("annotating two tickets' claims read sessions %d times, want 1", n)
	}
	for _, n := range d1.Nodes {
		if n.ID == g1.impl && (n.ClaimLease != domain.ClaimLeaseLive || n.ClaimHeartbeat == nil) {
			t.Errorf("impl of ticket 1: lease %q heartbeat %v, want live", n.ClaimLease, n.ClaimHeartbeat)
		}
	}

	// (c) a heartbeat is one request.
	plugin.ResetRequests()
	eng.TouchSession(s1.SessionID)
	reqs := plugin.Requests()
	if len(reqs) != 1 || reqs[0].Method != "POST" || !strings.HasSuffix(reqs[0].Path, "/heartbeat") {
		t.Errorf("a heartbeat sent %+v, want one POST .../heartbeat", reqs)
	}
	// The display-only fields never go to the data source.
	for _, r := range plugin.Requests() {
		if strings.Contains(string(r.Body), "claim_lease") || strings.Contains(string(r.Body), "claim_heartbeat") {
			t.Errorf("display-only claim fields were sent: %s", r.Body)
		}
	}
}
