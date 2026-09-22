package engine

import (
	"errors"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
)

// DFLT-00138: GraphEngine.CreateLabel, behind `graph-engine create-label`.

func requireAPIErrorCode(t *testing.T, err error, code domain.ErrorCode) {
	t.Helper()
	var apiErr *domain.APIError
	if err == nil || !errors.As(err, &apiErr) || apiErr.Code != code {
		t.Fatalf("expected %s, got %v", code, err)
	}
}

func labelCount(t *testing.T, eng *GraphEngine, projectID string) int {
	t.Helper()
	labels, err := eng.repo.ListLabelsByProject(projectID)
	if err != nil {
		t.Fatalf("ListLabelsByProject: %v", err)
	}
	return len(labels)
}

func TestCreateLabel_ExplicitColorIsUsed(t *testing.T) {
	eng, _, projectID := newTestEngine(t)
	l, err := eng.CreateLabel(projectID, "  bug  ", "red")
	if err != nil {
		t.Fatalf("CreateLabel: %v", err)
	}
	if l.Name != "bug" || l.Color != domain.LabelColorRed || l.ProjectID != projectID || l.ID == "" {
		t.Errorf("unexpected label %+v", l)
	}
}

func TestCreateLabel_OmittedColorPicksUnusedThenLeastUsed(t *testing.T) {
	eng, _, projectID := newTestEngine(t)

	// Palette order while colors remain unused.
	for i, want := range domain.LabelColors {
		l, err := eng.CreateLabel(projectID, "L"+string(rune('a'+i)), "")
		if err != nil {
			t.Fatalf("CreateLabel #%d: %v", i, err)
		}
		if l.Color != want {
			t.Fatalf("label #%d got color %q, want %q", i, l.Color, want)
		}
	}
	// All ten used once: a duplicate is allowed and ties go to the palette's first.
	l, err := eng.CreateLabel(projectID, "extra1", "")
	if err != nil {
		t.Fatalf("CreateLabel with the palette exhausted: %v", err)
	}
	if l.Color != domain.LabelColorGray {
		t.Errorf("got %q, want gray", l.Color)
	}
	// gray now used twice: red is the least used, earliest.
	if l, err = eng.CreateLabel(projectID, "extra2", ""); err != nil || l.Color != domain.LabelColorRed {
		t.Errorf("got %q, %v; want red", l.Color, err)
	}
	if n := labelCount(t, eng, projectID); n != 12 {
		t.Errorf("label count = %d, want 12", n)
	}
}

func TestCreateLabel_OmittedColorIgnoresOtherProjects(t *testing.T) {
	eng, repo, projectID := newTestEngine(t)
	other, err := repo.CreateProject("Other", "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := eng.CreateLabel(other.ID, "x", "gray"); err != nil {
		t.Fatalf("CreateLabel(other): %v", err)
	}
	l, err := eng.CreateLabel(projectID, "first", "")
	if err != nil || l.Color != domain.LabelColorGray {
		t.Errorf("got %q, %v; want gray", l.Color, err)
	}
}

func TestCreateLabel_StoreValidationWritesNothing(t *testing.T) {
	eng, _, projectID := newTestEngine(t)
	if _, err := eng.CreateLabel(projectID, "Bug", "red"); err != nil {
		t.Fatalf("CreateLabel: %v", err)
	}
	cases := []struct {
		name, color string
		code        domain.ErrorCode
	}{
		{"", "red", domain.ErrCodeInvalidLabelName},
		{"   ", "red", domain.ErrCodeInvalidLabelName},
		{"   ", "", domain.ErrCodeInvalidLabelName},
		{strings.Repeat("a", 51), "red", domain.ErrCodeInvalidLabelName},
		{"ok", "crimson", domain.ErrCodeInvalidLabelColor},
		{"ok", "Red", domain.ErrCodeInvalidLabelColor},
		{"bug", "blue", domain.ErrCodeLabelNameTaken},
		{"bug", "", domain.ErrCodeLabelNameTaken},
	}
	for _, tc := range cases {
		_, err := eng.CreateLabel(projectID, tc.name, tc.color)
		requireAPIErrorCode(t, err, tc.code)
	}
	if n := labelCount(t, eng, projectID); n != 1 {
		t.Errorf("label count = %d, want 1 (nothing created)", n)
	}
	for _, color := range []string{"red", ""} {
		_, err := eng.CreateLabel("no-such-project", "ok", color)
		requireAPIErrorCode(t, err, domain.ErrCodeProjectNotFound)
	}
}

func TestResolveLabelNames_NoLabelsPointsAtCreateLabel(t *testing.T) {
	eng, _, projectID := newTestEngine(t)
	_, err := eng.CreateTicketWithOptions(projectID, "T", "", CreateTicketOptions{LabelNames: []string{"unknown"}})
	apiErr := requireLabelNotFound(t, err)
	if !strings.Contains(apiErr.Message, `"graph-engine create-label"`) {
		t.Errorf("message should point at graph-engine create-label: %q", apiErr.Message)
	}
}
