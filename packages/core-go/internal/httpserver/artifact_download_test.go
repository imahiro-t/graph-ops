package httpserver

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/store"
)

// downloadFixture builds a ticket graph directly in the repository for the
// "download all artifacts" tests (DFLT-00373).
type downloadFixture struct {
	t      *testing.T
	s      *Server
	repo   store.GraphRepository
	ticket domain.Ticket
}

func newDownloadFixture(t *testing.T, description string) *downloadFixture {
	t.Helper()
	s, repo, projectID := newTestServer(t)
	ticket, err := repo.CreateTicket(projectID, domain.Ticket{Title: "Zip", Description: description, Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	return &downloadFixture{t: t, s: s, repo: repo, ticket: ticket}
}

// node creates a node and then waits until the clock has moved past its
// created_at. The store lists nodes by created_at only (no tiebreaker on the
// SQLite backend), so two nodes created within the same clock tick could come
// back in either order; waiting makes every node's created_at strictly later
// than the previous one's, so the "creation order" these tests rely on for
// parallel nodes is deterministic.
func (f *downloadFixture) node(name string) domain.GraphNode {
	f.t.Helper()
	n, err := f.repo.CreateNode(domain.GraphNode{
		ID: "node-" + engine.NewArtifactID(), TicketID: f.ticket.ID, Name: name,
		Type: domain.NodeTypeImplementation, Status: domain.NodeTODO,
	})
	if err != nil {
		f.t.Fatalf("CreateNode: %v", err)
	}
	created, err := time.Parse(time.RFC3339Nano, n.CreatedAt)
	if err != nil {
		f.t.Fatalf("parsing created_at %q: %v", n.CreatedAt, err)
	}
	for !time.Now().After(created.Add(time.Microsecond)) {
		time.Sleep(50 * time.Microsecond)
	}
	return n
}

func (f *downloadFixture) edge(from, to domain.GraphNode, cond domain.EdgeCondition) {
	f.t.Helper()
	if _, err := f.repo.CreateEdge(domain.GraphEdge{
		ID: "edge-" + engine.NewArtifactID(), TicketID: f.ticket.ID,
		FromNodeID: from.ID, ToNodeID: to.ID, Condition: cond,
	}); err != nil {
		f.t.Fatalf("CreateEdge: %v", err)
	}
}

func (f *downloadFixture) artifact(n domain.GraphNode, name, content string, metadata *string) {
	f.t.Helper()
	if _, err := f.repo.CreateArtifact(domain.Artifact{
		ID: engine.NewArtifactID(), TicketID: f.ticket.ID, NodeID: n.ID,
		Name: name, Type: domain.ArtifactText, Content: &content, Metadata: metadata,
	}); err != nil {
		f.t.Fatalf("CreateArtifact: %v", err)
	}
}

func (f *downloadFixture) download() (*httptest.ResponseRecorder, map[string]string, []string) {
	f.t.Helper()
	return downloadTicketZip(f.t, f.s, f.ticket.ID)
}

// downloadTicketZip calls the endpoint and returns the response, the zip's
// entries by name, and the entry names in archive order.
func downloadTicketZip(t *testing.T, s *Server, ticketID string) (*httptest.ResponseRecorder, map[string]string, []string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/tickets/"+ticketID+"/artifacts/download", nil)
	req.Host = testHost
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		return rec, nil, nil
	}
	body := rec.Body.Bytes()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("reading zip: %v", err)
	}
	entries := make(map[string]string, len(zr.File))
	names := make([]string, 0, len(zr.File))
	for _, zf := range zr.File {
		rc, err := zf.Open()
		if err != nil {
			t.Fatalf("opening %s: %v", zf.Name, err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("reading %s: %v", zf.Name, err)
		}
		entries[zf.Name] = string(data)
		names = append(names, zf.Name)
	}
	return rec, entries, names
}

func assertEntryNames(t *testing.T, names []string, want []string) {
	t.Helper()
	if !slices.Equal(names, want) {
		t.Errorf("zip entries =\n  %q\nwant\n  %q", names, want)
	}
}

func TestDownloadTicketArtifacts_NumbersNodeFoldersAndIncludesDescription(t *testing.T) {
	const desc = "## 概要\n説明本文\n"
	f := newDownloadFixture(t, desc)
	plan := f.node("計画作成")
	impl := f.node("実装")
	review := f.node("コードレビュー")
	f.edge(plan, impl, domain.EdgeSuccess)
	f.edge(impl, review, domain.EdgeSuccess)
	f.artifact(plan, "計画", "plan body", nil)
	f.artifact(impl, "実装メモ", "impl body", nil)
	f.artifact(review, "レビュー", "review body", nil)

	rec, entries, names := f.download()
	if rec.Code != http.StatusOK {
		t.Fatalf("download: %d %s", rec.Code, rec.Body.String())
	}
	assertEntryNames(t, names, []string{
		"00_チケット説明.md",
		"01_計画作成/計画.md",
		"02_実装/実装メモ.md",
		"03_コードレビュー/レビュー.md",
	})
	if got := entries["00_チケット説明.md"]; got != desc {
		t.Errorf("description entry = %q, want the description verbatim %q", got, desc)
	}
	if got := entries["02_実装/実装メモ.md"]; got != "impl body" {
		t.Errorf("impl entry = %q", got)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("Content-Type = %q, want application/zip", ct)
	}
	wantCD := contentDispositionAttachment(sanitizeFilenameComponent(f.ticket.ID+"-Zip") + ".zip")
	if cd := rec.Header().Get("Content-Disposition"); cd != wantCD {
		t.Errorf("Content-Disposition = %q, want %q", cd, wantCD)
	}
}

func TestDownloadTicketArtifacts_SkipsNodesWithoutWritableArtifacts(t *testing.T) {
	f := newDownloadFixture(t, "")
	a := f.node("A")
	empty := f.node("成果物なし")
	broken := f.node("デコード不能")
	blank := f.node("空内容")
	b := f.node("B")
	f.edge(a, empty, domain.EdgeSuccess)
	f.edge(empty, broken, domain.EdgeSuccess)
	f.edge(broken, blank, domain.EdgeSuccess)
	f.edge(blank, b, domain.EdgeSuccess)
	f.artifact(a, "a", "a body", nil)
	base64Meta := `{"encoding":"base64"}`
	f.artifact(broken, "bad", "!!! not base64 !!!", &base64Meta)
	f.artifact(blank, "blank", "", nil)
	f.artifact(b, "b", "b body", nil)

	_, _, names := f.download()
	assertEntryNames(t, names, []string{"01_A/a.md", "02_B/b.md"})
}

func TestDownloadTicketArtifacts_OmitsBlankDescription(t *testing.T) {
	for _, desc := range []string{"", "  \n\t\n "} {
		t.Run(fmt.Sprintf("%q", desc), func(t *testing.T) {
			f := newDownloadFixture(t, desc)
			n := f.node("N")
			f.artifact(n, "x", "x body", nil)
			_, _, names := f.download()
			assertEntryNames(t, names, []string{"01_N/x.md"})
		})
	}
}

// TestDownloadTicketArtifacts_UsesTopologicalOrder covers a graph whose
// creation order disagrees with its edges (the review gate and a late-added
// front node were created first), an iteration_loop edge pointing back, and
// two parallel nodes, which keep their creation order.
func TestDownloadTicketArtifacts_UsesTopologicalOrder(t *testing.T) {
	f := newDownloadFixture(t, "d")
	gate := f.node("Gate")
	impl := f.node("Impl")
	parallelX := f.node("ParallelX")
	parallelY := f.node("ParallelY")
	front := f.node("Front") // created last but runs first
	f.edge(front, impl, domain.EdgeSuccess)
	f.edge(impl, parallelX, domain.EdgeSuccess)
	f.edge(impl, parallelY, domain.EdgeSuccess)
	f.edge(parallelX, gate, domain.EdgeSuccess)
	f.edge(parallelY, gate, domain.EdgeSuccess)
	f.edge(gate, impl, domain.EdgeLoop)
	for _, n := range []domain.GraphNode{gate, impl, parallelX, parallelY, front} {
		f.artifact(n, "out", n.Name, nil)
	}

	_, _, names := f.download()
	assertEntryNames(t, names, []string{
		"00_チケット説明.md",
		"01_Front/out.md",
		"02_Impl/out.md",
		"03_ParallelX/out.md",
		"04_ParallelY/out.md",
		"05_Gate/out.md",
	})
}

// TestDownloadTicketArtifacts_PadsToWidestNumber: with 100 numbered nodes
// every prefix, the description's included, is three digits wide, and name
// order equals node order.
func TestDownloadTicketArtifacts_PadsToWidestNumber(t *testing.T) {
	f := newDownloadFixture(t, "d")
	var want []string
	want = append(want, "000_チケット説明.md")
	for i := 0; i < 100; i++ {
		// Names deliberately sort against node order (Z-099 ... Z-000).
		name := fmt.Sprintf("Z-%03d", 99-i)
		n := f.node(name)
		f.artifact(n, "out", name, nil)
		want = append(want, fmt.Sprintf("%03d_%s/out.md", i+1, name))
	}

	_, _, names := f.download()
	assertEntryNames(t, names, want)
	sorted := slices.Clone(names)
	sort.Strings(sorted)
	if !slices.Equal(sorted, names) {
		t.Errorf("name-sorted entries differ from node order:\n  %q", sorted)
	}
}

func TestDownloadTicketArtifacts_KeepsCollisionSuffixAndSeparatesSameNamedNodes(t *testing.T) {
	f := newDownloadFixture(t, "")
	first := f.node("Review")
	second := f.node("Review")
	f.edge(first, second, domain.EdgeSuccess)
	f.artifact(first, "verdict", "round 1", nil)
	f.artifact(first, "verdict", "round 2", nil)
	f.artifact(second, "verdict", "other", nil)

	_, entries, names := f.download()
	assertEntryNames(t, names, []string{
		"01_Review/verdict.md",
		"01_Review/verdict-2.md",
		"02_Review/verdict.md",
	})
	if entries["01_Review/verdict.md"] != "round 1" || entries["01_Review/verdict-2.md"] != "round 2" {
		t.Errorf("collision entries = %q / %q", entries["01_Review/verdict.md"], entries["01_Review/verdict-2.md"])
	}
}

func TestDownloadTicketArtifacts_UnknownTicketIs404(t *testing.T) {
	s, _, _ := newTestServer(t)
	rec, _, _ := downloadTicketZip(t, s, "TEST-99999")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestOrderNodesForDownload(t *testing.T) {
	nodes := func(ids ...string) []domain.GraphNode {
		out := make([]domain.GraphNode, len(ids))
		for i, id := range ids {
			out[i] = domain.GraphNode{ID: id}
		}
		return out
	}
	e := func(from, to string, cond domain.EdgeCondition) domain.GraphEdge {
		return domain.GraphEdge{FromNodeID: from, ToNodeID: to, Condition: cond}
	}
	cases := []struct {
		name  string
		nodes []domain.GraphNode
		edges []domain.GraphEdge
		want  []string
	}{
		{"empty", nil, nil, []string{}},
		{"no edges keeps input order", nodes("a", "b", "c"), nil, []string{"a", "b", "c"}},
		{"already topological is unchanged", nodes("a", "b", "c"),
			[]domain.GraphEdge{e("a", "b", domain.EdgeSuccess), e("b", "c", domain.EdgeSuccess)}, []string{"a", "b", "c"}},
		{"edges override input order", nodes("c", "b", "a"),
			[]domain.GraphEdge{e("a", "b", domain.EdgeSuccess), e("b", "c", domain.EdgeFailure)}, []string{"a", "b", "c"}},
		{"parallel ties keep input order", nodes("y", "root", "x", "join"),
			[]domain.GraphEdge{e("root", "x", domain.EdgeSuccess), e("root", "y", domain.EdgeSuccess), e("x", "join", domain.EdgeSuccess), e("y", "join", domain.EdgeSuccess)},
			[]string{"root", "y", "x", "join"}},
		{"iteration_loop is ignored", nodes("a", "b"),
			[]domain.GraphEdge{e("a", "b", domain.EdgeSuccess), e("b", "a", domain.EdgeLoop)}, []string{"a", "b"}},
		{"edges to unknown nodes are ignored", nodes("a", "b"),
			[]domain.GraphEdge{e("ghost", "a", domain.EdgeSuccess), e("b", "ghost", domain.EdgeSuccess)}, []string{"a", "b"}},
		{"forward cycle goes last in input order", nodes("c1", "free", "c2"),
			[]domain.GraphEdge{e("c1", "c2", domain.EdgeSuccess), e("c2", "c1", domain.EdgeSuccess)}, []string{"free", "c1", "c2"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := orderNodesForDownload(tc.nodes, tc.edges)
			ids := make([]string, len(got))
			for i, n := range got {
				ids[i] = n.ID
			}
			if !slices.Equal(ids, tc.want) {
				t.Errorf("order = %q, want %q", ids, tc.want)
			}
		})
	}
}
