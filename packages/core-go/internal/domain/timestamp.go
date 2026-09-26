package domain

import (
	"strings"
	"time"
)

// Timestamps are stored and returned as time.RFC3339Nano strings, which drop
// trailing zeros from the fractional seconds. Two such strings therefore do
// not compare as their instants do once their precision differs: the later
// "…43.10910002Z" sorts before the earlier "…43.1091Z" as a string ('0' <
// 'Z'). Anything that orders timestamps -- or picks the newest one -- must
// compare them as times, through TimestampKey or CompareTimestamps.

// TimestampKey is a timestamp string parsed once for repeated comparison
// (sorting a listing compares each element many times).
type TimestampKey struct {
	raw string
	t   time.Time
	ok  bool
}

// NewTimestampKey parses s as time.RFC3339Nano, which also accepts values
// without fractional seconds, any number of fractional digits and offsets
// other than Z. A value that does not parse is kept and still comparable;
// see Compare.
func NewTimestampKey(s string) TimestampKey {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return TimestampKey{raw: s}
	}
	return TimestampKey{raw: s, t: t, ok: true}
}

// Compare returns a negative number when k is earlier than o, 0 when they
// are the same instant, and a positive number when k is later. A value that
// did not parse sorts before every value that did, and two such values
// compare as strings, so the result is a total order and safe to sort by.
// Two parsed values at the same instant compare equal even when spelled
// differently ("…43.1Z" and "…43.100Z", or the same instant at two
// offsets); callers settle those ties themselves (by ID, or by keeping the
// incoming order with a stable sort).
func (k TimestampKey) Compare(o TimestampKey) int {
	switch {
	case k.ok && o.ok:
		return k.t.Compare(o.t)
	case k.ok:
		return 1
	case o.ok:
		return -1
	default:
		return strings.Compare(k.raw, o.raw)
	}
}

// CompareTimestamps compares two timestamp strings as times; see
// TimestampKey.Compare for the result and the handling of unparseable
// values.
func CompareTimestamps(a, b string) int {
	return NewTimestampKey(a).Compare(NewTimestampKey(b))
}
