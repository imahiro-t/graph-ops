package store

import (
	"errors"
	"fmt"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"

	"github.com/graph-ops/core-go/internal/domain"
)

// retryMySQLDeadlock is exercised here without a server (DFLT-00329): the
// run function stands in for a transaction that InnoDB picks as a deadlock
// victim a given number of times. The MySQL race tests show the retries
// are rarely needed; these show what happens when they are.
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

	t.Run("one deadlock is retried away", func(t *testing.T) {
		run, calls := failing(1, nil)
		if err := retryMySQLDeadlock("x", run); err != nil {
			t.Fatalf("err = %v, want success on the retry", err)
		}
		if *calls != 2 {
			t.Fatalf("ran %d times, want 2", *calls)
		}
	})
	t.Run("a deadlock on every attempt is CONCURRENT_WRITE_CONFLICT", func(t *testing.T) {
		run, calls := failing(mysqlDeadlockAttempts, nil)
		err := retryMySQLDeadlock("the node transition of ticket T-1", run)
		var apiErr *domain.APIError
		if !errors.As(err, &apiErr) || apiErr.Code != domain.ErrCodeConcurrentWriteConflict {
			t.Fatalf("err = %v, want CONCURRENT_WRITE_CONFLICT", err)
		}
		var myErr *mysqldriver.MySQLError
		if errors.As(err, &myErr) {
			t.Fatal("the bare driver error is still reachable through errors.As")
		}
		if *calls != mysqlDeadlockAttempts {
			t.Fatalf("ran %d times, want %d", *calls, mysqlDeadlockAttempts)
		}
	})
	t.Run("any other error is returned at once", func(t *testing.T) {
		other := &mysqldriver.MySQLError{Number: 1062, Message: "Duplicate entry"}
		run, calls := failing(0, other)
		if err := retryMySQLDeadlock("x", run); !errors.Is(err, other) {
			t.Fatalf("err = %v, want the original error", err)
		}
		if *calls != 1 {
			t.Fatalf("ran %d times, want 1", *calls)
		}
	})
	t.Run("a conflict is not retried", func(t *testing.T) {
		conflict := &NodeTransitionConflictError{Reason: ConflictStatus}
		run, calls := failing(0, conflict)
		if err := retryMySQLDeadlock("x", run); !errors.Is(err, ErrNodeTransitionConflict) || *calls != 1 {
			t.Fatalf("err = %v after %d runs, want the conflict after 1", err, *calls)
		}
	})
}
