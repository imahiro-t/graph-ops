package runner

import (
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/autopilot"
	"github.com/graph-ops/core-go/internal/domain"
)

// DFLT-00142 phase 3: automatic decisions (D7), the summary artifact, and
// the start decision as the Service applies it to the DB tree.

func TestDecisions_RecordThenAttachOnce(t *testing.T) {
	h := newHarness(t)
	runID, c := launchSilent(t, h)
	if err := h.svc.RecordDecision(runID, c, "refine", "adopted the recommended criteria because ..."); err != nil {
		t.Fatal(err)
	}
	// Interrupted before a node exists: worker-context still shows it.
	ctx, err := h.svc.WorkerContext(runID, c)
	if err != nil || strings.Join(ctx.PendingDecisions, ",") != "refine" {
		t.Fatalf("ctx = %+v, %v", ctx, err)
	}
	node, err := h.repo.CreateNode(domain.GraphNode{TicketID: c, Name: "plan", Type: domain.NodeTypePlan, Status: domain.NodeInProgress, MaxIterations: 3})
	if err != nil {
		t.Fatal(err)
	}
	res, err := h.svc.AttachDecisions(runID, c, node.ID)
	if err != nil || strings.Join(res.Attached, ",") != "autopilot-decision-refine" {
		t.Fatalf("attach = %+v, %v", res, err)
	}
	if s := h.st(runID, c); len(s.PendingDecisions) != 0 {
		t.Fatal("pending not cleared")
	}
	// A repeated record+attach (a resumed worker) does not duplicate it.
	h.svc.RecordDecision(runID, c, "refine", "again")
	res, err = h.svc.AttachDecisions(runID, c, node.ID)
	if err != nil || len(res.Attached) != 0 || strings.Join(res.Skipped, ",") != "autopilot-decision-refine" {
		t.Fatalf("second attach = %+v, %v", res, err)
	}
	arts, _ := h.repo.ListArtifactsByTicket(c)
	n := 0
	for _, a := range arts {
		if a.Name == "autopilot-decision-refine" {
			n++
			if a.NodeID != node.ID || a.Type != domain.ArtifactText || *a.Content != "adopted the recommended criteria because ..." {
				t.Fatalf("artifact = %+v", a)
			}
		}
	}
	if n != 1 {
		t.Fatalf("%d refine artifacts", n)
	}
}

func TestDecisions_Validation(t *testing.T) {
	h := newHarness(t)
	runID, c := launchSilent(t, h)
	if err := h.svc.RecordDecision(runID, c, "../x", "y"); err == nil {
		t.Fatal("bad kind accepted")
	}
	if err := h.svc.RecordDecision(runID, c, "refine", "  "); err == nil {
		t.Fatal("empty content accepted")
	}
	other := h.ticket("O", "")
	n, _ := h.repo.CreateNode(domain.GraphNode{TicketID: other, Name: "plan", Type: domain.NodeTypePlan, Status: domain.NodeTODO, MaxIterations: 3})
	if _, err := h.svc.AttachDecisions(runID, c, n.ID); err == nil {
		t.Fatal("a node of another ticket accepted")
	}
}

func TestSummary_SavedOnTheRootReleaseNode(t *testing.T) {
	h := newHarness(t)
	r := h.ticket("R", "")
	c := h.ticket("C", r)
	h.ticket("D", r)
	plan, _ := h.repo.CreateNode(domain.GraphNode{TicketID: r, Name: "plan", Type: domain.NodeTypePlan, Status: domain.NodeDone, MaxIterations: 3})
	release, _ := h.repo.CreateNode(domain.GraphNode{TicketID: r, Name: "release", Type: domain.NodeTypeRelease, Status: domain.NodeDone, MaxIterations: 3, IsManual: true})
	_ = plan
	h.setStatus(c, domain.TicketDone)
	run := h.start(r, autopilot.ModeTree)
	h.drive(run.RunID, nil)
	sum, err := h.svc.Summary(run.RunID)
	if err != nil || sum.SavedTo != release.ID {
		t.Fatalf("saved to %q, %v", sum.SavedTo, err)
	}
	for _, want := range []string{r, c, autopilot.ReasonAlreadyDone, "worktree-" + r, "finished", "did " + r} {
		if !strings.Contains(sum.Markdown, want) {
			t.Fatalf("summary lacks %q:\n%s", want, sum.Markdown)
		}
	}
	arts, _ := h.repo.ListArtifactsByNode(release.ID)
	if len(arts) != 1 || arts[0].Name != SummaryArtifactName {
		t.Fatalf("artifacts = %+v", arts)
	}
	// Unchanged summary: not saved twice.
	if again, _ := h.svc.Summary(run.RunID); again.SavedTo != "" {
		t.Fatal("saved again")
	}
}

func TestStart_UsesTheDBTreeForOverlaps(t *testing.T) {
	h := newHarness(t)
	r := h.ticket("R", "")
	c := h.ticket("C", r)
	g := h.ticket("G", c)
	h.start(r, autopilot.ModeTree)
	for _, id := range []string{c, g} {
		_, err := h.svc.Start(id, autopilot.ModeTicket, "", false)
		assertAPICode(t, err, autopilot.ErrCodeAlreadyRunning)
	}
	other := h.ticket("Other", "")
	h.start(other, autopilot.ModeTree)
}

func TestStart_InProgressRootIsProcessed(t *testing.T) {
	h := newHarness(t)
	r := h.ticket("R", "")
	h.setStatus(r, domain.TicketInProgress)
	run := h.start(r, autopilot.ModeTicket)
	next, err := h.svc.Next(run.RunID)
	if err != nil || next.Action.Action != autopilot.ActionLaunch || next.Ticket != r {
		t.Fatalf("next = %+v, %v", next, err)
	}
}

func TestStart_AdoptsAReservedRun(t *testing.T) {
	h := newHarness(t)
	r := h.ticket("R", "")
	reserved, err := h.svc.Start(r, autopilot.ModeTree, "", true)
	if err != nil || reserved.State != autopilot.RunStarting {
		t.Fatalf("reserve = %+v, %v", reserved, err)
	}
	if _, err := h.svc.Next(reserved.RunID); err == nil {
		t.Fatal("next on an unadopted reservation")
	}
	adopted, err := h.svc.Start(r, autopilot.ModeTree, reserved.RunID, false)
	if err != nil || !adopted.Adopted || adopted.RunID != reserved.RunID || adopted.State != autopilot.RunRunning {
		t.Fatalf("adopt = %+v, %v", adopted, err)
	}
	if next, _ := h.svc.Next(adopted.RunID); next.Action.Action != autopilot.ActionLaunch {
		t.Fatalf("next = %+v", next)
	}
	// Cancel (the Web UI's failed terminal) of another reservation.
	o := h.ticket("O", "")
	res, _ := h.svc.Start(o, autopilot.ModeTicket, "", true)
	if err := h.svc.CancelReservation(res.RunID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Start(o, autopilot.ModeTicket, "", false); err != nil {
		t.Fatalf("start after cancel: %v", err)
	}
}

func TestStatus_ListsRunsWithTheCurrentTicket(t *testing.T) {
	h := newHarness(t)
	runID, c := launchSilent(t, h)
	h.svc.Touch(runID, c, "waiting for approval")
	list, err := h.svc.Status(h.projectID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	s := list[0]
	if s.RunID != runID || !s.Active || s.Current != c || s.CurrentRole != "work" || s.AwaitingHuman != "waiting for approval" || s.Tickets[c] != autopilot.TicketLaunched {
		t.Fatalf("status = %+v", s)
	}
}
