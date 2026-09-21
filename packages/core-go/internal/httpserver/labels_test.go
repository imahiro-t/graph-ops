package httpserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/store"
)

// DFLT-00084: label master endpoints and PATCH /api/tickets/{id}'s label_ids.

func doRawJSON(t *testing.T, s *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	req.Host = testHost
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(csrfHeaderName, "1")
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	return rec
}

func expectStatus(t *testing.T, rec *httptest.ResponseRecorder, status int) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("expected %d, got %d: %s", status, rec.Code, rec.Body.String())
	}
}

func expectErrorCode(t *testing.T, rec *httptest.ResponseRecorder, status int, code domain.ErrorCode) {
	t.Helper()
	expectStatus(t, rec, status)
	if got := decodeError(t, rec).Code; got != code {
		t.Fatalf("expected error code %s, got %s: %s", code, got, rec.Body.String())
	}
}

func apiCreateLabel(t *testing.T, s *Server, projectID, name, color string) domain.Label {
	t.Helper()
	rec := doJSON(t, s, http.MethodPost, "/api/projects/"+projectID+"/labels", map[string]any{"name": name, "color": color})
	expectStatus(t, rec, http.StatusCreated)
	var l domain.Label
	if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
		t.Fatalf("decoding label: %v", err)
	}
	return l
}

func apiListLabels(t *testing.T, s *Server, projectID string) []domain.LabelUsage {
	t.Helper()
	rec := doJSON(t, s, http.MethodGet, "/api/projects/"+projectID+"/labels", nil)
	expectStatus(t, rec, http.StatusOK)
	var out []domain.LabelUsage
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding labels: %v", err)
	}
	return out
}

type apiTicket struct {
	ID       string         `json:"id"`
	Priority string         `json:"priority"`
	Labels   []domain.Label `json:"labels"`
}

func apiGetTicket(t *testing.T, s *Server, id string) apiTicket {
	t.Helper()
	rec := doJSON(t, s, http.MethodGet, "/api/tickets/"+id, nil)
	expectStatus(t, rec, http.StatusOK)
	var tk apiTicket
	if err := json.Unmarshal(rec.Body.Bytes(), &tk); err != nil {
		t.Fatalf("decoding ticket: %v", err)
	}
	return tk
}

func apiLabelNames(labels []domain.Label) string {
	names := make([]string, 0, len(labels))
	for _, l := range labels {
		names = append(names, l.Name)
	}
	return strings.Join(names, ",")
}

func TestLabelsAPI_CreateListRenameRecolorDelete(t *testing.T) {
	s, _, projectID := newTestServer(t)

	bug := apiCreateLabel(t, s, projectID, "バグ", "red")
	if !strings.HasPrefix(bug.ID, "label-") || bug.ProjectID != projectID || bug.Name != "バグ" || bug.Color != "red" {
		t.Errorf("unexpected label: %+v", bug)
	}
	if l := apiCreateLabel(t, s, projectID, "  機能追加  ", "blue"); l.Name != "機能追加" {
		t.Errorf("name must be trimmed: %+v", l)
	}
	list := apiListLabels(t, s, projectID)
	if len(list) != 2 || list[0].TicketCount != 0 {
		t.Errorf("unexpected list: %+v", list)
	}

	// Every palette color.
	for _, c := range domain.LabelColors {
		if l := apiCreateLabel(t, s, projectID, "色-"+string(c), string(c)); l.Color != c {
			t.Errorf("color %s: got %+v", c, l)
		}
	}

	// 50 characters OK, 51 not.
	for _, ch := range []string{"a", "字"} {
		if l := apiCreateLabel(t, s, projectID, strings.Repeat(ch, 50), "gray"); l.Name != strings.Repeat(ch, 50) {
			t.Errorf("50 x %q: %+v", ch, l)
		}
		rec := doJSON(t, s, http.MethodPost, "/api/projects/"+projectID+"/labels", map[string]any{"name": strings.Repeat(ch, 51), "color": "gray"})
		expectErrorCode(t, rec, http.StatusBadRequest, domain.ErrCodeInvalidLabelName)
	}
	padded := "  " + strings.Repeat("b", 50) + "  "
	if l := apiCreateLabel(t, s, projectID, padded, "gray"); l.Name != strings.Repeat("b", 50) {
		t.Errorf("padded 50: %+v", l)
	}

	count := len(apiListLabels(t, s, projectID))
	for _, bad := range []struct {
		name, color string
		code        domain.ErrorCode
	}{
		{"", "red", domain.ErrCodeInvalidLabelName},
		{"   ", "red", domain.ErrCodeInvalidLabelName},
		{"　", "red", domain.ErrCodeInvalidLabelName},
		{"新規", "magenta", domain.ErrCodeInvalidLabelColor},
		{"新規", "", domain.ErrCodeInvalidLabelColor},
	} {
		rec := doJSON(t, s, http.MethodPost, "/api/projects/"+projectID+"/labels", map[string]any{"name": bad.name, "color": bad.color})
		expectErrorCode(t, rec, http.StatusBadRequest, bad.code)
	}
	if got := len(apiListLabels(t, s, projectID)); got != count {
		t.Errorf("rejected creates must not add labels: %d -> %d", count, got)
	}

	// Duplicates.
	apiCreateLabel(t, s, projectID, "Bug", "red")
	for _, dup := range []string{"Bug", "bug", "BUG", " Bug ", "  bug  "} {
		rec := doJSON(t, s, http.MethodPost, "/api/projects/"+projectID+"/labels", map[string]any{"name": dup, "color": "blue"})
		expectErrorCode(t, rec, http.StatusBadRequest, domain.ErrCodeLabelNameTaken)
	}
	rec := doJSON(t, s, http.MethodPost, "/api/projects/"+projectID+"/labels", map[string]any{"name": "バグ", "color": "blue"})
	expectErrorCode(t, rec, http.StatusBadRequest, domain.ErrCodeLabelNameTaken)

	// Unknown project.
	rec = doJSON(t, s, http.MethodPost, "/api/projects/proj-missing/labels", map[string]any{"name": "バグ", "color": "red"})
	expectErrorCode(t, rec, http.StatusNotFound, domain.ErrCodeProjectNotFound)
	rec = doJSON(t, s, http.MethodGet, "/api/projects/proj-missing/labels", nil)
	expectErrorCode(t, rec, http.StatusNotFound, domain.ErrCodeProjectNotFound)

	// Rename / recolor.
	rec = doJSON(t, s, http.MethodPatch, "/api/labels/"+bug.ID, map[string]any{"name": "不具合"})
	expectStatus(t, rec, http.StatusOK)
	var renamed domain.Label
	_ = json.Unmarshal(rec.Body.Bytes(), &renamed)
	if renamed.ID != bug.ID || renamed.Name != "不具合" || renamed.Color != "red" {
		t.Errorf("rename: %+v", renamed)
	}
	rec = doJSON(t, s, http.MethodPatch, "/api/labels/"+bug.ID, map[string]any{"color": "orange"})
	expectStatus(t, rec, http.StatusOK)
	var recolored domain.Label
	_ = json.Unmarshal(rec.Body.Bytes(), &recolored)
	if recolored.Name != "不具合" || recolored.Color != "orange" {
		t.Errorf("recolor: %+v", recolored)
	}
	rec = doJSON(t, s, http.MethodPatch, "/api/labels/"+bug.ID, map[string]any{"name": "  修正  "})
	expectStatus(t, rec, http.StatusOK)
	found := false
	for _, l := range apiListLabels(t, s, projectID) {
		if l.ID == bug.ID {
			found = l.Name == "修正"
		}
	}
	if !found {
		t.Error("the trimmed rename must be listed")
	}
	rec = doJSON(t, s, http.MethodPatch, "/api/labels/"+bug.ID, map[string]any{"name": strings.Repeat("漢", 50)})
	expectStatus(t, rec, http.StatusOK)
	rec = doJSON(t, s, http.MethodPatch, "/api/labels/"+bug.ID, map[string]any{"name": "修正"})
	expectStatus(t, rec, http.StatusOK)

	for _, body := range []struct {
		raw  string
		code domain.ErrorCode
	}{
		{`{"name": ""}`, domain.ErrCodeInvalidLabelName},
		{`{"name": "   "}`, domain.ErrCodeInvalidLabelName},
		{`{"name": "` + strings.Repeat("a", 51) + `"}`, domain.ErrCodeInvalidLabelName},
		{`{"color": "rainbow"}`, domain.ErrCodeInvalidLabelColor},
		{`{"name": "不具合", "color": "rainbow"}`, domain.ErrCodeInvalidLabelColor},
		{`{"name": "bug"}`, domain.ErrCodeLabelNameTaken},
		{`{"name": " BUG "}`, domain.ErrCodeLabelNameTaken},
	} {
		rec := doRawJSON(t, s, http.MethodPatch, "/api/labels/"+bug.ID, body.raw)
		expectErrorCode(t, rec, http.StatusBadRequest, body.code)
	}
	for _, l := range apiListLabels(t, s, projectID) {
		if l.ID == bug.ID && (l.Name != "修正" || l.Color != "orange") {
			t.Errorf("rejected updates must leave the label unchanged: %+v", l)
		}
	}

	// Own case change.
	lower := apiCreateLabel(t, s, projectID, "docs", "gray")
	rec = doJSON(t, s, http.MethodPatch, "/api/labels/"+lower.ID, map[string]any{"name": "Docs"})
	expectStatus(t, rec, http.StatusOK)

	// Missing label.
	rec = doJSON(t, s, http.MethodPatch, "/api/labels/label-missing", map[string]any{"name": "不具合"})
	expectErrorCode(t, rec, http.StatusNotFound, domain.ErrCodeLabelNotFound)
	rec = doJSON(t, s, http.MethodDelete, "/api/labels/label-missing", nil)
	expectErrorCode(t, rec, http.StatusNotFound, domain.ErrCodeLabelNotFound)

	// Unused delete.
	rec = doJSON(t, s, http.MethodDelete, "/api/labels/"+lower.ID, nil)
	expectStatus(t, rec, http.StatusOK)
	var del map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &del)
	if del["success"] != true || del["removed_ticket_count"] != float64(0) {
		t.Errorf("unexpected delete response: %v", del)
	}
	for _, l := range apiListLabels(t, s, projectID) {
		if l.ID == lower.ID {
			t.Error("deleted label still listed")
		}
	}
}

func TestLabelsAPI_SameNameInAnotherProject(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	beta, err := repo.CreateProject("Beta", "BETA")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	apiCreateLabel(t, s, projectID, "Bug", "red")
	apiCreateLabel(t, s, beta.ID, "Bug", "red")
	if len(apiListLabels(t, s, projectID)) != 1 || len(apiListLabels(t, s, beta.ID)) != 1 {
		t.Error("each project should have its own Bug")
	}
}

func TestTicketsAPI_LabelIDs(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	beta, err := repo.CreateProject("Beta", "BETA")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	bug := apiCreateLabel(t, s, projectID, "バグ", "red")
	feat := apiCreateLabel(t, s, projectID, "機能追加", "blue")
	ui := apiCreateLabel(t, s, projectID, "UI", "purple")
	betaBug := apiCreateLabel(t, s, beta.ID, "Bug", "red")

	created, err := repo.CreateTicket(projectID, domain.Ticket{Title: "A-1", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	id := created.ID

	// No labels -> [] in list and detail.
	listRec := doJSON(t, s, http.MethodGet, listTicketsPath(projectID), nil)
	if !strings.Contains(listRec.Body.String(), `"labels":[]`) {
		t.Errorf("list must carry labels: []: %s", listRec.Body.String())
	}
	detailRec := doJSON(t, s, http.MethodGet, "/api/tickets/"+id, nil)
	if !strings.Contains(detailRec.Body.String(), `"labels":[]`) {
		t.Errorf("detail must carry labels: []: %s", detailRec.Body.String())
	}

	patch := func(body string) *httptest.ResponseRecorder {
		return doRawJSON(t, s, http.MethodPatch, "/api/tickets/"+id, body)
	}

	rec := patch(`{"label_ids": ["` + bug.ID + `", "` + ui.ID + `"]}`)
	expectStatus(t, rec, http.StatusOK)
	tk := apiGetTicket(t, s, id)
	if apiLabelNames(tk.Labels) != "UI,バグ" {
		t.Errorf("attach: %q", apiLabelNames(tk.Labels))
	}
	for _, l := range tk.Labels {
		if l.ID == "" || l.Name == "" || l.Color == "" {
			t.Errorf("label lacks fields: %+v", l)
		}
	}

	expectStatus(t, patch(`{"label_ids": ["`+feat.ID+`"]}`), http.StatusOK)
	if got := apiLabelNames(apiGetTicket(t, s, id).Labels); got != "機能追加" {
		t.Errorf("replace: %q", got)
	}
	expectStatus(t, patch(`{"label_ids": []}`), http.StatusOK)
	if got := apiGetTicket(t, s, id).Labels; got == nil || len(got) != 0 {
		t.Errorf("clear: %+v", got)
	}
	expectStatus(t, patch(`{"label_ids": ["`+bug.ID+`", "`+bug.ID+`"]}`), http.StatusOK)
	if got := apiLabelNames(apiGetTicket(t, s, id).Labels); got != "バグ" {
		t.Errorf("duplicates: %q", got)
	}
	expectStatus(t, patch(`{"priority": "HIGH"}`), http.StatusOK)
	tk = apiGetTicket(t, s, id)
	if tk.Priority != "HIGH" || apiLabelNames(tk.Labels) != "バグ" {
		t.Errorf("patch without label_ids: %+v", tk)
	}

	for _, bad := range []struct {
		labelIDs string
		code     domain.ErrorCode
	}{
		{`["` + betaBug.ID + `"]`, domain.ErrCodeLabelNotFound},
		{`["label-missing"]`, domain.ErrCodeLabelNotFound},
		{`["` + ui.ID + `", "label-missing"]`, domain.ErrCodeLabelNotFound},
		{`null`, domain.ErrCodeValidation},
	} {
		rec := patch(`{"priority": "LOW", "label_ids": ` + bad.labelIDs + `}`)
		expectErrorCode(t, rec, http.StatusBadRequest, bad.code)
		tk := apiGetTicket(t, s, id)
		if tk.Priority != "HIGH" || apiLabelNames(tk.Labels) != "バグ" {
			t.Errorf("label_ids %s: the ticket must be unchanged: %+v", bad.labelIDs, tk)
		}
	}

	// Missing ticket: 404 TICKET_NOT_FOUND, nothing linked.
	rec = doRawJSON(t, s, http.MethodPatch, "/api/tickets/TEST-99999", `{"label_ids": ["`+feat.ID+`"]}`)
	expectErrorCode(t, rec, http.StatusNotFound, domain.ErrCodeTicketNotFound)
	for _, l := range apiListLabels(t, s, projectID) {
		if l.ID == feat.ID && l.TicketCount != 0 {
			t.Errorf("a failed PATCH must not link the label: %+v", l)
		}
	}
	// ...also without label_ids.
	rec = doRawJSON(t, s, http.MethodPatch, "/api/tickets/TEST-99999", `{"priority": "HIGH"}`)
	expectErrorCode(t, rec, http.StatusNotFound, domain.ErrCodeTicketNotFound)

	// Rename/recolor propagate; usage count; in-use delete.
	second, err := repo.CreateTicket(projectID, domain.Ticket{Title: "A-2", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	third, err := repo.CreateTicket(projectID, domain.Ticket{Title: "A-3", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	for _, tid := range []string{second.ID, third.ID} {
		ids := []string{bug.ID}
		if tid == third.ID {
			ids = append(ids, feat.ID)
		}
		if _, err := repo.UpdateTicket(tid, store.TicketPatch{LabelIDs: &ids}); err != nil {
			t.Fatalf("UpdateTicket: %v", err)
		}
	}
	for _, l := range apiListLabels(t, s, projectID) {
		want := map[string]int{bug.ID: 3, feat.ID: 1, ui.ID: 0}[l.ID]
		if l.TicketCount != want {
			t.Errorf("%s ticket_count = %d, want %d", l.Name, l.TicketCount, want)
		}
	}
	expectStatus(t, doJSON(t, s, http.MethodPatch, "/api/labels/"+bug.ID, map[string]any{"name": "不具合", "color": "pink"}), http.StatusOK)
	for _, tid := range []string{id, second.ID} {
		tk := apiGetTicket(t, s, tid)
		if len(tk.Labels) != 1 || tk.Labels[0].ID != bug.ID || tk.Labels[0].Name != "不具合" || tk.Labels[0].Color != "pink" {
			t.Errorf("%s: rename not reflected: %+v", tid, tk.Labels)
		}
	}
	listRec = doJSON(t, s, http.MethodGet, listTicketsPath(projectID), nil)
	var listed []apiTicket
	_ = json.Unmarshal(listRec.Body.Bytes(), &listed)
	for _, tk := range listed {
		for _, l := range tk.Labels {
			if l.ID == bug.ID && l.Name != "不具合" {
				t.Errorf("list shows a stale label: %+v", l)
			}
		}
	}

	rec = doJSON(t, s, http.MethodDelete, "/api/labels/"+bug.ID, nil)
	expectStatus(t, rec, http.StatusOK)
	var del map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &del)
	if del["removed_ticket_count"] != float64(3) {
		t.Errorf("removed_ticket_count = %v, want 3", del["removed_ticket_count"])
	}
	if got := apiLabelNames(apiGetTicket(t, s, third.ID).Labels); got != "機能追加" {
		t.Errorf("A-3 after delete: %q", got)
	}
	for _, tid := range []string{id, second.ID} {
		if got := apiGetTicket(t, s, tid).Labels; len(got) != 0 {
			t.Errorf("%s after delete: %+v", tid, got)
		}
	}
}
