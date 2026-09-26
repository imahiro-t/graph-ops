package terminal

import (
	"context"
	"fmt"
	"os"
	"os/exec"
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
func useAppleTerminalTab(cfg Config, opts LaunchOptions) bool {
	return goos == "darwin" && cfg.TerminalCommand == "" && os.Getenv("TMUX") == "" &&
		!opts.SkipAppleTerminalTab && validAppleTerminalTTY(opts.AppleTerminalTTY)
}

// Error numbers appleTerminalTabScript raises itself. They are the two
// failures that return quickly and may not happen on the next launch, so
// they do not disable the tab for the run (see classifyTabFailure).
const (
	tabErrWindowNotFound = 9101
	tabErrTabNotOpened   = 9102
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
// references. `do script` is the last statement: every failure before it
// ends the script with an error, so falling back to a new window never runs
// the session twice.
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
		set tabCount to count of tabs of window id targetID
		set index of window id targetID to 1
		activate
	end tell
	tell application "System Events" to tell process "Terminal" to keystroke "t" using command down
	set opened to false
	repeat 30 times
		try
			tell application "Terminal" to set nowCount to count of tabs of window id targetID
		on error
			error "graph-ops: the Terminal window of " & targetTTY & " went away" number 9101
		end try
		if nowCount > tabCount then
			set opened to true
			exit repeat
		end if
		delay 0.1
	end repeat
	if not opened then error "graph-ops: no new tab appeared in the Terminal window of " & targetTTY number 9102
	tell application "Terminal" to do script shellCommand in (selected tab of window id targetID)
end run`

// tabScriptTimeout bounds the osascript run. The script itself gives up
// after about 3 seconds of waiting for the tab; what takes longer is almost
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
	tabErr, disable := openAppleTerminalTab(tty, shellQuote(scriptPath))
	if tabErr == "" {
		return LaunchOutcome{UsedTab: true}, nil
	}
	out := LaunchOutcome{TabError: tabErr, DisableTab: disable}
	if err := runLauncher("open", []string{"-a", "Terminal", scriptPath}); err != nil {
		return out, fmt.Errorf("%w (a Terminal.app tab could not be opened either: %s)", err, tabErr)
	}
	return out, nil
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
var tabRetryableError = regexp.MustCompile(`\((` + strconv.Itoa(tabErrWindowNotFound) + `|` + strconv.Itoa(tabErrTabNotOpened) + `)\)`)

// classifyTabFailure decides LaunchOutcome.DisableTab. It is an allow list:
// only the script's own two quick errors keep the tab for the next launch;
// everything else -- a timeout, a missing Automation (-1743) or
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
