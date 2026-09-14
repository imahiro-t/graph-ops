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
