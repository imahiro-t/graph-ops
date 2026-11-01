package autopilot

import (
	"testing"
	"time"
)

func TestRunProgress(t *testing.T) {
	t0 := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	at := func(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }
	run := func(states ...*TicketState) *Run {
		r := &Run{}
		for _, st := range states {
			r.addTicket(st)
		}
		return r
	}
	cases := []struct {
		name string
		run  *Run
		want Progress
	}{
		{"nil run", nil, Progress{}},
		{"just started", run(&TicketState{ID: "T-1", Status: TicketQueued}), Progress{Remaining: 1}},
		{
			"a worker running, nothing finished",
			run(&TicketState{ID: "T-1", Status: TicketLaunched, Role: RoleWork, LaunchedGeneration: 1, AwaitingHuman: "gate"}),
			Progress{InProgress: 1, Current: "T-1", CurrentAwaitingHuman: "gate"},
		},
		{
			"a tree part way through",
			run(
				&TicketState{ID: "T-1", Status: TicketDone, LastRole: RoleWork, LaunchedGeneration: 1, LastActivityAt: at(10)},
				&TicketState{ID: "T-2", Status: TicketBlocked, LastRole: RoleWork, LaunchedGeneration: 1, LastActivityAt: at(30)},
				&TicketState{ID: "T-3", Status: TicketFailed, LastRole: RoleWork, LaunchedGeneration: 1, LastActivityAt: at(20)},
				&TicketState{ID: "T-4", Status: TicketLaunched, Role: RoleWork, LaunchedGeneration: 1, LastActivityAt: at(40)},
				&TicketState{ID: "T-5", Status: TicketQueued},
				&TicketState{ID: "T-6", Status: TicketSkipped, Reason: ReasonParentFailed},
			),
			Progress{Done: 1, Blocked: 1, Failed: 1, InProgress: 1, Remaining: 1, Skipped: 1, Current: "T-4", Last: "T-2", LastOutcome: TicketBlocked},
		},
		{
			"a failed ticket to retry counts as remaining",
			run(&TicketState{ID: "T-1", Status: TicketFailed, RetryPending: true, LastRole: RoleWork, LaunchedGeneration: 1, LastActivityAt: at(5)}),
			Progress{Remaining: 1, Last: "T-1", LastOutcome: TicketFailed},
		},
		{
			"a ticket done without being launched is not the last result",
			run(&TicketState{ID: "T-1", Status: TicketDone, Reason: ReasonAlreadyDone}),
			Progress{Done: 1},
		},
		{
			"a tie goes to the later ticket in the order; launch time stands in for activity",
			run(
				&TicketState{ID: "T-1", Status: TicketDone, LastRole: RoleWork, LaunchedAt: at(10)},
				&TicketState{ID: "T-2", Status: TicketDone, LastRole: RoleWork, LaunchedAt: at(10)},
			),
			Progress{Done: 2, Last: "T-2", LastOutcome: TicketDone},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := RunProgress(c.run); got != c.want {
				t.Fatalf("RunProgress = %+v, want %+v", got, c.want)
			}
		})
	}
}
