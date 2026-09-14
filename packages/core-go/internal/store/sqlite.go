package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/project"
)

const schemaDDL = `
CREATE TABLE IF NOT EXISTS projects (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	prefix TEXT NOT NULL,
	work_dir TEXT NOT NULL,
	ticket_seq INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_projects_prefix_nocase ON projects(prefix COLLATE NOCASE);

-- Holds the single "currently selected project" the Web UI and CLI both
-- read/write (see GetCurrentProjectID/SetCurrentProjectID). A single row
-- keyed on id=1, upserted in place -- there is never more than one current
-- project process-wide.
CREATE TABLE IF NOT EXISTS app_state (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	current_project_id TEXT REFERENCES projects(id)
);

CREATE TABLE IF NOT EXISTS tickets (
	id TEXT PRIMARY KEY,
	project_id TEXT REFERENCES projects(id),
	title TEXT NOT NULL,
	description TEXT NOT NULL,
	status TEXT NOT NULL,
	auto_executable INTEGER NOT NULL DEFAULT 1,
	blocked INTEGER NOT NULL DEFAULT 0,
	node_seq INTEGER NOT NULL DEFAULT 0,
	refined_at TEXT,
	closed_reason TEXT,
	assignee_name TEXT,
	graph_expanded_at TEXT,
	priority TEXT,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS nodes (
	id TEXT PRIMARY KEY,
	ticket_id TEXT NOT NULL,
	name TEXT NOT NULL,
	type TEXT NOT NULL,
	status TEXT NOT NULL,
	iteration_count INTEGER NOT NULL DEFAULT 0,
	max_iterations INTEGER NOT NULL DEFAULT 3,
	assignee TEXT,
	is_manual INTEGER NOT NULL DEFAULT 0,
	gate_id TEXT,
	criteria TEXT,
	config_id TEXT,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	FOREIGN KEY(ticket_id) REFERENCES tickets(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS edges (
	id TEXT PRIMARY KEY,
	ticket_id TEXT NOT NULL,
	from_node_id TEXT NOT NULL,
	to_node_id TEXT NOT NULL,
	condition TEXT,
	created_at TEXT NOT NULL,
	FOREIGN KEY(ticket_id) REFERENCES tickets(id) ON DELETE CASCADE,
	FOREIGN KEY(from_node_id) REFERENCES nodes(id) ON DELETE CASCADE,
	FOREIGN KEY(to_node_id) REFERENCES nodes(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS artifacts (
	id TEXT PRIMARY KEY,
	ticket_id TEXT NOT NULL,
	node_id TEXT NOT NULL,
	name TEXT NOT NULL,
	type TEXT NOT NULL,
	content TEXT,
	file_path TEXT,
	metadata TEXT,
	created_at TEXT NOT NULL,
	FOREIGN KEY(ticket_id) REFERENCES tickets(id) ON DELETE CASCADE,
	FOREIGN KEY(node_id) REFERENCES nodes(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_nodes_ticket ON nodes(ticket_id);
CREATE INDEX IF NOT EXISTS idx_edges_ticket ON edges(ticket_id);
CREATE INDEX IF NOT EXISTS idx_artifacts_ticket ON artifacts(ticket_id);
CREATE INDEX IF NOT EXISTS idx_artifacts_node ON artifacts(node_id);
CREATE INDEX IF NOT EXISTS idx_tickets_project ON tickets(project_id);
`

// SQLiteRepository implements GraphRepository on top of database/sql with the
// pure-Go modernc.org/sqlite driver. The pool is pinned to a single
// connection: this is a local, single-user tool, SQLite serializes writers
// anyway, and it sidesteps the per-connection semantics of PRAGMA
// foreign_keys under Go's connection pooling. It also lets a single
// *sql.Tx safely span the read-modify-write sequences that mint ticket/node
// IDs (see CreateTicket/CreateNode) without a second goroutine's query
// stealing the connection in between.
type SQLiteRepository struct {
	db *sql.DB
}

func NewSQLiteRepository(dbPath string) (*SQLiteRepository, error) {
	// The DB's parent directory may not exist yet, and this is the one place
	// every route into SQLite (store.Open included) passes through, so create
	// it here. sql.Open below connects lazily, so a missing directory would
	// not surface at this point at all: it would fail later, at the first
	// query, as an opaque "unable to open database file" naming no path.
	//
	// This is a backstop for an arbitrary dbPath rather than the thing that
	// creates the default $HOME/.graph-ops (see cmd/graph-engine's
	// defaultDataDir). In the default configuration loadRuntimeConfig runs
	// first on every subcommand and has already created the sibling artifacts
	// directory -- and hence $HOME/.graph-ops -- by the time a repository is
	// opened. What is left for this line is any dbPath whose parent nothing
	// else made: an explicit GRAPH_DB_PATH/dbPath naming a fresh directory,
	// artifacts pointed elsewhere, or another caller of store.Open.
	//
	// 0o700 matches loadRuntimeConfig and runtimeconfig.Save; see the former
	// for why the mode is user-only and why it is applied unconditionally
	// instead of only for the default path. Whichever of the three runs first
	// in a fresh environment is the one that fixes the mode, so they have to
	// agree.
	//
	// No guard on the result: filepath.Dir always yields a non-empty path
	// (a bare filename gives "."), and MkdirAll on an existing directory --
	// "." included -- is a no-op.
	dbDir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dbDir, 0o700); err != nil {
		return nil, fmt.Errorf("creating sqlite db directory %s: %w", dbDir, err)
	}
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening sqlite db: %w", err)
	}
	db.SetMaxOpenConns(1)
	return &SQLiteRepository{db: db}, nil
}

func (r *SQLiteRepository) Init() error {
	if _, err := r.db.Exec(schemaDDL); err != nil {
		return fmt.Errorf("applying schema: %w", err)
	}
	return nil
}

func shortUUID() string {
	return uuid.New().String()[:8]
}

func nullableString(s *string) sql.NullString {
	if s == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *s, Valid: true}
}

// nullableTicketPriority is nullableString's counterpart for
// domain.TicketPriority, whose underlying type is a distinct string type
// rather than string itself.
func nullableTicketPriority(p *domain.TicketPriority) sql.NullString {
	if p == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(*p), Valid: true}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// --- Tickets ---

const ticketSelectCols = `id, project_id, title, description, status, auto_executable, blocked, refined_at, closed_reason, assignee_name, graph_expanded_at, priority, created_at, updated_at`

// CreateTicket mints projectID's next ticket ID (<prefix>-<seq:05d>) and
// inserts t under it, all inside one *sql.Tx so the read-increment-write of
// projects.ticket_seq can never race with a concurrent CreateTicket/
// CreateNode call stealing the pool's single connection in between (see
// SQLiteRepository's doc comment).
func (r *SQLiteRepository) CreateTicket(projectID string, t domain.Ticket) (domain.Ticket, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return domain.Ticket{}, err
	}
	defer tx.Rollback() //nolint:errcheck

	var prefix string
	var seq int
	row := tx.QueryRow(`SELECT prefix, ticket_seq FROM projects WHERE id = ?`, projectID)
	if err := row.Scan(&prefix, &seq); err != nil {
		if err == sql.ErrNoRows {
			return domain.Ticket{}, domain.NewAPIError(domain.ErrCodeProjectNotFound, "project %s not found", projectID)
		}
		return domain.Ticket{}, fmt.Errorf("loading project %s: %w", projectID, err)
	}
	seq++
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.Exec(`UPDATE projects SET ticket_seq = ?, updated_at = ? WHERE id = ?`, seq, now, projectID); err != nil {
		return domain.Ticket{}, fmt.Errorf("incrementing project ticket_seq: %w", err)
	}
	id := fmt.Sprintf("%s-%05d", prefix, seq)

	_, err = tx.Exec(
		`INSERT INTO tickets (id, project_id, title, description, status, auto_executable, blocked, node_seq, assignee_name, priority, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?)`,
		id, projectID, t.Title, t.Description, t.Status,
		boolToInt(t.AutoExecutable), boolToInt(t.Blocked), nullableString(t.Assignee), nullableTicketPriority(t.Priority), now, now,
	)
	if err != nil {
		return domain.Ticket{}, fmt.Errorf("inserting ticket: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return domain.Ticket{}, err
	}
	got, err := r.GetTicket(id)
	if err != nil {
		return domain.Ticket{}, err
	}
	return *got, nil
}

func scanTicket(row interface {
	Scan(dest ...any) error
}) (*domain.Ticket, error) {
	var t domain.Ticket
	var projectID, refinedAt, closedReason, assigneeName, graphExpandedAt, priority sql.NullString
	var autoExec, blocked int
	if err := row.Scan(&t.ID, &projectID, &t.Title, &t.Description, &t.Status,
		&autoExec, &blocked, &refinedAt, &closedReason, &assigneeName, &graphExpandedAt, &priority, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil, err
	}
	if projectID.Valid {
		t.ProjectID = projectID.String
	}
	if refinedAt.Valid {
		t.RefinedAt = &refinedAt.String
	}
	if closedReason.Valid {
		t.ClosedReason = &closedReason.String
	}
	if graphExpandedAt.Valid {
		t.GraphExpandedAt = &graphExpandedAt.String
	}
	t.AutoExecutable = autoExec != 0
	t.Blocked = blocked != 0
	if assigneeName.Valid {
		t.Assignee = &assigneeName.String
	}
	if priority.Valid {
		p := domain.TicketPriority(priority.String)
		t.Priority = &p
	}
	return &t, nil
}

// GetTicket looks up a ticket by its ID.
func (r *SQLiteRepository) GetTicket(id string) (*domain.Ticket, error) {
	row := r.db.QueryRow(`SELECT `+ticketSelectCols+` FROM tickets WHERE id = ?`, id)
	t, err := scanTicket(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting ticket %s: %w", id, err)
	}
	return t, nil
}

func (r *SQLiteRepository) GetTicketDetail(id string) (*domain.TicketDetail, error) {
	t, err := r.GetTicket(id)
	if err != nil || t == nil {
		return nil, err
	}
	nodes, err := r.ListNodesByTicket(t.ID)
	if err != nil {
		return nil, err
	}
	edges, err := r.ListEdgesByTicket(t.ID)
	if err != nil {
		return nil, err
	}
	artifacts, err := r.ListArtifactsByTicket(t.ID)
	if err != nil {
		return nil, err
	}
	return &domain.TicketDetail{Ticket: *t, Nodes: nodes, Edges: edges, Artifacts: artifacts}, nil
}

func (r *SQLiteRepository) ListTickets() ([]domain.Ticket, error) {
	rows, err := r.db.Query(`SELECT ` + ticketSelectCols + ` FROM tickets ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("listing tickets: %w", err)
	}
	defer rows.Close()
	out := []domain.Ticket{}
	for rows.Next() {
		t, err := scanTicket(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

func (r *SQLiteRepository) ListTicketsByProject(projectID string) ([]domain.Ticket, error) {
	rows, err := r.db.Query(`SELECT `+ticketSelectCols+` FROM tickets WHERE project_id = ? ORDER BY created_at DESC`, projectID)
	if err != nil {
		return nil, fmt.Errorf("listing tickets for project %s: %w", projectID, err)
	}
	defer rows.Close()
	out := []domain.Ticket{}
	for rows.Next() {
		t, err := scanTicket(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

func (r *SQLiteRepository) UpdateTicket(id string, patch TicketPatch) (domain.Ticket, error) {
	cur, err := r.GetTicket(id)
	if err != nil {
		return domain.Ticket{}, err
	}
	if cur == nil {
		return domain.Ticket{}, fmt.Errorf("ticket %s not found", id)
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
	cur.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)

	_, err = r.db.Exec(
		`UPDATE tickets SET title=?, description=?, status=?, auto_executable=?, blocked=?, refined_at=?, closed_reason=?, assignee_name=?, graph_expanded_at=?, priority=?, updated_at=?
		 WHERE id=?`,
		cur.Title, cur.Description, cur.Status,
		boolToInt(cur.AutoExecutable), boolToInt(cur.Blocked), nullableString(cur.RefinedAt), nullableString(cur.ClosedReason), nullableString(cur.Assignee), nullableString(cur.GraphExpandedAt), nullableTicketPriority(cur.Priority), cur.UpdatedAt, cur.ID,
	)
	if err != nil {
		return domain.Ticket{}, fmt.Errorf("updating ticket %s: %w", id, err)
	}
	return *cur, nil
}

func (r *SQLiteRepository) DeleteTicket(id string) error {
	cur, err := r.GetTicket(id)
	if err != nil {
		return err
	}
	if cur == nil {
		return nil
	}
	_, err = r.db.Exec(`DELETE FROM tickets WHERE id = ?`, cur.ID)
	return err
}

// --- Nodes ---

// CreateNode mints n.TicketID's next node ID (<ticketID>-<seq:02d>, since a
// ticket is capped at 99 nodes) and inserts n under it, inside one *sql.Tx
// (see CreateTicket's doc comment for why).
func (r *SQLiteRepository) CreateNode(n domain.GraphNode) (domain.GraphNode, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return domain.GraphNode{}, err
	}
	defer tx.Rollback() //nolint:errcheck

	var seq int
	row := tx.QueryRow(`SELECT node_seq FROM tickets WHERE id = ?`, n.TicketID)
	if err := row.Scan(&seq); err != nil {
		if err == sql.ErrNoRows {
			return domain.GraphNode{}, fmt.Errorf("ticket %s not found", n.TicketID)
		}
		return domain.GraphNode{}, fmt.Errorf("loading ticket %s: %w", n.TicketID, err)
	}
	seq++
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.Exec(`UPDATE tickets SET node_seq = ?, updated_at = ? WHERE id = ?`, seq, now, n.TicketID); err != nil {
		return domain.GraphNode{}, fmt.Errorf("incrementing ticket node_seq: %w", err)
	}
	id := fmt.Sprintf("%s-%02d", n.TicketID, seq)

	maxIter := n.MaxIterations
	if maxIter == 0 {
		maxIter = 3
	}
	_, err = tx.Exec(
		`INSERT INTO nodes (id, ticket_id, name, type, status, iteration_count, max_iterations, assignee, is_manual, gate_id, criteria, config_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, n.TicketID, n.Name, n.Type, n.Status, n.IterationCount, maxIter,
		nullableString(n.Assignee), boolToInt(n.IsManual), nullableString(n.GateID), nullableString(n.Criteria), nullableString(n.ConfigID),
		now, now,
	)
	if err != nil {
		return domain.GraphNode{}, fmt.Errorf("inserting node: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return domain.GraphNode{}, err
	}
	got, err := r.GetNode(id)
	if err != nil {
		return domain.GraphNode{}, err
	}
	return *got, nil
}

func scanNode(row interface {
	Scan(dest ...any) error
}) (*domain.GraphNode, error) {
	var n domain.GraphNode
	var assignee, gateID, criteria, configID sql.NullString
	var isManual int
	if err := row.Scan(&n.ID, &n.TicketID, &n.Name, &n.Type, &n.Status, &n.IterationCount,
		&n.MaxIterations, &assignee, &isManual, &gateID, &criteria, &configID, &n.CreatedAt, &n.UpdatedAt); err != nil {
		return nil, err
	}
	if assignee.Valid {
		n.Assignee = &assignee.String
	}
	if gateID.Valid {
		n.GateID = &gateID.String
	}
	if criteria.Valid {
		n.Criteria = &criteria.String
	}
	if configID.Valid {
		n.ConfigID = &configID.String
	}
	n.IsManual = isManual != 0
	return &n, nil
}

const nodeSelectCols = `id, ticket_id, name, type, status, iteration_count, max_iterations, assignee, is_manual, gate_id, criteria, config_id, created_at, updated_at`

// GetNode looks up a node by its ID.
func (r *SQLiteRepository) GetNode(id string) (*domain.GraphNode, error) {
	row := r.db.QueryRow(`SELECT `+nodeSelectCols+` FROM nodes WHERE id = ?`, id)
	n, err := scanNode(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting node %s: %w", id, err)
	}
	return n, nil
}

func (r *SQLiteRepository) ListNodesByTicket(ticketID string) ([]domain.GraphNode, error) {
	rows, err := r.db.Query(`SELECT `+nodeSelectCols+` FROM nodes WHERE ticket_id = ? ORDER BY created_at ASC`, ticketID)
	if err != nil {
		return nil, fmt.Errorf("listing nodes for ticket %s: %w", ticketID, err)
	}
	defer rows.Close()
	out := []domain.GraphNode{}
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	return out, rows.Err()
}

func (r *SQLiteRepository) UpdateNode(id string, patch NodePatch) (domain.GraphNode, error) {
	cur, err := r.GetNode(id)
	if err != nil {
		return domain.GraphNode{}, err
	}
	if cur == nil {
		return domain.GraphNode{}, fmt.Errorf("node %s not found", id)
	}
	if patch.Name != nil {
		cur.Name = *patch.Name
	}
	if patch.Type != nil {
		cur.Type = *patch.Type
	}
	if patch.Status != nil {
		cur.Status = *patch.Status
	}
	if patch.IterationCount != nil {
		cur.IterationCount = *patch.IterationCount
	}
	if patch.MaxIterations != nil {
		cur.MaxIterations = *patch.MaxIterations
	}
	if patch.Assignee != nil {
		cur.Assignee = *patch.Assignee
	}
	if patch.IsManual != nil {
		cur.IsManual = *patch.IsManual
	}
	if patch.GateID != nil {
		cur.GateID = patch.GateID
	}
	if patch.Criteria != nil {
		cur.Criteria = patch.Criteria
	}
	cur.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)

	_, err = r.db.Exec(
		`UPDATE nodes SET name=?, type=?, status=?, iteration_count=?, max_iterations=?, assignee=?, is_manual=?, gate_id=?, criteria=?, updated_at=?
		 WHERE id=?`,
		cur.Name, cur.Type, cur.Status, cur.IterationCount, cur.MaxIterations,
		nullableString(cur.Assignee), boolToInt(cur.IsManual), nullableString(cur.GateID), nullableString(cur.Criteria),
		cur.UpdatedAt, cur.ID,
	)
	if err != nil {
		return domain.GraphNode{}, fmt.Errorf("updating node %s: %w", id, err)
	}
	return *cur, nil
}

func (r *SQLiteRepository) DeleteNode(id string) error {
	cur, err := r.GetNode(id)
	if err != nil {
		return err
	}
	if cur == nil {
		return nil
	}
	_, err = r.db.Exec(`DELETE FROM nodes WHERE id = ?`, cur.ID)
	return err
}

// --- Edges ---

func (r *SQLiteRepository) CreateEdge(e domain.GraphEdge) (domain.GraphEdge, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	condition := e.Condition
	if condition == "" {
		condition = domain.EdgeAlways
	}
	_, err := r.db.Exec(
		`INSERT INTO edges (id, ticket_id, from_node_id, to_node_id, condition, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		e.ID, e.TicketID, e.FromNodeID, e.ToNodeID, condition, now,
	)
	if err != nil {
		return domain.GraphEdge{}, fmt.Errorf("inserting edge: %w", err)
	}
	e.Condition = condition
	e.CreatedAt = now
	return e, nil
}

func (r *SQLiteRepository) ListEdgesByTicket(ticketID string) ([]domain.GraphEdge, error) {
	rows, err := r.db.Query(
		`SELECT id, ticket_id, from_node_id, to_node_id, condition, created_at FROM edges WHERE ticket_id = ? ORDER BY created_at ASC`,
		ticketID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing edges for ticket %s: %w", ticketID, err)
	}
	defer rows.Close()
	out := []domain.GraphEdge{}
	for rows.Next() {
		var e domain.GraphEdge
		if err := rows.Scan(&e.ID, &e.TicketID, &e.FromNodeID, &e.ToNodeID, &e.Condition, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r *SQLiteRepository) ClearEdgesByTicket(ticketID string) error {
	_, err := r.db.Exec(`DELETE FROM edges WHERE ticket_id = ?`, ticketID)
	return err
}

// --- Artifacts ---

func (r *SQLiteRepository) CreateArtifact(a domain.Artifact) (domain.Artifact, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := r.db.Exec(
		`INSERT INTO artifacts (id, ticket_id, node_id, name, type, content, file_path, metadata, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.TicketID, a.NodeID, a.Name, a.Type,
		nullableString(a.Content), nullableString(a.FilePath), nullableString(a.Metadata), now,
	)
	if err != nil {
		return domain.Artifact{}, fmt.Errorf("inserting artifact: %w", err)
	}
	a.CreatedAt = now
	a.HasContent = a.Content != nil && *a.Content != ""
	return a, nil
}

func scanArtifact(row interface {
	Scan(dest ...any) error
}) (*domain.Artifact, error) {
	var a domain.Artifact
	var content, filePath, metadata sql.NullString
	if err := row.Scan(&a.ID, &a.TicketID, &a.NodeID, &a.Name, &a.Type, &content, &filePath, &metadata, &a.CreatedAt); err != nil {
		return nil, err
	}
	if content.Valid {
		a.Content = &content.String
	}
	if filePath.Valid {
		a.FilePath = &filePath.String
	}
	if metadata.Valid {
		a.Metadata = &metadata.String
	}
	a.HasContent = content.Valid && content.String != ""
	return &a, nil
}

const artifactSelectCols = `id, ticket_id, node_id, name, type, content, file_path, metadata, created_at`

// artifactSummaryCols backs ListArtifactsByTicket, which feeds both
// GET /api/tickets/{id} (polled every 15s by every open Web UI tab for every
// ticket, see App.tsx's fetchAllTickets) and `graph-engine get-ticket`. Once
// html/image artifacts started carrying their real bytes in `content`
// (base64 for image) instead of just a file_path string, inlining that
// column here made both of those responses grow without bound as
// screenshots/HTML reports accumulate on a ticket -- a performance
// regression flagged in DFLT-00006's non-functional review (art-b4607fba).
// So this summary omits `content` for html/image rows (their real bytes are
// fetched on demand, only when actually previewed, via
// GET /api/artifacts/{id}/content) while still inlining it for
// text/gherkin/json, which the Web UI and CLI render inline and which stay
// small. `has_content` tells callers whether there is anything to fetch for
// an html/image row whose content was omitted here, independent of
// file_path (content may exist with no originating file_path at all, for
// artifacts created from inline content rather than a file).
const artifactSummaryCols = `id, ticket_id, node_id, name, type,
	CASE WHEN type IN ('html', 'image') THEN NULL ELSE content END,
	file_path, metadata, created_at,
	CASE WHEN content IS NOT NULL AND content != '' THEN 1 ELSE 0 END`

func scanArtifactSummary(row interface {
	Scan(dest ...any) error
}) (*domain.Artifact, error) {
	var a domain.Artifact
	var content, filePath, metadata sql.NullString
	var hasContent bool
	if err := row.Scan(&a.ID, &a.TicketID, &a.NodeID, &a.Name, &a.Type, &content, &filePath, &metadata, &a.CreatedAt, &hasContent); err != nil {
		return nil, err
	}
	if content.Valid {
		a.Content = &content.String
	}
	if filePath.Valid {
		a.FilePath = &filePath.String
	}
	if metadata.Valid {
		a.Metadata = &metadata.String
	}
	a.HasContent = hasContent
	return &a, nil
}

func (r *SQLiteRepository) GetArtifact(id string) (*domain.Artifact, error) {
	row := r.db.QueryRow(`SELECT `+artifactSelectCols+` FROM artifacts WHERE id = ?`, id)
	a, err := scanArtifact(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting artifact %s: %w", id, err)
	}
	return a, nil
}

func (r *SQLiteRepository) ListArtifactsByTicket(ticketID string) ([]domain.Artifact, error) {
	rows, err := r.db.Query(`SELECT `+artifactSummaryCols+` FROM artifacts WHERE ticket_id = ? ORDER BY created_at ASC`, ticketID)
	if err != nil {
		return nil, fmt.Errorf("listing artifacts for ticket %s: %w", ticketID, err)
	}
	defer rows.Close()
	out := []domain.Artifact{}
	for rows.Next() {
		a, err := scanArtifactSummary(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

func (r *SQLiteRepository) ListArtifactsByNode(nodeID string) ([]domain.Artifact, error) {
	rows, err := r.db.Query(`SELECT `+artifactSelectCols+` FROM artifacts WHERE node_id = ? ORDER BY created_at ASC`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("listing artifacts for node %s: %w", nodeID, err)
	}
	defer rows.Close()
	out := []domain.Artifact{}
	for rows.Next() {
		a, err := scanArtifact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// --- Projects ---

const projectSelectCols = `id, name, prefix, work_dir, created_at, updated_at`

func scanProject(row interface {
	Scan(dest ...any) error
}) (*domain.Project, error) {
	var p domain.Project
	if err := row.Scan(&p.ID, &p.Name, &p.Prefix, &p.WorkDir, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	return &p, nil
}

// CreateProject resolves prefix inside the same transaction that reads
// every existing prefix and inserts the new row, so two concurrent
// CreateProject calls can never both resolve to (and insert) the same
// auto-generated prefix -- the same atomicity concern CreateTicket/CreateNode
// address for their own counters (see SQLiteRepository's doc comment).
func (r *SQLiteRepository) CreateProject(name, prefix, workDir string) (domain.Project, error) {
	if name == "" {
		return domain.Project{}, domain.NewAPIError(domain.ErrCodeValidation, "project name is required")
	}
	if workDir == "" || !filepath.IsAbs(workDir) {
		return domain.Project{}, domain.NewAPIError(domain.ErrCodeValidation, "work_dir must be an absolute path")
	}

	tx, err := r.db.Begin()
	if err != nil {
		return domain.Project{}, err
	}
	defer tx.Rollback() //nolint:errcheck

	rows, err := tx.Query(`SELECT prefix FROM projects`)
	if err != nil {
		return domain.Project{}, fmt.Errorf("listing existing prefixes: %w", err)
	}
	var existing []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return domain.Project{}, err
		}
		existing = append(existing, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return domain.Project{}, err
	}
	rows.Close()

	resolvedPrefix, err := project.ResolvePrefix(name, prefix, existing)
	if err != nil {
		return domain.Project{}, err
	}

	id := "proj-" + shortUUID()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.Exec(
		`INSERT INTO projects (id, name, prefix, work_dir, ticket_seq, created_at, updated_at) VALUES (?, ?, ?, ?, 0, ?, ?)`,
		id, name, resolvedPrefix, workDir, now, now,
	); err != nil {
		return domain.Project{}, fmt.Errorf("inserting project: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return domain.Project{}, err
	}

	got, err := r.GetProject(id)
	if err != nil {
		return domain.Project{}, err
	}
	return *got, nil
}

func (r *SQLiteRepository) GetProject(id string) (*domain.Project, error) {
	row := r.db.QueryRow(`SELECT `+projectSelectCols+` FROM projects WHERE id = ?`, id)
	p, err := scanProject(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting project %s: %w", id, err)
	}
	return p, nil
}

func (r *SQLiteRepository) ListProjects() ([]domain.Project, error) {
	rows, err := r.db.Query(`SELECT ` + projectSelectCols + ` FROM projects ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("listing projects: %w", err)
	}
	defer rows.Close()
	out := []domain.Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func (r *SQLiteRepository) UpdateProject(id string, patch ProjectPatch) (domain.Project, error) {
	cur, err := r.GetProject(id)
	if err != nil {
		return domain.Project{}, err
	}
	if cur == nil {
		return domain.Project{}, domain.NewAPIError(domain.ErrCodeProjectNotFound, "project %s not found", id)
	}
	if patch.Name != nil {
		cur.Name = *patch.Name
	}
	if patch.WorkDir != nil {
		if *patch.WorkDir == "" || !filepath.IsAbs(*patch.WorkDir) {
			return domain.Project{}, domain.NewAPIError(domain.ErrCodeValidation, "work_dir must be an absolute path")
		}
		cur.WorkDir = *patch.WorkDir
	}
	cur.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)

	_, err = r.db.Exec(`UPDATE projects SET name=?, work_dir=?, updated_at=? WHERE id=?`, cur.Name, cur.WorkDir, cur.UpdatedAt, cur.ID)
	if err != nil {
		return domain.Project{}, fmt.Errorf("updating project %s: %w", id, err)
	}
	return *cur, nil
}

// DeleteProject removes projectID and every ticket under it. Deleting
// tickets.project_id doesn't cascade on its own (unlike
// nodes/edges/artifacts under a ticket), so the tickets are deleted
// explicitly first, inside the same transaction as the project row itself --
// an accidental partial delete (tickets gone, project row still present, or
// vice versa) would be worse than either succeeding or failing outright.
// app_state.current_project_id also has no cascade, so it's cleared first if
// it pointed at this project.
func (r *SQLiteRepository) DeleteProject(id string) error {
	cur, err := r.GetProject(id)
	if err != nil {
		return err
	}
	if cur == nil {
		return nil
	}

	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.Exec(`UPDATE app_state SET current_project_id = NULL WHERE current_project_id = ?`, id); err != nil {
		return fmt.Errorf("clearing current project pointer for %s: %w", id, err)
	}
	if _, err := tx.Exec(`DELETE FROM tickets WHERE project_id = ?`, id); err != nil {
		return fmt.Errorf("deleting tickets under project %s: %w", id, err)
	}
	if _, err := tx.Exec(`DELETE FROM projects WHERE id = ?`, id); err != nil {
		return fmt.Errorf("deleting project %s: %w", id, err)
	}

	return tx.Commit()
}

func (r *SQLiteRepository) GetCurrentProjectID() (string, error) {
	var id sql.NullString
	row := r.db.QueryRow(`SELECT current_project_id FROM app_state WHERE id = 1`)
	err := row.Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("getting current project: %w", err)
	}
	if !id.Valid {
		return "", nil
	}
	return id.String, nil
}

func (r *SQLiteRepository) SetCurrentProjectID(projectID string) error {
	_, err := r.db.Exec(
		`INSERT INTO app_state (id, current_project_id) VALUES (1, ?)
		 ON CONFLICT(id) DO UPDATE SET current_project_id = excluded.current_project_id`,
		projectID,
	)
	if err != nil {
		return fmt.Errorf("setting current project: %w", err)
	}
	return nil
}
