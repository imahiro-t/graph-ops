package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/store"
)

// Label master API (DFLT-00084). Labels are project-scoped rows in the
// (possibly team-shared) DB, managed from the Web UI's settings; there is
// deliberately no CLI equivalent. Validation and the duplicate-name rule
// live in the store (see store.GraphRepository's label methods), and their
// APIErrors map to 400/404 through statusForError.

// handleListLabels: GET /api/projects/{id}/labels -> []domain.LabelUsage.
func (s *Server) handleListLabels(w http.ResponseWriter, r *http.Request) {
	labels, err := s.repo.ListLabelsByProject(r.PathValue("id"))
	if err != nil {
		writeError(w, statusForError(err, http.StatusInternalServerError), err)
		return
	}
	writeJSON(w, http.StatusOK, labels)
}

// handleCreateLabel: POST /api/projects/{id}/labels, body {name, color}.
func (s *Server) handleCreateLabel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	label, err := s.repo.CreateLabel(r.PathValue("id"), body.Name, body.Color)
	if err != nil {
		writeError(w, statusForError(err, http.StatusInternalServerError), err)
		return
	}
	writeJSON(w, http.StatusCreated, label)
}

// handleUpdateLabel: PATCH /api/labels/{id}, body {name?, color?}. An absent
// (or null) field is left unchanged.
func (s *Server) handleUpdateLabel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name  *string `json:"name"`
		Color *string `json:"color"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	label, err := s.repo.UpdateLabel(r.PathValue("id"), store.LabelPatch{Name: body.Name, Color: body.Color})
	if err != nil {
		writeError(w, statusForError(err, http.StatusInternalServerError), err)
		return
	}
	writeJSON(w, http.StatusOK, label)
}

type deleteLabelResponse struct {
	Success            bool `json:"success"`
	RemovedTicketCount int  `json:"removed_ticket_count"`
}

// handleDeleteLabel: DELETE /api/labels/{id}. The label is detached from
// every ticket first; the response says from how many. The Web UI asks for
// confirmation (showing the usage count) before calling this.
func (s *Server) handleDeleteLabel(w http.ResponseWriter, r *http.Request) {
	removed, err := s.repo.DeleteLabel(r.PathValue("id"))
	if err != nil {
		writeError(w, statusForError(err, http.StatusInternalServerError), err)
		return
	}
	writeJSON(w, http.StatusOK, deleteLabelResponse{Success: true, RemovedTicketCount: removed})
}

// nullableStringSlice decodes PATCH /api/tickets/{id}'s "label_ids" so the
// handler can tell the key absent (leave labels unchanged) from an explicit
// null (rejected: use [] to remove every label) from an array (replace).
type nullableStringSlice struct {
	Present bool
	Null    bool
	Value   []string
}

func (n *nullableStringSlice) UnmarshalJSON(data []byte) error {
	n.Present = true
	if string(data) == "null" {
		n.Null = true
		return nil
	}
	var v []string
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	if v == nil {
		v = []string{}
	}
	n.Value = v
	return nil
}

// statusForTicketUpdateError is statusForError for PATCH /api/tickets/{id}:
// a LABEL_NOT_FOUND there means the request's own label_ids named a label
// that doesn't exist or belongs to another project -- a malformed request,
// so 400 rather than the 404 the label endpoints use.
func statusForTicketUpdateError(err error) int {
	var apiErr *domain.APIError
	if errors.As(err, &apiErr) && apiErr.Code == domain.ErrCodeLabelNotFound {
		return http.StatusBadRequest
	}
	return statusForError(err, http.StatusInternalServerError)
}
