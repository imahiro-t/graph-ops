package store

import "time"

// nextUpdatedAt is the updated_at a ticket write stores (DFLT-00330): now,
// in UTC with RFC3339Nano precision -- unless that would not be later than
// prev, the value the row holds right now, in which case prev plus one
// nanosecond. TicketPatch.IfUpdatedAt compares updated_at as an exact
// string, so every write must leave a string the row has never held before;
// a clock that did not move (or moved back) between two writes must not
// hand out the same value twice.
//
// The comparison is on the parsed times, never on the strings: RFC3339Nano
// drops trailing zeros, so "…:01Z" sorts after "…:01.5Z" as text although it
// is earlier. A prev that does not parse (a row written by something else)
// cannot be compared, and now is used as it is.
func nextUpdatedAt(prev string, now time.Time) string {
	now = now.UTC()
	if p, err := time.Parse(time.RFC3339Nano, prev); err == nil {
		p = p.UTC()
		if !now.After(p) {
			now = p.Add(time.Nanosecond)
		}
	}
	return now.Format(time.RFC3339Nano)
}
