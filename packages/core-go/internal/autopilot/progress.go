package autopilot

import "time"

// Progress is how far a run has got, as the controller's status band shows
// it (DFLT-00383): its tickets counted by state, the ticket whose session is
// running, and the latest ticket that reached a result.
type Progress struct {
	Done       int
	InProgress int
	// Remaining counts the tickets still to be worked: queued ones, and
	// failed or blocked ones the run will launch once more (RetryPending).
	Remaining int
	Blocked   int
	Failed    int
	Skipped   int
	// Current is the ticket whose session is running ("" when none), and
	// CurrentAwaitingHuman what that session waits for a person on.
	Current              string
	CurrentAwaitingHuman string
	// Last is the latest launched ticket that reached done, failed or
	// blocked ("" when none yet), and LastOutcome that state.
	Last        string
	LastOutcome string
}

// RunProgress computes run's Progress. Only the tickets the run has met so
// far are counted: a tree run discovers its children as it goes, so the
// total can grow.
//
// "Latest" is judged by the ticket's last sign of life (LastActivityAt, else
// LaunchedAt); a tie goes to the ticket later in the run's order.
func RunProgress(run *Run) Progress {
	var p Progress
	if run == nil {
		return p
	}
	var lastAt time.Time
	for _, id := range run.Order {
		st := run.Tickets[id]
		if st == nil {
			continue
		}
		switch st.Status {
		case TicketDone:
			p.Done++
		case TicketLaunched:
			p.InProgress++
		case TicketQueued:
			p.Remaining++
		case TicketSkipped:
			p.Skipped++
		case TicketFailed, TicketBlocked:
			switch {
			case st.RetryPending:
				p.Remaining++
			case st.Status == TicketFailed:
				p.Failed++
			default:
				p.Blocked++
			}
		}
		final := st.Status == TicketDone || st.Status == TicketFailed || st.Status == TicketBlocked
		if !final || (st.LastRole == "" && st.LaunchedGeneration == 0) {
			continue
		}
		at := st.LastActivityAt
		if at.IsZero() {
			at = st.LaunchedAt
		}
		if p.Last == "" || !at.Before(lastAt) {
			p.Last, p.LastOutcome, lastAt = st.ID, st.Status, at
		}
	}
	if st := run.ActiveSession(); st != nil {
		p.Current, p.CurrentAwaitingHuman = st.ID, st.AwaitingHuman
	}
	return p
}
