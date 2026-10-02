package terminal

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// DFLT-00362: the tab is not tried while the screen is locked, a failed
// tab says what the script saw (diagnostics), a tab that opens late is
// still used, and a launch that tries no tab or a run with no tty says why.

func TestDetectAppleTerminalTTYWithReason(t *testing.T) {
	deep := map[int]string{}
	for pid := 1000; pid > 900; pid-- {
		deep[pid] = fmt.Sprintf("?? %d", pid-1)
	}
	cases := []struct {
		name       string
		env        map[string]string
		goos       string
		tree       map[int]string
		wantTTY    string
		wantReason string
	}{
		{name: "found", env: appleTerminalEnv, tree: map[int]string{1000: "?? 999", 999: "ttys004 1"}, wantTTY: "/dev/ttys004"},
		{name: "not macOS", env: appleTerminalEnv, goos: "linux", tree: map[int]string{1000: "ttys001 1"}, wantReason: TTYReasonNotDarwin},
		{name: "iTerm", env: map[string]string{"TERM_PROGRAM": "iTerm.app"}, tree: map[int]string{1000: "ttys001 1"}, wantReason: TTYReasonTermProgram},
		{name: "no TERM_PROGRAM", env: map[string]string{}, tree: map[int]string{1000: "ttys001 1"}, wantReason: TTYReasonTermProgram},
		{name: "tmux", env: map[string]string{"TERM_PROGRAM": "Apple_Terminal", "TMUX": "/tmp/tmux,1,0"}, tree: map[int]string{1000: "ttys001 1"}, wantReason: TTYReasonTmux},
		{name: "ps refused", env: appleTerminalEnv, tree: map[int]string{}, wantReason: TTYReasonPSFailed},
		{name: "garbled ps output", env: appleTerminalEnv, tree: map[int]string{1000: "ttys001"}, wantReason: TTYReasonPSUnparsable},
		{name: "unparsable ppid", env: appleTerminalEnv, tree: map[int]string{1000: "?? abc"}, wantReason: TTYReasonPSUnparsable},
		{name: "unexpected tty form", env: appleTerminalEnv, tree: map[int]string{1000: "console 1"}, wantReason: TTYReasonInvalidTTY},
		{name: "no tty up to pid 1", env: appleTerminalEnv, tree: map[int]string{1000: "?? 999", 999: "?? 1"}, wantReason: TTYReasonNotFound},
		{name: "hop limit", env: appleTerminalEnv, tree: deep, wantReason: TTYReasonNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fakeProcessTree(t, tc.env, 1000, tc.tree)
			if tc.goos != "" {
				goos = tc.goos
			}
			tty, reason := DetectAppleTerminalTTYWithReason()
			if tty != tc.wantTTY || reason != tc.wantReason {
				t.Fatalf("got (%q, %q), want (%q, %q)", tty, reason, tc.wantTTY, tc.wantReason)
			}
			if DetectAppleTerminalTTY() != tc.wantTTY {
				t.Fatal("DetectAppleTerminalTTY disagrees")
			}
		})
	}
}

// The skip reasons are checked in a fixed order: the first that applies
// is the one reported.
func TestAppleTerminalTabSkipReason_Order(t *testing.T) {
	cases := []struct {
		name string
		goos string
		tmux string
		cfg  Config
		opts LaunchOptions
		want string
	}{
		{"everything wrong on linux", "linux", "x", Config{TerminalCommand: "c"}, LaunchOptions{SkipAppleTerminalTab: true}, NoTabNotDarwin},
		{"terminalCommand before tmux", "darwin", "x", Config{TerminalCommand: "c"}, LaunchOptions{SkipAppleTerminalTab: true}, NoTabTerminalCommand},
		{"tmux before disabled", "darwin", "x", Config{}, LaunchOptions{SkipAppleTerminalTab: true}, NoTabTmux},
		{"disabled before no tty", "darwin", "", Config{}, LaunchOptions{SkipAppleTerminalTab: true}, NoTabDisabled},
		{"disabled before invalid tty", "darwin", "", Config{}, LaunchOptions{SkipAppleTerminalTab: true, AppleTerminalTTY: "bogus"}, NoTabDisabled},
		{"no tty", "darwin", "", Config{}, LaunchOptions{}, NoTabNoTTY},
		{"invalid tty", "darwin", "", Config{}, LaunchOptions{AppleTerminalTTY: "/dev/pts/1"}, NoTabInvalidTTY},
		{"tab", "darwin", "", Config{}, LaunchOptions{AppleTerminalTTY: testTTY}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			oldGoos, oldGetenv := goos, getenv
			goos = tc.goos
			getenv = func(k string) string {
				if k == "TMUX" {
					return tc.tmux
				}
				return ""
			}
			defer func() { goos, getenv = oldGoos, oldGetenv }()
			if got := appleTerminalTabSkipReason(tc.cfg, tc.opts); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			if useAppleTerminalTab(tc.cfg, tc.opts) != (tc.want == "") {
				t.Fatal("useAppleTerminalTab disagrees")
			}
		})
	}
}

// While the screen is locked no script runs (no activate, no Cmd+T to the
// lock screen): the session opens in a new window at once, and the tab
// stays enabled.
func TestLaunchWithOptions_ScreenLockedSkipsTheScript(t *testing.T) {
	onDarwin(t)
	screenLockState = func() string { return ScreenLocked }
	calls := fakeCommands(t, nil)
	out, err := launchTab(t, Config{}, LaunchOptions{AppleTerminalTTY: testTTY})
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatalf("calls = %+v, want only open", *calls)
	}
	openScriptOf(t, (*calls)[0])
	want := TabFailure{Kind: TabFailureScreenLocked, Diagnostics: TabDiagnostics{ScreenLock: ScreenLocked}}
	if out.UsedTab || out.DisableTab || out.TabFailure == nil || *out.TabFailure != want || !strings.Contains(out.TabError, "screen is locked") {
		t.Fatalf("outcome = %+v (failure %+v)", out, out.TabFailure)
	}
	if out.TabFailure.EmptyTabPossible() || out.TabFailure.SlowTimeout() {
		t.Fatal("a skipped script leaves no tab and is no timeout")
	}
}

// An unknown lock state tries the tab as before.
func TestLaunchWithOptions_UnknownScreenLockTriesTheTab(t *testing.T) {
	onDarwin(t)
	screenLockState = func() string { return ScreenLockUnknown }
	calls := fakeCommands(t, nil)
	out, err := launchTab(t, Config{}, LaunchOptions{AppleTerminalTTY: testTTY})
	if err != nil || !out.UsedTab || len(*calls) != 1 || (*calls)[0].Name != "osascript" {
		t.Fatalf("outcome = %+v, err %v, calls %+v", out, err, *calls)
	}
}

// A failure carries what the script logged, sanitized, and the lock state
// read before and after; the diagnostics lines stay out of TabError.
func TestLaunchWithOptions_FailureDiagnostics(t *testing.T) {
	onDarwin(t)
	reads := 0
	screenLockState = func() string {
		reads++
		if reads == 1 {
			return ScreenUnlocked
		}
		return ScreenLocked
	}
	out9102 := phaseOutput(TabPhaseKeystrokeSent,
		tabDiagPrefix+"frontmost_app_before=ターミナル\n"+
			tabDiagPrefix+"tabs_before=12\n"+
			tabDiagPrefix+"frontmost_app=Google\x1b[31m Chrome‮\n"+
			tabDiagPrefix+"front_window_id=55704\n"+
			tabDiagPrefix+"front_window_bounds={249, 301, 969, 806}\n"+
			tabDiagPrefix+"target_window_id=55701\n"+
			tabDiagPrefix+"target_window_bounds={249, 301, 969, 806}\n"+
			tabDiagPrefix+"tabs_after=12\n"+
			tabDiagPrefix+"bogus_key=x\n"+
			"54:97: execution error: graph-ops: no new tab appeared in the Terminal window of /dev/ttys003 (9102)\n")
	fakeCommands(t, func(ctx context.Context, name string, args []string) ([]byte, error) {
		if name == "osascript" {
			return exitFailure(out9102)(ctx, name, args)
		}
		return nil, nil
	})
	out, err := launchTab(t, Config{}, LaunchOptions{AppleTerminalTTY: testTTY})
	if err != nil {
		t.Fatal(err)
	}
	want := TabDiagnostics{
		ScreenLock: ScreenUnlocked, ScreenLockAfter: ScreenLocked,
		FrontmostApp: "Google[31m Chrome", FrontmostAppBefore: "ターミナル",
		FrontWindowID: "55704", FrontWindowBounds: "{249, 301, 969, 806}",
		TargetWindowID: "55701", TargetWindowBounds: "{249, 301, 969, 806}",
		TabsBefore: "12", TabsAfter: "12",
	}
	if out.TabFailure == nil || out.TabFailure.Diagnostics != want || out.TabFailure.ErrorNumber != 9102 {
		t.Fatalf("failure = %+v\nwant diagnostics %+v", out.TabFailure, want)
	}
	if strings.Contains(out.TabError, tabDiagPrefix) || strings.Contains(out.TabError, "Chrome") {
		t.Fatalf("TabError %q carries diagnostics lines", out.TabError)
	}
}

// A tab found only in the last look is used, and the outcome says so.
func TestLaunchWithOptions_LateTabIsUsed(t *testing.T) {
	onDarwin(t)
	fakeCommands(t, func(ctx context.Context, name string, args []string) ([]byte, error) {
		return []byte(phaseOutput(TabPhaseKeystrokeSent, tabDiagPrefix+"late_tab=true\n") + tabPhasePrefix + TabPhaseTabFound + "\n" + tabPhasePrefix + TabPhaseCommandSending + "\n"), nil
	})
	out, err := launchTab(t, Config{}, LaunchOptions{AppleTerminalTTY: testTTY})
	if err != nil {
		t.Fatal(err)
	}
	if out != (LaunchOutcome{UsedTab: true, LateTab: true}) {
		t.Fatalf("outcome = %+v", out)
	}
}

func TestParseTabDiagnostics(t *testing.T) {
	long := strings.Repeat("x", 300)
	d := parseTabDiagnostics(strings.Join([]string{
		"  " + tabDiagPrefix + "frontmost_app=first",
		tabDiagPrefix + "frontmost_app=second",
		tabDiagPrefix + "front_window_id=" + long,
		tabDiagPrefix + "no equals sign",
		"frontmost_app_before=not a diag line",
		tabDiagPrefix + "late_tab=yes",
		tabDiagPrefix + "target_window_bounds=a=b",
	}, "\n"))
	if d.FrontmostApp != "second" || len([]rune(d.FrontWindowID)) != 128 || d.FrontmostAppBefore != "" || d.LateTab || d.TargetWindowBounds != "a=b" {
		t.Fatalf("diagnostics = %+v", d)
	}
	if phase, rest := splitTabScriptOutput(tabDiagPrefix + "frontmost_app=x\n" + tabPhasePrefix + "frontmost\nboom"); phase != TabPhaseFrontmost || rest != "boom" {
		t.Fatalf("split = (%q, %q)", phase, rest)
	}
}

func TestAppleTerminalTabScript_Diagnostics(t *testing.T) {
	script := appleTerminalTabScript
	diag := scriptSection(t, "on diagnose(targetID)", "end diagnose")
	for _, key := range []string{"frontmost_app", "front_window_id", "front_window_bounds", "target_window_id", "target_window_bounds", "tabs_after"} {
		if !strings.Contains(diag, `log "`+tabDiagPrefix+key+`=" & `) {
			t.Errorf("diagnose does not log %s", key)
		}
	}
	if !strings.Contains(diag, "with timeout of 1 second") || !strings.Contains(scriptSection(t, "on frontmostAppName()", "end frontmostAppName"), "with timeout of 1 second") {
		t.Error("the diagnostics' Apple events must be bounded to 1 second")
	}
	if strings.Count(diag, "try") < 2*4 {
		t.Error("every read of diagnose must be in a try")
	}
	// Before the 9103 (after the rethrow of a read error, which keeps its
	// number) and before each 9102.
	front := scriptSection(t, "if not inFront then", "end if\n\tlog \"graph-ops-phase: frontmost\"")
	if strings.Index(front, "number lastErrNum") > strings.Index(front, "my diagnose(targetID)") || strings.Index(front, "my diagnose(targetID)") > strings.Index(front, "number 9103") {
		t.Errorf("the 9103 must be preceded by diagnose, after the rethrow: %q", front)
	}
	if n := strings.Count(script, "my diagnose(targetID)"); n != 4 {
		t.Errorf("diagnose is called %d times, want 4 (the 9103 and three 9102s)", n)
	}
	for _, marker := range []string{
		"my diagnose(targetID)\n\t\tif lastReadErr is not \"\" then error",
		"my diagnose(targetID)\n\t\terror \"graph-ops: the new tab in the Terminal window of \" & targetTTY & \" closed\" number 9102",
		"my diagnose(targetID)\n\t\terror \"graph-ops: the new tab did not open",
	} {
		if !strings.Contains(script, marker) {
			t.Errorf("the script lacks %q", marker)
		}
	}
	// Just before Cmd+T: the frontmost app and the number of tabs.
	before := scriptSection(t, "delay 0.2", `keystroke "t"`)
	if !strings.Contains(before, `log "`+tabDiagPrefix+`frontmost_app_before=" & my frontmostAppName()`) || !strings.Contains(before, `log "`+tabDiagPrefix+`tabs_before=`) {
		t.Errorf("the frontmost app and the tabs must be logged before Cmd+T: %q", before)
	}
}

// The last look 2 seconds after the wait: one new tab is taken (and logged
// as late), more than one is 9104, a failed read is kept for the 9102.
func TestAppleTerminalTabScript_LateLook(t *testing.T) {
	late := scriptSection(t, "delay 2", "my diagnose(targetID)")
	for _, want := range []string{
		"set lateTTYs to my freshTTYsOf(my terminalTTYs(), knownTTYs)",
		"set lastReadErr to errMsg",
		"if (count of lateTTYs) > 1 then error",
		"number 9104",
		"set newTTY to item 1 of lateTTYs",
		`log "` + tabDiagPrefix + `late_tab=true"`,
	} {
		if !strings.Contains(late, want) {
			t.Errorf("the last look lacks %q", want)
		}
	}
	if i, j := strings.Index(appleTerminalTabScript, "repeat 30 times"), strings.Index(appleTerminalTabScript, "delay 2"); i < 0 || j < i {
		t.Error("the last look must come after the 6-second wait")
	}
}

func TestScreenLockFromIORegPlist(t *testing.T) {
	session := func(uid int, extra string) string {
		return fmt.Sprintf(`<dict><key>kCGSSessionUserIDKey</key><integer>%d</integer><key>kCGSSessionOnConsoleKey</key><true/>%s</dict>`, uid, extra)
	}
	doc := func(users string) []byte {
		return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict><key>IOKitBuildVersion</key><string>x</string><key>IOConsoleUsers</key><array>` + users + `</array><key>IORegistryEntryName</key><string>Root</string></dict></plist>`)
	}
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"unlocked (no key)", doc(session(501, "")), ScreenUnlocked},
		{"locked", doc(session(501, `<key>CGSSessionScreenIsLocked</key><true/>`)), ScreenLocked},
		{"locked as 1", doc(session(501, `<key>CGSSessionScreenIsLocked</key><integer>1</integer>`)), ScreenLocked},
		{"explicitly false", doc(session(501, `<key>CGSSessionScreenIsLocked</key><false/>`)), ScreenUnlocked},
		{"another user's lock", doc(session(502, `<key>CGSSessionScreenIsLocked</key><true/>`) + session(501, "")), ScreenUnlocked},
		{"ours among others", doc(session(502, "") + session(501, `<key>CGSSessionScreenIsLocked</key><true/>`)), ScreenLocked},
		{"no console users", []byte(`<plist version="1.0"><dict><key>IORegistryEntryName</key><string>Root</string></dict></plist>`), ScreenLockUnknown},
		{"not a plist", []byte("garbage"), ScreenLockUnknown},
		{"empty", nil, ScreenLockUnknown},
		{"truncated", doc(session(501, ""))[:120], ScreenLockUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := screenLockFromIORegPlist(tc.data, 501); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
