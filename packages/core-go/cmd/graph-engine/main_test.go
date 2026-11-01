package main

import (
	"os"
	"testing"

	"github.com/graph-ops/core-go/internal/sessionstatus"
)

// TestMain keeps the Claude Code session these tests may be run from out of
// them (DFLT-00383): with CLAUDE_CODE_SESSION_ID inherited, a command a test
// runs would write that session's status band. A test that wants a status
// sets the variable itself. A CLI subprocess (runMainIfSubprocess) keeps
// what its test passed it.
func TestMain(m *testing.M) {
	if os.Getenv(cliSubprocessEnv) == "" {
		os.Unsetenv(sessionstatus.EnvSessionID)
	}
	os.Exit(m.Run())
}
