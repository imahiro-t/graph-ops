package terminal

import (
	"context"
	"fmt"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// DFLT-00377: the tab is opened by pressing Terminal's New Tab menu item
// (found by its Cmd+T shortcut) rather than by a synthesized Cmd+T, the
// front is checked again right before, and the script tries once more when
// no tab appeared. A failure also records the input state around it.

func TestAppleTerminalTabScript_NewTabMenuItemLookup(t *testing.T) {
	lookup := scriptSection(t, "on newTabMenuItem()", "end newTabMenuItem")
	for _, want := range []string{
		// By shortcut, never by title (the titles depend on the language),
		// at the top level of each menu and one submenu down.
		`value of attribute "AXMenuItemCmdChar" of menu items of menu 1 of menu bar item i of menu bar 1`,
		`value of attribute "AXMenuItemCmdModifiers" of menu items of menu 1 of menu bar item i of menu bar 1`,
		`value of attribute "AXMenuItemCmdChar" of menu items of menu 1 of menu items of menu 1 of menu bar item i of menu bar 1`,
		`value of attribute "AXMenuItemCmdModifiers" of menu items of menu 1 of menu items of menu 1 of menu bar item i of menu bar 1`,
		`my isCmdKey(item j of c1, item j of m1, "T")`,
		`my isCmdKey(item k of s, item k of sm, "T")`,
		// Only in the menu that also has Cmd+N, only when it is the only
		// one, and never when another menu has a Cmd+T item.
		`my isCmdKey(item j of c1, item j of m1, "N")`,
		`my isCmdKey(item k of s, item k of sm, "N")`,
		"if hasNewWindow then",
		"set elsewhere to true",
		"if elsewhere or (count of found) is not 1 then return missing value",
		// Bounded: each read to 1 second, the whole lookup to about 2.
		"with timeout of 1 second",
		"((current date) - startedAt) > 2 then return missing value",
		// Any failure (Accessibility refused, a menu that cannot be read)
		// means no item: the script then sends Cmd+T as before.
		"on error\n\t\treturn missing value",
	} {
		if !strings.Contains(lookup, want) {
			t.Errorf("newTabMenuItem lacks %q", want)
		}
	}
	if strings.Contains(lookup, "name of") || strings.Contains(lookup, "title") {
		t.Error("newTabMenuItem must not look items up by their names")
	}
	key := scriptSection(t, "on isCmdKey(theChar, theMods, theKey)", "end isCmdKey")
	if !strings.Contains(key, "(theChar as text) is theKey and (theMods as integer) is 0") {
		t.Errorf("isCmdKey must take the key with Cmd alone (modifiers 0): %q", key)
	}
	// Looked up once, after System Events first answered (the permission
	// prompt stays at the frontmost statement) and while Terminal comes to
	// the front, before the wait for it.
	run := scriptSection(t, "on run argv", "end run")
	if strings.Count(run, "my newTabMenuItem()") != 1 {
		t.Fatal("the menu item must be looked up exactly once")
	}
	if i, j, k := strings.Index(run, `log "graph-ops-phase: system-events-ok"`), strings.Index(run, "set menuItem to my newTabMenuItem()"), strings.Index(run, "set w to my waitForFront(targetID, frontPolls)"); !(i < j && j < k) {
		t.Error("the menu item must be looked up after system-events-ok and before the wait for the front")
	}
}

func TestAppleTerminalTabScript_OpensAfterCheckingTheFrontAgain(t *testing.T) {
	run := scriptSection(t, "on run argv", "end run")
	attempt := scriptSection(t, "repeat with attempt from 1 to 2", "repeat polls times")
	// The check right before: Terminal frontmost with the target window in
	// front, and nothing that waits between it and the press or keystroke.
	check := scriptSection(t, "on targetInFront(targetID)", "end targetInFront")
	for _, want := range []string{`frontmost of process "Terminal"`, "id of front window", "return terminalFront and frontID is targetID", "return false"} {
		if !strings.Contains(check, want) {
			t.Errorf("targetInFront lacks %q", want)
		}
	}
	afterCheck := attempt[strings.Index(attempt, "end repeat"):]
	if strings.Contains(afterCheck, "delay") || strings.Contains(afterCheck, "frontmostAppName") {
		t.Errorf("nothing may wait or read between the check and the opening: %q", afterCheck)
	}
	// Refocusing: at most twice, sharing the front polls, else 9103.
	for _, want := range []string{
		"if refocus > 2 or frontPolls < 1 then",
		"my bringToFront(targetID)",
		"set w to my waitForFront(targetID, frontPolls)",
		"set frontPolls to frontPolls - (item 2 of w)",
		"number 9103",
	} {
		if !strings.Contains(attempt, want) {
			t.Errorf("the check before an attempt lacks %q", want)
		}
	}
	// The press: only at the first attempt, and only the item the lookup
	// found; a failed press falls back to Cmd+T in the same attempt.
	for _, want := range []string{
		"if menuItem is not missing value and attempt is 1 then",
		`perform action "AXPress" of menu item itemIndex of menu 1 of menu bar item barIndex of menu bar 1`,
		`perform action "AXPress" of menu item subIndex of menu 1 of menu item itemIndex of menu 1 of menu bar item barIndex of menu bar 1`,
		"set pressed to true",
		"if not pressed then",
	} {
		if !strings.Contains(attempt, want) {
			t.Errorf("the opening lacks %q", want)
		}
	}
	if strings.Count(appleTerminalTabScript, `perform action "AXPress"`) != 2 {
		t.Error("AXPress must only press the New Tab menu item (top level or submenu)")
	}
	if i, j := strings.Index(attempt, "my targetInFront(targetID)"), strings.Index(attempt, `perform action "AXPress"`); i < 0 || j < i {
		t.Error("the menu item must be pressed only after the check of the front")
	}
	if strings.Count(run, "repeat with attempt from 1 to 2") != 1 || strings.Count(appleTerminalTabScript, "repeat with attempt") != 1 {
		t.Error("the tab must be opened at most twice")
	}
}

func TestAppleTerminalTabScript_RetryOnce(t *testing.T) {
	loop := scriptSection(t, "repeat with attempt from 1 to 2", "if newTTY is missing value then\n\t\tdelay 2")
	retry := scriptSection(t, "if newTTY is not missing value then exit repeat", "end repeat")
	for _, want := range []string{
		"if attempt is 1 then",
		`log "graph-ops-diag: retry_frontmost_app=" & my frontmostAppName()`,
		`log "graph-ops-diag: retried=true"`,
		"if frontPolls < 10 then set frontPolls to 10",
	} {
		if !strings.Contains(retry, want) {
			t.Errorf("the retry lacks %q", want)
		}
	}
	// The ttys known before the first attempt are kept: a late tab of the
	// first attempt next to the retry's is two new tabs, 9104.
	if strings.Count(appleTerminalTabScript, "set knownTTYs to") != 1 {
		t.Error("the ttys must be recorded once, before the first attempt")
	}
	if !strings.Contains(loop, "if (count of freshTTYs) > 1 then error") {
		t.Error("two new tabs must still be 9104")
	}
	// The last look comes after both attempts.
	if i, j := strings.Index(appleTerminalTabScript, "repeat with attempt from 1 to 2"), strings.Index(appleTerminalTabScript, "\t\tdelay 2\n"); j < i {
		t.Error("the last look must come after the retry")
	}
}

func TestTabPhaseRank_MenuPhasesRankWithTheKeystroke(t *testing.T) {
	if tabPhaseRank(TabPhaseMenuPressing) != tabPhaseRank(TabPhaseKeystrokeSending) || tabPhaseRank(TabPhaseMenuPressed) != tabPhaseRank(TabPhaseKeystrokeSent) {
		t.Fatal("the menu phases must rank with their keystroke counterparts")
	}
	if tabPhaseRank(TabPhaseMenuPressing) == 0 {
		t.Fatal("menu-pressing must be a phase")
	}
	if phase, _ := splitTabScriptOutput(tabPhasePrefix + TabPhaseFrontmost + "\n" + tabPhasePrefix + TabPhaseMenuPressed + "\nboom"); phase != TabPhaseMenuPressed {
		t.Fatalf("phase = %q, want %q", phase, TabPhaseMenuPressed)
	}
	for _, tc := range []struct {
		phase       string
		wantDisable bool
	}{{TabPhaseMenuPressing, false}, {TabPhaseMenuPressed, false}} {
		if got := classifyTabFailure(true, tc.phase, "whatever"); got != tc.wantDisable {
			t.Errorf("timeout after %q: disable = %v", tc.phase, got)
		}
		if !(&TabFailure{Kind: TabFailureTimeout, Phase: tc.phase}).SlowTimeout() {
			t.Errorf("timeout after %q must be a slow timeout", tc.phase)
		}
	}
}

// menuOutput is osascript's output of a script that pressed the menu item
// and then logged tail.
func menuOutput(pressed bool, tail string) string {
	out := phaseOutput(TabPhaseFrontmost, "") + tabPhasePrefix + TabPhaseMenuPressing + "\n"
	if pressed {
		out += tabDiagPrefix + "open_method=menu\n" + tabPhasePrefix + TabPhaseMenuPressed + "\n"
	}
	return out + tail
}

func TestLaunchWithOptions_MenuAndRetryFailures(t *testing.T) {
	cases := []struct {
		name         string
		respond      func(ctx context.Context, name string, args []string) ([]byte, error)
		want         TabFailure
		wantInError  string
		wantEmptyTab bool
	}{
		{
			name:    "9102 after the menu item was pressed",
			respond: exitFailure(menuOutput(true, "54:97: execution error: graph-ops: no new tab appeared in the Terminal window of /dev/ttys003 (9102)")),
			want: TabFailure{Kind: TabFailureScriptError, Phase: TabPhaseMenuPressed, KeystrokeSent: true, ErrorNumber: 9102,
				Diagnostics: TabDiagnostics{OpenMethod: "menu"}},
			wantInError: "9102", wantEmptyTab: true,
		},
		{
			name:    "slow timeout while the menu item is pressed",
			respond: timeoutAfterOutput(menuOutput(false, "")),
			want:    TabFailure{Kind: TabFailureTimeout, Phase: TabPhaseMenuPressing},
			// The press may have been carried out.
			wantInError: "the New Tab menu item may have been pressed, so a new tab may have been left empty", wantEmptyTab: true,
		},
		{
			// The retry's Cmd+T is on its way when osascript is killed: the
			// last phase is keystroke-sending, but the first attempt had
			// already pressed the menu item.
			name: "slow timeout during the retry",
			respond: timeoutAfterOutput(menuOutput(true, tabDiagPrefix+"retry_frontmost_app=Google Chrome\n"+tabDiagPrefix+"retried=true\n"+
				tabDiagPrefix+"open_method=keystroke-retry\n"+tabPhasePrefix+TabPhaseKeystrokeSending+"\n")),
			want: TabFailure{Kind: TabFailureTimeout, Phase: TabPhaseKeystrokeSending, KeystrokeSent: true,
				Diagnostics: TabDiagnostics{OpenMethod: "keystroke-retry", Retried: true, RetryFrontmostApp: "Google Chrome"}},
			wantInError: "after keystroke-sending", wantEmptyTab: true,
		},
		{
			// The front was lost right before the retry: 9103 with the
			// first attempt sent.
			name: "9103 before the retry",
			respond: exitFailure(menuOutput(true, tabDiagPrefix+"retried=true\n"+tabDiagPrefix+"refocused=true\n"+
				"0:1: execution error: graph-ops: Terminal did not come back to the front with the window of /dev/ttys003, so it was not opened (9103)")),
			want: TabFailure{Kind: TabFailureScriptError, Phase: TabPhaseMenuPressed, KeystrokeSent: true, ErrorNumber: 9103,
				Diagnostics: TabDiagnostics{OpenMethod: "menu", Retried: true, Refocused: true}},
			wantInError: "9103", wantEmptyTab: true,
		},
		{
			// No item found: Cmd+T as before; a 9103 at the check before the
			// first attempt sent nothing.
			name: "9103 at the check before the first attempt",
			respond: exitFailure(phaseOutput(TabPhaseFrontmost, tabDiagPrefix+"refocused=true\n"+tabDiagPrefix+"front_window_id_before=55704\n"+
				"0:1: execution error: graph-ops: Terminal lost the front with the window of /dev/ttys003 just before the new tab was to be opened, so it was not opened (9103)")),
			want: TabFailure{Kind: TabFailureScriptError, Phase: TabPhaseFrontmost, ErrorNumber: 9103,
				Diagnostics: TabDiagnostics{Refocused: true, FrontWindowIDBefore: "55704"}},
			wantInError: "9103",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			onDarwin(t)
			secureInputOwner = func() string { return "412 loginwindow" }
			hidIdleMS = func() string { return "250" }
			oldTimeout := tabScriptTimeout
			tabScriptTimeout = 20 * time.Millisecond
			defer func() { tabScriptTimeout = oldTimeout }()
			fakeCommands(t, tc.respond)
			out, err := launchTab(t, Config{}, LaunchOptions{AppleTerminalTTY: testTTY})
			if err != nil {
				t.Fatal(err)
			}
			if out.TabFailure == nil {
				t.Fatal("no failure")
			}
			want := tc.want
			want.Diagnostics.ScreenLock, want.Diagnostics.ScreenLockAfter = ScreenUnlocked, ScreenUnlocked
			want.Diagnostics.SecureInput, want.Diagnostics.HIDIdleMS = "412 loginwindow", "250"
			if *out.TabFailure != want {
				t.Fatalf("failure = %+v\nwant      %+v", *out.TabFailure, want)
			}
			if out.DisableTab || out.UsedTab || out.RetriedTab || !strings.Contains(out.TabError, tc.wantInError) {
				t.Fatalf("outcome = %+v, want %q in TabError", out, tc.wantInError)
			}
			if out.TabFailure.EmptyTabPossible() != tc.wantEmptyTab {
				t.Fatalf("EmptyTabPossible = %v, want %v", out.TabFailure.EmptyTabPossible(), tc.wantEmptyTab)
			}
		})
	}
}

// timeoutAfterOutput makes osascript print output and then hang until it
// is killed.
func timeoutAfterOutput(output string) func(ctx context.Context, name string, args []string) ([]byte, error) {
	return func(ctx context.Context, name string, args []string) ([]byte, error) {
		if name == "osascript" {
			<-ctx.Done()
			return []byte(output), ctx.Err()
		}
		return nil, nil
	}
}

// A tab that opens at the retry is used, and the outcome says so; nothing
// about the input state is read for a tab that opens.
func TestLaunchWithOptions_RetriedTabIsUsed(t *testing.T) {
	onDarwin(t)
	reads := 0
	secureInputOwner = func() string { reads++; return "" }
	hidIdleMS = func() string { reads++; return "" }
	calls := fakeCommands(t, func(ctx context.Context, name string, args []string) ([]byte, error) {
		return []byte(menuOutput(true, tabDiagPrefix+"retried=true\n"+tabDiagPrefix+"open_method=keystroke-retry\n"+
			tabPhasePrefix+TabPhaseKeystrokeSending+"\n"+tabPhasePrefix+TabPhaseKeystrokeSent+"\n"+
			tabPhasePrefix+TabPhaseTabFound+"\n"+tabPhasePrefix+TabPhaseCommandSending+"\n")), nil
	})
	out, err := launchTab(t, Config{}, LaunchOptions{AppleTerminalTTY: testTTY})
	if err != nil {
		t.Fatal(err)
	}
	if out != (LaunchOutcome{UsedTab: true, RetriedTab: true}) {
		t.Fatalf("outcome = %+v", out)
	}
	if len(*calls) != 1 || reads != 0 {
		t.Fatalf("calls = %+v, input state reads = %d; want osascript only", *calls, reads)
	}
}

// The input state is read after a failure, outside the lock, and a read
// that fails ("") neither changes the failure nor stops the fallback.
func TestLaunchWithOptions_InputStateReadsDoNotBlockTheFallback(t *testing.T) {
	lockPath := onDarwin(t)
	heldDuringRead := false
	hidIdleMS = func() string {
		if !lockIsFree(t, lockPath) {
			heldDuringRead = true
		}
		return ""
	}
	secureInputOwner = func() string { return "" }
	calls := fakeCommands(t, func(ctx context.Context, name string, args []string) ([]byte, error) {
		if name == "osascript" {
			return exitFailure(phaseOutput(TabPhaseKeystrokeSent, "execution error: graph-ops: no new tab appeared in the Terminal window of /dev/ttys003 (9102)"))(ctx, name, args)
		}
		return nil, nil
	})
	out, err := launchTab(t, Config{}, LaunchOptions{AppleTerminalTTY: testTTY})
	if err != nil {
		t.Fatal(err)
	}
	if heldDuringRead {
		t.Fatal("the input state must be read after the tab lock is released")
	}
	if out.TabFailure == nil || out.TabFailure.Diagnostics.HIDIdleMS != "" || out.TabFailure.Diagnostics.SecureInput != "" || out.TabFailure.ErrorNumber != 9102 {
		t.Fatalf("failure = %+v", out.TabFailure)
	}
	if len(*calls) != 2 || (*calls)[1].Name != "open" {
		t.Fatalf("calls = %+v, want osascript then open", *calls)
	}
}

func TestParseTabDiagnostics_DFLT00377Keys(t *testing.T) {
	d := parseTabDiagnostics(strings.Join([]string{
		tabDiagPrefix + "front_window_id_before=55701",
		tabDiagPrefix + "open_method=keystroke-menu-failed",
		tabDiagPrefix + "open_method=menu",
		tabDiagPrefix + "retried=true",
		tabDiagPrefix + "retry_frontmost_app=Google\x1b[31m Chrome",
		tabDiagPrefix + "refocused=true",
		// Read by Go, never logged by the script: ignored.
		tabDiagPrefix + "hid_idle_ms=5",
		tabDiagPrefix + "secure_input=1 x",
	}, "\n"))
	want := TabDiagnostics{FrontWindowIDBefore: "55701", OpenMethod: "menu", Retried: true, RetryFrontmostApp: "Google[31m Chrome", Refocused: true}
	if d != want {
		t.Fatalf("diagnostics = %+v\nwant          %+v", d, want)
	}
	if parseTabDiagnostics(tabDiagPrefix + "retried=yes\n" + tabDiagPrefix + "refocused=1").Retried {
		t.Fatal("only retried=true counts")
	}
}

func TestSecureInputPIDFromIORegPlist(t *testing.T) {
	session := func(uid int, extra string) string {
		return fmt.Sprintf(`<dict><key>kCGSSessionUserIDKey</key><integer>%d</integer>%s</dict>`, uid, extra)
	}
	doc := func(users string) []byte {
		return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict><key>IOConsoleUsers</key><array>` + users + `</array></dict></plist>`)
	}
	cases := []struct {
		name string
		data []byte
		want int64
	}{
		{"none", doc(session(501, "")), 0},
		{"held", doc(session(501, `<key>kCGSSessionSecureInputPID</key><integer>412</integer>`)), 412},
		{"another user's session", doc(session(502, `<key>kCGSSessionSecureInputPID</key><integer>99</integer>`) + session(501, "")), 0},
		{"no user id is ours", doc(`<dict><key>kCGSSessionSecureInputPID</key><integer>7</integer></dict>`), 7},
		{"zero", doc(session(501, `<key>kCGSSessionSecureInputPID</key><integer>0</integer>`)), 0},
		{"not a plist", []byte("garbage"), 0},
		{"no console users", []byte(`<plist version="1.0"><dict></dict></plist>`), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := secureInputPIDFromIORegPlist(tc.data, 501); got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestHIDIdleMSFromIOReg(t *testing.T) {
	out := `+-o IOHIDSystem  <class IOHIDSystem, id 0x1000007d0, registered>
    {
      "IOClass" = "IOHIDSystem"
      "HIDIdleTime" = 17742833333
    }`
	if got := hidIdleMSFromIOReg(out); got != "17742" {
		t.Fatalf("got %q, want 17742", got)
	}
	for _, bad := range []string{"", `"HIDIdleTime" = -1`, `"HIDIdleTime" = 99999999999999999999999`, "nothing"} {
		if got := hidIdleMSFromIOReg(bad); got != "" {
			t.Errorf("hidIdleMSFromIOReg(%q) = %q, want empty", bad, got)
		}
	}
}

// On a Mac, the real reads answer within their bound: the idle time as a
// number of milliseconds, the secure input owner as "" or "<pid> <name>".
func TestReadInputState_OnThisMac(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("ioreg is macOS only")
	}
	start := time.Now()
	idle := readHIDIdleMS()
	if _, err := strconv.ParseUint(idle, 10, 64); err != nil {
		t.Fatalf("readHIDIdleMS() = %q, want a number", idle)
	}
	if owner := readSecureInputOwner(); owner != "" && !regexp.MustCompile(`^[0-9]+( .+)?$`).MatchString(owner) {
		t.Fatalf("readSecureInputOwner() = %q", owner)
	}
	if d := time.Since(start); d > 2*inputStateTimeout {
		t.Fatalf("the reads took %s", d)
	}
}
