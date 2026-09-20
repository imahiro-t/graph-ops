package store

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode"

	"gopkg.in/yaml.v3"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/store/httpdatasourcetest"
)

const testToken = "test-token"

// startPlugin runs the reference plugin on a loopback httptest server.
func startPlugin(t *testing.T, token string) (*httpdatasourcetest.Plugin, *httptest.Server) {
	t.Helper()
	p := httpdatasourcetest.New(token)
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)
	return p, srv
}

func openHTTP(t *testing.T, url, token string) *HTTPRepository {
	t.Helper()
	repo, err := Open(Config{Backend: "http", HTTPURL: url, HTTPToken: token})
	if err != nil {
		t.Fatalf("Open(http): %v", err)
	}
	r, ok := repo.(*HTTPRepository)
	if !ok {
		t.Fatalf("expected *HTTPRepository, got %T", repo)
	}
	return r
}

// newRepoAgainst builds an HTTPRepository for handler without the Open
// handshake, so error-path tests can answer anything they like.
func newRepoAgainst(t *testing.T, handler http.Handler, token string) (*HTTPRepository, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &HTTPRepository{
		baseURL: srv.URL, token: token,
		client:           newHTTPDataSourceClient(5 * time.Second),
		maxResponseBytes: defaultHTTPDataSourceMaxResponseBytes,
	}, srv
}

func TestOpen_HTTPBackendHandshakesThenInits(t *testing.T) {
	p, srv := startPlugin(t, testToken)
	openHTTP(t, srv.URL, testToken)
	reqs := p.Requests()
	if len(reqs) != 2 {
		t.Fatalf("expected exactly 2 requests (GET /protocol, POST /init), got %d: %+v", len(reqs), reqs)
	}
	if reqs[0].Method != "GET" || reqs[0].Path != "/protocol" {
		t.Errorf("first request = %s %s, want GET /protocol", reqs[0].Method, reqs[0].Path)
	}
	if reqs[1].Method != "POST" || reqs[1].Path != "/init" {
		t.Errorf("second request = %s %s, want POST /init", reqs[1].Method, reqs[1].Path)
	}
}

func TestOpen_UnsupportedBackendNamesHTTP(t *testing.T) {
	_, err := Open(Config{Backend: "postgres"})
	if err == nil || !strings.Contains(err.Error(), `"sqlite", "mysql" or "http"`) {
		t.Fatalf("expected an error listing sqlite, mysql and http, got %v", err)
	}
}

func TestValidateBackend(t *testing.T) {
	for _, ok := range []string{"", "sqlite", "mysql", "http"} {
		if err := ValidateBackend(ok); err != nil {
			t.Errorf("ValidateBackend(%q) = %v, want nil", ok, err)
		}
	}
	if err := ValidateBackend("postgres"); err == nil {
		t.Error("ValidateBackend(postgres) = nil, want an error")
	}
}

// --- all 32 operations ---

type opCase struct {
	method, path string
	call         func(t *testing.T, r *HTTPRepository, f *fixture)
}

// fixture is the state the operation table runs against.
type fixture struct {
	projectID, ticketID, nodeID, artifactID, labelID string
}

func seedFixture(t *testing.T, r *HTTPRepository) *fixture {
	t.Helper()
	proj, err := r.CreateProject("Proj", "PRJ")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	label, err := r.CreateLabel(proj.ID, "bug", "red")
	if err != nil {
		t.Fatalf("CreateLabel: %v", err)
	}
	ticket, err := r.CreateTicket(proj.ID, domain.Ticket{Title: "T", Status: domain.TicketTODO, Labels: []domain.Label{{ID: label.ID}}})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	node, err := r.CreateNode(domain.GraphNode{TicketID: ticket.ID, Name: "plan", Type: domain.NodeTypePlan, Status: domain.NodeTODO})
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	content := "hello"
	art, err := r.CreateArtifact(domain.Artifact{ID: "art-fixture", TicketID: ticket.ID, NodeID: node.ID, Name: "a", Type: domain.ArtifactText, Content: &content})
	if err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}
	return &fixture{projectID: proj.ID, ticketID: ticket.ID, nodeID: node.ID, artifactID: art.ID, labelID: label.ID}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func operationTable() map[string]opCase {
	str := func(s string) *string { return &s }
	return map[string]opCase{
		"Init": {"POST", "/init", func(t *testing.T, r *HTTPRepository, f *fixture) { must(t, r.Init()) }},
		"CreateTicket": {"POST", "/projects/{projectId}/tickets", func(t *testing.T, r *HTTPRepository, f *fixture) {
			got, err := r.CreateTicket(f.projectID, domain.Ticket{Title: "new", Status: domain.TicketTODO, Priority: domain.TicketPriorityHigh})
			must(t, err)
			if got.Title != "new" || got.ProjectID != f.projectID || got.Priority != domain.TicketPriorityHigh || got.Labels == nil {
				t.Errorf("CreateTicket decoded %+v", got)
			}
		}},
		"GetTicket": {"GET", "/tickets/{ticketId}", func(t *testing.T, r *HTTPRepository, f *fixture) {
			got, err := r.GetTicket(f.ticketID)
			must(t, err)
			if got == nil || got.ID != f.ticketID || len(got.Labels) != 1 || got.Labels[0].ID != f.labelID {
				t.Errorf("GetTicket decoded %+v", got)
			}
		}},
		"GetTicketDetail": {"GET", "/tickets/{ticketId}/detail", func(t *testing.T, r *HTTPRepository, f *fixture) {
			got, err := r.GetTicketDetail(f.ticketID)
			must(t, err)
			if got == nil || got.ID != f.ticketID || len(got.Nodes) != 1 || len(got.Artifacts) != 1 || got.Edges == nil {
				t.Errorf("GetTicketDetail decoded %+v", got)
			}
		}},
		"ListTickets": {"GET", "/tickets", func(t *testing.T, r *HTTPRepository, f *fixture) {
			got, err := r.ListTickets()
			must(t, err)
			if len(got) != 1 || got[0].ID != f.ticketID {
				t.Errorf("ListTickets decoded %+v", got)
			}
		}},
		"ListTicketsByProject": {"GET", "/projects/{projectId}/tickets", func(t *testing.T, r *HTTPRepository, f *fixture) {
			got, err := r.ListTicketsByProject(f.projectID)
			must(t, err)
			if len(got) != 1 {
				t.Errorf("ListTicketsByProject decoded %+v", got)
			}
		}},
		"UpdateTicket": {"PATCH", "/tickets/{ticketId}", func(t *testing.T, r *HTTPRepository, f *fixture) {
			got, err := r.UpdateTicket(f.ticketID, TicketPatch{Title: str("renamed")})
			must(t, err)
			if got.Title != "renamed" {
				t.Errorf("UpdateTicket decoded %+v", got)
			}
		}},
		"DeleteTicket": {"DELETE", "/tickets/{ticketId}", func(t *testing.T, r *HTTPRepository, f *fixture) {
			must(t, r.DeleteTicket(f.ticketID))
		}},
		"CreateNode": {"POST", "/tickets/{ticketId}/nodes", func(t *testing.T, r *HTTPRepository, f *fixture) {
			got, err := r.CreateNode(domain.GraphNode{TicketID: f.ticketID, Name: "impl", Type: domain.NodeTypeImplementation, Status: domain.NodeTODO})
			must(t, err)
			if got.ID != f.ticketID+"-02" || got.MaxIterations != 3 {
				t.Errorf("CreateNode decoded %+v", got)
			}
		}},
		"GetNode": {"GET", "/nodes/{nodeId}", func(t *testing.T, r *HTTPRepository, f *fixture) {
			got, err := r.GetNode(f.nodeID)
			must(t, err)
			if got == nil || got.Name != "plan" {
				t.Errorf("GetNode decoded %+v", got)
			}
		}},
		"ListNodesByTicket": {"GET", "/tickets/{ticketId}/nodes", func(t *testing.T, r *HTTPRepository, f *fixture) {
			got, err := r.ListNodesByTicket(f.ticketID)
			must(t, err)
			if len(got) != 1 {
				t.Errorf("ListNodesByTicket decoded %+v", got)
			}
		}},
		"UpdateNode": {"PATCH", "/nodes/{nodeId}", func(t *testing.T, r *HTTPRepository, f *fixture) {
			done := domain.NodeDone
			got, err := r.UpdateNode(f.nodeID, NodePatch{Status: &done})
			must(t, err)
			if got.Status != domain.NodeDone {
				t.Errorf("UpdateNode decoded %+v", got)
			}
		}},
		"DeleteNode": {"DELETE", "/nodes/{nodeId}", func(t *testing.T, r *HTTPRepository, f *fixture) {
			must(t, r.DeleteNode(f.nodeID))
		}},
		"CreateEdge": {"POST", "/tickets/{ticketId}/edges", func(t *testing.T, r *HTTPRepository, f *fixture) {
			got, err := r.CreateEdge(domain.GraphEdge{ID: "edge-x", TicketID: f.ticketID, FromNodeID: f.nodeID, ToNodeID: f.nodeID})
			must(t, err)
			if got.ID != "edge-x" || got.Condition != domain.EdgeAlways || got.CreatedAt == "" {
				t.Errorf("CreateEdge decoded %+v", got)
			}
		}},
		"ListEdgesByTicket": {"GET", "/tickets/{ticketId}/edges", func(t *testing.T, r *HTTPRepository, f *fixture) {
			got, err := r.ListEdgesByTicket(f.ticketID)
			must(t, err)
			if got == nil {
				t.Errorf("ListEdgesByTicket returned nil, want a non-nil slice")
			}
		}},
		"ClearEdgesByTicket": {"DELETE", "/tickets/{ticketId}/edges", func(t *testing.T, r *HTTPRepository, f *fixture) {
			must(t, r.ClearEdgesByTicket(f.ticketID))
		}},
		"CreateArtifact": {"POST", "/tickets/{ticketId}/artifacts", func(t *testing.T, r *HTTPRepository, f *fixture) {
			c := "x"
			got, err := r.CreateArtifact(domain.Artifact{ID: "art-2", TicketID: f.ticketID, NodeID: f.nodeID, Name: "n", Type: domain.ArtifactText, Content: &c})
			must(t, err)
			if got.ID != "art-2" || !got.HasContent {
				t.Errorf("CreateArtifact decoded %+v", got)
			}
		}},
		"GetArtifact": {"GET", "/artifacts/{artifactId}", func(t *testing.T, r *HTTPRepository, f *fixture) {
			got, err := r.GetArtifact(f.artifactID)
			must(t, err)
			if got == nil || got.Content == nil || *got.Content != "hello" {
				t.Errorf("GetArtifact decoded %+v", got)
			}
		}},
		"ListArtifactsByTicket": {"GET", "/tickets/{ticketId}/artifacts", func(t *testing.T, r *HTTPRepository, f *fixture) {
			got, err := r.ListArtifactsByTicket(f.ticketID)
			must(t, err)
			if len(got) != 1 {
				t.Errorf("ListArtifactsByTicket decoded %+v", got)
			}
		}},
		"ListArtifactsByNode": {"GET", "/nodes/{nodeId}/artifacts", func(t *testing.T, r *HTTPRepository, f *fixture) {
			got, err := r.ListArtifactsByNode(f.nodeID)
			must(t, err)
			if len(got) != 1 {
				t.Errorf("ListArtifactsByNode decoded %+v", got)
			}
		}},
		"CreateProject": {"POST", "/projects", func(t *testing.T, r *HTTPRepository, f *fixture) {
			got, err := r.CreateProject("Second", "")
			must(t, err)
			if got.Name != "Second" || got.Prefix == "" || got.ID == "" {
				t.Errorf("CreateProject decoded %+v", got)
			}
		}},
		"GetProject": {"GET", "/projects/{projectId}", func(t *testing.T, r *HTTPRepository, f *fixture) {
			got, err := r.GetProject(f.projectID)
			must(t, err)
			if got == nil || got.Prefix != "PRJ" {
				t.Errorf("GetProject decoded %+v", got)
			}
		}},
		"ListProjects": {"GET", "/projects", func(t *testing.T, r *HTTPRepository, f *fixture) {
			got, err := r.ListProjects()
			must(t, err)
			if len(got) != 1 {
				t.Errorf("ListProjects decoded %+v", got)
			}
		}},
		"UpdateProject": {"PATCH", "/projects/{projectId}", func(t *testing.T, r *HTTPRepository, f *fixture) {
			got, err := r.UpdateProject(f.projectID, ProjectPatch{Name: str("Renamed")})
			must(t, err)
			if got.Name != "Renamed" {
				t.Errorf("UpdateProject decoded %+v", got)
			}
		}},
		"DeleteProject": {"DELETE", "/projects/{projectId}", func(t *testing.T, r *HTTPRepository, f *fixture) {
			must(t, r.DeleteProject(f.projectID))
		}},
		"CreateLabel": {"POST", "/projects/{projectId}/labels", func(t *testing.T, r *HTTPRepository, f *fixture) {
			got, err := r.CreateLabel(f.projectID, "feature", "blue")
			must(t, err)
			if got.Name != "feature" || got.Color != domain.LabelColorBlue {
				t.Errorf("CreateLabel decoded %+v", got)
			}
		}},
		"GetLabel": {"GET", "/labels/{labelId}", func(t *testing.T, r *HTTPRepository, f *fixture) {
			got, err := r.GetLabel(f.labelID)
			must(t, err)
			if got == nil || got.Name != "bug" {
				t.Errorf("GetLabel decoded %+v", got)
			}
		}},
		"ListLabelsByProject": {"GET", "/projects/{projectId}/labels", func(t *testing.T, r *HTTPRepository, f *fixture) {
			got, err := r.ListLabelsByProject(f.projectID)
			must(t, err)
			if len(got) != 1 || got[0].TicketCount != 1 {
				t.Errorf("ListLabelsByProject decoded %+v", got)
			}
		}},
		"UpdateLabel": {"PATCH", "/labels/{labelId}", func(t *testing.T, r *HTTPRepository, f *fixture) {
			got, err := r.UpdateLabel(f.labelID, LabelPatch{Color: str("green")})
			must(t, err)
			if got.Color != domain.LabelColorGreen {
				t.Errorf("UpdateLabel decoded %+v", got)
			}
		}},
		"DeleteLabel": {"DELETE", "/labels/{labelId}", func(t *testing.T, r *HTTPRepository, f *fixture) {
			n, err := r.DeleteLabel(f.labelID)
			must(t, err)
			if n != 1 {
				t.Errorf("DeleteLabel = %d, want 1", n)
			}
		}},
		"GetCurrentProjectID": {"GET", "/current-project", func(t *testing.T, r *HTTPRepository, f *fixture) {
			_, err := r.GetCurrentProjectID()
			must(t, err)
		}},
		"SetCurrentProjectID": {"PUT", "/current-project", func(t *testing.T, r *HTTPRepository, f *fixture) {
			must(t, r.SetCurrentProjectID(f.projectID))
		}},
	}
}

// composedHTTPMethods names the GraphRepository methods HTTPRepository
// satisfies by combining operations the datasource already exposes, rather
// than by calling one of its own. They are excluded from the operation table
// and from the OpenAPI operationId check below because there is no wire
// operation for them to match: adding one would oblige every datasource
// implementation (the Jira sample included) to grow an endpoint it could not
// implement any better than this composition does.
//
// ClaimNode (DFLT-00102) is GetNode followed by UpdateNode. The SQL backends
// do it as a single compare-and-swap; over HTTP the remote is the system of
// record and the check and the write stay two calls, which
// GraphRepository.ClaimNode's doc comment states outright.
var composedHTTPMethods = map[string]bool{"ClaimNode": true}

// graphRepositoryMethodNames lists the methods that must each map to exactly
// one datasource operation -- every GraphRepository method except the composed
// ones above.
func graphRepositoryMethodNames() []string {
	typ := reflect.TypeOf((*GraphRepository)(nil)).Elem()
	var names []string
	for i := 0; i < typ.NumMethod(); i++ {
		if name := typ.Method(i).Name; !composedHTTPMethods[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func TestHTTPRepository_OperationTableCoversAll32Methods(t *testing.T) {
	names := graphRepositoryMethodNames()
	if len(names) != 32 {
		t.Fatalf("GraphRepository has %d methods, want 32", len(names))
	}
	var tableNames []string
	for name := range operationTable() {
		tableNames = append(tableNames, name)
	}
	sort.Strings(tableNames)
	if !reflect.DeepEqual(names, tableNames) {
		t.Fatalf("operation table keys %v != GraphRepository methods %v", tableNames, names)
	}
}

// TestHTTPRepository_ClaimNodeComposesExistingOperations pins the claim's
// shape over HTTP: it reaches the datasource only through operations the
// protocol already has (GET /nodes/{id}, then PATCH /nodes/{id}), never a new
// one, and it reports a node somebody else already holds as (nil, nil)
// without issuing the PATCH at all.
func TestHTTPRepository_ClaimNodeComposesExistingOperations(t *testing.T) {
	p, srv := startPlugin(t, testToken)
	r := openHTTP(t, srv.URL, testToken)
	f := seedFixture(t, r)
	excluded := []domain.NodeStatus{domain.NodeDone, domain.NodeInProgress, domain.NodeInReview}
	p.ResetRequests()

	claimed, err := r.ClaimNode(f.nodeID, domain.NodeInProgress, excluded)
	must(t, err)
	if claimed == nil || claimed.Status != domain.NodeInProgress {
		t.Fatalf("ClaimNode = %+v, want the node at IN PROGRESS", claimed)
	}
	var got []string
	for _, req := range p.Requests() {
		got = append(got, req.Method+" "+req.Path)
	}
	want := []string{"GET /nodes/" + f.nodeID, "PATCH /nodes/" + f.nodeID}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ClaimNode issued %v, want %v", got, want)
	}

	// It is IN PROGRESS now, so it is in the excluded set: the second claim
	// must come back empty, and must not write.
	p.ResetRequests()
	again, err := r.ClaimNode(f.nodeID, domain.NodeInProgress, excluded)
	must(t, err)
	if again != nil {
		t.Fatalf("second ClaimNode = %+v, want nil (already claimed)", again)
	}
	for _, req := range p.Requests() {
		if req.Method != http.MethodGet {
			t.Fatalf("a refused claim issued %s %s; it must not write", req.Method, req.Path)
		}
	}
}

func TestHTTPRepository_EveryOperationMapsToItsRequest(t *testing.T) {
	for name, tc := range operationTable() {
		t.Run(name, func(t *testing.T) {
			p, srv := startPlugin(t, testToken)
			r := openHTTP(t, srv.URL, testToken)
			f := seedFixture(t, r)
			p.ResetRequests()

			tc.call(t, r, f)

			reqs := p.Requests()
			if len(reqs) != 1 {
				t.Fatalf("expected exactly one request, got %d: %+v", len(reqs), reqs)
			}
			req := reqs[0]
			wantPath := strings.NewReplacer(
				"{projectId}", f.projectID, "{ticketId}", f.ticketID, "{nodeId}", f.nodeID,
				"{artifactId}", f.artifactID, "{labelId}", f.labelID,
			).Replace(tc.path)
			if req.Method != tc.method || req.Path != wantPath {
				t.Errorf("request = %s %s, want %s %s", req.Method, req.Path, tc.method, wantPath)
			}
			if got := req.Header.Get("Authorization"); got != "Bearer "+testToken {
				t.Errorf("Authorization = %q", got)
			}
			if got := req.Header.Get(HTTPDataSourceProtocolHeader); got != "1.0" {
				t.Errorf("%s = %q, want 1.0", HTTPDataSourceProtocolHeader, got)
			}
		})
	}
}

func TestHTTPRepository_PathIDsAreEscaped(t *testing.T) {
	p, srv := startPlugin(t, "")
	r := openHTTP(t, srv.URL, "")
	p.ResetRequests()
	got, err := r.GetTicket("a/b c")
	if err != nil || got != nil {
		t.Fatalf("GetTicket(missing) = %v, %v; want nil, nil", got, err)
	}
	if path := p.Requests()[0].Path; path != "/tickets/a%2Fb%20c" {
		t.Fatalf("path = %q, want /tickets/a%%2Fb%%20c", path)
	}
}

func TestHTTPRepository_DeleteLabelReturnsDetachedCount(t *testing.T) {
	_, srv := startPlugin(t, "")
	r := openHTTP(t, srv.URL, "")
	proj, _ := r.CreateProject("P", "P")
	label, _ := r.CreateLabel(proj.ID, "x", "red")
	for i := 0; i < 2; i++ {
		if _, err := r.CreateTicket(proj.ID, domain.Ticket{Title: "t", Labels: []domain.Label{{ID: label.ID}}}); err != nil {
			t.Fatal(err)
		}
	}
	n, err := r.DeleteLabel(label.ID)
	if err != nil || n != 2 {
		t.Fatalf("DeleteLabel = %d, %v; want 2, nil", n, err)
	}
}

func TestHTTPRepository_CurrentProject(t *testing.T) {
	p, srv := startPlugin(t, "")
	r := openHTTP(t, srv.URL, "")
	id, err := r.GetCurrentProjectID()
	if err != nil || id != "" {
		t.Fatalf("GetCurrentProjectID on a fresh plugin = %q, %v; want \"\", nil", id, err)
	}
	p.ResetRequests()
	if err := r.SetCurrentProjectID("proj-1"); err != nil {
		t.Fatal(err)
	}
	req := p.Requests()[0]
	if req.Method != "PUT" || req.Path != "/current-project" {
		t.Fatalf("request = %s %s", req.Method, req.Path)
	}
	var body map[string]any
	if err := json.Unmarshal(req.Body, &body); err != nil || !reflect.DeepEqual(body, map[string]any{"project_id": "proj-1"}) {
		t.Fatalf("body = %s (%v)", req.Body, err)
	}
	if id, _ := r.GetCurrentProjectID(); id != "proj-1" {
		t.Fatalf("GetCurrentProjectID after set = %q", id)
	}
}

func TestHTTPRepository_ArtifactListingMayOmitContent(t *testing.T) {
	_, srv := startPlugin(t, "")
	r := openHTTP(t, srv.URL, "")
	f := seedFixture(t, r)
	html := "<html><body>report</body></html>"
	img := "iVBORw0KGgo="
	for _, a := range []domain.Artifact{
		{ID: "art-html", Type: domain.ArtifactHTML, Content: &html},
		{ID: "art-img", Type: domain.ArtifactImage, Content: &img},
	} {
		a.TicketID, a.NodeID, a.Name = f.ticketID, f.nodeID, a.ID
		if _, err := r.CreateArtifact(a); err != nil {
			t.Fatal(err)
		}
	}
	list, err := r.ListArtifactsByTicket(f.ticketID)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range list {
		if domain.IsFileBackedArtifactType(a.Type) && (!a.HasContent || a.Content != nil) {
			t.Errorf("listed %s artifact: has_content=%v content=%v; want true and omitted", a.Type, a.HasContent, a.Content)
		}
	}
	got, err := r.GetArtifact("art-img")
	if err != nil || got == nil || got.Content == nil || *got.Content != img {
		t.Fatalf("GetArtifact(image) = %+v, %v; want the base64 content back", got, err)
	}
}

// --- patch three states ---

func lastBody(t *testing.T, p *httpdatasourcetest.Plugin) map[string]json.RawMessage {
	t.Helper()
	reqs := p.Requests()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(reqs[len(reqs)-1].Body, &m); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	return m
}

func TestHTTPRepository_AssigneeThreeStates(t *testing.T) {
	p, srv := startPlugin(t, "")
	r := openHTTP(t, srv.URL, "")
	f := seedFixture(t, r)
	alice, bob := "alice", "bob"
	var nilStr *string
	title := "t"
	cases := []struct {
		name   string
		update func() error
		want   string // "" = key absent
	}{
		{"ticket omitted", func() error { _, err := r.UpdateTicket(f.ticketID, TicketPatch{Title: &title}); return err }, ""},
		{"ticket null", func() error { _, err := r.UpdateTicket(f.ticketID, TicketPatch{Assignee: &nilStr}); return err }, "null"},
		{"ticket set", func() error { a := &alice; _, err := r.UpdateTicket(f.ticketID, TicketPatch{Assignee: &a}); return err }, `"alice"`},
		{"node omitted", func() error { _, err := r.UpdateNode(f.nodeID, NodePatch{Name: &title}); return err }, ""},
		{"node null", func() error { _, err := r.UpdateNode(f.nodeID, NodePatch{Assignee: &nilStr}); return err }, "null"},
		{"node set", func() error { b := &bob; _, err := r.UpdateNode(f.nodeID, NodePatch{Assignee: &b}); return err }, `"bob"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.update(); err != nil {
				t.Fatal(err)
			}
			raw, ok := lastBody(t, p)["assignee"]
			if tc.want == "" {
				if ok {
					t.Fatalf("assignee key present (%s), want absent", raw)
				}
				return
			}
			if !ok || string(raw) != tc.want {
				t.Fatalf("assignee = %s (present=%v), want %s", raw, ok, tc.want)
			}
		})
	}
}

func TestHTTPRepository_LabelIDsThreeStates(t *testing.T) {
	p, srv := startPlugin(t, "")
	r := openHTTP(t, srv.URL, "")
	f := seedFixture(t, r)
	l2, _ := r.CreateLabel(f.projectID, "second", "blue")
	title := "t"
	empty := []string{}
	both := []string{f.labelID, l2.ID}

	if _, err := r.UpdateTicket(f.ticketID, TicketPatch{Title: &title}); err != nil {
		t.Fatal(err)
	}
	if _, ok := lastBody(t, p)["label_ids"]; ok {
		t.Error("label_ids present when LabelIDs is nil")
	}
	if _, err := r.UpdateTicket(f.ticketID, TicketPatch{LabelIDs: &empty}); err != nil {
		t.Fatal(err)
	}
	if raw := lastBody(t, p)["label_ids"]; string(raw) != "[]" {
		t.Errorf("label_ids = %s, want []", raw)
	}
	got, err := r.UpdateTicket(f.ticketID, TicketPatch{LabelIDs: &both})
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(both)
	if raw := lastBody(t, p)["label_ids"]; string(raw) != string(want) {
		t.Errorf("label_ids = %s, want %s", raw, want)
	}
	if len(got.Labels) != 2 {
		t.Errorf("ticket labels after replace = %+v", got.Labels)
	}
}

// --- error mapping ---

func errorHandler(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func TestHTTPRepository_KnownErrorCodesBecomeAPIErrors(t *testing.T) {
	cases := []struct {
		status int
		code   domain.ErrorCode
	}{
		{404, domain.ErrCodeTicketNotFound}, {404, domain.ErrCodeNodeNotFound}, {404, domain.ErrCodeArtifactNotFound},
		{404, domain.ErrCodeProjectNotFound}, {404, domain.ErrCodeLabelNotFound}, {409, domain.ErrCodeLabelNameTaken},
		{400, domain.ErrCodeInvalidLabelName}, {400, domain.ErrCodeInvalidLabelColor}, {400, domain.ErrCodeInvalidPrefix},
		{409, domain.ErrCodePrefixTaken}, {400, domain.ErrCodeValidation}, {500, domain.ErrCodeInternal},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%d %s", tc.status, tc.code), func(t *testing.T) {
			r, _ := newRepoAgainst(t, errorHandler(tc.status, fmt.Sprintf(`{"error":{"code":%q,"message":"msg"}}`, tc.code)), "")
			// UpdateTicket is not a Get operation, so a NOT_FOUND code is an
			// error here rather than (nil, nil).
			_, err := r.UpdateTicket("X-1", TicketPatch{})
			var apiErr *domain.APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("err = %v (%T), want *domain.APIError", err, err)
			}
			if apiErr.Code != tc.code || apiErr.Message != "msg" {
				t.Fatalf("APIError = %+v, want code %s message msg", apiErr, tc.code)
			}
		})
	}
}

func TestHTTPRepository_GetNotFoundIsNilNil(t *testing.T) {
	cases := []struct {
		op   string
		code domain.ErrorCode
		call func(r *HTTPRepository) (any, error)
	}{
		{"GetTicket", domain.ErrCodeTicketNotFound, func(r *HTTPRepository) (any, error) { v, err := r.GetTicket("x"); return v, err }},
		{"GetTicketDetail", domain.ErrCodeTicketNotFound, func(r *HTTPRepository) (any, error) { v, err := r.GetTicketDetail("x"); return v, err }},
		{"GetNode", domain.ErrCodeNodeNotFound, func(r *HTTPRepository) (any, error) { v, err := r.GetNode("x"); return v, err }},
		{"GetArtifact", domain.ErrCodeArtifactNotFound, func(r *HTTPRepository) (any, error) { v, err := r.GetArtifact("x"); return v, err }},
		{"GetProject", domain.ErrCodeProjectNotFound, func(r *HTTPRepository) (any, error) { v, err := r.GetProject("x"); return v, err }},
		{"GetLabel", domain.ErrCodeLabelNotFound, func(r *HTTPRepository) (any, error) { v, err := r.GetLabel("x"); return v, err }},
	}
	for _, tc := range cases {
		t.Run(tc.op, func(t *testing.T) {
			r, _ := newRepoAgainst(t, errorHandler(404, fmt.Sprintf(`{"error":{"code":%q,"message":"gone"}}`, tc.code)), "")
			v, err := tc.call(r)
			if err != nil || !reflect.ValueOf(v).IsNil() {
				t.Fatalf("%s = %v, %v; want nil, nil", tc.op, v, err)
			}
		})
	}
}

func TestHTTPRepository_UninterpretableErrorsAreGeneric(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"unknown code", 400, `{"error":{"code":"SOMETHING_ELSE","message":"x"}}`},
		{"non-JSON body", 400, "oops"},
		{"502 empty body", 502, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := newRepoAgainst(t, errorHandler(tc.status, tc.body), "")
			_, err := r.ListTickets()
			if err == nil {
				t.Fatal("expected an error")
			}
			var apiErr *domain.APIError
			if errors.As(err, &apiErr) {
				t.Fatalf("got *domain.APIError %+v, want a generic error", apiErr)
			}
			msg := err.Error()
			if !strings.HasPrefix(msg, "http data source:") || !strings.Contains(msg, "GET /tickets") ||
				!strings.Contains(msg, fmt.Sprintf("status %d", tc.status)) {
				t.Fatalf("error %q lacks the prefix, method, path or status", msg)
			}
		})
	}
}

func TestHTTPRepository_AuthErrorsAreDistinguished(t *testing.T) {
	for _, status := range []int{401, 403} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			r, _ := newRepoAgainst(t, errorHandler(status, `{"error":{"code":"UNAUTHORIZED","message":"no"}}`), "tok")
			_, err := r.ListProjects()
			if !errors.Is(err, errHTTPDataSourceUnauthorized) || !strings.Contains(err.Error(), "rejected the bearer token") {
				t.Fatalf("err = %v, want a token-rejected error", err)
			}
		})
	}
}

func TestHTTPRepository_Timeout(t *testing.T) {
	release := make(chan struct{})
	r, _ := newRepoAgainst(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		select {
		case <-release:
		case <-req.Context().Done():
		}
	}), "")
	defer close(release)
	r.client = newHTTPDataSourceClient(100 * time.Millisecond)
	start := time.Now()
	_, err := r.ListProjects()
	if err == nil || !strings.Contains(err.Error(), "Timeout") && !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("err = %v, want a timeout error", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("call took %v; the timeout did not bound it", time.Since(start))
	}
}

func TestHTTPRepository_ErrorsNeverContainTheToken(t *testing.T) {
	const secret = "super-secret-token"
	responses := []struct {
		status int
		body   string
	}{
		{401, "denied " + secret},
		{500, "internal failure while handling Bearer " + secret},
		{400, `{"error":{"code":"WHATEVER","message":"token ` + secret + `"}}`},
		{400, `{"error":{"code":"VALIDATION_ERROR","message":"bad ` + secret + `"}}`},
		{500, "<html>not json " + secret + "</html>"},
	}
	for i, resp := range responses {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			r, _ := newRepoAgainst(t, errorHandler(resp.status, resp.body), secret)
			_, err := r.ListTickets()
			if err == nil {
				t.Fatal("expected an error")
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("error message contains the token: %q", err.Error())
			}
		})
	}
}

func TestHTTPRepository_ResponseBodyIsCapped(t *testing.T) {
	r, _ := newRepoAgainst(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("[" + strings.Repeat(" ", 4096) + "]"))
	}), "")
	r.maxResponseBytes = 1024
	_, err := r.ListTickets()
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("err = %v, want a size-limit error", err)
	}
	if defaultHTTPDataSourceMaxResponseBytes != 64<<20 {
		t.Fatalf("default limit = %d, want 64 MiB", defaultHTTPDataSourceMaxResponseBytes)
	}
}

// --- security settings ---

func TestValidateHTTPDataSourceSettings(t *testing.T) {
	cases := []struct {
		url, token string
		wantErr    string // "" = success
	}{
		{"http://localhost:8787", "", ""},
		{"http://127.0.0.1:8787", "", ""},
		{"http://127.0.0.2:8787", "", ""},
		{"http://[::1]:8787", "", ""},
		{"http://example.com", "t", "plaintext http:// is only allowed for a loopback"},
		{"http://192.168.1.10:8787", "t", "plaintext http:// is only allowed for a loopback"},
		{"https://example.com", "", "requires a bearer token"},
		{"https://example.com", "t", ""},
		{"https://localhost:8443", "", ""},
	}
	for _, tc := range cases {
		err := ValidateHTTPDataSourceSettings(tc.url, tc.token)
		if tc.wantErr == "" {
			if err != nil {
				t.Errorf("ValidateHTTPDataSourceSettings(%q, %q) = %v, want nil", tc.url, tc.token, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("ValidateHTTPDataSourceSettings(%q, %q) = %v, want %q", tc.url, tc.token, err, tc.wantErr)
		}
	}
}

func TestValidateHTTPDataSourceSettings_MalformedURLs(t *testing.T) {
	for _, u := range []string{"", "ftp://example.com", "https://", "https://user:pass@example.com", "https://example.com/#frag", "example.com", "https://example.com/?q=1"} {
		if err := ValidateHTTPDataSourceSettings(u, "t"); err == nil {
			t.Errorf("ValidateHTTPDataSourceSettings(%q) = nil, want an error", u)
		}
	}
}

func TestOpen_HTTPRejectsInsecureSettingsBeforeAnyRequest(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()
	// A non-loopback plaintext URL is refused by validation alone.
	if _, err := Open(Config{Backend: "http", HTTPURL: "http://example.com", HTTPToken: "t"}); err == nil ||
		!strings.Contains(err.Error(), "plaintext") {
		t.Fatalf("Open(http://example.com) = %v", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("a request was sent despite invalid settings")
	}
}

func TestOpen_HTTPSVerifiesCertificatesWithoutPlaintextFallback(t *testing.T) {
	var handlerHits atomic.Int32
	var serverLog bytes.Buffer
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerHits.Add(1)
		httpdatasourcetest.New("").ServeHTTP(w, r)
	}))
	srv.Config.ErrorLog = log.New(&serverLog, "", 0)
	srv.StartTLS()
	defer srv.Close()

	_, err := Open(Config{Backend: "http", HTTPURL: srv.URL, HTTPToken: "t"})
	if err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("Open against a self-signed server = %v, want a certificate verification error", err)
	}
	if handlerHits.Load() != 0 {
		t.Fatalf("the handler was reached %d times; the TLS handshake should have failed first", handlerHits.Load())
	}
	// A plaintext retry against the TLS port would make the server log this.
	if strings.Contains(serverLog.String(), "HTTP request to an HTTPS server") {
		t.Fatalf("a plaintext HTTP request was sent after the TLS failure: %s", serverLog.String())
	}
}

func TestHTTPDataSourceClient_TLSConfig(t *testing.T) {
	c := newHTTPDataSourceClient(0)
	tr := c.Transport.(*http.Transport)
	if tr.TLSClientConfig == nil || tr.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("TLS verification must be enabled")
	}
	if tr.TLSClientConfig.MinVersion != tls.VersionTLS12 {
		t.Fatalf("MinVersion = %x, want TLS 1.2", tr.TLSClientConfig.MinVersion)
	}
	if c.Timeout != defaultHTTPDataSourceTimeout {
		t.Fatalf("Timeout = %v", c.Timeout)
	}
}

func TestHTTPRepository_DoesNotFollowRedirects(t *testing.T) {
	var targetHits atomic.Int32
	var targetAuth atomic.Value
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
		targetAuth.Store(r.Header.Get("Authorization"))
		_, _ = w.Write([]byte("[]"))
	}))
	defer target.Close()
	r, _ := newRepoAgainst(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, target.URL+"/projects", http.StatusFound)
	}), "secret")
	_, err := r.ListProjects()
	if err == nil || !strings.Contains(err.Error(), "status 302") {
		t.Fatalf("err = %v, want a 302 error", err)
	}
	if targetHits.Load() != 0 {
		t.Fatalf("the redirect target was contacted (Authorization %v)", targetAuth.Load())
	}
}

// --- protocol version handshake ---

func protocolServer(t *testing.T, protocolStatus int, protocolBody string) (*httptest.Server, *httpdatasourcetest.Plugin) {
	t.Helper()
	p := httpdatasourcetest.New("")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/protocol" {
			w.WriteHeader(protocolStatus)
			_, _ = w.Write([]byte(protocolBody))
			return
		}
		p.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, p
}

func TestOpen_HTTPAcceptsSameMajorDifferentMinor(t *testing.T) {
	srv, _ := protocolServer(t, 200, `{"protocol":"graph-ops-datasource","version":"1.3"}`)
	if _, err := Open(Config{Backend: "http", HTTPURL: srv.URL}); err != nil {
		t.Fatalf("Open against a 1.3 server: %v", err)
	}
}

func TestOpen_HTTPRejectsDifferentMajor(t *testing.T) {
	srv, p := protocolServer(t, 200, `{"protocol":"graph-ops-datasource","version":"2.0"}`)
	_, err := Open(Config{Backend: "http", HTTPURL: srv.URL})
	want := "incompatible data source protocol: server speaks 2.0, graph-engine requires 1.x"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
	for _, req := range p.Requests() {
		if req.Path == "/init" {
			t.Fatal("POST /init was sent to an incompatible server")
		}
	}
}

func TestOpen_HTTPRejectsForeignProtocol(t *testing.T) {
	srv, _ := protocolServer(t, 200, `{"protocol":"something-else","version":"1.0"}`)
	_, err := Open(Config{Backend: "http", HTTPURL: srv.URL})
	if err == nil || !strings.Contains(err.Error(), "does not look like a GraphOps data source") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpen_HTTPRejectsMissingProtocolEndpoint(t *testing.T) {
	srv, _ := protocolServer(t, 404, "404 page not found")
	_, err := Open(Config{Backend: "http", HTTPURL: srv.URL})
	if err == nil || !strings.Contains(err.Error(), "does not look like a GraphOps data source") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpen_HTTPWrongTokenFailsAtStartup(t *testing.T) {
	p, srv := startPlugin(t, "right-token")
	_, err := Open(Config{Backend: "http", HTTPURL: srv.URL, HTTPToken: "wrong-token"})
	if err == nil || !strings.Contains(err.Error(), "rejected the bearer token") {
		t.Fatalf("err = %v, want a token-rejected error", err)
	}
	reqs := p.Requests()
	if len(reqs) != 1 || reqs[0].Path != "/protocol" || reqs[0].Header.Get("Authorization") != "Bearer wrong-token" {
		t.Fatalf("expected only GET /protocol with the token, got %+v", reqs)
	}
}

// --- OpenAPI document consistency ---

func loadOpenAPI(t *testing.T) (map[string]any, string) {
	t.Helper()
	path := filepath.Join("..", "..", "..", "..", "docs", "http-datasource", "openapi.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	return doc, string(raw)
}

func TestOpenAPI_VersionMatchesProtocolConstant(t *testing.T) {
	doc, _ := loadOpenAPI(t)
	info := doc["info"].(map[string]any)
	if info["version"] != HTTPDataSourceProtocolVersion {
		t.Fatalf("info.version = %v, want %s", info["version"], HTTPDataSourceProtocolVersion)
	}
	if v, _ := doc["openapi"].(string); !strings.HasPrefix(v, "3.1") {
		t.Fatalf("openapi = %v, want 3.1.x", doc["openapi"])
	}
}

func lowerCamel(s string) string {
	r := []rune(s)
	r[0] = unicode.ToLower(r[0])
	return string(r)
}

func TestOpenAPI_OperationIDsMatchGraphRepository(t *testing.T) {
	doc, _ := loadOpenAPI(t)
	var ops []string
	for _, item := range doc["paths"].(map[string]any) {
		for _, op := range item.(map[string]any) {
			m, ok := op.(map[string]any)
			if !ok {
				continue
			}
			if id, ok := m["operationId"].(string); ok && id != "protocol" {
				ops = append(ops, id)
			}
		}
	}
	sort.Strings(ops)
	var want []string
	for _, n := range graphRepositoryMethodNames() {
		want = append(want, lowerCamel(n))
	}
	sort.Strings(want)
	if !reflect.DeepEqual(ops, want) {
		t.Fatalf("operationIds %v\nwant %v", ops, want)
	}
}

func TestOpenAPI_DocumentsTheContract(t *testing.T) {
	doc, raw := loadOpenAPI(t)
	schemes := doc["components"].(map[string]any)["securitySchemes"].(map[string]any)
	if _, ok := schemes["bearerAuth"]; !ok {
		t.Error("securitySchemes.bearerAuth is missing")
	}
	for code := range knownHTTPDataSourceErrorCodes {
		if !strings.Contains(raw, string(code)) {
			t.Errorf("error code %s is not listed", code)
		}
	}
	for _, phrase := range []string{
		"must be processed atomically",
		"absent key",
		"`null`",
		"recommended",
		"regardless of the HTTP status",
		// Timeout budget and retry policy, which plugins must design for.
		fmt.Sprintf("gives up on each request after %d seconds", int(defaultHTTPDataSourceTimeout/time.Second)),
		fmt.Sprintf("answer every request within %d seconds", int(defaultHTTPDataSourceTimeout/time.Second)),
		"never retries a request automatically",
		"the outcome of a write\n      request is uncertain",
		"must not blindly repeat a\n      create request",
	} {
		if !strings.Contains(raw, phrase) {
			t.Errorf("openapi.yaml does not mention %q", phrase)
		}
	}
}
