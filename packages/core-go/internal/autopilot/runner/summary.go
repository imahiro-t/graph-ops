package runner

import (
	"fmt"
	"strings"

	"github.com/graph-ops/core-go/internal/autopilot"
	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
)

// SummaryArtifactName is the tree summary's artifact name (D7).
const SummaryArtifactName = "autopilot-tree-summary"

// SummaryResult is Summary's result: the Markdown, and where it was saved.
type SummaryResult struct {
	Markdown string
	// SavedTo is the node the summary artifact was saved on; "" when the
	// root has no node yet (nothing to attach it to) or it was unchanged.
	SavedTo string
}

// Summary renders the run's summary as Markdown -- every ticket's result,
// branch, merge state and summary, the skips and their reasons, what is not
// in the root branch, late reports, unresponsive sessions, and why the run
// stopped -- and saves it on the root ticket's release node (falling back to
// its newest node) as autopilot-tree-summary (autopilot summary). Saving
// again with identical content is skipped.
func (s *Service) Summary(runID string) (SummaryResult, error) {
	projectID, err := s.findRun(runID)
	if err != nil {
		return SummaryResult{}, err
	}
	run, err := s.registry().Load(projectID, runID)
	if err != nil {
		return SummaryResult{}, err
	}
	if run == nil {
		return SummaryResult{}, domain.NewAPIError(autopilot.ErrCodeRunNotFound, "AUTOPILOT_RUN_NOT_FOUND: run %s not found", runID)
	}
	titles := map[string]string{}
	if idx, err := s.index(run.ProjectID); err == nil {
		for id, t := range idx.byID {
			titles[id] = t.Title
		}
	}
	md := RenderSummary(run, titles)
	res := SummaryResult{Markdown: md}

	nodes, err := s.Repo.ListNodesByTicket(run.RootTicketID)
	if err != nil {
		return res, err
	}
	var target *domain.GraphNode
	for i := range nodes {
		if nodes[i].Type == domain.NodeTypeRelease {
			target = &nodes[i]
		}
	}
	if target == nil && len(nodes) > 0 {
		target = &nodes[len(nodes)-1]
	}
	if target == nil {
		return res, nil
	}
	existing, err := s.Repo.ListArtifactsByNode(target.ID)
	if err != nil {
		return res, err
	}
	for i := len(existing) - 1; i >= 0; i-- {
		if existing[i].Name == SummaryArtifactName {
			if existing[i].Content != nil && *existing[i].Content == md {
				return res, nil
			}
			break
		}
	}
	content := md
	if _, err := s.Repo.CreateArtifact(domain.Artifact{
		ID: engine.NewArtifactID(), TicketID: run.RootTicketID, NodeID: target.ID,
		Name: SummaryArtifactName, Type: domain.ArtifactText, Content: &content,
	}); err != nil {
		return res, err
	}
	res.SavedTo = target.ID
	return res, nil
}

// RenderSummary renders run as Markdown. titles maps ticket IDs to titles
// (missing ones are left out).
func RenderSummary(run *autopilot.Run, titles map[string]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Autopilot summary: %s (%s)\n\n", run.RootTicketID, run.Mode)
	fmt.Fprintf(&b, "- Run: `%s`\n- State: %s\n", run.ID, run.State)
	fmt.Fprintf(&b, "- Settings: mainReflection=%s, onFailure=%s, maxTickets=%d, maxDepth=%d, stallTimeoutMinutes=%d\n",
		run.Settings.MainReflection, run.Settings.OnFailure, run.Settings.MaxTickets, run.Settings.MaxDepth, run.Settings.StallTimeoutMinutes)
	if run.StopReason != "" {
		fmt.Fprintf(&b, "- Stopped: %s", run.StopReason)
		if run.StopDetail != "" {
			fmt.Fprintf(&b, " -- %s", oneLine(run.StopDetail))
		}
		b.WriteString(" (run the same command again to resume)\n")
	}
	if len(run.Stops) > 0 && (run.StopReason == "" || len(run.Stops) > 1) {
		b.WriteString("- Earlier stops:")
		for _, st := range run.Stops {
			fmt.Fprintf(&b, " %s %s (%s);", st.At.UTC().Format("2006-01-02 15:04"), st.Reason, st.Ticket)
		}
		b.WriteString("\n")
	}
	root := run.Ticket(run.RootTicketID)
	switch {
	case run.Mode == autopilot.ModeTree && root != nil && root.Status == autopilot.TicketDone:
		switch {
		case run.Settings.MainReflection == autopilot.MainReflectionBranch:
			fmt.Fprintf(&b, "- Main: not reflected (mainReflection is branch); the tree's work is on `%s`\n", root.Branch)
		case run.Finalize == nil:
			fmt.Fprintf(&b, "- Main: not reflected yet (finalize has not run); the tree's work is on `%s`\n", root.Branch)
		case run.Finalize.Status == autopilot.TicketDone:
			fmt.Fprintf(&b, "- Main: finalize done (%s)", run.Settings.MainReflection)
			if run.Finalize.Summary != "" {
				fmt.Fprintf(&b, " -- %s", oneLine(run.Finalize.Summary))
			}
			b.WriteString("\n")
		default:
			fmt.Fprintf(&b, "- Main: NOT reflected -- finalize %s", run.Finalize.Status)
			if run.Finalize.Reason != "" {
				fmt.Fprintf(&b, " (%s)", run.Finalize.Reason)
			}
			if run.Finalize.Detail != "" {
				fmt.Fprintf(&b, ": %s", oneLine(run.Finalize.Detail))
			}
			fmt.Fprintf(&b, "; the root branch `%s` is kept\n", root.Branch)
		}
	case root != nil && (root.Status == autopilot.TicketFailed || root.Status == autopilot.TicketBlocked):
		fmt.Fprintf(&b, "- Main: NOT reflected -- the root %s %s; its branch `%s` is kept\n", root.ID, root.Status, root.Branch)
	}

	b.WriteString("\n## Tickets\n\n| Ticket | Result | Reason | Branch | Merge | Summary |\n|---|---|---|---|---|---|\n")
	for _, id := range run.Order {
		st := run.Tickets[id]
		if st == nil {
			continue
		}
		name := id
		if t := titles[id]; t != "" {
			name = id + " " + t
		}
		reason := st.Reason
		if st.Detail != "" {
			reason += ": " + st.Detail
		}
		if st.AwaitingHuman != "" {
			reason = "awaiting a person: " + st.AwaitingHuman
		}
		status := st.Status
		if st.Role != "" {
			status += " (" + st.Role + " running)"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n",
			cell(name), cell(status), cell(reason), cell(code(st.Branch)), cell(st.Merge), cell(st.Summary))
	}

	var outside, unresponsive, late []*autopilot.TicketState
	for _, id := range run.Order {
		st := run.Tickets[id]
		if st == nil {
			continue
		}
		if (st.Status == autopilot.TicketFailed || st.Status == autopilot.TicketBlocked) && st.Branch != "" && id != run.RootTicketID && st.Merge != autopilot.MergedSubtree {
			outside = append(outside, st)
		}
		if st.Reason == autopilot.ReasonUnresponsive {
			unresponsive = append(unresponsive, st)
		}
		if st.LateReport != "" {
			late = append(late, st)
		}
	}
	if run.Mode == autopilot.ModeTree && len(outside) > 0 {
		b.WriteString("\n## Work not in the root branch\n\n")
		for _, st := range outside {
			fmt.Fprintf(&b, "- %s (%s, %s): branch `%s`\n", st.ID, st.Status, st.Reason, st.Branch)
		}
	}
	if len(unresponsive) > 0 || (run.Finalize != nil && run.Finalize.Reason == autopilot.ReasonUnresponsive) {
		b.WriteString("\n## Unresponsive sessions\n\nIf the terminal is still open, check what it shows before re-running.\n\n")
		for _, st := range unresponsive {
			fmt.Fprintf(&b, "- %s: worktree `%s` -- %s\n", st.ID, st.Worktree, oneLine(st.Detail))
		}
		if run.Finalize != nil && run.Finalize.Reason == autopilot.ReasonUnresponsive && root != nil {
			fmt.Fprintf(&b, "- %s (finalize): worktree `%s` -- %s\n", root.ID, root.Worktree, oneLine(run.Finalize.Detail))
		}
	}
	if len(late) > 0 {
		b.WriteString("\n## Late reports\n\n")
		for _, st := range late {
			fmt.Fprintf(&b, "- %s: %s\n", st.ID, oneLine(st.LateReport))
		}
	}
	return b.String()
}

func code(s string) string {
	if s == "" {
		return ""
	}
	return "`" + s + "`"
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func cell(s string) string {
	return strings.ReplaceAll(oneLine(s), "|", "\\|")
}
