package terminal

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// This file is the Terminal.app tab path (DFLT-00154): the autopilot opens
// every child session as a new tab of the window its orchestrator runs in,
// instead of one new window per session. The window is identified by the
// tty of the orchestrator's tab, detected once when the run is started
// (DetectAppleTerminalTTY) and kept in the run record; each launch looks for
// the window that has a tab on that tty (appleTerminalTabScript). Whenever
// the tab cannot be opened, the session opens in a new window exactly as
// before (`open -a Terminal <script>.command`), so a launch never fails on
// the tab's account.

// getenv, getpid and psOutput are package variables so tests can fake the
// environment and the process tree without running ps.
var (
	getenv   = os.Getenv
	getpid   = os.Getpid
	psOutput = func(pid int) (string, error) {
		out, err := exec.Command("ps", "-o", "tty=,ppid=", "-p", strconv.Itoa(pid)).Output()
		return string(out), err
	}
)

// maxTTYParentHops bounds the walk up the process tree in
// DetectAppleTerminalTTY.
const maxTTYParentHops = 16

// appleTerminalTTYPattern is the only tty form the tab path accepts: a
// Terminal.app tab's pseudo-terminal. Anything else (a Linux /dev/pts/N, a
// value with shell or AppleScript syntax in it) never reaches osascript.
var appleTerminalTTYPattern = regexp.MustCompile(`^/dev/ttys[0-9]+$`)

// validAppleTerminalTTY reports whether tty is a value the tab path accepts.
func validAppleTerminalTTY(tty string) bool {
	return appleTerminalTTYPattern.MatchString(tty)
}

// DetectAppleTerminalTTY returns the tty of the Terminal.app tab this
// process runs in (e.g. "/dev/ttys003"), or "" when it is not running in
// Terminal.app (another terminal, tmux, not macOS) or the tty cannot be
// found. It needs no AppleScript and no permission: only ps.
//
// graph-engine run from Claude Code's Bash tool may have no controlling
// terminal of its own, so the process tree is walked upwards (at most
// maxTTYParentHops steps) until a process with a tty is found -- the claude
// session, and above it the shell of the Terminal.app tab. Any failure of
// ps (a sandbox that refuses it included) gives "".
func DetectAppleTerminalTTY() string {
	if goos != "darwin" || getenv("TERM_PROGRAM") != "Apple_Terminal" || getenv("TMUX") != "" {
		return ""
	}
	pid := getpid()
	for i := 0; i < maxTTYParentHops && pid > 1; i++ {
		out, err := psOutput(pid)
		if err != nil {
			return ""
		}
		fields := strings.Fields(out)
		if len(fields) != 2 {
			return ""
		}
		if tty := fields[0]; tty != "??" && tty != "-" {
			if candidate := "/dev/" + tty; validAppleTerminalTTY(candidate) {
				return candidate
			}
			return ""
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil {
			return ""
		}
		pid = ppid
	}
	return ""
}

// useAppleTerminalTab reports whether LaunchWithOptions takes the tab path:
// the same place in buildLaunchArgvWithArgs's order as `open -a Terminal`
// (no terminalCommand, not in tmux, darwin), and only when the caller gave a
// valid tty and has not disabled the tab for its run.
// TMUX is read through getenv like everywhere else in this file (outside
// tests it is os.Getenv, what buildLaunchArgvWithArgs reads).
func useAppleTerminalTab(cfg Config, opts LaunchOptions) bool {
	return goos == "darwin" && cfg.TerminalCommand == "" && getenv("TMUX") == "" &&
		!opts.SkipAppleTerminalTab && validAppleTerminalTTY(opts.AppleTerminalTTY)
}

// Error numbers appleTerminalTabScript raises itself. They are the
// failures that return quickly and may not happen on the next launch, so
// they do not disable the tab for the run (see classifyTabFailure). Each of
// them is raised before `do script`, so no session has started when they
// fall back to a new window.
const (
	// tabErrWindowNotFound: no Terminal window has a tab on the tty (the
	// orchestrator's window was closed), or it went away while waiting.
	tabErrWindowNotFound = 9101
	// tabErrTabNotOpened: no new tab appeared in the window after Cmd+T,
	// or it closed before the command could be run in it. A tab that opens
	// late (or in another window) is left empty, not closed: see
	// docs/autopilot.md for why.
	tabErrTabNotOpened = 9102
	// tabErrNotFrontmost: Terminal, with the target window in front, did
	// not become the frontmost app, so Cmd+T was never sent (it would have
	// gone to whatever app the user is working in). Only when Terminal's
	// state could be read at least once while waiting: if every read failed,
	// the script rethrows the last read error with its own number instead,
	// so a failure that would repeat is not taken for a passing one (a
	// permission error usually surfaces earlier, from the activate and
	// frontmost statements before the wait, with its own number anyway).
	tabErrNotFrontmost = 9103
	// tabErrNewTabAmbiguous: more than one new tab appeared in the window
	// (another process opened one at the same time), so which one is ours
	// cannot be told and the command is run in none of them. The tab this
	// launch opened is left empty among them, not closed (docs/autopilot.md).
	tabErrNewTabAmbiguous = 9104
)

// appleTerminalTabScript opens a new tab in the Terminal.app window that has
// a tab on the tty argv[1], and runs the shell command argv[2] in it. Both
// arrive as arguments of `on run argv`, never spliced into the source, so the
// script is a constant and needs no AppleScript escaping whatever the path of
// the command script contains.
//
// Terminal.app's scripting dictionary has no "make new tab", and `do script
// ... in window` types into the window's current tab rather than a new one,
// so the tab is opened by sending Cmd+T through System Events -- which is
// what needs the Accessibility permission, next to the Automation
// permission for Terminal and System Events. The window is referred to by
// its id throughout, since bringing it to the front reorders the index-based
// references.
//
// Two things keep the keystroke and the command from landing anywhere else:
//
//   - A keystroke goes to whatever app is frontmost, not to the process the
//     tell names. So Cmd+T is sent only after Terminal is seen frontmost with
//     the target window in front (polled for up to about 2 seconds), and
//     then only after a further 0.2 seconds, which gives Terminal time to
//     be ready to open the tab. If the user switches to another app within
//     those 0.2 seconds, Cmd+T can reach that app instead; no new tab then
//     appears in the target window, the script fails with 9102 and the
//     session falls back to a new window, and an empty tab may be left
//     wherever the keystroke landed. When Terminal is not seen in front,
//     the script sends no key at all and fails with 9103 -- or,
//     when Terminal's state could not be read even once while polling, with
//     the last read error's own number, so that classifyTabFailure disables
//     the tab instead of retrying it on every launch. (The permission errors
//     of the activate and frontmost statements just before the polling are
//     not caught at all and keep their numbers too.)
//   - The command is never sent to "the selected tab", which the user (or
//     another run's launch in the same window) can change at any moment.
//     The ttys of every tab of every Terminal window are recorded before
//     Cmd+T, and the command goes to the one tab whose tty is new. No new
//     tab within about 6 seconds (polled every 0.2 seconds) is 9102; more
//     than one (someone else opened a tab at the same time) is 9104, and
//     the command is run in none of them. A poll whose read of the ttys
//     fails is skipped rather than taken for "no tabs" (which could never
//     show the new one), and the last such error is named in the 9102.
//
// Terminal.app on current macOS reports every tab of a window to
// AppleScript as a window of its own with a single tab (DFLT-00183: the
// three tabs of one window came back as three windows with one tab each,
// all with the same bounds), while older versions list the tabs inside
// their window. So the new tab is looked for in every window, not only in
// the target one, and is accepted as part of the orchestrator's window only
// when the window reporting it has the target window's bounds -- true of
// the tabs of one window under either model. The bounds are compared after
// the tab appeared (for up to about 2 seconds), never with those from before
// the Cmd+T: a window that gets its second tab grows by the height of its
// new tab bar. For the same reason the bottom edge is not compared at all,
// and the left, top and right edges may differ by up to 4 points
// (sameWindowBounds): an exact match (DFLT-00154 to DFLT-00360) failed on
// a tab bar appearing, an animation or a window nudged at the screen's edge,
// leaving the new tab empty (DFLT-00361). A new tab in a window at another
// place or of another width (the Cmd+T reached another Terminal window) is
// 9102, with both bounds in the message, and gets no command.
//
// Both windows are found by windowOfTTY, which reads the ids of every
// window and the ttys of every tab of every window in two Apple events and
// matches them up by position, then confirms the match with one more event
// on the window found (the windows may change between the two reads). Only
// when the reads do not line up does it fall back to asking tab by tab.
// Before DFLT-00361 both lookups always asked tab by tab: several Apple
// events per tab, which with 20-30 tabs open (each a window of its own,
// most running a busy claude session) could take long enough for the whole
// script to reach tabScriptTimeout -- and a timeout disabled the tab for
// the rest of the run.
//
// The script logs its progress as "graph-ops-phase: <phase>" lines (see
// the tabPhase constants), which osascript writes to stderr as it goes, so
// they are in the output even when osascript is killed on
// tabScriptTimeout. openAppleTerminalTab takes the last one as how far the
// script got: whether Terminal and System Events answered at all (a
// timeout before that is a permission prompt nobody answers), and whether
// Cmd+T was already sent (a new tab may then be left empty).
//
// `do script` is the last statement: every failure before it ends the
// script with an error, so falling back to a new window never runs the
// session twice.
//
// To try it by hand from a Terminal.app tab (macOS may ask for the
// permissions), write the constant out and run it against the tab's own tty:
//
//	GRAPH_OPS_TAB_SCRIPT_OUT=/tmp/tab.applescript go test ./internal/terminal -run TestAppleTerminalTabScript_Structure
//	osascript /tmp/tab.applescript "$(tty)" "echo hello"
const appleTerminalTabScript = `on terminalTTYs()
	tell application "Terminal" to set perWindow to tty of every tab of every window
	set found to {}
	repeat with entry in perWindow
		set e to contents of entry
		if class of e is not list then set e to {e}
		repeat with x in e
			set v to contents of x
			if v is not missing value and v is not "" then set end of found to v
		end repeat
	end repeat
	return found
end terminalTTYs

on windowOfTTY(theTTY)
	repeat 2 times
		set winIDs to missing value
		set winTTYs to missing value
		try
			tell application "Terminal"
				set winIDs to id of every window
				set winTTYs to tty of every tab of every window
			end tell
		end try
		if winIDs is not missing value and winTTYs is not missing value and (count of winIDs) is (count of winTTYs) then
			set foundID to missing value
			repeat with i from 1 to count of winTTYs
				set e to contents of item i of winTTYs
				if class of e is not list then set e to {e}
				if e contains theTTY then
					set foundID to item i of winIDs
					exit repeat
				end if
			end repeat
			if foundID is missing value then return missing value
			try
				tell application "Terminal" to set checkTTYs to tty of every tab of window id foundID
				if class of checkTTYs is not list then set checkTTYs to {checkTTYs}
				if checkTTYs contains theTTY then return foundID
			end try
		end if
	end repeat
	tell application "Terminal"
		repeat with w in windows
			try
				repeat with t in tabs of w
					if tty of t is theTTY then return id of w
				end repeat
			end try
		end repeat
	end tell
	return missing value
end windowOfTTY

on boundsText(b)
	return "{" & (item 1 of b) & ", " & (item 2 of b) & ", " & (item 3 of b) & ", " & (item 4 of b) & "}"
end boundsText

on sameWindowBounds(a, b)
	repeat with i from 1 to 3
		set d to (item i of a) - (item i of b)
		if d < 0 then set d to -d
		if d > 4 then return false
	end repeat
	return true
end sameWindowBounds

on run argv
	set targetTTY to item 1 of argv
	set shellCommand to item 2 of argv
	set targetID to my windowOfTTY(targetTTY)
	if targetID is missing value then error "graph-ops: no Terminal window has a tab on " & targetTTY number 9101
	log "graph-ops-phase: window-found"
	set knownTTYs to my terminalTTYs()
	tell application "Terminal"
		set index of window id targetID to 1
		activate
	end tell
	tell application "System Events" to set frontmost of process "Terminal" to true
	log "graph-ops-phase: system-events-ok"
	set inFront to false
	set pollOK to false
	set lastErrMsg to ""
	set lastErrNum to missing value
	repeat 20 times
		try
			tell application "System Events" to set terminalFront to frontmost of process "Terminal"
			tell application "Terminal" to set frontID to id of front window
			set pollOK to true
			if terminalFront and frontID is targetID then
				set inFront to true
				exit repeat
			end if
		on error errMsg number errNum
			set lastErrMsg to errMsg
			set lastErrNum to errNum
		end try
		delay 0.1
	end repeat
	if not inFront then
		if not pollOK and lastErrNum is not missing value then error "graph-ops: Terminal's state could not be read while waiting for it to come to the front: " & lastErrMsg number lastErrNum
		error "graph-ops: Terminal did not come to the front with the window of " & targetTTY & ", so no key was sent" number 9103
	end if
	log "graph-ops-phase: frontmost"
	delay 0.2
	tell application "System Events" to tell process "Terminal" to keystroke "t" using command down
	log "graph-ops-phase: keystroke-sent"
	set newTTY to missing value
	set lastReadErr to ""
	repeat 30 times
		try
			tell application "Terminal" to get id of window id targetID
		on error
			error "graph-ops: the Terminal window of " & targetTTY & " went away" number 9101
		end try
		set nowTTYs to missing value
		try
			set nowTTYs to my terminalTTYs()
		on error errMsg number errNum
			set lastReadErr to errMsg & " [" & errNum & "]"
		end try
		if nowTTYs is not missing value then
			set freshTTYs to {}
			repeat with x in nowTTYs
				set v to contents of x
				if knownTTYs does not contain v then set end of freshTTYs to v
			end repeat
			if (count of freshTTYs) > 1 then error "graph-ops: more than one new tab appeared in Terminal while opening a tab in the window of " & targetTTY number 9104
			if (count of freshTTYs) is 1 then
				set newTTY to item 1 of freshTTYs
				exit repeat
			end if
		end if
		delay 0.2
	end repeat
	if newTTY is missing value then
		if lastReadErr is not "" then error "graph-ops: no new tab appeared in the Terminal window of " & targetTTY & "; the last failed read of Terminal's tabs: " & lastReadErr number 9102
		error "graph-ops: no new tab appeared in the Terminal window of " & targetTTY number 9102
	end if
	set newID to my windowOfTTY(newTTY)
	set newTab to missing value
	if newID is not missing value then
		try
			tell application "Terminal" to set newTab to first tab of window id newID whose tty is newTTY
		end try
	end if
	if newTab is missing value then error "graph-ops: the new tab in the Terminal window of " & targetTTY & " closed" number 9102
	set sameWindow to false
	set boundsNote to "could not be read"
	repeat 20 times
		try
			tell application "Terminal"
				set targetBounds to bounds of window id targetID
				set newBounds to bounds of window id newID
			end tell
			set boundsNote to "target " & my boundsText(targetBounds) & ", new tab " & my boundsText(newBounds)
			if my sameWindowBounds(targetBounds, newBounds) then
				set sameWindow to true
				exit repeat
			end if
		end try
		delay 0.1
	end repeat
	if not sameWindow then error "graph-ops: the new tab did not open in the Terminal window of " & targetTTY & " (bounds " & boundsNote & ")" number 9102
	log "graph-ops-phase: tab-found"
	log "graph-ops-phase: command-sending"
	tell application "Terminal" to do script shellCommand in newTab
end run`

// defaultTabScriptTimeout is tabScriptTimeout's initial value (tests may
// shorten the variable); tabLockWait is derived from it, so changing it here
// moves both.
const defaultTabScriptTimeout = 15 * time.Second

// tabScriptTimeout bounds the osascript run. The script itself gives up
// after about 2 seconds of waiting for Terminal to come to the front, 0.2
// seconds before Cmd+T, about 6 seconds of waiting for the tab and about 2
// seconds of checking that it opened in the target window -- about 10.2
// seconds at worst. The 15 seconds add room for starting osascript and
// reading Terminal's windows on top of that. What takes longer is a
// permission prompt nobody answers when the script had not yet heard from
// both Terminal and System Events, and Terminal answering slowly after
// that (see classifyTabFailure). A variable so tests can shorten it.
var tabScriptTimeout = defaultTabScriptTimeout

// maxTabErrorLen caps LaunchOutcome.TabError, which the runner keeps in the
// run record.
const maxTabErrorLen = 300

// launchAppleTerminalTab writes the same .command script the new-window path
// runs, and tries to run it in a new tab of tty's window; if that fails, it
// opens the very same script with `open -a Terminal` instead.
func launchAppleTerminalTab(workDir, claudeBin string, extraArgs []string, prompt, tty string) (LaunchOutcome, error) {
	scriptPath, err := writeCommandScriptWithArgs(workDir, claudeBin, extraArgs, prompt)
	if err != nil {
		return LaunchOutcome{}, fmt.Errorf("preparing terminal launch script: %w", err)
	}
	var tabErr string
	var disable bool
	var failure *TabFailure
	if unlock, busy := lockTabLaunch(); busy != "" {
		// Other graph-ops processes kept the tab lock busy: a quick,
		// passing failure like 9101-9104, so the tab stays enabled. No
		// script ran, so there is no phase, no error number and no Cmd+T.
		tabErr, failure = busy, &TabFailure{Kind: TabFailureLockBusy}
	} else {
		tabErr, disable, failure = openAppleTerminalTab(tty, shellQuote(scriptPath))
		unlock()
	}
	if tabErr == "" {
		return LaunchOutcome{UsedTab: true}, nil
	}
	out := LaunchOutcome{TabError: tabErr, DisableTab: disable, TabFailure: failure}
	if err := runLauncher("open", []string{"-a", "Terminal", scriptPath}); err != nil {
		return out, fmt.Errorf("%w (a Terminal.app tab could not be opened either: %s)", err, tabErr)
	}
	return out, nil
}

// tabLockPath returns the per-user file whose flock serializes the tab path
// across processes (lockTabLaunch). It is under $HOME rather than $TMPDIR
// because a sandbox (Claude Code's Bash tool, for one) may give every
// session its own TMPDIR. A variable so tests can point it at a temp dir.
var tabLockPath = func() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".graph-ops", "autopilot", "terminal-tab.lock"), nil
}

// tabLockMargin is how much longer than tabScriptTimeout lockTabLaunch
// waits for the lock.
const tabLockMargin = 5 * time.Second

// tabLockWait bounds how long lockTabLaunch waits for another process's tab
// launch. That launch's osascript may run for all of tabScriptTimeout before
// it is killed and the lock released, so the wait is tabScriptTimeout plus
// tabLockMargin: a waiter never gives up just before one holder finishes.
// It follows defaultTabScriptTimeout when that changes. A variable so tests
// can shorten it.
//
// The wait covers one holder only. Each waiter counts it from when it
// started waiting, and lockTabLaunch only polls, with no queue, so when
// several launches wait at once the lock goes to whichever polls first
// after a release, not to the one that has waited longest. If other
// launches keep taking the lock in between, any of the waiters (not
// necessarily the latest to arrive) can reach its deadline and fall back to
// a new window. That is deliberate and not covered by a longer wait. An
// osascript normally finishes in a few seconds, so the lock normally comes
// free often enough, though with no queue nothing guarantees a given waiter
// gets it in time. The wait runs out when the holders' runs add up past it:
// one running close to tabScriptTimeout (a permission prompt left
// unanswered, say), several passing failures of several seconds each
// (9102/9103), or an unusual number of waiters. Most of these mean something
// is wrong with Terminal, and then the waiter's own tab would most likely
// fail the same way; either way it still opens a new window. And a wait
// that grew with the number of waiters would stretch a launch's worst case
// (about 45 seconds) in proportion, where a bounded wait keeps it fixed. See
// the lock in docs/autopilot.md.
var tabLockWait = defaultTabScriptTimeout + tabLockMargin

// tabLockPoll is how often lockTabLaunch retries a held lock.
const tabLockPoll = 50 * time.Millisecond

// lockTabLaunch serializes the tab path across processes -- two autopilot
// runs whose orchestrators share a window, or two launches of one run --
// so one Cmd+T and its new tab are matched up before the next Cmd+T is
// sent. appleTerminalTabScript already refuses to guess (9104) when two tabs
// appear at once; the lock is what keeps that from happening in the first
// place.
//
// It returns the function releasing the lock, or, when other processes
// (one, or several taking the lock between them) held it for all of
// tabLockWait since this call started waiting, a non-empty reason to fall
// back to a new window. Waiters are not served in any order -- whichever
// polls first after a release gets the lock -- so with several waiters any
// of them may fall back; that is intended (see tabLockWait). The lock is a
// convenience, not a requirement: if its file cannot be created or locked
// at all (no home directory, a sandbox refusing the write), the tab is
// tried without it.
func lockTabLaunch() (unlock func(), busy string) {
	noop := func() {}
	path, err := tabLockPath()
	if err != nil {
		return noop, ""
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return noop, ""
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return noop, ""
	}
	deadline := time.Now().Add(tabLockWait)
	for {
		ok, err := tryFlock(f)
		if err != nil {
			_ = f.Close()
			return noop, ""
		}
		if ok {
			// Closing the file releases the flock.
			return func() { _ = f.Close() }, ""
		}
		if !time.Now().Before(deadline) {
			_ = f.Close()
			return nil, fmt.Sprintf("other graph-ops processes kept the Terminal tab lock for more than %s", tabLockWait)
		}
		time.Sleep(tabLockPoll)
	}
}

// Phases appleTerminalTabScript logs as it goes ("graph-ops-phase: <name>"
// on stderr), in the order it reaches them. The last one in the output is
// how far a failed script got (TabFailure.Phase).
const (
	// TabPhaseWindowFound: the first Apple event to Terminal (finding the
	// orchestrator's window) answered, so Terminal's Automation permission
	// is granted.
	TabPhaseWindowFound = "window-found"
	// TabPhaseSystemEventsOK: the first Apple event to System Events (making
	// Terminal frontmost) answered too, so its Automation permission is
	// granted as well. A timeout from here on is Terminal answering slowly,
	// not a permission prompt.
	TabPhaseSystemEventsOK = "system-events-ok"
	// TabPhaseFrontmost: Terminal was seen frontmost with the target window
	// in front.
	TabPhaseFrontmost = "frontmost"
	// TabPhaseKeystrokeSent: Cmd+T was sent. A failure from here on may
	// leave the new tab empty.
	TabPhaseKeystrokeSent = "keystroke-sent"
	// TabPhaseTabFound: the new tab was found and judged to be in the
	// orchestrator's window.
	TabPhaseTabFound = "tab-found"
	// TabPhaseCommandSending: `do script` is about to run. A timeout here
	// may have started the session in the tab as well as in the fallback
	// window.
	TabPhaseCommandSending = "command-sending"
)

// tabPhases lists the phases in the order the script reaches them.
var tabPhases = []string{
	TabPhaseWindowFound, TabPhaseSystemEventsOK, TabPhaseFrontmost,
	TabPhaseKeystrokeSent, TabPhaseTabFound, TabPhaseCommandSending,
}

// tabPhasePrefix starts every phase line of the script's output.
const tabPhasePrefix = "graph-ops-phase: "

// tabPhaseRank returns phase's position in tabPhases, counting from 1, or 0
// for "" or anything that is not a phase.
func tabPhaseRank(phase string) int {
	for i, p := range tabPhases {
		if p == phase {
			return i + 1
		}
	}
	return 0
}

// splitTabScriptOutput separates the phase lines from the rest of
// osascript's output: it returns the last phase logged ("" for none) and
// the other lines, joined by spaces.
func splitTabScriptOutput(output string) (phase, rest string) {
	var others []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if p, ok := strings.CutPrefix(line, tabPhasePrefix); ok {
			if tabPhaseRank(p) > 0 {
				phase = p
			}
			continue
		}
		others = append(others, line)
	}
	return phase, strings.Join(others, " ")
}

// tabErrorNumber matches the error number osascript puts at the end of its
// "execution error: ... (<number>)" line.
var tabErrorNumber = regexp.MustCompile(`\((-?[0-9]+)\)\s*$`)

// parseTabErrorNumber returns the error number at the end of msg, or 0.
func parseTabErrorNumber(msg string) int {
	m := tabErrorNumber.FindStringSubmatch(msg)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return n
}

// openAppleTerminalTab runs appleTerminalTabScript and returns "" when the
// tab opened, or why it did not, whether that should disable the tab for
// the rest of the run, and the failure's details.
func openAppleTerminalTab(tty, command string) (tabErr string, disable bool, failure *TabFailure) {
	ctx, cancel := context.WithTimeout(context.Background(), tabScriptTimeout)
	defer cancel()

	output, err := runCommand(ctx, "osascript", "-e", appleTerminalTabScript, tty, command)
	if err == nil {
		return "", false, nil
	}
	timedOut := ctx.Err() != nil
	phase, msg := splitTabScriptOutput(string(output))
	failure = &TabFailure{Kind: TabFailureScriptError, Phase: phase, KeystrokeSent: tabPhaseRank(phase) >= tabPhaseRank(TabPhaseKeystrokeSent)}
	if timedOut {
		failure.Kind = TabFailureTimeout
		msg = tabTimeoutMessage(phase)
	} else {
		if msg == "" {
			msg = err.Error()
		}
		failure.ErrorNumber = parseTabErrorNumber(msg)
	}
	return truncateRunes("osascript: "+msg, maxTabErrorLen), classifyTabFailure(timedOut, phase, msg), failure
}

// tabTimeoutMessage says what a timeout after reaching phase most likely
// means (see classifyTabFailure).
func tabTimeoutMessage(phase string) string {
	head := fmt.Sprintf("osascript did not finish within %s", tabScriptTimeout)
	switch {
	case phase == "":
		return head + " before Terminal answered (waiting for an Automation permission prompt for Terminal?)"
	case tabPhaseRank(phase) < tabPhaseRank(TabPhaseSystemEventsOK):
		return head + " after Terminal answered but before System Events did (waiting for an Automation permission prompt for System Events?)"
	}
	msg := head + " after " + phase + " (Terminal answering slowly)"
	switch {
	case phase == TabPhaseCommandSending:
		msg += "; the session may have started in the new tab as well"
	case tabPhaseRank(phase) >= tabPhaseRank(TabPhaseKeystrokeSent):
		msg += "; a new tab may have been left empty"
	}
	return msg
}

// tabRetryableError matches the error numbers of appleTerminalTabScript's
// own errors in osascript's "execution error: ... (<number>)" line.
var tabRetryableError = regexp.MustCompile(`\((` + strings.Join([]string{
	strconv.Itoa(tabErrWindowNotFound),
	strconv.Itoa(tabErrTabNotOpened),
	strconv.Itoa(tabErrNotFrontmost),
	strconv.Itoa(tabErrNewTabAmbiguous),
}, "|") + `)\)`)

// classifyTabFailure decides LaunchOutcome.DisableTab from whether osascript
// timed out, the last phase the script logged, and its message (without the
// phase lines).
//
// A timeout disables the tab only when the script had not yet heard from
// both Terminal and System Events (no phase, or TabPhaseWindowFound): the
// first Apple event to either app waits on its Automation permission
// prompt, and one nobody answers would cost tabScriptTimeout on every
// launch. That also takes in Terminal merely answering slowly at the start
// (reading its tabs before Cmd+T, activating it), as it always did. From
// TabPhaseSystemEventsOK on, both permissions are granted, so a timeout is
// Terminal answering slowly: it does not disable the tab by itself, and the
// runner disables it after runner.MaxSlowTabTimeouts of them in a row (TabFailure.
// SlowTimeout). Before DFLT-00361 every timeout disabled the tab, so one
// slow moment in a long run sent all its later sessions to new windows.
//
// Otherwise it is an allow list: only the script's own quick errors
// (9101-9104) keep the tab for the next launch; everything else -- a missing
// Automation (-1743) or Accessibility (1002, -25211, -1719) permission, a
// sandbox refusing Apple events, Terminal's state that could not be read at
// all while waiting for it to come to the front (the script rethrows that
// read error with its own number rather than 9103), osascript missing,
// anything not foreseen -- disables it, so a run pays for such a failure
// once rather than on every launch, without depending on a complete list of
// macOS error numbers.
func classifyTabFailure(timedOut bool, phase, msg string) bool {
	if timedOut {
		return tabPhaseRank(phase) < tabPhaseRank(TabPhaseSystemEventsOK)
	}
	return !tabRetryableError.MatchString(msg)
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
