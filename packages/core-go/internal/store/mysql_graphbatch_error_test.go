package store

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	mysqldriver "github.com/go-sql-driver/mysql"

	"github.com/graph-ops/core-go/internal/domain"
)

// DFLT-00346: a MySQL server that writes binary logs with
// binlog_format=STATEMENT refuses the graph batch's READ COMMITTED INSERTs
// with Error 1665. MySQLRepository.CreateGraphBatch adds the cause and the
// fix to that error (explainMySQLGraphBatchError) and leaves every other
// error alone. Checked without a real server, like the 1062 mapping.

const mysql1665Message = "Cannot execute statement: impossible to write to binary log since BINLOG_FORMAT = STATEMENT and at least one table uses a storage engine limited to row-based logging. InnoDB is limited to row-logging when transaction isolation level is READ COMMITTED or READ UNCOMMITTED."

func TestExplainMySQLGraphBatchError_Explains1665(t *testing.T) {
	cases := map[string]func() (error, *mysqldriver.MySQLError){
		"bare": func() (error, *mysqldriver.MySQLError) {
			e := &mysqldriver.MySQLError{Number: 1665, Message: mysql1665Message}
			return e, e
		},
		"wrapped by createGraphBatchSQL": func() (error, *mysqldriver.MySQLError) {
			e := &mysqldriver.MySQLError{Number: 1665, Message: mysql1665Message}
			return fmt.Errorf("inserting node DFLT-00001-01: %w", e), e
		},
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			in, orig := mk()
			got := explainMySQLGraphBatchError(in)
			if got == nil {
				t.Fatal("got nil, want an error")
			}
			msg := got.Error()
			if !strings.Contains(msg, in.Error()) {
				t.Errorf("message %q lost the original error %q", msg, in.Error())
			}
			// The original MySQL text already says STATEMENT/READ
			// COMMITTED; look only at what was added.
			added := strings.TrimPrefix(msg, in.Error())
			for _, want := range []string{"binlog_format", "STATEMENT", "READ COMMITTED", "ROW", "MIXED"} {
				if !strings.Contains(added, want) {
					t.Errorf("added explanation %q does not mention %q", added, want)
				}
			}
			var myErr *mysqldriver.MySQLError
			if !errors.As(got, &myErr) || myErr != orig || myErr.Number != 1665 {
				t.Errorf("errors.As did not recover the original MySQLError 1665 from %v", got)
			}
		})
	}
}

func TestExplainMySQLGraphBatchError_LeavesOtherErrorsAlone(t *testing.T) {
	cases := map[string]error{
		"nil":             nil,
		"1062 dup entry":  &mysqldriver.MySQLError{Number: 1062, Message: "Duplicate entry"},
		"1213 deadlock":   &mysqldriver.MySQLError{Number: 1213, Message: "Deadlock found when trying to get lock"},
		"wrapped 1213":    fmt.Errorf("inserting edge: %w", &mysqldriver.MySQLError{Number: 1213, Message: "Deadlock"}),
		"ErrGraphChanged": ErrGraphChanged,
		"APIError":        domain.NewAPIError(domain.ErrCodeLabelNameTaken, "taken"),
		"plain error":     errors.New("boom"),
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			got := explainMySQLGraphBatchError(in)
			if got != in {
				t.Errorf("got %v, want the same value %v", got, in)
			}
		})
	}
	if !errors.Is(explainMySQLGraphBatchError(ErrGraphChanged), ErrGraphChanged) {
		t.Error("errors.Is(..., ErrGraphChanged) no longer holds")
	}
}
