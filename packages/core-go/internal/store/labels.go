package store

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/graph-ops/core-go/internal/domain"
)

// This file holds the label (DFLT-00084) and ticket-update logic shared by
// SQLiteRepository and MySQLRepository. The SQL is plain enough to be the
// same for both backends; the two differences are captured by sqlDialect:
// MySQL needs explicit "FOR UPDATE" row locks where SQLite's single pinned
// connection already serializes everything (see SQLiteRepository's doc
// comment), and each driver reports a UNIQUE violation differently.

// sqlDialect describes the per-backend differences the shared label code
// needs.
type sqlDialect struct {
	// forUpdate is appended to SELECTs that read a row the transaction is
	// about to modify based on what it read.
	forUpdate string
	// isUniqueViolation reports whether err is the driver's UNIQUE
	// constraint violation, so the labels table's (project_id, name) index
	// -- the last line of defense against two concurrent creates/renames --
	// surfaces as LABEL_NAME_TAKEN instead of a 500.
	isUniqueViolation func(error) bool
}

// sqlRunner is what *sql.DB and *sql.Tx have in common.
type sqlRunner interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

const labelSelectCols = `l.id, l.project_id, l.name, l.color, l.created_at, l.updated_at`

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func scanLabel(row interface{ Scan(dest ...any) error }, extra ...any) (domain.Label, error) {
	var l domain.Label
	dest := append([]any{&l.ID, &l.ProjectID, &l.Name, &l.Color, &l.CreatedAt, &l.UpdatedAt}, extra...)
	if err := row.Scan(dest...); err != nil {
		return domain.Label{}, err
	}
	return l, nil
}

// labelLess orders labels by name case-insensitively, falling back to the
// exact name and then the ID so the order is total and stable across DBs
// (their collations disagree on case/accents, so ordering isn't left to SQL).
func labelLess(a, b domain.Label) bool {
	la, lb := strings.ToLower(a.Name), strings.ToLower(b.Name)
	if la != lb {
		return la < lb
	}
	if a.Name != b.Name {
		return a.Name < b.Name
	}
	return a.ID < b.ID
}

func sortLabels(labels []domain.Label) {
	sort.SliceStable(labels, func(i, j int) bool { return labelLess(labels[i], labels[j]) })
}

// projectExists reports whether projectID exists, locking its row when the
// dialect uses row locks: label creation/renaming lock the project row so
// two concurrent name checks for the same project serialize.
func projectExists(q sqlRunner, d sqlDialect, projectID string) (bool, error) {
	var id string
	err := q.QueryRow(`SELECT id FROM projects WHERE id = ?`+d.forUpdate, projectID).Scan(&id)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("loading project %s: %w", projectID, err)
	}
	return true, nil
}

// ensureLabelNameFree returns LABEL_NAME_TAKEN if a label of projectID other
// than exceptID already has name, compared with strings.EqualFold. This is
// the rule both backends apply identically; the DB's own case-insensitive
// UNIQUE index differs per backend (SQLite's NOCASE folds ASCII only, MySQL's
// utf8mb4_general_ci folds more) and only backs this check up.
func ensureLabelNameFree(q sqlRunner, projectID, name, exceptID string) error {
	rows, err := q.Query(`SELECT id, name FROM labels WHERE project_id = ?`, projectID)
	if err != nil {
		return fmt.Errorf("listing label names for project %s: %w", projectID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, existing string
		if err := rows.Scan(&id, &existing); err != nil {
			return err
		}
		if id != exceptID && strings.EqualFold(existing, name) {
			return domain.NewAPIError(domain.ErrCodeLabelNameTaken, "a label named %q already exists in project %s", existing, projectID)
		}
	}
	return rows.Err()
}

func labelNameTakenOr(d sqlDialect, err error, projectID, name string, format string) error {
	if d.isUniqueViolation != nil && d.isUniqueViolation(err) {
		return domain.NewAPIError(domain.ErrCodeLabelNameTaken, "a label named %q already exists in project %s", name, projectID)
	}
	return fmt.Errorf(format, err)
}

func createLabel(db *sql.DB, d sqlDialect, projectID, rawName, rawColor string) (domain.Label, error) {
	name, err := domain.NormalizeLabelName(rawName)
	if err != nil {
		return domain.Label{}, err
	}
	color, err := domain.ParseLabelColor(rawColor)
	if err != nil {
		return domain.Label{}, err
	}

	tx, err := db.Begin()
	if err != nil {
		return domain.Label{}, err
	}
	defer tx.Rollback() //nolint:errcheck

	ok, err := projectExists(tx, d, projectID)
	if err != nil {
		return domain.Label{}, err
	}
	if !ok {
		return domain.Label{}, domain.NewAPIError(domain.ErrCodeProjectNotFound, "project %s not found", projectID)
	}
	if err := ensureLabelNameFree(tx, projectID, name, ""); err != nil {
		return domain.Label{}, err
	}

	now := nowRFC3339()
	label := domain.Label{
		ID: "label-" + shortUUID(), ProjectID: projectID, Name: name, Color: color,
		CreatedAt: now, UpdatedAt: now,
	}
	if _, err := tx.Exec(
		`INSERT INTO labels (id, project_id, name, color, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		label.ID, label.ProjectID, label.Name, string(label.Color), label.CreatedAt, label.UpdatedAt,
	); err != nil {
		return domain.Label{}, labelNameTakenOr(d, err, projectID, name, "inserting label: %w")
	}
	if err := tx.Commit(); err != nil {
		return domain.Label{}, labelNameTakenOr(d, err, projectID, name, "committing label: %w")
	}
	return label, nil
}

func getLabel(q sqlRunner, d sqlDialect, id string) (*domain.Label, error) {
	l, err := scanLabel(q.QueryRow(`SELECT `+labelSelectCols+` FROM labels l WHERE l.id = ?`+d.forUpdate, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting label %s: %w", id, err)
	}
	return &l, nil
}

func listLabelsByProject(db *sql.DB, projectID string) ([]domain.LabelUsage, error) {
	ok, err := projectExists(db, sqlDialect{}, projectID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, domain.NewAPIError(domain.ErrCodeProjectNotFound, "project %s not found", projectID)
	}
	rows, err := db.Query(
		`SELECT `+labelSelectCols+`, (SELECT COUNT(*) FROM ticket_labels tl WHERE tl.label_id = l.id)
		 FROM labels l WHERE l.project_id = ?`, projectID)
	if err != nil {
		return nil, fmt.Errorf("listing labels for project %s: %w", projectID, err)
	}
	defer rows.Close()
	out := []domain.LabelUsage{}
	for rows.Next() {
		var count int
		l, err := scanLabel(rows, &count)
		if err != nil {
			return nil, err
		}
		out = append(out, domain.LabelUsage{Label: l, TicketCount: count})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return labelLess(out[i].Label, out[j].Label) })
	return out, nil
}

func updateLabel(db *sql.DB, d sqlDialect, id string, patch LabelPatch) (domain.Label, error) {
	var name *string
	if patch.Name != nil {
		n, err := domain.NormalizeLabelName(*patch.Name)
		if err != nil {
			return domain.Label{}, err
		}
		name = &n
	}
	var color *domain.LabelColor
	if patch.Color != nil {
		c, err := domain.ParseLabelColor(*patch.Color)
		if err != nil {
			return domain.Label{}, err
		}
		color = &c
	}

	tx, err := db.Begin()
	if err != nil {
		return domain.Label{}, err
	}
	defer tx.Rollback() //nolint:errcheck

	// Read the label unlocked first to learn its project, then lock the
	// project row before the label row -- the same order createLabel takes
	// its lock in -- so a concurrent create and rename in one project queue
	// up instead of deadlocking.
	cur, err := getLabel(tx, sqlDialect{}, id)
	if err != nil {
		return domain.Label{}, err
	}
	if cur == nil {
		return domain.Label{}, domain.NewAPIError(domain.ErrCodeLabelNotFound, "label %s not found", id)
	}
	if _, err := projectExists(tx, d, cur.ProjectID); err != nil {
		return domain.Label{}, err
	}
	if cur, err = getLabel(tx, d, id); err != nil {
		return domain.Label{}, err
	}
	if cur == nil {
		return domain.Label{}, domain.NewAPIError(domain.ErrCodeLabelNotFound, "label %s not found", id)
	}
	if name != nil {
		if err := ensureLabelNameFree(tx, cur.ProjectID, *name, cur.ID); err != nil {
			return domain.Label{}, err
		}
		cur.Name = *name
	}
	if color != nil {
		cur.Color = *color
	}
	cur.UpdatedAt = nowRFC3339()
	if _, err := tx.Exec(`UPDATE labels SET name = ?, color = ?, updated_at = ? WHERE id = ?`,
		cur.Name, string(cur.Color), cur.UpdatedAt, cur.ID); err != nil {
		return domain.Label{}, labelNameTakenOr(d, err, cur.ProjectID, cur.Name, "updating label: %w")
	}
	if err := tx.Commit(); err != nil {
		return domain.Label{}, labelNameTakenOr(d, err, cur.ProjectID, cur.Name, "committing label update: %w")
	}
	return *cur, nil
}

// deleteLabel detaches the label from every ticket explicitly (rather than
// relying on ticket_labels' ON DELETE CASCADE alone) and deletes it, in one
// transaction, returning how many tickets carried it.
func deleteLabel(db *sql.DB, d sqlDialect, id string) (int, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck

	cur, err := getLabel(tx, d, id)
	if err != nil {
		return 0, err
	}
	if cur == nil {
		return 0, domain.NewAPIError(domain.ErrCodeLabelNotFound, "label %s not found", id)
	}
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM ticket_labels WHERE label_id = ?`, id).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting tickets for label %s: %w", id, err)
	}
	if _, err := tx.Exec(`DELETE FROM ticket_labels WHERE label_id = ?`, id); err != nil {
		return 0, fmt.Errorf("detaching label %s from tickets: %w", id, err)
	}
	if _, err := tx.Exec(`DELETE FROM labels WHERE id = ?`, id); err != nil {
		return 0, fmt.Errorf("deleting label %s: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}

// deleteProjectLabels removes every label of projectID (and any link to
// them) inside DeleteProject's transaction.
func deleteProjectLabels(tx *sql.Tx, projectID string) error {
	if _, err := tx.Exec(`DELETE FROM ticket_labels WHERE label_id IN (SELECT id FROM labels WHERE project_id = ?)`, projectID); err != nil {
		return fmt.Errorf("detaching labels of project %s: %w", projectID, err)
	}
	if _, err := tx.Exec(`DELETE FROM labels WHERE project_id = ?`, projectID); err != nil {
		return fmt.Errorf("deleting labels of project %s: %w", projectID, err)
	}
	return nil
}

// dedupeIDs drops repeated IDs, keeping first-seen order.
func dedupeIDs(ids []string) []string {
	seen := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// replaceTicketLabels validates that every ID in labelIDs is a label of
// projectID and then makes it the ticket's exact label set. Validation runs
// before anything is written; callers run it inside their own transaction so
// a failure rolls back the rest of their write too.
func replaceTicketLabels(tx *sql.Tx, ticketID, projectID string, labelIDs []string) error {
	ids := dedupeIDs(labelIDs)
	if len(ids) > 0 {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
		args := make([]any, 0, len(ids)+1)
		args = append(args, projectID)
		for _, id := range ids {
			args = append(args, id)
		}
		rows, err := tx.Query(`SELECT id FROM labels WHERE project_id = ? AND id IN (`+placeholders+`)`, args...)
		if err != nil {
			return fmt.Errorf("validating label ids: %w", err)
		}
		found := map[string]bool{}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			found[id] = true
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		var missing []string
		for _, id := range ids {
			if !found[id] {
				missing = append(missing, id)
			}
		}
		if len(missing) > 0 {
			return domain.NewAPIError(domain.ErrCodeLabelNotFound,
				"label(s) %s not found in project %s", strings.Join(missing, ", "), projectID)
		}
	}

	if _, err := tx.Exec(`DELETE FROM ticket_labels WHERE ticket_id = ?`, ticketID); err != nil {
		return fmt.Errorf("clearing labels of ticket %s: %w", ticketID, err)
	}
	now := nowRFC3339()
	for _, id := range ids {
		if _, err := tx.Exec(`INSERT INTO ticket_labels (ticket_id, label_id, created_at) VALUES (?, ?, ?)`, ticketID, id, now); err != nil {
			return fmt.Errorf("attaching label %s to ticket %s: %w", id, ticketID, err)
		}
	}
	return nil
}

// ticketLabelsJoin selects (ticket_id, label columns) for the links matched
// by the WHERE clause appended to it.
const ticketLabelsJoin = `SELECT tl.ticket_id, ` + labelSelectCols + `
	FROM ticket_labels tl JOIN labels l ON l.id = tl.label_id`

// loadLabelsByTicket runs one ticketLabelsJoin query (never one per ticket)
// and groups the result by ticket ID, each group sorted by labelLess.
func loadLabelsByTicket(q sqlRunner, where string, args ...any) (map[string][]domain.Label, error) {
	rows, err := q.Query(ticketLabelsJoin+" "+where, args...)
	if err != nil {
		return nil, fmt.Errorf("loading ticket labels: %w", err)
	}
	defer rows.Close()
	out := map[string][]domain.Label{}
	for rows.Next() {
		var ticketID string
		var l domain.Label
		if err := rows.Scan(&ticketID, &l.ID, &l.ProjectID, &l.Name, &l.Color, &l.CreatedAt, &l.UpdatedAt); err != nil {
			return nil, err
		}
		out[ticketID] = append(out[ticketID], l)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, labels := range out {
		sortLabels(labels)
	}
	return out, nil
}

// attachLabels sets each ticket's Labels from byTicket, always to a non-nil
// slice.
func attachLabels(tickets []domain.Ticket, byTicket map[string][]domain.Label) {
	for i := range tickets {
		if labels := byTicket[tickets[i].ID]; labels != nil {
			tickets[i].Labels = labels
		} else {
			tickets[i].Labels = []domain.Label{}
		}
	}
}

// getTicketWithLabels reads one ticket (nil if missing) plus its labels.
func getTicketWithLabels(q sqlRunner, d sqlDialect, id string) (*domain.Ticket, error) {
	t, err := scanTicket(q.QueryRow(`SELECT `+ticketSelectCols+` FROM tickets WHERE id = ?`+d.forUpdate, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting ticket %s: %w", id, err)
	}
	byTicket, err := loadLabelsByTicket(q, `WHERE tl.ticket_id = ?`, id)
	if err != nil {
		return nil, err
	}
	tickets := []domain.Ticket{*t}
	attachLabels(tickets, byTicket)
	return &tickets[0], nil
}

// listTicketsWithLabels runs the ticket query, closes its rows, and then
// loads every listed ticket's labels with a single labelsWhere query. The
// rows must be closed first: SQLite's pool has exactly one connection.
func listTicketsWithLabels(db *sql.DB, ticketQuery string, ticketArgs []any, labelsWhere string, labelArgs []any) ([]domain.Ticket, error) {
	rows, err := db.Query(ticketQuery, ticketArgs...)
	if err != nil {
		return nil, fmt.Errorf("listing tickets: %w", err)
	}
	out := []domain.Ticket{}
	for rows.Next() {
		t, err := scanTicket(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, *t)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if len(out) == 0 {
		return out, nil
	}
	byTicket, err := loadLabelsByTicket(db, labelsWhere, labelArgs...)
	if err != nil {
		return nil, err
	}
	attachLabels(out, byTicket)
	return out, nil
}

// updateTicket is UpdateTicket for both backends: the read, the optional
// label replacement and the row update share one transaction, so an invalid
// LabelIDs leaves every field untouched.
func updateTicket(db *sql.DB, d sqlDialect, id string, patch TicketPatch) (domain.Ticket, error) {
	tx, err := db.Begin()
	if err != nil {
		return domain.Ticket{}, err
	}
	defer tx.Rollback() //nolint:errcheck

	cur, err := getTicketWithLabels(tx, d, id)
	if err != nil {
		return domain.Ticket{}, err
	}
	if cur == nil {
		return domain.Ticket{}, domain.NewAPIError(domain.ErrCodeTicketNotFound, "ticket %s not found", id)
	}
	if patch.Title != nil {
		cur.Title = *patch.Title
	}
	if patch.Description != nil {
		cur.Description = *patch.Description
	}
	if patch.Status != nil {
		cur.Status = *patch.Status
	}
	if patch.AutoExecutable != nil {
		cur.AutoExecutable = *patch.AutoExecutable
	}
	if patch.Blocked != nil {
		cur.Blocked = *patch.Blocked
	}
	if patch.RefinedAt != nil {
		cur.RefinedAt = patch.RefinedAt
	}
	if patch.Assignee != nil {
		cur.Assignee = *patch.Assignee
	}
	if patch.ClosedReason != nil {
		cur.ClosedReason = patch.ClosedReason
	}
	if patch.GraphExpandedAt != nil {
		cur.GraphExpandedAt = patch.GraphExpandedAt
	}
	if patch.Priority != nil {
		cur.Priority = *patch.Priority
	}
	// A row still NULL/empty (e.g. written by an older graph-engine sharing
	// this DB after Init's backfill ran) must not be written back as '': fill
	// the default on write, as CreateTicket does, so any update -- even one
	// that doesn't touch priority -- leaves the row with a valid level.
	cur.Priority = domain.TicketPriority(ticketPriorityOrDefault(cur.Priority))
	cur.UpdatedAt = nowRFC3339()

	if patch.LabelIDs != nil {
		if err := replaceTicketLabels(tx, cur.ID, cur.ProjectID, *patch.LabelIDs); err != nil {
			return domain.Ticket{}, err
		}
	}

	if _, err := tx.Exec(
		`UPDATE tickets SET title=?, description=?, status=?, auto_executable=?, blocked=?, refined_at=?, closed_reason=?, assignee_name=?, graph_expanded_at=?, priority=?, updated_at=?
		 WHERE id=?`,
		cur.Title, cur.Description, cur.Status,
		boolToInt(cur.AutoExecutable), boolToInt(cur.Blocked), nullableString(cur.RefinedAt), nullableString(cur.ClosedReason), nullableString(cur.Assignee), nullableString(cur.GraphExpandedAt), string(cur.Priority), cur.UpdatedAt, cur.ID,
	); err != nil {
		return domain.Ticket{}, fmt.Errorf("updating ticket %s: %w", id, err)
	}

	if patch.LabelIDs != nil {
		byTicket, err := loadLabelsByTicket(tx, `WHERE tl.ticket_id = ?`, cur.ID)
		if err != nil {
			return domain.Ticket{}, err
		}
		tickets := []domain.Ticket{*cur}
		attachLabels(tickets, byTicket)
		cur = &tickets[0]
	}
	if err := tx.Commit(); err != nil {
		return domain.Ticket{}, err
	}
	return *cur, nil
}

// labelIDsOf extracts the IDs CreateTicket attaches from its input ticket.
func labelIDsOf(labels []domain.Label) []string {
	ids := make([]string, 0, len(labels))
	for _, l := range labels {
		ids = append(ids, l.ID)
	}
	return ids
}
