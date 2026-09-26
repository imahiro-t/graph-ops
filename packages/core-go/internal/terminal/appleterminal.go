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
	// or it closed before the command could be run in it.
	tabErrTabNotOpened = 9102
	// tabErrNotFrontmost: Terminal, with the target window in front, did
	// not become the frontmost app, so Cmd+T was never sent (it would have
	// gone to whatever app the user is working in).
	tabErrNotFrontmost = 9103
	// tabErrNewTabAmbiguous: more than one new tab appeared in the window
	// (another process opened one at the same time), so which one is ours
	// cannot be told and the command is run in none of them.
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
//     the target window in front (polled for up to about 2 seconds);
//     otherwise the script sends no key at all and fails with 9103.
//   - The command is never sent to "the selected tab", which the user (or
//     another run's launch in the same window) can change at any moment.
//     The ttys of the window's tabs are recorded before Cmd+T, and the
//     command goes to the one tab whose tty is new. No new tab within about
//     3 seconds is 9102; more than one (someone else opened a tab at the
//     same time) is 9104, and the command is run in none of them.
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
const appleTerminalTabScript = `on run argv
	set targetTTY to item 1 of argv
	set shellCommand to item 2 of argv
	tell application "Terminal"
		set targetID to missing value
		repeat with w in windows
			try
				repeat with t in tabs of w
					if tty of t is targetTTY then
						set targetID to id of w
						exit repeat
					end if
				end repeat
			end try
			if targetID is not missing value then exit repeat
		end repeat
		if targetID is missing value then error "graph-ops: no Terminal window has a tab on " & targetTTY number 9101
		set knownTTYs to tty of tabs of window id targetID
		set index of window id targetID to 1
		activate
	end tell
	tell application "System Events" to set frontmost of process "Terminal" to true
	set inFront to false
	repeat 20 times
		try
			tell application "System Events" to set terminalFront to frontmost of process "Terminal"
			tell application "Terminal" to set frontID to id of front window
			if terminalFront and frontID is targetID then
				set inFront to true
				exit repeat
			end if
		end try
		delay 0.1
	end repeat
	if not inFront then error "graph-ops: Terminal did not come to the front with the window of " & targetTTY & ", so no key was sent" number 9103
	tell application "System Events" to tell process "Terminal" to keystroke "t" using command down
	set newTTY to missing value
	repeat 30 times
		try
			tell application "Terminal" to set nowTTYs to tty of tabs of window id targetID
		on error
			error "graph-ops: the Terminal window of " & targetTTY & " went away" number 9101
		end try
		set freshTTYs to {}
		repeat with x in nowTTYs
			set v to contents of x
			if v is not missing value and v is not "" and knownTTYs does not contain v then set end of freshTTYs to v
		end repeat
		if (count of freshTTYs) > 1 then error "graph-ops: more than one new tab appeared in the Terminal window of " & targetTTY number 9104
		if (count of freshTTYs) is 1 then
			set newTTY to item 1 of freshTTYs
			exit repeat
		end if
		delay 0.1
	end repeat
	if newTTY is missing value then error "graph-ops: no new tab appeared in the Terminal window of " & targetTTY number 9102
	set newTab to missing value
	tell application "Terminal"
		try
			repeat with t in tabs of window id targetID
				if tty of t is newTTY then
					set newTab to contents of t
					exit repeat
				end if
			end repeat
		end try
	end tell
	if newTab is missing value then error "graph-ops: the new tab in the Terminal window of " & targetTTY & " closed" number 9102
	tell application "Terminal" to do script shellCommand in newTab
end run`

// tabScriptTimeout bounds the osascript run. The script itself gives up
// after about 2 seconds of waiting for Terminal to come to the front and 3
// seconds of waiting for the tab; what takes longer is almost
// always a permission prompt nobody answers. A variable so tests can
// shorten it.
var tabScriptTimeout = 10 * time.Second

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
	if unlock, busy := lockTabLaunch(); busy != "" {
		// Another graph-ops process is still opening a tab: a quick,
		// passing failure like 9101-9104, so the tab stays enabled.
		tabErr = busy
	} else {
		tabErr, disable = openAppleTerminalTab(tty, shellQuote(scriptPath))
		unlock()
	}
	if tabErr == "" {
		return LaunchOutcome{UsedTab: true}, nil
	}
	out := LaunchOutcome{TabError: tabErr, DisableTab: disable}
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

// tabLockWait bounds how long lockTabLaunch waits for another process's tab
// launch (which itself is bounded by tabScriptTimeout). A variable so tests
// can shorten it.
var tabLockWait = 10 * time.Second

// tabLockPoll is how often lockTabLaunch retries a held lock.
const tabLockPoll = 50 * time.Millisecond

// lockTabLaunch serializes the tab path across processes -- two autopilot
// runs whose orchestrators share a window, or two launches of one run --
// so one Cmd+T and its new tab are matched up before the next Cmd+T is
// sent. appleTerminalTabScript already refuses to guess (9104) when two tabs
// appear at once; the lock is what keeps that from happening in the first
// place.
//
// It returns the function releasing the lock, or, when another process
// held it for all of tabLockWait, a non-empty reason to fall back to a new
// window. The lock is a convenience, not a requirement: if its file cannot
// be created or locked at all (no home directory, a sandbox refusing the
// write), the tab is tried without it.
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
			return nil, fmt.Sprintf("another graph-ops process kept opening a Terminal tab for more than %s", tabLockWait)
		}
		time.Sleep(tabLockPoll)
	}
}

// openAppleTerminalTab runs appleTerminalTabScript and returns "" when the
// tab opened, or why it did not and whether that should disable the tab for
// the rest of the run.
func openAppleTerminalTab(tty, command string) (tabErr string, disable bool) {
	ctx, cancel := context.WithTimeout(context.Background(), tabScriptTimeout)
	defer cancel()

	output, err := runCommand(ctx, "osascript", "-e", appleTerminalTabScript, tty, command)
	if err == nil {
		return "", false
	}
	timedOut := ctx.Err() != nil
	msg := strings.TrimSpace(string(output))
	if timedOut {
		msg = fmt.Sprintf("osascript did not finish within %s (waiting for an Automation or Accessibility permission prompt?)", tabScriptTimeout)
	} else if msg == "" {
		msg = err.Error()
	}
	return truncateRunes("osascript: "+msg, maxTabErrorLen), classifyTabFailure(timedOut, msg)
}

// tabRetryableError matches the error numbers of appleTerminalTabScript's
// own errors in osascript's "execution error: ... (<number>)" line.
var tabRetryableError = regexp.MustCompile(`\((` + strings.Join([]string{
	strconv.Itoa(tabErrWindowNotFound),
	strconv.Itoa(tabErrTabNotOpened),
	strconv.Itoa(tabErrNotFrontmost),
	strconv.Itoa(tabErrNewTabAmbiguous),
}, "|") + `)\)`)

// classifyTabFailure decides LaunchOutcome.DisableTab. It is an allow list:
// only the script's own quick errors (9101-9104) keep the tab for the next
// launch; everything else -- a timeout, a missing Automation (-1743) or
// Accessibility (-25211, -1719) permission, a sandbox refusing Apple
// events, osascript missing, anything not foreseen -- disables it, so a run
// pays for such a failure (up to tabScriptTimeout) once rather than on every
// launch, without depending on a complete list of macOS error numbers.
func classifyTabFailure(timedOut bool, msg string) bool {
	if timedOut {
		return true
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
