package httpserver

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWithTicketContext(t *testing.T) {
	cases := []struct {
		name     string
		prompt   string
		ticketID string
		want     string
	}{
		{
			name:     "no ticket id leaves prompt untouched",
			prompt:   "この対応は終わったのでDONEもしくは削除して",
			ticketID: "",
			want:     "この対応は終わったのでDONEもしくは削除して",
		},
		{
			name:     "custom prompt without the ticket id gets it prepended",
			prompt:   "この対応は終わったのでDONEもしくは削除して",
			ticketID: "DFLT-00014",
			want:     "This instruction concerns ticket DFLT-00014.\n\nこの対応は終わったのでDONEもしくは削除して",
		},
		{
			name:     "default prompt already naming the ticket is left as-is",
			prompt:   "チケット DFLT-00014 の未完了ノードを実行してください",
			ticketID: "DFLT-00014",
			want:     "チケット DFLT-00014 の未完了ノードを実行してください",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := withTicketContext(tc.prompt, tc.ticketID)
			if got != tc.want {
				t.Fatalf("withTicketContext(%q, %q) = %q, want %q", tc.prompt, tc.ticketID, got, tc.want)
			}
		})
	}
}

// --- DFLT-00375: POST /api/claude/launch's optional model ---------------

// fakeClaudeLaunchServer returns a server whose launches run a stand-in for
// claude through a real shell (TerminalCommand), writing each argument it
// receives as a NUL-terminated record to the returned file.
func fakeClaudeLaunchServer(t *testing.T) (*Server, string) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "argv.out")
	bin := filepath.Join(dir, "fake-claude")
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\0' \"$a\"; done > '" + out + "'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	s, _, _ := newTestServer(t)
	s.cfg.TerminalCommand = "cd {cwd} && {command}"
	s.cfg.ClaudeBinary = "'" + bin + "'"
	s.cfg.TerminalWorkDir = dir
	return s, out
}

func launchedArgv(t *testing.T, out string) []string {
	t.Helper()
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading argv: %v", err)
	}
	parts := strings.Split(string(data), "\x00")
	return parts[:len(parts)-1]
}

func TestClaudeLaunch_ModelPassesModelArgument(t *testing.T) {
	s, out := fakeClaudeLaunchServer(t)
	prompt := "/graph-ops:process-ticket DFLT-00001 --model sonnet"
	rec := doJSON(t, s, http.MethodPost, "/api/claude/launch", map[string]any{"prompt": prompt, "model": "sonnet"})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	got := launchedArgv(t, out)
	want := []string{"--model", "sonnet", prompt}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("argv = %q, want %q", got, want)
	}
}

func TestClaudeLaunch_NoModelLaunchesAsBefore(t *testing.T) {
	for name, body := range map[string]map[string]any{
		"absent": {"prompt": "/graph-ops:refine-ticket DFLT-00001"},
		"empty":  {"prompt": "/graph-ops:refine-ticket DFLT-00001", "model": ""},
	} {
		t.Run(name, func(t *testing.T) {
			s, out := fakeClaudeLaunchServer(t)
			rec := doJSON(t, s, http.MethodPost, "/api/claude/launch", body)
			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
			}
			got := launchedArgv(t, out)
			if len(got) != 1 || got[0] != "/graph-ops:refine-ticket DFLT-00001" {
				t.Errorf("argv = %q, want the prompt alone", got)
			}
		})
	}
}

func TestClaudeLaunch_InvalidModelIsRefusedWithoutLaunching(t *testing.T) {
	for _, v := range []string{"opus; rm -rf /", "fable", "inherit"} {
		t.Run(v, func(t *testing.T) {
			s, out := fakeClaudeLaunchServer(t)
			rec := doJSON(t, s, http.MethodPost, "/api/claude/launch", map[string]any{"prompt": "x", "model": v})
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Errorf("a terminal was launched (argv file exists: %v)", err)
			}
		})
	}
}
