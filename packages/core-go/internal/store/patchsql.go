package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/graph-ops/core-go/internal/domain"
)

// This file holds the SQL write helpers SQLiteRepository and MySQLRepository
// share. Both dialects speak the same `?`-placeholder subset here, so the
// statements below are built once rather than kept in step by hand in two
// files (DFLT-00102).
//
// Why these writes name columns instead of rewriting the whole row: UpdateNode
// and UpdateProject used to SELECT the row, apply the patch to the in-memory
// copy and then UPDATE every column back. Two updates touching *different*
// columns -- the Web UI reassigning a node while complete-node moves its
// status, say -- would each write back the value they had read for the other's
// column, so whichever landed second silently reverted the first (BUG-13).
// Writing only the columns the patch actually names removes the overwrite
// entirely: the two statements touch disjoint columns and neither can undo the
// other.

// nodePatchAssignments turns patch into `col=?` assignments and their
// arguments, one pair per field the patch actually sets. Fields left nil are
// absent from the result, which is the whole point: a column no assignment
// names keeps whatever value it has in the database, including one another
// writer set moments ago.
//
// Assignee is the one field with three states rather than two (see
// NodePatch's doc comment): a nil *Assignee produces no assignment at all,
// while a non-nil Assignee pointing at a nil *string produces `assignee=NULL`.
// Collapsing those two into one would take the Web UI's "clear the assignee"
// away -- and this is precisely the code path whose overwrite bug was
// discovered through a lost assignee change, so losing it here would trade one
// defect for another in the same place.
func nodePatchAssignments(patch NodePatch) ([]string, []any) {
	sets := make([]string, 0, 9)
	args := make([]any, 0, 9)
	add := func(clause string, arg any) {
		sets = append(sets, clause)
		args = append(args, arg)
	}
	if patch.Name != nil {
		add("name=?", *patch.Name)
	}
	if patch.Type != nil {
		add("type=?", string(*patch.Type))
	}
	if patch.Status != nil {
		add("status=?", string(*patch.Status))
	}
	if patch.IterationCount != nil {
		add("iteration_count=?", *patch.IterationCount)
	}
	if patch.MaxIterations != nil {
		add("max_iterations=?", *patch.MaxIterations)
	}
	if patch.Assignee != nil {
		add("assignee=?", nullableString(*patch.Assignee))
	}
	if patch.IsManual != nil {
		add("is_manual=?", boolToInt(*patch.IsManual))
	}
	if patch.GateID != nil {
		add("gate_id=?", nullableString(patch.GateID))
	}
	if patch.Criteria != nil {
		add("criteria=?", nullableString(patch.Criteria))
	}
	return sets, args
}

// projectPatchAssignments is nodePatchAssignments for a project. Only name is
// patchable, and a nil Name has to produce no assignment at all: writing the
// name back unconditionally -- which is what UpdateProject used to do, reading
// it and passing it straight to the UPDATE -- undid a rename that landed
// between the read and the write.
func projectPatchAssignments(patch ProjectPatch) ([]string, []any) {
	if patch.Name == nil {
		return []string{}, []any{}
	}
	return []string{"name=?"}, []any{*patch.Name}
}

// updateNodeColumns is the shared body of SQLiteRepository.UpdateNode and
// MySQLRepository.UpdateNode. getNode is the caller's own GetNode.
//
// updated_at is always assigned, even when the patch names no other column:
// callers (and the Web UI's "last updated" column) have always been able to
// rely on a successful UpdateNode moving the timestamp, so an empty patch
// still touches the row rather than becoming a silent no-op.
//
// The row is read back *after* the write, never assembled from the values read
// before it. Assembling it would hand the caller a node whose other columns
// show the state from before a concurrent writer's change -- the same stale
// snapshot this function exists to stop being written back to the database.
// The read-back is not part of the write's atom, so it can also reflect a
// later writer's change; that is a truthful "current row", which is what the
// return value claims to be.
//
// A missing node is reported exactly as before (`node <id> not found`): the
// UPDATE simply matches no row, so nothing is written, and the read-back that
// follows comes up empty.
func updateNodeColumns(db *sql.DB, getNode func(string) (*domain.GraphNode, error), id string, patch NodePatch) (domain.GraphNode, error) {
	sets, args := nodePatchAssignments(patch)
	sets = append(sets, "updated_at=?")
	args = append(args, time.Now().UTC().Format(time.RFC3339Nano), id)

	if _, err := db.Exec(`UPDATE nodes SET `+strings.Join(sets, ", ")+` WHERE id=?`, args...); err != nil {
		return domain.GraphNode{}, fmt.Errorf("updating node %s: %w", id, err)
	}
	updated, err := getNode(id)
	if err != nil {
		return domain.GraphNode{}, err
	}
	if updated == nil {
		return domain.GraphNode{}, fmt.Errorf("node %s not found", id)
	}
	return *updated, nil
}

// updateProjectColumns is the shared body of SQLiteRepository.UpdateProject
// and MySQLRepository.UpdateProject. It follows updateNodeColumns' rules (see
// there); the only patchable column is name, and ticket_seq -- which
// AllocateTicketSeq bumps inside its own transaction -- was already left alone
// before this change.
//
// The lost update this fixes is narrower than UpdateNode's: a patch with a nil
// Name used to write back the name it had just read, so a rename landing in
// between was rolled back to the old name by an update that never meant to
// touch the name at all.
func updateProjectColumns(db *sql.DB, getProject func(string) (*domain.Project, error), id string, patch ProjectPatch) (domain.Project, error) {
	sets, args := projectPatchAssignments(patch)
	sets = append(sets, "updated_at=?")
	args = append(args, time.Now().UTC().Format(time.RFC3339Nano), id)

	if _, err := db.Exec(`UPDATE projects SET `+strings.Join(sets, ", ")+` WHERE id=?`, args...); err != nil {
		return domain.Project{}, fmt.Errorf("updating project %s: %w", id, err)
	}
	updated, err := getProject(id)
	if err != nil {
		return domain.Project{}, err
	}
	if updated == nil {
		return domain.Project{}, domain.NewAPIError(domain.ErrCodeProjectNotFound, "project %s not found", id)
	}
	return *updated, nil
}

// claimNodeCAS is the shared body of SQLiteRepository.ClaimNode and
// MySQLRepository.ClaimNode -- see GraphRepository.ClaimNode for the contract,
// including why newStatus has to be one of excluded.
func claimNodeCAS(db *sql.DB, getNode func(string) (*domain.GraphNode, error), id string, newStatus domain.NodeStatus, excluded []domain.NodeStatus) (*domain.GraphNode, error) {
	query := `UPDATE nodes SET status=?, updated_at=? WHERE id=?`
	args := []any{string(newStatus), time.Now().UTC().Format(time.RFC3339Nano), id}
	if len(excluded) > 0 {
		placeholders := make([]string, len(excluded))
		for i, s := range excluded {
			placeholders[i] = "?"
			args = append(args, string(s))
		}
		query += ` AND status NOT IN (` + strings.Join(placeholders, ", ") + `)`
	}

	res, err := db.Exec(query, args...)
	if err != nil {
		return nil, fmt.Errorf("claiming node %s: %w", id, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("claiming node %s: %w", id, err)
	}
	if affected == 0 {
		return nil, nil
	}
	return getNode(id)
}
