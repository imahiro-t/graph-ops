package store

import (
	"errors"
	"fmt"
	"testing"

	mysqldriver "github.com/go-sql-driver/mysql"

	"github.com/graph-ops/core-go/internal/domain"
)

// DFLT-00084 on MySQL. The contract tests need a real server and skip
// without GRAPH_TEST_MYSQL_HOST (see mysqlTestConfig); the 1062 mapping is
// checked without one.

func TestMySQLRepository_LabelsCRUD(t *testing.T) {
	repo := newTestMySQLRepo(t)
	runLabelCRUDContract(t, repo, repo.db)
}

func TestMySQLRepository_TicketLabels(t *testing.T) {
	repo := newTestMySQLRepo(t)
	runTicketLabelContract(t, repo, repo.db)
}

// TestMySQLRepository_LabelNameCollation: labels.name is utf8mb4_bin (so the
// UNIQUE key only rejects byte-identical names; see mysqlSchemaStatements)
// while the table default -- and so its FK columns -- stays
// utf8mb4_general_ci like every other table.
func TestMySQLRepository_LabelNameCollation(t *testing.T) {
	repo := newTestMySQLRepo(t)
	collation := func(column string) string {
		t.Helper()
		var c string
		if err := repo.db.QueryRow(
			`SELECT COLLATION_NAME FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'labels' AND COLUMN_NAME = ?`,
			column,
		).Scan(&c); err != nil {
			t.Fatalf("reading collation of labels.%s: %v", column, err)
		}
		return c
	}
	if c := collation("name"); c != "utf8mb4_bin" {
		t.Errorf("labels.name collation = %q, want utf8mb4_bin", c)
	}
	if c := collation("project_id"); c != "utf8mb4_general_ci" {
		t.Errorf("labels.project_id collation = %q, want utf8mb4_general_ci", c)
	}
}

func TestIsMySQLDuplicateKeyError(t *testing.T) {
	dup := &mysqldriver.MySQLError{Number: 1062, Message: "Duplicate entry 'x' for key 'idx_labels_project_name'"}
	if !isMySQLDuplicateKeyError(dup) {
		t.Error("error 1062 must be detected")
	}
	if !isMySQLDuplicateKeyError(fmt.Errorf("wrapped: %w", dup)) {
		t.Error("a wrapped error 1062 must be detected")
	}
	for _, other := range []error{
		&mysqldriver.MySQLError{Number: 1452, Message: "Cannot add or update a child row"},
		&mysqldriver.MySQLError{Number: 1213, Message: "Deadlock found"},
		errors.New("Duplicate entry"),
		nil,
	} {
		if isMySQLDuplicateKeyError(other) {
			t.Errorf("%v must not be treated as a duplicate key error", other)
		}
	}
}

// TestLabelNameTakenOr_MapsOnlyUniqueViolations: the store turns the
// dialect's UNIQUE violation into LABEL_NAME_TAKEN and wraps anything else.
func TestLabelNameTakenOr_MapsOnlyUniqueViolations(t *testing.T) {
	dup := &mysqldriver.MySQLError{Number: 1062, Message: "Duplicate entry"}
	err := labelNameTakenOr(mysqlDialect, dup, "proj-1", "Bug", "inserting label: %w")
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != domain.ErrCodeLabelNameTaken {
		t.Errorf("1062 must become LABEL_NAME_TAKEN, got %v", err)
	}

	other := &mysqldriver.MySQLError{Number: 1452, Message: "foreign key"}
	err = labelNameTakenOr(mysqlDialect, other, "proj-1", "Bug", "inserting label: %w")
	if errors.As(err, &apiErr) {
		t.Errorf("a non-1062 error must not become an APIError, got %v", err)
	}
	if !errors.Is(err, other) {
		t.Errorf("a non-1062 error must be wrapped, got %v", err)
	}
}
