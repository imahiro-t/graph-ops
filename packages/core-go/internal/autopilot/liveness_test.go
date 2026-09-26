package autopilot

import (
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
)

// DFLT-00189: DBFingerprint picks the newest artifact by time, not by string
// ("…43.10910002Z" is later than "…43.1091Z" but smaller as a string).
func TestDBFingerprint_NewestArtifactComparedAsTime(t *testing.T) {
	detail := func(artifactTimes ...string) *domain.TicketDetail {
		d := &domain.TicketDetail{Ticket: domain.Ticket{ID: "T-00001", UpdatedAt: "2026-09-26T10:00:00Z", Status: domain.TicketInProgress}}
		for _, ts := range artifactTimes {
			d.Artifacts = append(d.Artifacts, domain.Artifact{CreatedAt: ts})
		}
		return d
	}
	const earlier, later = "2026-09-26T10:00:43.1091Z", "2026-09-26T10:00:43.10910002Z"
	got := DBFingerprint(detail(earlier, later))
	if swapped := DBFingerprint(detail(later, earlier)); swapped != got {
		t.Fatalf("fingerprint depends on artifact order: %s vs %s", got, swapped)
	}
	// The fingerprint of the same two artifacts when the newest is the
	// later one: replacing the earlier with an even earlier value must not
	// change it, while replacing the later one must.
	if same := DBFingerprint(detail("2026-09-26T10:00:00Z", later)); same != got {
		t.Errorf("fingerprint %s changed when only the older artifact's time moved (%s): the newest was not %s", same, got, later)
	}
	if moved := DBFingerprint(detail(earlier, "2026-09-26T10:00:43.10910003Z")); moved == got {
		t.Errorf("fingerprint did not change when the newest artifact's time moved")
	}
}
