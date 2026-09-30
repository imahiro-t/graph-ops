package displayname

import (
	"time"
)

// This file covers the other strings from the shared data source that
// graph-engine prints next to a member's name (DFLT-00327): session and run
// IDs and timestamps. They are machine-made -- a UUID, a run ID, an RFC3339
// time -- so rather than cleaning them up like a name, anything that does
// not have their shape is replaced as a whole: a member who can write to the
// data source must not be able to put line breaks or escape sequences on
// somebody else's terminal through them either.

// InvalidID replaces an ID that does not look like one.
const InvalidID = "(invalid id)"

// UnknownTime replaces a timestamp that does not parse.
const UnknownTime = "(unknown time)"

// maxIDLen bounds a displayable ID. Session IDs are 36 characters and run
// IDs at most 68; the columns that hold them are VARCHAR(64) / VARCHAR(191).
const maxIDLen = 191

// ID returns id when it is 1-191 characters of ASCII letters, digits and
// ". _ : -" -- every ID graph-engine mints has that shape -- and InvalidID
// otherwise.
func ID(id string) string {
	if id == "" || len(id) > maxIDLen {
		return InvalidID
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '_', c == ':', c == '-':
		default:
			return InvalidID
		}
	}
	return id
}

// OptionalID is ID for a field that may legitimately be empty (DFLT-00339):
// "" stays "", anything else goes through ID. It keeps an empty column --
// a run's project ID, the ticket of a session that is not there -- from
// being shown as InvalidID.
func OptionalID(id string) string {
	if id == "" {
		return ""
	}
	return ID(id)
}

// Timestamp returns ts when it parses as an RFC3339 time (which admits no
// control characters) and is of a sane length, and UnknownTime otherwise.
func Timestamp(ts string) string {
	if len(ts) > 64 {
		return UnknownTime
	}
	if _, err := time.Parse(time.RFC3339Nano, ts); err != nil {
		return UnknownTime
	}
	return ts
}

// MemberLabel is how graph-engine's messages name a member: the name,
// sanitized (Sanitize), with " (name not set)" when it is the
// "<OS user>@<host>" stand-in, and "an unnamed member" when there is no
// name left. Every Go message that names a claimer or a session's owner
// goes through it, so the wording lives in one place (the Web UI has its
// own, memberLabel in packages/web).
func MemberLabel(name string, isFallback bool) string {
	name = Sanitize(name)
	switch {
	case name == "":
		return "an unnamed member"
	case isFallback:
		return name + " (name not set)"
	}
	return name
}
