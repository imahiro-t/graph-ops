// Package project implements the project-prefix resolution rules used when
// creating a Project: validating an explicitly-supplied prefix, or deriving
// and de-duplicating one from the project name when the caller omits it.
// It is deliberately pure/DB-free (see ResolvePrefix's existingPrefixes
// parameter) so the uniqueness check and insert can be wrapped in a single
// transaction by the caller (store.SQLiteRepository.CreateProject) without
// this package needing to know anything about SQL.
package project

import (
	"fmt"
	"math/rand"
	"regexp"
	"strings"

	"github.com/graph-ops/core-go/internal/domain"
)

// MaxPrefixLen is the maximum length (in ASCII alphanumeric characters) of a
// project prefix, per the ticket's completion criteria.
const MaxPrefixLen = 5

// fallbackBase is used when a project name contains no ASCII alphanumeric
// characters at all (e.g. a purely Japanese name) to derive a prefix from.
const fallbackBase = "PRJ"

// validPrefixRe is built from MaxPrefixLen rather than repeating the bound
// as a literal (DFLT-00023 C-7): validation (this regexp) and generation
// (derivePrefix's truncation below) enforce the same rule, and a hand-edited
// MaxPrefixLen used to leave the two disagreeing -- explicit prefixes would
// still be accepted at the old length while derived ones were cut to the new
// one. Package-level var initialization runs after constants are known, so
// there is no ordering hazard here.
var validPrefixRe = regexp.MustCompile(fmt.Sprintf(`^[A-Za-z0-9]{1,%d}$`, MaxPrefixLen))

// maxNumericSuffix is one past the largest numeric de-duplication suffix
// that can appear in a prefix. A suffix of MaxPrefixLen digits would leave
// no room for any of the base, so candidates stop just below
// 10^(MaxPrefixLen-1) -- 9999 for MaxPrefixLen == 5.
//
// The loop in derivePrefix used to be bounded by a literal 100000 with an
// in-body length check that always fired first, so the literal was
// unreachable (DFLT-00023 C-6). Deriving the bound removes the dead upper
// limit and keeps it correct if MaxPrefixLen ever changes; the set of
// candidates tried is unchanged.
var maxNumericSuffix = func() int {
	limit := 1
	for i := 1; i < MaxPrefixLen; i++ {
		limit *= 10
	}
	return limit
}()

const randomAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// ResolvePrefix validates an explicitly-supplied prefix, or derives one from
// name when explicit is empty. existingPrefixes lists every prefix already
// in use across all projects; the uniqueness check (and the correction
// scheme below) is case-insensitive, matching the DB's
// idx_projects_prefix_nocase unique index.
//
// Behavior:
//   - explicit != "": validated as-is (kept in the caller's given case) --
//     must be 1-5 ASCII alphanumeric characters and not collide
//     case-insensitively with an existing prefix. Violations are returned as
//     a *domain.APIError (ErrCodeInvalidPrefix / ErrCodePrefixTaken) so HTTP
//     handlers can map them to 400 directly.
//   - explicit == "": a candidate is derived from name (ASCII
//     letters/digits only, extracted and upper-cased, truncated to
//     MaxPrefixLen; "PRJ" if name has none), then corrected against
//     existingPrefixes by appending an increasing numeric suffix, and
//     finally by trying random alphanumeric strings if every numbered
//     candidate is somehow also taken.
func ResolvePrefix(name, explicit string, existingPrefixes []string) (string, error) {
	used := make(map[string]bool, len(existingPrefixes))
	for _, p := range existingPrefixes {
		used[strings.ToUpper(p)] = true
	}

	if explicit != "" {
		if !validPrefixRe.MatchString(explicit) {
			return "", domain.NewAPIError(domain.ErrCodeInvalidPrefix,
				"prefix must be 1-%d alphanumeric characters, got %q", MaxPrefixLen, explicit)
		}
		if used[strings.ToUpper(explicit)] {
			return "", domain.NewAPIError(domain.ErrCodePrefixTaken,
				"prefix %q is already in use", explicit)
		}
		return explicit, nil
	}

	base := extractUpperAlnum(name)
	if base == "" {
		base = fallbackBase
	}
	if len(base) > MaxPrefixLen {
		base = base[:MaxPrefixLen]
	}

	if !used[base] {
		return base, nil
	}

	// Append an increasing numeric suffix, trimming base just enough to
	// keep the whole candidate within MaxPrefixLen (e.g. "MYPRO" + "2" ->
	// "MYPR2").
	for n := 2; n < maxNumericSuffix; n++ {
		suffix := fmt.Sprintf("%d", n)
		keep := MaxPrefixLen - len(suffix)
		head := base
		if len(head) > keep {
			head = head[:keep]
		}
		candidate := head + suffix
		if !used[candidate] {
			return candidate, nil
		}
	}

	// Every numbered candidate was somehow also taken (pathological case,
	// e.g. an adversarial/very large existing prefix set): fall back to
	// random alphanumeric strings.
	for attempt := 0; attempt < 200; attempt++ {
		candidate := randomAlnum(MaxPrefixLen)
		if !used[candidate] {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("could not generate a unique %d-character project prefix", MaxPrefixLen)
}

func extractUpperAlnum(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - 'a' + 'A')
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		}
	}
	return b.String()
}

func randomAlnum(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = randomAlphabet[rand.Intn(len(randomAlphabet))]
	}
	return string(b)
}
