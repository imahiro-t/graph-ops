package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
)

// DFLT-00084: project-scoped labels and their links to tickets. The contract
// tests below take a GraphRepository plus its *sql.DB so the same assertions
// run against SQLite here and against MySQL in mysql_test.go.

func wantAPIErrorCode(t *testing.T, err error, code domain.ErrorCode) {
	t.Helper()
	var apiErr *domain.APIError
	if err == nil || !errors.As(err, &apiErr) || apiErr.Code != code {
		t.Fatalf("expected APIError %s, got %v", code, err)
	}
}

func countRows(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func mustCreateLabel(t *testing.T, repo GraphRepository, projectID, name, color string) domain.Label {
	t.Helper()
	l, err := repo.CreateLabel(projectID, name, color)
	if err != nil {
		t.Fatalf("CreateLabel(%q): %v", name, err)
	}
	return l
}

func mustTicket(t *testing.T, repo GraphRepository, projectID, title string) domain.Ticket {
	t.Helper()
	tk, err := repo.CreateTicket(projectID, domain.Ticket{Title: title, Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	return tk
}

func setLabels(t *testing.T, repo GraphRepository, ticketID string, ids ...string) domain.Ticket {
	t.Helper()
	if ids == nil {
		ids = []string{}
	}
	tk, err := repo.UpdateTicket(ticketID, TicketPatch{LabelIDs: &ids})
	if err != nil {
		t.Fatalf("UpdateTicket(LabelIDs=%v): %v", ids, err)
	}
	return tk
}

func labelNames(labels []domain.Label) []string {
	out := make([]string, 0, len(labels))
	for _, l := range labels {
		out = append(out, l.Name)
	}
	return out
}

func assertLabelNames(t *testing.T, where string, labels []domain.Label, want ...string) {
	t.Helper()
	if labels == nil {
		t.Fatalf("%s: labels must be a non-nil slice", where)
	}
	got := strings.Join(labelNames(labels), ",")
	if got != strings.Join(want, ",") {
		t.Errorf("%s: labels = [%s], want [%s]", where, got, strings.Join(want, ","))
	}
}

func getTicketLabels(t *testing.T, repo GraphRepository, id string) []domain.Label {
	t.Helper()
	tk, err := repo.GetTicket(id)
	if err != nil || tk == nil {
		t.Fatalf("GetTicket(%s): %v, %v", id, tk, err)
	}
	return tk.Labels
}

// runLabelCRUDContract covers label create/list/rename/recolor/delete and
// the duplicate-name rules.
func runLabelCRUDContract(t *testing.T, repo GraphRepository, db *sql.DB) {
	alpha, err := repo.CreateProject("Alpha", "ALP")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	beta, err := repo.CreateProject("Beta", "BET")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	bug := mustCreateLabel(t, repo, alpha.ID, "  Bug  ", "red")
	if !strings.HasPrefix(bug.ID, "label-") || bug.Name != "Bug" || bug.Color != domain.LabelColorRed || bug.ProjectID != alpha.ID {
		t.Errorf("unexpected created label: %+v", bug)
	}
	got, err := repo.GetLabel(bug.ID)
	if err != nil || got == nil || *got != bug {
		t.Errorf("GetLabel = %+v, %v; want %+v", got, err, bug)
	}
	if missing, err := repo.GetLabel("label-missing"); err != nil || missing != nil {
		t.Errorf("GetLabel(missing) = %+v, %v; want nil, nil", missing, err)
	}

	for _, c := range domain.LabelColors {
		mustCreateLabel(t, repo, beta.ID, "色-"+string(c), string(c))
	}

	// Duplicate names, case-insensitively and after trimming.
	for _, dup := range []string{"Bug", "bug", "BUG", " Bug ", "  bug  "} {
		_, err := repo.CreateLabel(alpha.ID, dup, "blue")
		wantAPIErrorCode(t, err, domain.ErrCodeLabelNameTaken)
	}
	jp := mustCreateLabel(t, repo, alpha.ID, "バグ", "red")
	_, err = repo.CreateLabel(alpha.ID, "バグ", "blue")
	wantAPIErrorCode(t, err, domain.ErrCodeLabelNameTaken)
	// Same name in another project is fine.
	mustCreateLabel(t, repo, beta.ID, "Bug", "red")

	// Invalid input.
	for _, bad := range []struct{ name, color string }{{"", "red"}, {"   ", "red"}, {"　", "red"}, {strings.Repeat("a", 51), "red"}, {strings.Repeat("字", 51), "red"}} {
		_, err := repo.CreateLabel(alpha.ID, bad.name, bad.color)
		wantAPIErrorCode(t, err, domain.ErrCodeInvalidLabelName)
	}
	for _, bad := range []string{"", "magenta"} {
		_, err := repo.CreateLabel(alpha.ID, "OK", bad)
		wantAPIErrorCode(t, err, domain.ErrCodeInvalidLabelColor)
	}
	_, err = repo.CreateLabel("proj-missing", "X", "red")
	wantAPIErrorCode(t, err, domain.ErrCodeProjectNotFound)
	if l, err := repo.CreateLabel(alpha.ID, strings.Repeat("字", 50), "gray"); err != nil || l.Name != strings.Repeat("字", 50) {
		t.Errorf("a 50-character name must be accepted: %+v, %v", l, err)
	} else if _, err := repo.DeleteLabel(l.ID); err != nil {
		t.Fatalf("DeleteLabel: %v", err)
	}

	list, err := repo.ListLabelsByProject(alpha.ID)
	if err != nil {
		t.Fatalf("ListLabelsByProject: %v", err)
	}
	if len(list) != 2 || list[0].Name != "Bug" || list[1].Name != "バグ" || list[0].TicketCount != 0 {
		t.Errorf("unexpected Alpha labels: %+v", list)
	}
	_, err = repo.ListLabelsByProject("proj-missing")
	wantAPIErrorCode(t, err, domain.ErrCodeProjectNotFound)

	// Rename / recolor.
	feature := mustCreateLabel(t, repo, alpha.ID, "Feature", "blue")
	newName := "bug"
	_, err = repo.UpdateLabel(feature.ID, LabelPatch{Name: &newName})
	wantAPIErrorCode(t, err, domain.ErrCodeLabelNameTaken)
	spaced := " BUG "
	_, err = repo.UpdateLabel(feature.ID, LabelPatch{Name: &spaced})
	wantAPIErrorCode(t, err, domain.ErrCodeLabelNameTaken)
	if got, _ := repo.GetLabel(feature.ID); got == nil || got.Name != "Feature" {
		t.Errorf("a rejected rename must leave the label unchanged: %+v", got)
	}
	selfCase := "BUG"
	renamed, err := repo.UpdateLabel(bug.ID, LabelPatch{Name: &selfCase})
	if err != nil || renamed.Name != "BUG" || renamed.ID != bug.ID || renamed.Color != domain.LabelColorRed {
		t.Errorf("changing only a label's own case must succeed: %+v, %v", renamed, err)
	}
	orange := "orange"
	recolored, err := repo.UpdateLabel(jp.ID, LabelPatch{Color: &orange})
	if err != nil || recolored.Color != domain.LabelColorOrange || recolored.Name != "バグ" {
		t.Errorf("recolor: %+v, %v", recolored, err)
	}
	trimmed := "  不具合  "
	if l, err := repo.UpdateLabel(jp.ID, LabelPatch{Name: &trimmed}); err != nil || l.Name != "不具合" {
		t.Errorf("rename must trim: %+v, %v", l, err)
	}
	for _, bad := range []string{"", "   ", strings.Repeat("a", 51)} {
		b := bad
		_, err := repo.UpdateLabel(jp.ID, LabelPatch{Name: &b})
		wantAPIErrorCode(t, err, domain.ErrCodeInvalidLabelName)
	}
	okName, rainbow := "OK", "rainbow"
	_, err = repo.UpdateLabel(jp.ID, LabelPatch{Name: &okName, Color: &rainbow})
	wantAPIErrorCode(t, err, domain.ErrCodeInvalidLabelColor)
	if got, _ := repo.GetLabel(jp.ID); got == nil || got.Name != "不具合" || got.Color != domain.LabelColorOrange {
		t.Errorf("a rejected update must leave the label unchanged: %+v", got)
	}
	_, err = repo.UpdateLabel("label-missing", LabelPatch{Name: &okName})
	wantAPIErrorCode(t, err, domain.ErrCodeLabelNotFound)
	_, err = repo.DeleteLabel("label-missing")
	wantAPIErrorCode(t, err, domain.ErrCodeLabelNotFound)

	// Unused delete.
	n, err := repo.DeleteLabel(feature.ID)
	if err != nil || n != 0 {
		t.Errorf("DeleteLabel(unused) = %d, %v", n, err)
	}
	if got, _ := repo.GetLabel(feature.ID); got != nil {
		t.Errorf("deleted label still readable: %+v", got)
	}
	if c := countRows(t, db, `SELECT COUNT(*) FROM labels WHERE project_id = ?`, alpha.ID); c != 2 {
		t.Errorf("Alpha should have 2 labels left, has %d", c)
	}
}

// runTicketLabelContract covers attaching/replacing/clearing labels, their
// validation, ordering, propagation of renames, usage counts and deletes.
func runTicketLabelContract(t *testing.T, repo GraphRepository, db *sql.DB) {
	alpha, err := repo.CreateProject("Alpha", "ALP")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	beta, err := repo.CreateProject("Beta", "BET")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	bug := mustCreateLabel(t, repo, alpha.ID, "バグ", "red")
	feat := mustCreateLabel(t, repo, alpha.ID, "機能追加", "blue")
	ui := mustCreateLabel(t, repo, alpha.ID, "UI", "purple")
	betaBug := mustCreateLabel(t, repo, beta.ID, "Bug", "red")

	a1 := mustTicket(t, repo, alpha.ID, "A-1")
	assertLabelNames(t, "new ticket", a1.Labels)
	assertLabelNames(t, "GetTicket without labels", getTicketLabels(t, repo, a1.ID))

	tk := setLabels(t, repo, a1.ID, bug.ID, ui.ID)
	assertLabelNames(t, "UpdateTicket result", tk.Labels, "UI", "バグ")
	assertLabelNames(t, "after attach", getTicketLabels(t, repo, a1.ID), "UI", "バグ")

	setLabels(t, repo, a1.ID, feat.ID)
	assertLabelNames(t, "after replace", getTicketLabels(t, repo, a1.ID), "機能追加")

	setLabels(t, repo, a1.ID, bug.ID, bug.ID)
	assertLabelNames(t, "duplicate ids", getTicketLabels(t, repo, a1.ID), "バグ")

	high := domain.TicketPriorityHigh
	if tk, err := repo.UpdateTicket(a1.ID, TicketPatch{Priority: &high}); err != nil || tk.Priority != high {
		t.Fatalf("priority-only update: %+v, %v", tk, err)
	}
	assertLabelNames(t, "patch without LabelIDs", getTicketLabels(t, repo, a1.ID), "バグ")

	// Invalid label IDs reject the whole patch.
	low := domain.TicketPriorityLow
	for _, ids := range [][]string{{betaBug.ID}, {"label-missing"}, {ui.ID, "label-missing"}} {
		ids := ids
		_, err := repo.UpdateTicket(a1.ID, TicketPatch{Priority: &low, LabelIDs: &ids})
		wantAPIErrorCode(t, err, domain.ErrCodeLabelNotFound)
		got, _ := repo.GetTicket(a1.ID)
		if got.Priority != high {
			t.Errorf("ids %v: priority must be unchanged, got %s", ids, got.Priority)
		}
		assertLabelNames(t, fmt.Sprintf("ids %v", ids), got.Labels, "バグ")
	}

	// Missing ticket.
	ids := []string{bug.ID}
	_, err = repo.UpdateTicket("ALP-99999", TicketPatch{LabelIDs: &ids})
	wantAPIErrorCode(t, err, domain.ErrCodeTicketNotFound)

	// Clear.
	setLabels(t, repo, a1.ID)
	assertLabelNames(t, "after clear", getTicketLabels(t, repo, a1.ID))

	// CreateTicket with labels, including a rejected foreign label.
	before := countRows(t, db, `SELECT COUNT(*) FROM tickets WHERE project_id = ?`, alpha.ID)
	_, err = repo.CreateTicket(alpha.ID, domain.Ticket{Title: "x", Status: domain.TicketTODO, Labels: []domain.Label{{ID: bug.ID}, {ID: betaBug.ID}}})
	wantAPIErrorCode(t, err, domain.ErrCodeLabelNotFound)
	if after := countRows(t, db, `SELECT COUNT(*) FROM tickets WHERE project_id = ?`, alpha.ID); after != before {
		t.Errorf("a rejected CreateTicket must not create a ticket: %d -> %d", before, after)
	}
	a2, err := repo.CreateTicket(alpha.ID, domain.Ticket{Title: "A-2", Status: domain.TicketTODO, Labels: []domain.Label{{ID: bug.ID, Name: "ignored"}}})
	if err != nil {
		t.Fatalf("CreateTicket with labels: %v", err)
	}
	assertLabelNames(t, "created with labels", a2.Labels, "バグ")

	// Case-insensitive ordering.
	lb := mustCreateLabel(t, repo, alpha.ID, "beta", "gray")
	la := mustCreateLabel(t, repo, alpha.ID, "Alpha", "gray")
	lg := mustCreateLabel(t, repo, alpha.ID, "gamma", "gray")
	setLabels(t, repo, a1.ID, lg.ID, lb.ID, la.ID)
	assertLabelNames(t, "ordering", getTicketLabels(t, repo, a1.ID), "Alpha", "beta", "gamma")

	// Rename/recolor propagate by ID.
	a3 := mustTicket(t, repo, alpha.ID, "A-3")
	setLabels(t, repo, a1.ID, bug.ID)
	setLabels(t, repo, a3.ID, bug.ID, feat.ID)
	newName, pink := "不具合", "pink"
	if _, err := repo.UpdateLabel(bug.ID, LabelPatch{Name: &newName, Color: &pink}); err != nil {
		t.Fatalf("UpdateLabel: %v", err)
	}
	for _, id := range []string{a1.ID, a2.ID, a3.ID} {
		labels := getTicketLabels(t, repo, id)
		found := false
		for _, l := range labels {
			if l.ID == bug.ID {
				found = true
				if l.Name != "不具合" || l.Color != domain.LabelColorPink {
					t.Errorf("%s: renamed label not reflected: %+v", id, l)
				}
			}
		}
		if !found {
			t.Errorf("%s: label %s missing: %+v", id, bug.ID, labels)
		}
	}
	byProject, err := repo.ListTicketsByProject(alpha.ID)
	if err != nil {
		t.Fatalf("ListTicketsByProject: %v", err)
	}
	all, err := repo.ListTickets()
	if err != nil {
		t.Fatalf("ListTickets: %v", err)
	}
	for _, list := range [][]domain.Ticket{byProject, all} {
		for _, tk := range list {
			if tk.Labels == nil {
				t.Errorf("%s: list labels must be non-nil", tk.ID)
			}
			for _, l := range tk.Labels {
				if l.ID == bug.ID && l.Name != "不具合" {
					t.Errorf("%s: list shows stale label %+v", tk.ID, l)
				}
			}
		}
	}

	// Usage counts and deleting an in-use label.
	usage, err := repo.ListLabelsByProject(alpha.ID)
	if err != nil {
		t.Fatalf("ListLabelsByProject: %v", err)
	}
	counts := map[string]int{}
	for _, u := range usage {
		counts[u.ID] = u.TicketCount
	}
	if counts[bug.ID] != 3 || counts[feat.ID] != 1 || counts[ui.ID] != 0 {
		t.Errorf("unexpected usage counts: %v", counts)
	}
	n, err := repo.DeleteLabel(bug.ID)
	if err != nil || n != 3 {
		t.Fatalf("DeleteLabel(in use) = %d, %v; want 3", n, err)
	}
	if c := countRows(t, db, `SELECT COUNT(*) FROM ticket_labels WHERE label_id = ?`, bug.ID); c != 0 {
		t.Errorf("ticket_labels still has %d rows for the deleted label", c)
	}
	assertLabelNames(t, "A-3 after delete", getTicketLabels(t, repo, a3.ID), "機能追加")
	assertLabelNames(t, "A-2 after delete", getTicketLabels(t, repo, a2.ID))

	// Deleting a ticket removes its links but keeps the label.
	if err := repo.DeleteTicket(a3.ID); err != nil {
		t.Fatalf("DeleteTicket: %v", err)
	}
	if c := countRows(t, db, `SELECT COUNT(*) FROM ticket_labels WHERE ticket_id = ?`, a3.ID); c != 0 {
		t.Errorf("ticket_labels still has %d rows for the deleted ticket", c)
	}
	if got, _ := repo.GetLabel(feat.ID); got == nil {
		t.Error("deleting a ticket must not delete its labels")
	}

	// Deleting a project removes its labels and links, leaving others.
	setLabels(t, repo, a1.ID, feat.ID)
	if err := repo.DeleteProject(alpha.ID); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if c := countRows(t, db, `SELECT COUNT(*) FROM labels WHERE project_id = ?`, alpha.ID); c != 0 {
		t.Errorf("labels of the deleted project remain: %d", c)
	}
	if c := countRows(t, db, `SELECT COUNT(*) FROM ticket_labels WHERE label_id = ?`, feat.ID); c != 0 {
		t.Errorf("links of the deleted project remain: %d", c)
	}
	if got, _ := repo.GetLabel(betaBug.ID); got == nil {
		t.Error("another project's label must survive DeleteProject")
	}
}

func TestSQLiteLabels_CRUD(t *testing.T) {
	repo := newTestRepo(t)
	runLabelCRUDContract(t, repo, repo.db)
}

func TestSQLiteLabels_TicketLabels(t *testing.T) {
	repo := newTestRepo(t)
	runTicketLabelContract(t, repo, repo.db)
}

// TestSQLiteLabels_ListDistributesLabelsWithOneQuery attaches different label
// combinations to 20 tickets and checks both list methods hand each ticket
// exactly its own labels.
func TestSQLiteLabels_ListDistributesLabels(t *testing.T) {
	repo, proj := newTestRepoWithProject(t)
	other, err := repo.CreateProject("Other", "OTH")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	var labels []domain.Label
	for i := 0; i < 5; i++ {
		labels = append(labels, mustCreateLabel(t, repo, proj.ID, fmt.Sprintf("L%d", i), "gray"))
	}
	otherLabel := mustCreateLabel(t, repo, other.ID, "O", "gray")
	otherTicket := mustTicket(t, repo, other.ID, "other")
	setLabels(t, repo, otherTicket.ID, otherLabel.ID)

	want := map[string]string{}
	for i := 0; i < 20; i++ {
		tk := mustTicket(t, repo, proj.ID, fmt.Sprintf("T%d", i))
		var ids, names []string
		for b := 0; b < 5; b++ {
			if i&(1<<b) != 0 {
				ids = append(ids, labels[b].ID)
				names = append(names, labels[b].Name)
			}
		}
		setLabels(t, repo, tk.ID, ids...)
		want[tk.ID] = strings.Join(names, ",")
	}
	want[otherTicket.ID] = "O"

	byProject, err := repo.ListTicketsByProject(proj.ID)
	if err != nil {
		t.Fatalf("ListTicketsByProject: %v", err)
	}
	all, err := repo.ListTickets()
	if err != nil {
		t.Fatalf("ListTickets: %v", err)
	}
	if len(byProject) != 20 || len(all) != 21 {
		t.Fatalf("unexpected list sizes: %d, %d", len(byProject), len(all))
	}
	for _, list := range [][]domain.Ticket{byProject, all} {
		for _, tk := range list {
			if tk.Labels == nil {
				t.Errorf("%s: labels must be non-nil", tk.ID)
			}
			if got := strings.Join(labelNames(tk.Labels), ","); got != want[tk.ID] {
				t.Errorf("%s: labels = %q, want %q", tk.ID, got, want[tk.ID])
			}
		}
	}
}

func TestSQLiteLabels_UniqueViolationDetection(t *testing.T) {
	repo, proj := newTestRepoWithProject(t)
	// Bypass the store's own check to hit the index directly.
	insert := `INSERT INTO labels (id, project_id, name, color, created_at, updated_at) VALUES (?, ?, ?, 'red', 'x', 'x')`
	if _, err := repo.db.Exec(insert, "label-a", proj.ID, "Bug"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	_, err := repo.db.Exec(insert, "label-b", proj.ID, "bug")
	if !sqliteDialect.isUniqueViolation(err) {
		t.Fatalf("the NOCASE unique index must reject a case variant, got %v", err)
	}
	if sqliteDialect.isUniqueViolation(errors.New("some other error")) || sqliteDialect.isUniqueViolation(nil) {
		t.Error("only UNIQUE violations may be detected")
	}
}
