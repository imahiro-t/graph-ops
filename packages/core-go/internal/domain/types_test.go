package domain

import "testing"

// TestIsFileBackedArtifactType pins the answer for every ArtifactType the
// package defines. The predicate is now the single definition shared by
// internal/httpserver (which rejects file_path and a client-supplied
// metadata.mime_type for the types it returns false for) and
// internal/artifactcontent (which only trusts a recorded mime_type for the
// types it returns true for) -- see DFLT-00023 D-5. Widening it therefore
// quietly widens what those consumers will accept from a client, so the
// exact membership is worth stating once here rather than leaving it to
// each consumer's own tests.
func TestIsFileBackedArtifactType(t *testing.T) {
	cases := map[ArtifactType]bool{
		ArtifactHTML:    true,
		ArtifactImage:   true,
		ArtifactText:    false,
		ArtifactGherkin: false,
		ArtifactJSON:    false,
		// An unrecognized type must not be treated as file-backed: the
		// consumers' checks are written as "not file-backed => stricter",
		// so defaulting the other way would relax them.
		ArtifactType("made_up"): false,
		ArtifactType(""):        false,
	}
	for artType, want := range cases {
		if got := IsFileBackedArtifactType(artType); got != want {
			t.Errorf("IsFileBackedArtifactType(%q) = %v, want %v", artType, got, want)
		}
	}
}

// TestAllNodeStatusesMatchesParseNodeStatus pins the closed NodeStatus enum
// in one place (DFLT-00136). GetExecutableNodes' claim compensation builds
// its CAS exclusion as "every status except the claimed one" from
// AllNodeStatuses, so a status ParseNodeStatus accepts but the list omits
// would silently fall outside that exclusion.
func TestAllNodeStatusesMatchesParseNodeStatus(t *testing.T) {
	want := []NodeStatus{NodeTODO, NodeInProgress, NodeInReview, NodeDone, NodeRejected, NodeAwaitingFix}
	got := AllNodeStatuses()
	if len(got) != len(want) {
		t.Fatalf("AllNodeStatuses() = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("AllNodeStatuses() = %q, want %q", got, want)
		}
		if parsed, err := ParseNodeStatus(string(want[i])); err != nil || parsed != want[i] {
			t.Errorf("ParseNodeStatus(%q) = %q, %v", want[i], parsed, err)
		}
	}
	// The caller gets its own copy.
	got[0] = "MUTATED"
	if AllNodeStatuses()[0] != NodeTODO {
		t.Error("AllNodeStatuses() returned a slice aliasing the package's list")
	}
	// The error wording predates the list and is kept verbatim.
	_, err := ParseNodeStatus("BOGUS")
	const wantErr = `invalid node status "BOGUS": must be one of "TODO", "IN PROGRESS", "IN REVIEW", "DONE", "REJECTED", "AWAITING FIX"`
	if err == nil || err.Error() != wantErr {
		t.Errorf("ParseNodeStatus(\"BOGUS\") error = %v, want %q", err, wantErr)
	}
}
