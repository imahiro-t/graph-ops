package autopilot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
)

// DFLT-00154: the run keeps its orchestrator's Terminal.app tty, and every
// start that decides who drives the run overwrites it (even with "") and
// clears a disabled tab path.

func beginTTY(t *testing.T, g *Registry, runID, tty string, reserve bool) BeginResult {
	t.Helper()
	res, err := g.Begin(BeginRequest{RootID: "R", ProjectID: "proj-A", RootStatus: domain.TicketTODO, Mode: ModeTree,
		RunID: runID, Reserve: reserve, Settings: Defaults(), TerminalTTY: tty})
	if err != nil {
		t.Fatalf("Begin(run %q, tty %q, reserve %v): %v", runID, tty, reserve, err)
	}
	return res
}

// withTabDisabled records a tty and a disabled tab path on the run, as an
// earlier orchestrator's launches would have, and stops it so a start takes
// it over.
func withTabDisabled(t *testing.T, g *Registry, run *Run, tty string) {
	t.Helper()
	run.TerminalTTY, run.TerminalTabDisabled = tty, "osascript: not authorized (-1743)"
	run.stop(g.now(), StopTicketFailed, "A", "x")
	saveRun(t, g, run)
}

func assertTTY(t *testing.T, run *Run, tty string) {
	t.Helper()
	if run.TerminalTTY != tty || run.TerminalTabDisabled != "" {
		t.Fatalf("tty = %q, disabled = %q; want tty %q and not disabled", run.TerminalTTY, run.TerminalTabDisabled, tty)
	}
}

func TestBegin_NewRunKeepsTheOrchestratorsTTY(t *testing.T) {
	g, _ := newRegistry(t)
	res := beginTTY(t, g, "", "/dev/ttys003", false)
	if !res.Created {
		t.Fatalf("res = %+v", res)
	}
	assertTTY(t, res.Run, "/dev/ttys003")
	loaded, _ := g.Load("proj-A", res.Run.ID)
	assertTTY(t, loaded, "/dev/ttys003")
}

func TestBegin_TakeoverOverwritesTheTTYEvenWithEmpty(t *testing.T) {
	for _, tc := range []struct{ name, tty string }{{"with a new tty", "/dev/ttys009"}, {"with none", ""}} {
		t.Run(tc.name, func(t *testing.T) {
			g, _ := newRegistry(t)
			run := beginTTY(t, g, "", "/dev/ttys003", false).Run
			withTabDisabled(t, g, run, "/dev/ttys003")
			res := beginTTY(t, g, "", tc.tty, false)
			if !res.TookOver || res.Run.ID != run.ID {
				t.Fatalf("res = %+v", res)
			}
			assertTTY(t, res.Run, tc.tty)
		})
	}
}

func TestBegin_TakeoverByRunIDOverwritesTheTTY(t *testing.T) {
	g, _ := newRegistry(t)
	run := beginTTY(t, g, "", "/dev/ttys003", false).Run
	withTabDisabled(t, g, run, "/dev/ttys003")
	res := beginTTY(t, g, run.ID, "", false)
	if !res.TookOver {
		t.Fatalf("res = %+v", res)
	}
	assertTTY(t, res.Run, "")
}

func TestBegin_ReservationClearsTheTTYAndAdoptionSetsIt(t *testing.T) {
	t.Run("new reservation", func(t *testing.T) {
		g, _ := newRegistry(t)
		// Even if a caller passed one, a reservation holds no tty.
		res := beginTTY(t, g, "", "/dev/ttys001", true)
		assertTTY(t, res.Run, "")
		adopted := beginTTY(t, g, res.Run.ID, "/dev/ttys005", false)
		if !adopted.Adopted {
			t.Fatalf("adopted = %+v", adopted)
		}
		assertTTY(t, adopted.Run, "/dev/ttys005")
	})
	t.Run("adopted with none", func(t *testing.T) {
		g, _ := newRegistry(t)
		res := beginTTY(t, g, "", "", true)
		adopted := beginTTY(t, g, res.Run.ID, "", false)
		assertTTY(t, adopted.Run, "")
	})
	t.Run("reservation taking over a run", func(t *testing.T) {
		g, _ := newRegistry(t)
		run := beginTTY(t, g, "", "/dev/ttys003", false).Run
		withTabDisabled(t, g, run, "/dev/ttys003")
		res := beginTTY(t, g, "", "", true)
		if !res.TookOver || res.Run.State != RunStarting {
			t.Fatalf("res = %+v", res)
		}
		// The old orchestrator's tty is not kept while the reservation
		// waits...
		assertTTY(t, res.Run, "")
		// ...and the adopting orchestrator's is what the run uses.
		adopted := beginTTY(t, g, run.ID, "/dev/ttys007", false)
		assertTTY(t, adopted.Run, "/dev/ttys007")
	})
	t.Run("cancelling puts the previous values back", func(t *testing.T) {
		g, _ := newRegistry(t)
		run := beginTTY(t, g, "", "/dev/ttys003", false).Run
		withTabDisabled(t, g, run, "/dev/ttys003")
		beginTTY(t, g, "", "", true)
		if err := g.CancelReservation("proj-A", run.ID); err != nil {
			t.Fatal(err)
		}
		back, _ := g.Load("proj-A", run.ID)
		if back.TerminalTTY != "/dev/ttys003" || back.TerminalTabDisabled == "" {
			t.Fatalf("restored = %+v", back)
		}
	})
}

func TestRun_FileWithoutTerminalFieldsStillLoads(t *testing.T) {
	g, _ := newRegistry(t)
	run := beginTTY(t, g, "", "", false).Run
	path := filepath.Join(g.Root, "proj-A", "runs", run.ID+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["terminal_tty"]; ok {
		t.Fatal("an empty tty should be omitted from the file")
	}
	delete(m, "terminal_tab_disabled")
	raw, _ = json.Marshal(m)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := g.Load("proj-A", run.ID)
	if err != nil || loaded == nil {
		t.Fatalf("Load: %v", err)
	}
	assertTTY(t, loaded, "")
}
