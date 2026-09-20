package store

import (
	"database/sql"
	"reflect"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
)

// The concurrency tests in concurrent_write_test.go show that the fix for
// BUG-13 matters; these show what it actually is, without depending on any
// scheduling at all. A patch's non-nil fields become assignments and its nil
// fields become nothing -- which is the whole guarantee, since a column no
// assignment names cannot be overwritten with a value read before somebody
// else's write.

func TestNodePatchAssignments_OnlyNamesTheFieldsThePatchSets(t *testing.T) {
	name := "n"
	nodeType := domain.NodeTypeReview
	status := domain.NodeInReview
	iteration, maxIterations := 2, 5
	assignee := strPtr("alice")
	cleared := (*string)(nil)
	manual := true
	gateID, criteria := "gate-1", "does it hold up"

	for _, tc := range []struct {
		name     string
		patch    NodePatch
		wantSets []string
		wantArgs []any
	}{
		{"empty patch assigns nothing", NodePatch{}, []string{}, []any{}},
		{"status only", NodePatch{Status: &status}, []string{"status=?"}, []any{"IN REVIEW"}},
		{
			"assignee set", NodePatch{Assignee: &assignee},
			[]string{"assignee=?"}, []any{sql.NullString{String: "alice", Valid: true}},
		},
		{
			"assignee cleared", NodePatch{Assignee: &cleared},
			[]string{"assignee=?"}, []any{sql.NullString{}},
		},
		{
			"every field at once",
			NodePatch{
				Name: &name, Type: &nodeType, Status: &status,
				IterationCount: &iteration, MaxIterations: &maxIterations,
				Assignee: &assignee, IsManual: &manual, GateID: &gateID, Criteria: &criteria,
			},
			[]string{"name=?", "type=?", "status=?", "iteration_count=?", "max_iterations=?", "assignee=?", "is_manual=?", "gate_id=?", "criteria=?"},
			[]any{
				"n", "review", "IN REVIEW", 2, 5,
				sql.NullString{String: "alice", Valid: true}, 1,
				sql.NullString{String: "gate-1", Valid: true},
				sql.NullString{String: "does it hold up", Valid: true},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sets, args := nodePatchAssignments(tc.patch)
			if !reflect.DeepEqual(sets, tc.wantSets) {
				t.Errorf("assignments = %v, want %v", sets, tc.wantSets)
			}
			if !reflect.DeepEqual(args, tc.wantArgs) {
				t.Errorf("args = %#v, want %#v", args, tc.wantArgs)
			}
		})
	}
}

func TestProjectPatchAssignments_NilNameAssignsNothing(t *testing.T) {
	sets, args := projectPatchAssignments(ProjectPatch{})
	if len(sets) != 0 || len(args) != 0 {
		t.Fatalf("a name-less patch produced %v / %v, want no assignment at all", sets, args)
	}

	sets, args = projectPatchAssignments(ProjectPatch{Name: strPtr("new name")})
	if !reflect.DeepEqual(sets, []string{"name=?"}) || !reflect.DeepEqual(args, []any{"new name"}) {
		t.Fatalf("assignments = %v / %v, want name=? / new name", sets, args)
	}
}
