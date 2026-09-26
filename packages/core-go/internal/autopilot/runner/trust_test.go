package runner

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/autopilot"
)

// DFLT-00182: start and launch report untrusted_folder when Claude Code has
// evidently not trusted the folder the sessions open in, and nothing else
// about the run depends on it. The home directory is always a temp
// directory: the real ~/.claude.json is never read.

// trustHome makes a fake home directory whose ~/.claude.json has the given
// content ("" writes no file) and points the harness's service at it.
func (h *harness) trustHome(content string) string {
	h.t.Helper()
	h.t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := h.t.TempDir()
	if content != "" {
		if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(content), 0o600); err != nil {
			h.t.Fatal(err)
		}
	}
	h.svc.HomeDir = home
	return home
}

// claudeJSON returns a ~/.claude.json trusting exactly the given paths.
func claudeJSON(t *testing.T, trusted ...string) string {
	t.Helper()
	projects := map[string]any{"/nowhere/else": map[string]any{"hasTrustDialogAccepted": false}}
	for _, p := range trusted {
		projects[p] = map[string]any{"hasTrustDialogAccepted": true}
	}
	b, err := json.Marshal(map[string]any{"projects": projects})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// nextLaunch asks next for the run's first action and checks it is the
// root's work launch.
func (h *harness) nextLaunch(runID, ticketID string) {
	h.t.Helper()
	res, err := h.svc.Next(runID)
	if err != nil {
		h.t.Fatal(err)
	}
	if res.Action.Action != autopilot.ActionLaunch || res.Ticket != ticketID || res.Role != autopilot.RoleWork {
		h.t.Fatalf("next = %+v, want a work launch of %s", res, ticketID)
	}
}

type trustCase struct {
	name      string
	config    func(h *harness) string
	untrusted bool
}

func trustCases() []trustCase {
	return []trustCase{
		{name: "untrusted", config: func(h *harness) string { return claudeJSON(h.t, "/some/other/project") }, untrusted: true},
		{name: "trusted", config: func(h *harness) string { return claudeJSON(h.t, h.gitRepo) }},
		{name: "a trusted parent", config: func(h *harness) string { return claudeJSON(h.t, filepath.Dir(h.gitRepo)) }},
		{name: "no file", config: func(h *harness) string { return "" }},
		{name: "unexpected format", config: func(h *harness) string { return `{"projects": {"/x": {"trusted": true}}}` }},
		{name: "broken JSON", config: func(h *harness) string { return `{"projects":` }},
	}
}

func TestTrust_StartReportsAnUntrustedLocalPathOnly(t *testing.T) {
	for _, tc := range trustCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.trustHome(tc.config(h))
			r := h.ticket("R", "")
			res := h.start(r, autopilot.ModeTree)
			want := ""
			if tc.untrusted {
				want = h.gitRepo
			}
			if res.UntrustedFolder != want {
				t.Fatalf("UntrustedFolder = %q, want %q", res.UntrustedFolder, want)
			}
			// The run starts the same either way.
			if !res.Created || res.State != autopilot.RunRunning || h.run(res.RunID).State != autopilot.RunRunning {
				t.Fatalf("start = %+v, stored state %s", res, h.run(res.RunID).State)
			}
			b, _ := json.Marshal(res)
			if got := strings.Contains(string(b), `"untrusted_folder"`); got != tc.untrusted {
				t.Fatalf("JSON %s: untrusted_folder present = %v, want %v", b, got, tc.untrusted)
			}
		})
	}
}

func TestTrust_ReservedStartAndAdoptionReportIt(t *testing.T) {
	for _, tc := range trustCases()[:2] {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.trustHome(tc.config(h))
			r := h.ticket("R", "")
			want := ""
			if tc.untrusted {
				want = h.gitRepo
			}
			reserved, err := h.svc.Start(r, autopilot.ModeTicket, "", true)
			if err != nil {
				t.Fatal(err)
			}
			if reserved.UntrustedFolder != want || reserved.State != autopilot.RunStarting {
				t.Fatalf("reserve = %+v, want untrusted %q", reserved, want)
			}
			adopted, err := h.svc.Start(r, autopilot.ModeTicket, reserved.RunID, false)
			if err != nil {
				t.Fatal(err)
			}
			if !adopted.Adopted || adopted.UntrustedFolder != want || adopted.State != autopilot.RunRunning {
				t.Fatalf("adopt = %+v, want untrusted %q", adopted, want)
			}
		})
	}
}

func TestTrust_StartWithoutAHomeDirNeverJudges(t *testing.T) {
	h := newHarness(t)
	r := h.ticket("R", "")
	if res := h.start(r, autopilot.ModeTicket); res.UntrustedFolder != "" {
		t.Fatalf("UntrustedFolder = %q with no home directory", res.UntrustedFolder)
	}
}

func TestTrust_FirstLaunchReportsTheNewWorktree(t *testing.T) {
	for _, tc := range trustCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.trustHome(tc.config(h))
			r := h.ticket("R", "")
			h.behave[r] = silentWorker
			run := h.start(r, autopilot.ModeTicket)
			wt := autopilot.WorktreePath(h.gitRepo, r)
			h.nextLaunch(run.RunID, r)
			if _, err := os.Stat(wt); !os.IsNotExist(err) {
				t.Fatalf("the worktree exists before the first launch: %v", err)
			}
			out, err := h.svc.Launch(run.RunID, r, autopilot.RoleWork)
			if err != nil {
				t.Fatal(err)
			}
			want := ""
			if tc.untrusted {
				want = wt
			}
			if out.UntrustedFolder != want {
				t.Fatalf("UntrustedFolder = %q, want %q", out.UntrustedFolder, want)
			}
			// The launch itself is the same either way.
			if len(h.launches) != 1 || h.launches[0].WorkDir != wt || out.Worktree != wt {
				t.Fatalf("launches = %+v, out = %+v", h.launches, out)
			}
			if st := h.st(run.RunID, r); st.Status != autopilot.TicketLaunched {
				t.Fatalf("status = %s", st.Status)
			}
			b, _ := json.Marshal(out)
			if got := strings.Contains(string(b), `"untrusted_folder"`); got != tc.untrusted {
				t.Fatalf("JSON %s: untrusted_folder present = %v, want %v", b, got, tc.untrusted)
			}
		})
	}
}

func TestTrust_FailedLaunchReportsNothing(t *testing.T) {
	t.Run("the terminal fails to open", func(t *testing.T) {
		h := newHarness(t)
		h.trustHome(claudeJSON(t, "/some/other/project"))
		r := h.ticket("R", "")
		run := h.start(r, autopilot.ModeTicket)
		h.nextLaunch(run.RunID, r)
		h.launchErr = errors.New("no terminal")
		out, err := h.svc.Launch(run.RunID, r, autopilot.RoleWork)
		if err == nil {
			t.Fatal("launch succeeded")
		}
		if out.UntrustedFolder != "" {
			t.Fatalf("UntrustedFolder = %q on a failed launch", out.UntrustedFolder)
		}
		if st := h.st(run.RunID, r); st.Status != autopilot.TicketQueued || st.LaunchFailures != 1 {
			t.Fatalf("state after a failed launch = %+v", st)
		}
	})
	t.Run("the worktree cannot be prepared", func(t *testing.T) {
		h := newHarness(t)
		h.trustHome(claudeJSON(t, "/some/other/project"))
		notARepo := t.TempDir()
		h.svc.LocalPath = func(string) string { return notARepo }
		r := h.ticket("R", "")
		run := h.start(r, autopilot.ModeTicket)
		h.nextLaunch(run.RunID, r)
		out, err := h.svc.Launch(run.RunID, r, autopilot.RoleWork)
		if err == nil {
			t.Fatal("launch succeeded")
		}
		if out.UntrustedFolder != "" {
			t.Fatalf("UntrustedFolder = %q on a failed launch", out.UntrustedFolder)
		}
		if len(h.launches) != 0 {
			t.Fatalf("the terminal opened: %+v", h.launches)
		}
	})
}
