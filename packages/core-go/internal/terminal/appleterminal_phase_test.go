package terminal

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// DFLT-00361: the tab script finds windows with bulk reads instead of one
// Apple event per tab, compares bounds with a tolerance, and logs its
// progress; a failed launch reports how far the script got, and a timeout
// only disables the tab while it may be a permission prompt.

// phaseOutput is osascript's output of a script that logged phases up to
// and including last, followed by tail.
func phaseOutput(last, tail string) string {
	if last == "" {
		return tail
	}
	var b strings.Builder
	for _, p := range tabPhases {
		b.WriteString(tabPhasePrefix + p + "\n")
		if p == last {
			break
		}
	}
	return b.String() + tail
}

// scriptSection returns the script between the first from and the next to.
func scriptSection(t *testing.T, from, to string) string {
	t.Helper()
	script := appleTerminalTabScript
	i := strings.Index(script, from)
	if i < 0 {
		t.Fatalf("the script lacks %q", from)
	}
	j := strings.Index(script[i:], to)
	if j < 0 {
		t.Fatalf("the script lacks %q after %q", to, from)
	}
	return script[i : i+j]
}

func TestAppleTerminalTabScript_BulkWindowLookup(t *testing.T) {
	script := appleTerminalTabScript
	lookup := scriptSection(t, "on windowOfTTY(theTTY)", "end windowOfTTY")
	for _, want := range []string{
		"repeat 2 times",
		"set winIDs to id of every window",
		"set winTTYs to tty of every tab of every window",
		"(count of winIDs) is (count of winTTYs)",
		"if class of e is not list then set e to {e}",
		"if e contains theTTY then",
		"tty of every tab of window id foundID",
		"if checkTTYs contains theTTY then return foundID",
		"if foundID is missing value then return missing value",
	} {
		if !strings.Contains(lookup, want) {
			t.Errorf("windowOfTTY lacks %q", want)
		}
	}
	// Asking tab by tab is only the fallback, after both bulk tries, and
	// nowhere else in the script.
	if strings.Count(script, "repeat with t in tabs of w") != 1 || !strings.Contains(lookup, "repeat with t in tabs of w") {
		t.Error("the tab-by-tab walk must only be windowOfTTY's fallback")
	}
	if strings.Index(lookup, "repeat with w in windows") < strings.Index(lookup, "if checkTTYs contains theTTY") {
		t.Error("the tab-by-tab fallback must come after the bulk reads")
	}
	for _, want := range []string{
		"set targetID to my windowOfTTY(targetTTY)",
		"set newID to my windowOfTTY(newTTY)",
	} {
		if strings.Count(script, want) != 1 {
			t.Errorf("the script must contain %q exactly once", want)
		}
	}
}

func TestAppleTerminalTabScript_BoundsTolerance(t *testing.T) {
	cmp := scriptSection(t, "on sameWindowBounds(a, b)", "end sameWindowBounds")
	for _, want := range []string{"repeat with i from 1 to 3", "if d < 0 then set d to -d", "if d > 4 then return false", "return true"} {
		if !strings.Contains(cmp, want) {
			t.Errorf("sameWindowBounds lacks %q", want)
		}
	}
	check := scriptSection(t, "set sameWindow to false", "tell application \"Terminal\" to do script")
	for _, want := range []string{"repeat 20 times", "my boundsText(targetBounds)", "my boundsText(newBounds)", `(bounds " & boundsNote & ")" number 9102`} {
		if !strings.Contains(check, want) {
			t.Errorf("the bounds check lacks %q", want)
		}
	}
	if strings.Contains(appleTerminalTabScript, "newBounds is targetBounds") {
		t.Error("the bounds must no longer be compared exactly")
	}
}

func TestAppleTerminalTabScript_NewTabPollKeepsReadErrors(t *testing.T) {
	poll := scriptSection(t, "repeat 30 times", "set newID to")
	if strings.Contains(poll, "set nowTTYs to {}") {
		t.Error("a failed read of the ttys must not count as no tabs")
	}
	for _, want := range []string{"set lastReadErr to errMsg", "if nowTTYs is not missing value then", `the last failed read of Terminal's tabs: " & lastReadErr number 9102`} {
		if !strings.Contains(poll, want) {
			t.Errorf("the new-tab poll lacks %q", want)
		}
	}
}

func TestAppleTerminalTabScript_Phases(t *testing.T) {
	script := appleTerminalTabScript
	lines := strings.Split(script, "\n")
	lineOf := func(marker string) int {
		t.Helper()
		for i, l := range lines {
			if strings.Contains(l, marker) {
				return i
			}
		}
		t.Fatalf("the script lacks %q", marker)
		return -1
	}
	logLine := func(phase string) string { return `log "` + tabPhasePrefix + phase + `"` }
	last := -1
	for _, p := range tabPhases {
		if strings.Count(script, logLine(p)) != 1 {
			t.Errorf("phase %s must be logged exactly once", p)
		}
		i := lineOf(logLine(p))
		if i <= last {
			t.Errorf("phase %s is out of order", p)
		}
		last = i
	}
	if strings.Count(script, tabPhasePrefix) != len(tabPhases) {
		t.Error("every phase line must be one of tabPhases")
	}
	// window-found: right after the 9101 of the orchestrator's window, and
	// before anything else is asked of Terminal.
	if lineOf(logLine(TabPhaseWindowFound)) != lineOf("number 9101")+1 || lineOf(logLine(TabPhaseWindowFound)) > lineOf("set knownTTYs to") {
		t.Error("window-found must follow the lookup of the orchestrator's window directly")
	}
	// system-events-ok: right after the first Apple event to System Events,
	// with none before it.
	se := lineOf(`tell application "System Events" to set frontmost of process "Terminal" to true`)
	if lineOf(logLine(TabPhaseSystemEventsOK)) != se+1 {
		t.Error("system-events-ok must directly follow the first Apple event to System Events")
	}
	if first := lineOf(`application "System Events"`); first != se {
		t.Errorf("the first Apple event to System Events must be the frontmost one (line %d), found one on line %d", se, first)
	}
	// frontmost after the 9103, keystroke-sent right after the keystroke.
	if lineOf(logLine(TabPhaseFrontmost)) < lineOf("number 9103") || lineOf(logLine(TabPhaseFrontmost)) > lineOf(`keystroke "t"`) {
		t.Error("frontmost must be logged between the 9103 and the keystroke")
	}
	if lineOf(logLine(TabPhaseKeystrokeSent)) != lineOf(`keystroke "t"`)+1 {
		t.Error("keystroke-sent must directly follow the keystroke")
	}
	// tab-found after the last 9102, command-sending right before do script,
	// which stays the last statement.
	if lineOf(logLine(TabPhaseTabFound)) < lineOf("if not sameWindow then error") {
		t.Error("tab-found must come after the window check")
	}
	if lineOf(logLine(TabPhaseCommandSending))+1 != lineOf("do script shellCommand in newTab") {
		t.Error("command-sending must directly precede do script")
	}
}

func TestSplitTabScriptOutput(t *testing.T) {
	cases := []struct {
		name, output, phase, rest string
	}{
		{"nothing", "", "", ""},
		{"error only", "0:1: execution error: x (9101)\n", "", "0:1: execution error: x (9101)"},
		{"phases then error", phaseOutput(TabPhaseKeystrokeSent, "54:97: execution error: graph-ops: x (9102)\n"), TabPhaseKeystrokeSent, "54:97: execution error: graph-ops: x (9102)"},
		{"phases only (killed)", phaseOutput(TabPhaseSystemEventsOK, ""), TabPhaseSystemEventsOK, ""},
		{"unknown phase ignored", tabPhasePrefix + "window-found\n" + tabPhasePrefix + "bogus\nboom", TabPhaseWindowFound, "boom"},
		{"other lines kept", "warning\n" + tabPhasePrefix + "frontmost\nexecution error: y (-1743)", TabPhaseFrontmost, "warning execution error: y (-1743)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			phase, rest := splitTabScriptOutput(tc.output)
			if phase != tc.phase || rest != tc.rest {
				t.Fatalf("got (%q, %q), want (%q, %q)", phase, rest, tc.phase, tc.rest)
			}
		})
	}
}

func TestParseTabErrorNumber(t *testing.T) {
	for msg, want := range map[string]int{
		"0:1: execution error: graph-ops: x (9102)":                                    9102,
		"execution error: Not authorized to send Apple events to Terminal. (-1743)":    -1743,
		"execution error: x (bounds target {1, 2, 3, 4}, new tab {5, 6, 7, 8}) (9102)": 9102,
		"something else went wrong":                                                    0,
		"exec: \"osascript\": executable file not found in $PATH":                      0,
	} {
		if got := parseTabErrorNumber(msg); got != want {
			t.Errorf("parseTabErrorNumber(%q) = %d, want %d", msg, got, want)
		}
	}
}

func TestClassifyTabFailure_TimeoutsByPhase(t *testing.T) {
	for _, tc := range []struct {
		phase       string
		wantDisable bool
	}{
		{"", true},
		{TabPhaseWindowFound, true},
		{TabPhaseSystemEventsOK, false},
		{TabPhaseFrontmost, false},
		{TabPhaseKeystrokeSent, false},
		{TabPhaseTabFound, false},
		{TabPhaseCommandSending, false},
	} {
		if got := classifyTabFailure(true, tc.phase, "whatever"); got != tc.wantDisable {
			t.Errorf("timeout after %q: disable = %v, want %v", tc.phase, got, tc.wantDisable)
		}
		f := &TabFailure{Kind: TabFailureTimeout, Phase: tc.phase}
		if f.SlowTimeout() == tc.wantDisable {
			t.Errorf("timeout after %q: SlowTimeout = %v", tc.phase, f.SlowTimeout())
		}
	}
	// Script errors keep the allow list whatever the phase.
	for _, phase := range append([]string{""}, tabPhases...) {
		if classifyTabFailure(false, phase, "execution error: x (9102)") {
			t.Errorf("9102 after %q must not disable the tab", phase)
		}
		if !classifyTabFailure(false, phase, "execution error: x (-1743)") {
			t.Errorf("-1743 after %q must disable the tab", phase)
		}
		if (&TabFailure{Kind: TabFailureScriptError, Phase: phase}).SlowTimeout() {
			t.Errorf("a script error after %q is not a slow timeout", phase)
		}
	}
	var none *TabFailure
	if none.SlowTimeout() || (&TabFailure{Kind: TabFailureLockBusy}).SlowTimeout() {
		t.Error("no failure and lock-busy are not slow timeouts")
	}
}

// timeoutAfter makes osascript log phases up to last and then hang until
// it is killed.
func timeoutAfter(last string) func(ctx context.Context, name string, args []string) ([]byte, error) {
	return func(ctx context.Context, name string, args []string) ([]byte, error) {
		if name == "osascript" {
			<-ctx.Done()
			return []byte(phaseOutput(last, "")), ctx.Err()
		}
		return nil, nil
	}
}

func TestLaunchWithOptions_TabFailureDetails(t *testing.T) {
	cases := []struct {
		name        string
		respond     func(ctx context.Context, name string, args []string) ([]byte, error)
		want        TabFailure
		wantDisable bool
		wantInError string
	}{
		{
			name:    "9102 on the bounds after Cmd+T",
			respond: exitFailure(phaseOutput(TabPhaseKeystrokeSent, "54:97: execution error: graph-ops: the new tab did not open in the Terminal window of /dev/ttys003 (bounds target {0, 25, 800, 600}, new tab {400, 25, 1200, 600}) (9102)")),
			want:    TabFailure{Kind: TabFailureScriptError, Phase: TabPhaseKeystrokeSent, KeystrokeSent: true, ErrorNumber: 9102}, wantInError: "new tab {400, 25, 1200, 600}",
		},
		{
			name:    "9101 before any phase",
			respond: exitFailure("0:1: execution error: graph-ops: no Terminal window has a tab on /dev/ttys003 (9101)"),
			want:    TabFailure{Kind: TabFailureScriptError, ErrorNumber: 9101}, wantInError: "9101",
		},
		{
			name:    "9103 after System Events answered",
			respond: exitFailure(phaseOutput(TabPhaseSystemEventsOK, "0:1: execution error: graph-ops: Terminal did not come to the front with the window of /dev/ttys003, so no key was sent (9103)")),
			want:    TabFailure{Kind: TabFailureScriptError, Phase: TabPhaseSystemEventsOK, ErrorNumber: 9103}, wantInError: "9103",
		},
		{
			name:    "System Events automation refused",
			respond: exitFailure(phaseOutput(TabPhaseWindowFound, "execution error: Not authorized to send Apple events to System Events. (-1743)")),
			want:    TabFailure{Kind: TabFailureScriptError, Phase: TabPhaseWindowFound, ErrorNumber: -1743}, wantDisable: true, wantInError: "-1743",
		},
		{
			name:    "timeout before Terminal answered",
			respond: timeoutAfter(""),
			want:    TabFailure{Kind: TabFailureTimeout}, wantDisable: true, wantInError: "before Terminal answered",
		},
		{
			name:    "timeout waiting on System Events",
			respond: timeoutAfter(TabPhaseWindowFound),
			want:    TabFailure{Kind: TabFailureTimeout, Phase: TabPhaseWindowFound}, wantDisable: true, wantInError: "before System Events did",
		},
		{
			name:    "slow timeout before Cmd+T",
			respond: timeoutAfter(TabPhaseFrontmost),
			want:    TabFailure{Kind: TabFailureTimeout, Phase: TabPhaseFrontmost}, wantInError: "after frontmost (Terminal answering slowly)",
		},
		{
			name:    "slow timeout after Cmd+T",
			respond: timeoutAfter(TabPhaseKeystrokeSent),
			want:    TabFailure{Kind: TabFailureTimeout, Phase: TabPhaseKeystrokeSent, KeystrokeSent: true}, wantInError: "a new tab may have been left empty",
		},
		{
			name:    "slow timeout while sending the command",
			respond: timeoutAfter(TabPhaseCommandSending),
			want:    TabFailure{Kind: TabFailureTimeout, Phase: TabPhaseCommandSending, KeystrokeSent: true}, wantInError: "may have started in the new tab as well",
		},
		{
			name: "osascript missing",
			respond: func(ctx context.Context, name string, args []string) ([]byte, error) {
				if name == "osascript" {
					return nil, &exec.Error{Name: "osascript", Err: exec.ErrNotFound}
				}
				return nil, nil
			},
			want: TabFailure{Kind: TabFailureScriptError}, wantDisable: true, wantInError: "executable file not found",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			onDarwin(t)
			oldTimeout := tabScriptTimeout
			tabScriptTimeout = 20 * time.Millisecond
			defer func() { tabScriptTimeout = oldTimeout }()
			calls := fakeCommands(t, tc.respond)

			out, err := launchTab(t, Config{}, LaunchOptions{AppleTerminalTTY: testTTY})
			if err != nil {
				t.Fatal(err)
			}
			if out.TabFailure == nil || *out.TabFailure != tc.want {
				t.Fatalf("failure = %+v, want %+v", out.TabFailure, tc.want)
			}
			if out.UsedTab || out.DisableTab != tc.wantDisable || !strings.Contains(out.TabError, tc.wantInError) {
				t.Fatalf("outcome = %+v, want DisableTab=%v and %q in TabError", out, tc.wantDisable, tc.wantInError)
			}
			if strings.Contains(out.TabError, tabPhasePrefix) {
				t.Fatalf("TabError %q carries phase lines", out.TabError)
			}
			if len(*calls) != 2 || (*calls)[1].Name != "open" {
				t.Fatalf("calls = %+v, want osascript then open", *calls)
			}
		})
	}
}

// A tab that opens reports no failure, whatever phases the script logged.
func TestLaunchWithOptions_TabOpenedHasNoFailure(t *testing.T) {
	onDarwin(t)
	fakeCommands(t, func(ctx context.Context, name string, args []string) ([]byte, error) {
		return []byte(phaseOutput(TabPhaseCommandSending, "")), nil
	})
	out, err := launchTab(t, Config{}, LaunchOptions{AppleTerminalTTY: testTTY})
	if err != nil {
		t.Fatal(err)
	}
	if out != (LaunchOutcome{UsedTab: true}) {
		t.Fatalf("outcome = %+v", out)
	}
}
