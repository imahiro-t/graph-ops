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
	// found.
	for _, want := range []string{
		"if attempt is 1 and menuItem is not missing value then",
		`perform action "AXPress" of menu item itemIndex of menu 1 of menu bar item barIndex of menu bar 1`,
		`perform action "AXPress" of menu item subIndex of menu 1 of menu item itemIndex of menu 1 of menu bar item barIndex of menu bar 1`,
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
	retry := scriptSection(t, "repeat with attempt from 1 to 2", "set refocus to 0")
	for _, want := range []string{
		"if attempt is 2 then",
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

// Each menu's four lists are read afresh: all four are reset at every
// menu, read into temporaries and taken together only when all of them
// were read, and used only when they are as long as each other -- so a read
// failing part-way never pairs this menu's keys with the previous menu's
// modifiers or submenus.
func TestAppleTerminalTabScript_NewTabMenuItemReadsEachMenuAfresh(t *testing.T) {
	menu := scriptSection(t, "repeat with i from 2 to barCount", "if hasNewWindow then")
	reset := menu[:strings.Index(menu, "try")]
	for _, v := range []string{"c1", "m1", "c2", "m2"} {
		if !strings.Contains(reset, "set "+v+" to missing value") {
			t.Errorf("%s must be reset before each menu is read", v)
		}
	}
	read := scriptSection(t, `tell application "System Events" to tell process "Terminal"`+"\n\t\t\t\t\t\tset readC1", "end try")
	for _, v := range []string{"readC1", "readM1", "readC2", "readM2"} {
		if !strings.Contains(read, "set "+v+" to value of attribute") {
			t.Errorf("the menu must be read into %s", v)
		}
	}
	for _, v := range []string{"set c1 to value", "set m1 to value", "set c2 to value", "set m2 to value"} {
		if strings.Contains(read, v) {
			t.Errorf("%q: a read must not go straight into the variables used", v)
		}
	}
	if !strings.HasSuffix(strings.TrimSpace(read), "set {c1, m1, c2, m2} to {readC1, readM1, readC2, readM2}") {
		t.Errorf("the four reads must be taken together, as the try's last statement: %q", read)
	}
	if !strings.Contains(menu, "if c1 is not missing value and (count of c1) is (count of m1) and (count of c1) is (count of c2) and (count of c1) is (count of m2) then") {
		t.Error("the lists must be used only when all four were read and line up")
	}
}

// A press that returns an error sends nothing else in that attempt (the
// press may have been carried out): Cmd+T is only sent where no menu item
// is pressed, and the retry is what opens the tab when the press did not.
func TestAppleTerminalTabScript_FailedPressSendsNoKeystroke(t *testing.T) {
	press := scriptSection(t, "if attempt is 1 and menuItem is not missing value then", "\n\t\telse\n")
	if strings.Contains(press, `keystroke "t"`) {
		t.Errorf("the menu branch must not send Cmd+T: %q", press)
	}
	for _, want := range []string{
		`log "graph-ops-diag: open_method=menu"`,
		"on error\n\t\t\t\tlog \"graph-ops-diag: open_method=menu-failed\"",
		// After the try: the press may have been carried out either way.
		"end try\n\t\t\tlog \"graph-ops-phase: menu-pressed\"",
	} {
		if !strings.Contains(press, want) {
			t.Errorf("the press lacks %q", want)
		}
	}
	if strings.Count(appleTerminalTabScript, `keystroke "t" using command down`) != 1 {
		t.Error("Cmd+T must be sent from one place only")
	}
	if strings.Contains(appleTerminalTabScript, "keystroke-menu-failed") {
		t.Error("a failed press no longer falls back to Cmd+T in the same attempt")
	}
}

// Before the retry, the ttys are read again: a first attempt's tab that
// appeared late is taken instead of opening a second one.
func TestAppleTerminalTabScript_LateFirstTabIsTakenBeforeTheRetry(t *testing.T) {
	late := scriptSection(t, "on lateFirstTab(knownTTYs, targetTTY)", "end lateFirstTab")
	for _, want := range []string{
		"set nowTTYs to my terminalTTYs()",
		"if nowTTYs is missing value then return missing value",
		"if (count of freshTTYs) > 1 then error",
		"number 9104",
		`log "graph-ops-diag: late_tab=true"`,
		"return item 1 of freshTTYs",
	} {
		if !strings.Contains(late, want) {
			t.Errorf("lateFirstTab lacks %q", want)
		}
	}
	// Once before anything is logged for the retry...
	pre := scriptSection(t, "repeat with attempt from 1 to 2", "set refocus to 0")
	i, j := strings.Index(pre, "my lateFirstTab(knownTTYs, targetTTY)"), strings.Index(pre, `log "graph-ops-diag: retried=true"`)
	if i < 0 || j < i || !strings.Contains(pre[i:j], "if newTTY is not missing value then exit repeat") {
		t.Errorf("the ttys must be read, and a late tab taken, before the retry is logged: %q", pre)
	}
	// ...and after the wait before each check, right before the check.
	check := scriptSection(t, "set refocus to 0", "if my targetInFront(targetID) then exit repeat")
	if !strings.Contains(check, "delay 0.2\n\t\t\tif attempt is 2 then\n\t\t\t\tset newTTY to my lateFirstTab(knownTTYs, targetTTY)\n\t\t\t\tif newTTY is not missing value then exit repeat") {
		t.Errorf("the ttys must be read again right before the retry's check: %q", check)
	}
	after := scriptSection(t, "my bringToFront(targetID)\n\t\t\tset w to", `log "graph-ops-diag: frontmost_app_before="`)
	if !strings.Contains(after, "if newTTY is not missing value then\n\t\t\tlog \"graph-ops-diag: retried=false\"\n\t\t\texit repeat") {
		t.Errorf("a late tab found at the check must undo retried and skip the opening: %q", after)
	}
	if strings.Count(appleTerminalTabScript, "my lateFirstTab(") != 2 {
		t.Error("lateFirstTab must be called twice, both for the retry")
	}
}

// After a retry, a second new tab appearing next to the one taken is
// logged (stray_tab), and the command still goes to the tab taken.
func TestAppleTerminalTabScript_StrayTabAfterTheRetry(t *testing.T) {
	if !strings.Contains(appleTerminalTabScript, "\t\t\tlog \"graph-ops-phase: keystroke-sent\"\n\t\t\tif attempt is 2 then set retrySent to true") {
		t.Error("retrySent must be set right after the retry's Cmd+T")
	}
	if strings.Count(appleTerminalTabScript, "set retrySent to") != 2 {
		t.Error("retrySent must be set to false once and to true once")
	}
	stray := scriptSection(t, "if not sameWindow then", "do script shellCommand in newTab")
	for _, want := range []string{
		"if retrySent then",
		"repeat 5 times",
		"if (count of my freshTTYsOf(my terminalTTYs(), knownTTYs)) > 1 then set strayTab to true",
		"delay 0.2",
		`if strayTab then log "graph-ops-diag: stray_tab=true"`,
	} {
		if !strings.Contains(stray, want) {
			t.Errorf("the stray tab check lacks %q", want)
		}
	}
	if strings.Contains(stray[strings.Index(stray, "if retrySent then"):], "error ") {
		t.Error("a stray tab must not fail the launch")
	}
	// do script stays the last statement.
	if !strings.HasSuffix(appleTerminalTabScript, "\tlog \"graph-ops-phase: command-sending\"\n\ttell application \"Terminal\" to do script shellCommand in newTab\nend run") {
		t.Error("do script must stay the last statement")
	}
}

// What the launch makes of the late and stray tabs.
func TestLaunchWithOptions_LateFirstTabAndStrayTab(t *testing.T) {
	success := func(tail string) func(ctx context.Context, name string, args []string) ([]byte, error) {
		return func(ctx context.Context, name string, args []string) ([]byte, error) {
			return []byte(menuOutput(true, tail+tabPhasePrefix+TabPhaseTabFound+"\n"+tabPhasePrefix+TabPhaseCommandSending+"\n")), nil
		}
	}
	cases := []struct {
		name string
		tail string
		want LaunchOutcome
	}{
		{
			// Seen before anything was logged for the retry.
			name: "first attempt's tab seen before the retry",
			tail: tabDiagPrefix + "late_tab=true\n",
			want: LaunchOutcome{UsedTab: true, LateTab: true},
		},
		{
			// Seen at the retry's check: retried is undone.
			name: "first attempt's tab seen at the retry's check",
			tail: tabDiagPrefix + "retry_frontmost_app=Terminal\n" + tabDiagPrefix + "retried=true\n" + tabDiagPrefix + "late_tab=true\n" + tabDiagPrefix + "retried=false\n",
			want: LaunchOutcome{UsedTab: true, LateTab: true},
		},
		{
			name: "a stray tab after the retry",
			tail: tabDiagPrefix + "retried=true\n" + tabDiagPrefix + "open_method=keystroke-retry\n" +
				tabPhasePrefix + TabPhaseKeystrokeSending + "\n" + tabPhasePrefix + TabPhaseKeystrokeSent + "\n" + tabDiagPrefix + "stray_tab=true\n",
			want: LaunchOutcome{UsedTab: true, RetriedTab: true, StrayTab: true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			onDarwin(t)
			fakeCommands(t, success(tc.tail))
			out, err := launchTab(t, Config{}, LaunchOptions{AppleTerminalTTY: testTTY})
			if err != nil {
				t.Fatal(err)
			}
			if out != tc.want {
				t.Fatalf("outcome = %+v, want %+v", out, tc.want)
			}
		})
	}
	if d := parseTabDiagnostics(tabDiagPrefix + "stray_tab=true"); !d.StrayTab {
		t.Fatal("stray_tab=true must be parsed")
	}
	if d := parseTabDiagnostics(tabDiagPrefix + "stray_tab=1"); d.StrayTab {
		t.Fatal("only stray_tab=true counts")
	}
}

// A press that returned an error (open_method=menu-failed) and then no tab
// even after the retry: 9102 with the press counted as sent.
func TestLaunchWithOptions_FailedPressThenRetry(t *testing.T) {
	onDarwin(t)
	secureInputOwner = func() string { return "" }
	hidIdleMS = func() string { return "" }
	out := phaseOutput(TabPhaseFrontmost, "") + tabPhasePrefix + TabPhaseMenuPressing + "\n" + tabDiagPrefix + "open_method=menu-failed\n" +
		tabPhasePrefix + TabPhaseMenuPressed + "\n" + tabDiagPrefix + "retried=true\n" + tabDiagPrefix + "open_method=keystroke-retry\n" +
		tabPhasePrefix + TabPhaseKeystrokeSending + "\n" + tabPhasePrefix + TabPhaseKeystrokeSent + "\n" +
		"54:97: execution error: graph-ops: no new tab appeared in the Terminal window of /dev/ttys003 (9102)"
	fakeCommands(t, func(ctx context.Context, name string, args []string) ([]byte, error) {
		if name == "osascript" {
			return exitFailure(out)(ctx, name, args)
		}
		return nil, nil
	})
	got, err := launchTab(t, Config{}, LaunchOptions{AppleTerminalTTY: testTTY})
	if err != nil {
		t.Fatal(err)
	}
	f := got.TabFailure
	if f == nil || f.ErrorNumber != 9102 || !f.KeystrokeSent || !f.EmptyTabPossible() || f.Diagnostics.OpenMethod != "keystroke-retry" || !f.Diagnostics.Retried || got.DisableTab {
		t.Fatalf("outcome = %+v, failure = %+v", got, f)
	}
}
