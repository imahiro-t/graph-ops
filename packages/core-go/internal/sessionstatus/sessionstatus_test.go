package sessionstatus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

const sid = "0f8e2c1a-1111-4222-8333-944455556666"

var now = time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)

func TestValidSessionIDAndPath(t *testing.T) {
	dir := t.TempDir()
	for _, ok := range []string{sid, "abc", "A_b-9", strings.Repeat("a", MaxSessionIDLen)} {
		if !ValidSessionID(ok) {
			t.Errorf("ValidSessionID(%q) = false", ok)
		}
		p, err := Path(dir, ok)
		if err != nil || p != filepath.Join(dir, DirName, ok+".json") {
			t.Errorf("Path(%q) = %q, %v", ok, p, err)
		}
	}
	for _, bad := range []string{"", "..", "../x", "a/b", `a\b`, "a.b", "a b", "a\x1b", "ｱ", strings.Repeat("a", MaxSessionIDLen+1)} {
		if ValidSessionID(bad) {
			t.Errorf("ValidSessionID(%q) = true", bad)
		}
		if _, err := Path(dir, bad); err == nil {
			t.Errorf("Path(%q) accepted", bad)
		}
		if _, err := Update(dir, bad, now, func(*Status) {}); err == nil {
			t.Errorf("Update(%q) accepted", bad)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("a refused ID wrote something: %v", entries)
	}
}

func TestUpdateReadMergeAndClear(t *testing.T) {
	dir := t.TempDir()
	if st, err := Read(dir, sid); st != nil || err != nil {
		t.Fatalf("Read before any write = %v, %v", st, err)
	}
	st, err := Update(dir, sid, now, func(st *Status) {
		st.Role, st.Phase = RoleWorker, PhaseRunning
		st.Ticket = &Ticket{ID: "DFLT-00001", Title: "タイトル"}
		st.Nodes = []Node{{ID: "DFLT-00001-01", Name: "計画作成", Type: "plan"}}
	})
	if err != nil {
		t.Fatal(err)
	}
	if st.Version != Version || st.SessionID != sid || st.UpdatedAt != now.Format(time.RFC3339Nano) {
		t.Fatalf("stamp = %+v", st)
	}
	// A second update changes only what it touches.
	later := now.Add(time.Minute)
	if _, err := Update(dir, sid, later, func(st *Status) { st.Phase, st.Detail = PhaseAwaitingHuman, "質問" }); err != nil {
		t.Fatal(err)
	}
	got, err := Read(dir, sid)
	if err != nil || got == nil {
		t.Fatalf("Read = %v, %v", got, err)
	}
	if got.Phase != PhaseAwaitingHuman || got.Detail != "質問" || got.Ticket.Title != "タイトル" || len(got.Nodes) != 1 || got.UpdatedAt != later.Format(time.RFC3339Nano) {
		t.Fatalf("merged = %+v", got)
	}
	// No temporary file is left behind.
	entries, _ := os.ReadDir(Dir(dir))
	if len(entries) != 1 || entries[0].Name() != sid+".json" {
		t.Fatalf("status dir = %v", entries)
	}

	cleared, err := Clear(dir, sid, later)
	if !cleared || err != nil {
		t.Fatalf("Clear = %v, %v", cleared, err)
	}
	if st, _ := Read(dir, sid); st != nil {
		t.Fatalf("after Clear = %+v", st)
	}
	if cleared, err := Clear(dir, sid, later); cleared || err != nil {
		t.Fatalf("second Clear = %v, %v", cleared, err)
	}
}

func TestUnparseableFileReadsAsNone(t *testing.T) {
	dir := t.TempDir()
	p, _ := Path(dir, sid)
	os.MkdirAll(filepath.Dir(p), 0o700)
	os.WriteFile(p, []byte("{not json"), 0o600)
	if st, err := Read(dir, sid); st != nil || err != nil {
		t.Fatalf("Read = %v, %v", st, err)
	}
	if _, err := Update(dir, sid, now, func(st *Status) { st.Phase = PhaseRunning }); err != nil {
		t.Fatal(err)
	}
	if st, _ := Read(dir, sid); st == nil || st.Phase != PhaseRunning {
		t.Fatalf("rewritten = %+v", st)
	}
}

func TestPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	dir := t.TempDir()
	if _, err := Update(dir, sid, now, func(st *Status) { st.Phase = PhaseRunning }); err != nil {
		t.Fatal(err)
	}
	di, _ := os.Stat(Dir(dir))
	p, _ := Path(dir, sid)
	fi, _ := os.Stat(p)
	if di.Mode().Perm() != 0o700 || fi.Mode().Perm() != 0o600 {
		t.Fatalf("dir %v file %v", di.Mode().Perm(), fi.Mode().Perm())
	}
}

func TestPruneRemovesOnlyForgottenFiles(t *testing.T) {
	dir := t.TempDir()
	status := Dir(dir)
	os.MkdirAll(status, 0o700)
	old := filepath.Join(status, "old-session.json")
	fresh := filepath.Join(status, "fresh-session.json")
	oldTmp := filepath.Join(status, ".status-1.tmp")
	other := filepath.Join(status, "notes.txt")
	for _, f := range []string{old, fresh, oldTmp, other} {
		os.WriteFile(f, []byte("{}"), 0o600)
	}
	stale := now.Add(-PruneAfter - time.Hour)
	for _, f := range []string{old, oldTmp, other} {
		os.Chtimes(f, stale, stale)
	}
	recent := now.Add(-PruneAfter + time.Hour)
	os.Chtimes(fresh, recent, recent)

	// A new file's first write prunes.
	if _, err := Update(dir, sid, now, func(st *Status) { st.Phase = PhaseRunning }); err != nil {
		t.Fatal(err)
	}
	for f, want := range map[string]bool{old: false, oldTmp: false, fresh: true, other: true} {
		if _, err := os.Stat(f); (err == nil) != want {
			t.Errorf("%s exists = %v, want %v", filepath.Base(f), err == nil, want)
		}
	}
	// Clear prunes too.
	os.WriteFile(old, []byte("{}"), 0o600)
	os.Chtimes(old, stale, stale)
	Clear(dir, sid, now)
	if _, err := os.Stat(old); err == nil {
		t.Error("Clear did not prune")
	}
}

func TestNormalizeMakesEveryStringSafe(t *testing.T) {
	long := strings.Repeat("あ", 300)
	cases := []struct {
		name, in, want string
	}{
		{"plain Japanese", "複数ターミナル実行時の表示", "複数ターミナル実行時の表示"},
		{"line breaks and tabs", "a\nb\r\nc\td", "a b c d"},
		{"ANSI escape", "\x1b[2J\x1b]0;evil\x07title", "[2J]0;eviltitle"},
		{"C1 control", "a\u009b31mb", "a31mb"},
		{"bidi and zero width", "ab\u202ecd\u200be", "abcde"},
		{"invalid UTF-8", "a\xffb", "a\ufffdb"},
		{"DEL", "a\x7fb", "ab"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := Normalize(Status{
				Ticket:    &Ticket{ID: "DFLT-1", Title: c.in},
				Detail:    c.in,
				Nodes:     []Node{{ID: "DFLT-1-01", Name: c.in, Type: c.in}},
				Autopilot: &Autopilot{RunID: "run-1", Mode: c.in, State: c.in, AwaitingHuman: c.in, Current: &RunTicket{TicketID: "DFLT-2", Title: c.in}, Last: &RunTicket{TicketID: "DFLT-3", Title: c.in, Outcome: "done"}},
			})
			for name, got := range map[string]string{
				"title": st.Ticket.Title, "detail": st.Detail, "node name": st.Nodes[0].Name, "node type": st.Nodes[0].Type,
				"mode": st.Autopilot.Mode, "state": st.Autopilot.State, "awaiting": st.Autopilot.AwaitingHuman,
				"current": st.Autopilot.Current.Title, "last": st.Autopilot.Last.Title,
			} {
				if got != c.want {
					t.Errorf("%s = %q, want %q", name, got, c.want)
				}
			}
		})
	}

	st := Normalize(Status{Ticket: &Ticket{ID: "T-1", Title: long}, Detail: long})
	if n := utf8.RuneCountInString(st.Ticket.Title); n != 128 {
		t.Errorf("title runes = %d", n)
	}
	if n := utf8.RuneCountInString(st.Detail); n != MaxDetailRunes {
		t.Errorf("detail runes = %d", n)
	}
	if again := Normalize(st); again.Ticket.Title != st.Ticket.Title || again.Detail != st.Detail {
		t.Error("Normalize is not idempotent")
	}
}

func TestNormalizeDropsWhatIsNotItsShape(t *testing.T) {
	nodes := make([]Node, 0, MaxNodes+3)
	nodes = append(nodes, Node{ID: "bad id\n", Name: "x"})
	for i := 0; i < MaxNodes+2; i++ {
		nodes = append(nodes, Node{ID: "N-1", Name: "n"})
	}
	st := Normalize(Status{
		SessionID: "../x", UpdatedAt: "yesterday\n", Language: "ja\x1b", Role: "admin", Phase: "hacking",
		Ticket: &Ticket{ID: "DFLT-1\x1b[2J", Title: "t"},
		Nodes:  nodes,
		Autopilot: &Autopilot{RunID: "run-1", RootTicketID: "a/b", Counts: Counts{Done: -1, Remaining: 2},
			Current: &RunTicket{TicketID: "\n"}, Last: &RunTicket{TicketID: "DFLT-3", Outcome: "won\x1b"}},
	})
	if st.SessionID != "" || st.UpdatedAt != "" || st.Language != "" || st.Role != "" || st.Phase != "" || st.Ticket != nil {
		t.Errorf("not dropped: %+v", st)
	}
	if len(st.Nodes) != MaxNodes {
		t.Errorf("nodes = %d", len(st.Nodes))
	}
	a := st.Autopilot
	if a.RootTicketID != "" || a.Counts.Done != 0 || a.Counts.Remaining != 2 || a.Current != nil || a.Last == nil || a.Last.Outcome != "" {
		t.Errorf("autopilot = %+v (current %+v last %+v)", a, a.Current, a.Last)
	}
	if Normalize(Status{Autopilot: &Autopilot{RunID: "run 1"}}).Autopilot != nil {
		t.Error("a run without a usable ID is kept")
	}
	ok := Normalize(Status{Language: "pt-BR", Role: RoleController, Phase: PhaseAwaitingApproval})
	if ok.Language != "pt-BR" || ok.Role != RoleController || ok.Phase != PhaseAwaitingApproval {
		t.Errorf("valid values dropped: %+v", ok)
	}
}

func TestWrittenFileHoldsOnlyNormalizedText(t *testing.T) {
	dir := t.TempDir()
	if _, err := Update(dir, sid, now, func(st *Status) {
		st.Ticket = &Ticket{ID: "DFLT-1", Title: "evil\x1b[31m\ntitle"}
		st.Detail = "line1\nline2"
	}); err != nil {
		t.Fatal(err)
	}
	p, _ := Path(dir, sid)
	data, _ := os.ReadFile(p)
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `\u001b`) || strings.Contains(string(data), `\n`) {
		t.Fatalf("file holds control characters: %s", data)
	}
}

func TestPhasesAreClosed(t *testing.T) {
	for _, p := range Phases {
		if !ValidPhase(p) {
			t.Errorf("%s", p)
		}
	}
	if ValidPhase("") || ValidPhase("Running") || ValidRole("") || ValidRole("admin") {
		t.Error("an unknown phase or role is valid")
	}
}
