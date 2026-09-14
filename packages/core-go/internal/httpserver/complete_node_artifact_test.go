package httpserver

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/store"
)

// TestHandleCompleteNode_ReviewLoopBackReturnsAwaitingFix (DFLT-00042): the
// HTTP complete endpoint passes engine.CompleteNode's result through as-is,
// so a failing review with an iteration_loop edge answers
// {"nextStatus":"AWAITING FIX","loopedBack":true} and the node is persisted
// as AWAITING FIX.
func TestHandleCompleteNode_ReviewLoopBackReturnsAwaitingFix(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	ticket, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	plan, err := repo.CreateNode(domain.GraphNode{
		ID: "node-" + engine.NewArtifactID(), TicketID: ticket.ID, Name: "plan",
		Type: domain.NodeTypePlan, Status: domain.NodeDone, MaxIterations: 3,
	})
	if err != nil {
		t.Fatalf("CreateNode(plan): %v", err)
	}
	review, err := repo.CreateNode(domain.GraphNode{
		ID: "node-" + engine.NewArtifactID(), TicketID: ticket.ID, Name: "plan_review",
		Type: domain.NodeTypeReview, Status: domain.NodeInReview, MaxIterations: 3,
	})
	if err != nil {
		t.Fatalf("CreateNode(plan_review): %v", err)
	}
	if _, err := repo.CreateEdge(domain.GraphEdge{
		ID: "edge-" + engine.NewArtifactID(), TicketID: ticket.ID,
		FromNodeID: review.ID, ToNodeID: plan.ID, Condition: domain.EdgeLoop,
	}); err != nil {
		t.Fatalf("CreateEdge: %v", err)
	}

	rec := postCompleteNode(t, s, review.ID, map[string]any{"passed": false})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v (%s)", err, rec.Body.String())
	}
	if body["nextStatus"] != "AWAITING FIX" || body["loopedBack"] != true {
		t.Errorf("expected nextStatus AWAITING FIX / loopedBack true, got %s", rec.Body.String())
	}
	if got, _ := repo.GetNode(review.ID); got.Status != domain.NodeAwaitingFix {
		t.Errorf("plan_review status = %s, want AWAITING FIX", got.Status)
	}
	if got, _ := repo.GetNode(plan.ID); got.Status != domain.NodeTODO || got.IterationCount != 1 {
		t.Errorf("plan = %s / %d, want TODO / 1", got.Status, got.IterationCount)
	}
}

// postCompleteNode mirrors postArtifact (tickets_test.go) for
// POST /api/nodes/{id}/complete.
func postCompleteNode(t *testing.T, s *Server, nodeID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/"+nodeID+"/complete", bytes.NewReader(raw))
	req.Host = testHost
	req.Header.Set("Content-Type", "application/json")
	// Every non-GET request must carry this (see server.go's csrfHeaderName)
	// or withCORS rejects it with 403 before the handler ever runs.
	req.Header.Set(csrfHeaderName, "1")
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	return rec
}

func newImplNode(t *testing.T, repo store.GraphRepository, projectID string) domain.GraphNode {
	t.Helper()
	ticket, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	node, err := repo.CreateNode(domain.GraphNode{
		ID: "node-" + engine.NewArtifactID(), TicketID: ticket.ID, Name: "Impl",
		Type: domain.NodeTypeImplementation, Status: domain.NodeTODO,
	})
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	return node
}

// TestHandleCompleteNode_InlineImageArtifact_MagicByteCheckEnforced is the
// regression test for Security review art-cdbe6a11 (6th security review,
// DFLT-00006), finding #2: POST /api/nodes/{id}/complete's inline
// "artifacts" array used to bypass every validation handleCreateArtifact
// enforces, because it went straight to engine.CompleteNode ->
// repo.CreateArtifact with no call through prepareArtifactForCreate at all.
// The reported exploit: an "image" artifact whose content is actually a
// <script> tag, with metadata.mime_type forged to "text/html", submitted via
// complete-node instead of the dedicated artifacts endpoint. Before the fix
// this succeeded (200 OK, node completed, artifact stored) and a subsequent
// GET .../content served the payload as text/html verbatim -- a full stored
// XSS. It must now be rejected the same way handleCreateArtifact already
// rejects it: EncodeInline's magic-byte check fails because the payload
// isn't a real image, regardless of the claimed mime_type, and nothing is
// persisted -- neither the artifact nor the node completion.
func TestHandleCompleteNode_InlineImageArtifact_MagicByteCheckEnforced(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	node := newImplNode(t, repo, projectID)

	payload := base64.StdEncoding.EncodeToString([]byte("<script>alert(document.domain)</script>"))
	rec := postCompleteNode(t, s, node.ID, map[string]any{
		"passed": true,
		"artifacts": []map[string]any{
			{
				"name":     "complete-node-image-xss",
				"type":     "image",
				"content":  payload,
				"metadata": `{"encoding":"base64","mime_type":"text/html; charset=utf-8"}`,
			},
		},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 rejecting the non-image payload, got %d: %s", rec.Code, rec.Body.String())
	}

	// Nothing must have been persisted: the node must still be TODO and no
	// artifact must exist for it. A handler that validates but still
	// completes the node (or still stores the artifact) would reopen the
	// same hole in a slightly different shape.
	gotNode, err := repo.GetNode(node.ID)
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if gotNode.Status == domain.NodeDone {
		t.Errorf("node was completed despite the rejected artifact; status = %s", gotNode.Status)
	}
	artifacts, err := repo.ListArtifactsByNode(node.ID)
	if err != nil {
		t.Fatalf("ListArtifactsByNode: %v", err)
	}
	if len(artifacts) != 0 {
		t.Errorf("expected no artifacts to be stored, got %d", len(artifacts))
	}
}

// TestHandleCompleteNode_InlineTextArtifact_FilePathRejected covers the
// text/gherkin/json half of the same bypass: complete-node's artifacts array
// must reject file_path for non-html/image types exactly like
// handleCreateArtifact does (Security review art-65fe221a's fix), not just
// the html/image magic-byte case.
func TestHandleCompleteNode_InlineTextArtifact_FilePathRejected(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	node := newImplNode(t, repo, projectID)

	rec := postCompleteNode(t, s, node.ID, map[string]any{
		"passed": true,
		"artifacts": []map[string]any{
			{"name": "note", "type": "text", "file_path": "whatever.txt"},
		},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 rejecting file_path for type text, got %d: %s", rec.Code, rec.Body.String())
	}

	gotNode, err := repo.GetNode(node.ID)
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if gotNode.Status == domain.NodeDone {
		t.Errorf("node was completed despite the rejected artifact; status = %s", gotNode.Status)
	}
}

// TestHandleCompleteNode_ValidArtifact_StillWorks is the non-regression
// counterpart: a well-formed inline artifact (no file_path, no forged
// metadata) must still be accepted and the node must still complete, so the
// new validation choke point doesn't overreach into rejecting legitimate
// complete-node calls.
func TestHandleCompleteNode_ValidArtifact_StillWorks(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	node := newImplNode(t, repo, projectID)

	rec := postCompleteNode(t, s, node.ID, map[string]any{
		"passed": true,
		"artifacts": []map[string]any{
			{"name": "note", "type": "text", "content": "all good"},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	gotNode, err := repo.GetNode(node.ID)
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if gotNode.Status != domain.NodeDone {
		t.Errorf("node status = %s, want DONE", gotNode.Status)
	}
	artifacts, err := repo.ListArtifactsByNode(node.ID)
	if err != nil {
		t.Fatalf("ListArtifactsByNode: %v", err)
	}
	if len(artifacts) != 1 || artifacts[0].Content == nil || *artifacts[0].Content != "all good" {
		t.Errorf("unexpected stored artifacts: %+v", artifacts)
	}
}

// TestHandleCompleteNode_NoArtifacts_StillWorks confirms the common call
// shape (no inline artifacts at all) is unaffected by the node lookup added
// alongside the fix -- it must not require a node to have any artifacts, and
// must not perform an unnecessary lookup that could itself fail oddly.
func TestHandleCompleteNode_NoArtifacts_StillWorks(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	node := newImplNode(t, repo, projectID)

	rec := postCompleteNode(t, s, node.ID, map[string]any{"passed": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	gotNode, err := repo.GetNode(node.ID)
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if gotNode.Status != domain.NodeDone {
		t.Errorf("node status = %s, want DONE", gotNode.Status)
	}
}
