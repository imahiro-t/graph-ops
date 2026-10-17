package terminal

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// DFLT-00379: the stray tabs that appear after the tab script's 1 second are
// looked for afterwards with ps (FindLateStrayTTYs): an empty Terminal.app
// tab -- a tty whose oldest process is a login started by Terminal.app,
// with nothing but login and shells on it -- that opened in the given span.

var strayNow = time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)

// strayPS is a ps output modelled on a real one (Terminal.app's pid 643).
// Elapsed times count back from strayNow:
//   - ttys000: the orchestrator's tab, running claude, opened long ago;
//   - ttys002: a shell of another terminal app (VS Code, tmux: no login);
//   - ttys003: an empty Terminal.app tab opened 30 seconds ago;
//   - ttys004: a Terminal.app tab opened 30 seconds ago, running claude;
//   - ttys005: an empty Terminal.app tab opened 10 minutes ago;
//   - ttys006: an empty tab whose login is not Terminal.app's (iTerm2);
//   - ttys007: an empty Terminal.app tab opened 5 seconds ago;
//   - ttys008: an empty Terminal.app tab opened 2 days ago.
const strayPS = `??         643     1 02-09:50:39 /System/Applications/Utilities/Terminal.app/Contents/MacOS/Terminal
??         900     1 02-09:50:39 /Applications/iTerm.app/Contents/MacOS/iTerm2
ttys000  76807   643    01:49:46 login -pf someone
ttys000  76808 76807    01:49:46 -zsh
ttys000  76841 76808    01:49:45 /bin/bash /var/folders/x/T/graph-engine-launch-1.command
ttys000  76845 76841    01:49:45 claude --permission-mode auto /graph-ops:autopilot-tree X-1 --run run-1
ttys002   1372  1022 02-09:50:23 /bin/zsh -il
ttys003  79526   643       00:30 login -pf someone
ttys003  79527 79526       00:30 -zsh
ttys004  79600   643       00:30 login -pf someone
ttys004  79601 79600       00:30 -zsh
ttys004  79602 79601       00:29 /bin/bash /var/folders/x/T/graph-engine-launch-2.command
ttys004  79603 79602       00:29 claude --permission-mode auto /graph-ops:autopilot-worker run-1 X-2 --role work
ttys005  79700   643       10:00 login -pf someone
ttys005  79701 79700       10:00 -bash
ttys006  79800   900       00:30 login -pf someone
ttys006  79801 79800       00:30 -zsh
ttys007  79900   643       00:05 login -pf someone
ttys007  79901 79900       00:04 /bin/zsh
ttys008  80000   643 02-00:00:00 login -pf someone
ttys008  80001 80000 02-00:00:00 -fish
`

func TestParseStrayCandidates(t *testing.T) {
	cases := []struct {
		name        string
		output      string
		from, until time.Time
		exclude     []string
		want        []string
	}{
		{
			name: "an empty Terminal.app tab in the span",
			from: strayNow.Add(-40 * time.Second), until: strayNow.Add(-20 * time.Second),
			want: []string{"/dev/ttys003"},
		},
		{
			name: "the span takes in several",
			from: strayNow.Add(-11 * time.Minute), until: strayNow,
			want: []string{"/dev/ttys003", "/dev/ttys005", "/dev/ttys007"},
		},
		{
			name: "elapsed in days",
			from: strayNow.Add(-49 * time.Hour), until: strayNow.Add(-47 * time.Hour),
			want: []string{"/dev/ttys008"},
		},
		{
			name: "elapsed in hours",
			from: strayNow.Add(-2 * time.Hour), until: strayNow.Add(-time.Hour),
			want: nil, // ttys000 runs claude
		},
		{
			name: "too old and too new are left out",
			from: strayNow.Add(-20 * time.Second), until: strayNow.Add(-10 * time.Second),
			want: nil,
		},
		{
			name: "one second of room before from for etime",
			from: strayNow.Add(-29 * time.Second), until: strayNow.Add(-20 * time.Second),
			want: []string{"/dev/ttys003"},
		},
		{
			name: "one second of room after until for etime",
			from: strayNow.Add(-40 * time.Second), until: strayNow.Add(-31 * time.Second),
			want: []string{"/dev/ttys003"},
		},
		{
			name: "no more than one second of room",
			from: strayNow.Add(-28 * time.Second), until: strayNow.Add(-20 * time.Second),
			want: nil,
		},
		{
			name: "excluded ttys are left out",
			from: strayNow.Add(-11 * time.Minute), until: strayNow,
			exclude: []string{"/dev/ttys003", "/dev/ttys007"},
			want:    []string{"/dev/ttys005"},
		},
		{
			name:   "no Terminal.app process: nothing",
			output: strings.Replace(strayPS, "/System/Applications/Utilities/Terminal.app/Contents/MacOS/Terminal", "/usr/sbin/somethingelse", 1),
			from:   strayNow.Add(-11 * time.Minute), until: strayNow,
			want: nil,
		},
		{
			name:   "warnings and short lines mixed in are skipped",
			output: "ps: some warning on stderr\n\nttys009 1 2\n" + strayPS + "garbage line\n",
			from:   strayNow.Add(-40 * time.Second), until: strayNow.Add(-20 * time.Second),
			want: []string{"/dev/ttys003"},
		},
		{
			name: "a tty that is not ttysN is never taken",
			output: strayPS + "pts/1    81000   643       00:30 login -pf someone\n" +
				"console  81001   643       00:30 login -pf someone\n",
			from: strayNow.Add(-40 * time.Second), until: strayNow.Add(-20 * time.Second),
			want: []string{"/dev/ttys003"},
		},
		{
			name:   "a helper process left on the tty keeps it from being taken",
			output: strayPS + "ttys003  79530 79527       00:29 gitstatusd-darwin-arm64 -s -1\n",
			from:   strayNow.Add(-40 * time.Second), until: strayNow.Add(-20 * time.Second),
			want: nil,
		},
		{
			name: "a tty whose oldest process is a shell is not a Terminal.app tab",
			output: strayPS + "ttys010  81100  1022       00:31 -zsh\n" +
				"ttys010  81101   643       00:30 login -pf someone\n",
			from: strayNow.Add(-40 * time.Second), until: strayNow.Add(-20 * time.Second),
			want: []string{"/dev/ttys003"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			output := tc.output
			if output == "" {
				output = strayPS
			}
			got, err := parseStrayCandidates(output, tc.from, tc.until, tc.exclude, strayNow)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseStrayCandidates_Unparsable(t *testing.T) {
	for _, output := range []string{"", "ps: illegal option\nusage: ps ...\n", "ttys003 x y 00:30 login\n"} {
		if got, err := parseStrayCandidates(output, strayNow.Add(-time.Minute), strayNow, nil, strayNow); err == nil {
			t.Errorf("%q: want an error, got %q", output, got)
		}
	}
}

func TestParseEtime(t *testing.T) {
	cases := map[string]time.Duration{
		"00:05":       5 * time.Second,
		"10:00":       10 * time.Minute,
		"01:49:46":    time.Hour + 49*time.Minute + 46*time.Second,
		"02-09:50:39": 2*24*time.Hour + 9*time.Hour + 50*time.Minute + 39*time.Second,
	}
	for in, want := range cases {
		if got, ok := parseEtime(in); !ok || got != want {
			t.Errorf("parseEtime(%q) = %v, %v; want %v", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "5", "1:2:3:4", "02-10:00", "a:b", "-1:00", "1-", ":30"} {
		if got, ok := parseEtime(in); ok {
			t.Errorf("parseEtime(%q) = %v, want not ok", in, got)
		}
	}
}

func TestFindLateStrayTTYs_RunsPSOnce(t *testing.T) {
	var calls [][]string
	fakeCommands(t, func(ctx context.Context, name string, args []string) ([]byte, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("ps must run under a timeout")
		}
		calls = append(calls, append([]string{name}, args...))
		return []byte(strayPS), nil
	})
	got, err := FindLateStrayTTYs(strayNow.Add(-40*time.Second), strayNow.Add(-20*time.Second), []string{"/dev/ttys000"}, strayNow)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"/dev/ttys003"}) {
		t.Fatalf("got %q", got)
	}
	want := [][]string{{"ps", "-A", "-o", "tty=,pid=,ppid=,etime=,command="}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %q", calls)
	}
}

func TestFindLateStrayTTYs_PSFails(t *testing.T) {
	fakeCommands(t, func(ctx context.Context, name string, args []string) ([]byte, error) {
		return []byte("ps: operation not permitted"), errors.New("exit status 1")
	})
	if got, err := FindLateStrayTTYs(strayNow.Add(-time.Minute), strayNow, nil, strayNow); err == nil {
		t.Fatalf("want an error, got %q", got)
	}
}

// The tab script logs the tty of the tab it took right before `do script`,
// after the stray tab check, and the launch passes it on.
func TestAppleTerminalTabScript_LogsTheNewTTY(t *testing.T) {
	logNewTTY := `log "graph-ops-diag: new_tty=" & newTTY`
	if strings.Count(appleTerminalTabScript, logNewTTY) != 1 {
		t.Fatal("new_tty must be logged once")
	}
	stray := strings.Index(appleTerminalTabScript, `if strayTab then log "graph-ops-diag: stray_tab=true"`)
	newTTY := strings.Index(appleTerminalTabScript, logNewTTY)
	sending := strings.Index(appleTerminalTabScript, `log "graph-ops-phase: command-sending"`)
	if stray < 0 || !(stray < newTTY && newTTY < sending) {
		t.Fatalf("new_tty must be logged after the stray tab check and before command-sending (%d, %d, %d)", stray, newTTY, sending)
	}
	if !strings.HasSuffix(appleTerminalTabScript, "\t"+logNewTTY+"\n\tlog \"graph-ops-phase: command-sending\"\n\ttell application \"Terminal\" to do script shellCommand in newTab\nend run") {
		t.Error("new_tty must directly precede command-sending, and do script stay the last statement")
	}
}

func TestParseTabDiagnostics_NewTTY(t *testing.T) {
	cases := map[string]string{
		"/dev/ttys012":           "/dev/ttys012",
		"/dev/pts/3":             "",
		"ttys012":                "",
		"/dev/ttys012; rm -rf /": "",
		"missing value":          "",
	}
	for in, want := range cases {
		if got := parseTabDiagnostics(tabDiagPrefix + "new_tty=" + in).NewTTY; got != want {
			t.Errorf("new_tty=%q: got %q, want %q", in, got, want)
		}
	}
	// An invalid value after a valid one does not keep the earlier one.
	if got := parseTabDiagnostics(tabDiagPrefix + "new_tty=/dev/ttys001\n" + tabDiagPrefix + "new_tty=bogus").NewTTY; got != "" {
		t.Errorf("got %q", got)
	}
}

func TestLaunchWithOptions_PassesTheNewTTYOn(t *testing.T) {
	onDarwin(t)
	fakeCommands(t, func(ctx context.Context, name string, args []string) ([]byte, error) {
		return []byte(menuOutput(true, tabDiagPrefix+"retried=true\n"+tabDiagPrefix+"open_method=keystroke-retry\n"+
			tabPhasePrefix+TabPhaseTabFound+"\n"+tabDiagPrefix+"new_tty=/dev/ttys021\n"+tabPhasePrefix+TabPhaseCommandSending+"\n")), nil
	})
	out, err := launchTab(t, Config{}, LaunchOptions{AppleTerminalTTY: testTTY})
	if err != nil {
		t.Fatal(err)
	}
	want := LaunchOutcome{UsedTab: true, RetriedTab: true, OpenMethod: "keystroke-retry", NewTTY: "/dev/ttys021"}
	if out != want {
		t.Fatalf("outcome = %+v, want %+v", out, want)
	}
}
