package store

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"

	"github.com/graph-ops/core-go/internal/domain"
)

// captureMySQLDeadlockLog points mysqlDeadlockLogger at a JSON buffer for
// the rest of the test and returns a function that decodes what was
// logged so far, one map per record (DFLT-00347).
func captureMySQLDeadlockLog(t *testing.T) func() []map[string]any {
	t.Helper()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	saved := mysqlDeadlockLogger
	mysqlDeadlockLogger = func() *slog.Logger { return logger }
	t.Cleanup(func() { mysqlDeadlockLogger = saved })
	return func() []map[string]any {
		t.Helper()
		var records []map[string]any
		sc := bufio.NewScanner(bytes.NewReader(buf.Bytes()))
		for sc.Scan() {
			var rec map[string]any
			if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
				t.Fatalf("log line %q is not JSON: %v", sc.Text(), err)
			}
			records = append(records, rec)
		}
		return records
	}
}

// oneMySQLDeadlockRecord asserts exactly one record was logged and that it
// carries want (JSON-decoded values: numbers are float64).
func oneMySQLDeadlockRecord(t *testing.T, records []map[string]any, want map[string]any) map[string]any {
	t.Helper()
	if len(records) != 1 {
		t.Fatalf("logged %d records, want 1: %v", len(records), records)
	}
	rec := records[0]
	for k, v := range want {
		if rec[k] != v {
			t.Errorf("record[%q] = %v, want %v (record %v)", k, rec[k], v, rec)
		}
	}
	return rec
}

// retryMySQLDeadlock is exercised here without a server (DFLT-00329): the
// run function stands in for a transaction that InnoDB picks as a deadlock
// victim a given number of times. The MySQL race tests show the retries
// are rarely needed; these show what happens when they are, and what is
// logged about it (DFLT-00347).
func TestRetryMySQLDeadlock(t *testing.T) {
	saved := mysqlDeadlockBackoff
	mysqlDeadlockBackoff = func() time.Duration { return 0 }
	t.Cleanup(func() { mysqlDeadlockBackoff = saved })

	deadlock := &mysqldriver.MySQLError{Number: mysqlErDeadlock, Message: "Deadlock found when trying to get lock; try restarting transaction"}
	failing := func(deadlocks int, then error) (func() error, *int) {
		calls := 0
		return func() error {
			calls++
			if calls <= deadlocks {
				return fmt.Errorf("committing: %w", deadlock)
			}
			return then
		}, &calls
	}
	ticket := slog.String("ticket_id", "T-1")

	t.Run("one deadlock is retried away", func(t *testing.T) {
		logged := captureMySQLDeadlockLog(t)
		run, calls := failing(1, nil)
		if err := retryMySQLDeadlock("node_transition", "x", ticket, run); err != nil {
			t.Fatalf("err = %v, want success on the retry", err)
		}
		if *calls != 2 {
			t.Fatalf("ran %d times, want 2", *calls)
		}
		oneMySQLDeadlockRecord(t, logged(), map[string]any{
			"level":        "INFO",
			"msg":          "MySQL deadlock resolved by retrying",
			"event":        "mysql_deadlock_retry_succeeded",
			"op":           "node_transition",
			"ticket_id":    "T-1",
			"attempts":     float64(2),
			"max_attempts": float64(mysqlDeadlockAttempts),
		})
	})
	t.Run("success on the last attempt logs how many it took", func(t *testing.T) {
		logged := captureMySQLDeadlockLog(t)
		run, calls := failing(mysqlDeadlockAttempts-1, nil)
		if err := retryMySQLDeadlock("create_artifact", "adding artifact art-1", slog.String("artifact_id", "art-1"), run); err != nil {
			t.Fatalf("err = %v, want success on the last attempt", err)
		}
		if *calls != mysqlDeadlockAttempts {
			t.Fatalf("ran %d times, want %d", *calls, mysqlDeadlockAttempts)
		}
		rec := oneMySQLDeadlockRecord(t, logged(), map[string]any{
			"level":        "INFO",
			"event":        "mysql_deadlock_retry_succeeded",
			"op":           "create_artifact",
			"artifact_id":  "art-1",
			"attempts":     float64(mysqlDeadlockAttempts),
			"max_attempts": float64(mysqlDeadlockAttempts),
		})
		if _, ok := rec["error"]; ok {
			t.Errorf("a success record carries an error: %v", rec)
		}
	})
	t.Run("a deadlock on every attempt is CONCURRENT_WRITE_CONFLICT", func(t *testing.T) {
		logged := captureMySQLDeadlockLog(t)
		run, calls := failing(mysqlDeadlockAttempts, nil)
		err := retryMySQLDeadlock("node_transition", "the node transition of ticket T-1", ticket, run)
		var apiErr *domain.APIError
		if !errors.As(err, &apiErr) || apiErr.Code != domain.ErrCodeConcurrentWriteConflict {
			t.Fatalf("err = %v, want CONCURRENT_WRITE_CONFLICT", err)
		}
		wantMsg := "the node transition of ticket T-1 kept colliding with other writes to the same ticket (MySQL deadlock, 3 attempts); nothing was written, so the same call can simply be made again: committing: " + deadlock.Error()
		if apiErr.Message != wantMsg {
			t.Errorf("message = %q, want %q", apiErr.Message, wantMsg)
		}
		var myErr *mysqldriver.MySQLError
		if errors.As(err, &myErr) {
			t.Fatal("the bare driver error is still reachable through errors.As")
		}
		if *calls != mysqlDeadlockAttempts {
			t.Fatalf("ran %d times, want %d", *calls, mysqlDeadlockAttempts)
		}
		oneMySQLDeadlockRecord(t, logged(), map[string]any{
			"level":     "WARN",
			"msg":       "MySQL deadlock retries exhausted",
			"event":     "mysql_deadlock_retries_exhausted",
			"op":        "node_transition",
			"ticket_id": "T-1",
			"attempts":  float64(mysqlDeadlockAttempts),
			"error":     "committing: " + deadlock.Error(),
		})
	})
	t.Run("success on the first attempt logs nothing", func(t *testing.T) {
		logged := captureMySQLDeadlockLog(t)
		run, calls := failing(0, nil)
		if err := retryMySQLDeadlock("node_transition", "x", ticket, run); err != nil || *calls != 1 {
			t.Fatalf("err = %v after %d runs, want success after 1", err, *calls)
		}
		if records := logged(); len(records) != 0 {
			t.Fatalf("logged %v, want nothing", records)
		}
	})
	t.Run("any other error is returned at once", func(t *testing.T) {
		logged := captureMySQLDeadlockLog(t)
		other := &mysqldriver.MySQLError{Number: 1062, Message: "Duplicate entry"}
		run, calls := failing(0, other)
		if err := retryMySQLDeadlock("create_artifact", "x", slog.String("artifact_id", "art-1"), run); !errors.Is(err, other) {
			t.Fatalf("err = %v, want the original error", err)
		}
		if *calls != 1 {
			t.Fatalf("ran %d times, want 1", *calls)
		}
		if records := logged(); len(records) != 0 {
			t.Fatalf("logged %v, want nothing", records)
		}
	})
	t.Run("a conflict is not retried", func(t *testing.T) {
		logged := captureMySQLDeadlockLog(t)
		conflict := &NodeTransitionConflictError{Reason: ConflictStatus}
		run, calls := failing(0, conflict)
		if err := retryMySQLDeadlock("node_transition", "x", ticket, run); !errors.Is(err, ErrNodeTransitionConflict) || *calls != 1 {
			t.Fatalf("err = %v after %d runs, want the conflict after 1", err, *calls)
		}
		if records := logged(); len(records) != 0 {
			t.Fatalf("logged %v, want nothing", records)
		}
	})
	t.Run("a conflict after a deadlock is returned and not logged", func(t *testing.T) {
		logged := captureMySQLDeadlockLog(t)
		conflict := &NodeTransitionConflictError{Reason: ConflictStatus}
		run, calls := failing(1, conflict)
		if err := retryMySQLDeadlock("node_transition", "x", ticket, run); !errors.Is(err, ErrNodeTransitionConflict) || *calls != 2 {
			t.Fatalf("err = %v after %d runs, want the conflict after 2", err, *calls)
		}
		if records := logged(); len(records) != 0 {
			t.Fatalf("logged %v, want nothing", records)
		}
	})
}
