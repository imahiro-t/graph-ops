// Package terminal opens an external, interactive terminal running `claude`
// with a seeded prompt -- instead of the server spawning claude headlessly
// with --dangerously-skip-permissions, the human sees and drives the session
// themselves, approving tool use as normal.
package terminal

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// goos is a package variable (not a direct runtime.GOOS reference) so tests
// can fake a different platform.
var goos = runtime.GOOS

// lookPath is a package variable (not a direct exec.LookPath reference) so
// tests can fake whether Windows Terminal is installed without depending on
// the actual PATH of the machine running the test.
var lookPath = exec.LookPath

// Config carries the one user-configurable override: an arbitrary shell
// command template for terminal emulators auto-detection doesn't cover
// (iTerm, wezterm, kitty, VS Code, cmux, Windows Terminal, ...).
type Config struct {
	// TerminalCommand is a shell command template with two placeholders:
	// {cwd} (the project directory, already shell-quoted) and {command}
	// (the `claude "<prompt>"` invocation). In {command} the prompt is
	// shell-quoted but the claude binary/command is not, because it is
	// configured as a shell fragment -- see Launch's doc comment
	// (DFLT-00023 C-8). When set, this template always takes precedence
	// over auto-detection.
	TerminalCommand string
}

// Launch opens a terminal running `claudeBin "<prompt>"` in workDir. It waits
// for the launcher itself to finish (tmux/open/a custom terminalCommand are
// all expected to hand off and return within a second or two) so real
// failures are caught and returned instead of silently reporting success; it
// does NOT wait for the interactive claude session that launcher opens. A
// 10s timeout guards against a misbehaving custom terminalCommand hanging
// the request.
//
// claudeBin is deliberately NOT run through shellQuote on the shell-backed
// paths below, unlike workDir and prompt (DFLT-00023 C-8). It is specified
// as a *shell command fragment*, not as a path to one executable: a value
// like "npx claude" or "mise exec -- claude" is supported and is interpolated
// into `sh -c` for the shell to word-split and expand. That is the whole
// reason quoting it is wrong -- quoting would turn "npx claude" into a
// request for a single executable of that literal name, breaking the
// configurations that rely on it today. The consequence to be aware of is
// the flip side: a binary path containing a space or a shell metacharacter
// (e.g. /Users/My Name/bin/claude) cannot be given bare, and must be
// pre-quoted by whoever sets it.
//
// The value reaches here only from the CLAUDE_BIN environment variable or
// the home config file's "claudeBinary" -- both local, both outside the HTTP
// API's editable surface (the app-settings tab does not expose it) -- so
// this is a configuration contract, not an injection sink. Anyone who can
// write those already runs code as this user.
//
// The tmux path is the one exception and cannot honor the fragment form:
// tmux execs the trailing argv directly with no shell, so claudeBin is
// passed there as a single argv element and only a plain executable name or
// path works. Changing any of this changes existing users' configs, so it
// is a deliberate spec decision, not an oversight.
//
// The windows path preserves the fragment contract the same way darwin
// does: claudeBin is written raw into a generated PowerShell script for
// PowerShell to word-split, not passed as a literal argv element. It uses
// PowerShell rather than a cmd.exe batch script specifically because
// PowerShell's single-quoted string literals -- like POSIX sh's -- may
// contain a literal newline and are still parsed as one token; cmd.exe's
// batch parser is line-oriented and has no such construct; a quoted
// argument that contains an embedded "\n" or "\r\n" (prompt text routinely
// does -- see withTicketContext in claude_launch.go) is parsed by cmd.exe as
// the end of the current command and the start of a new one, silently
// truncating the argument and letting the remainder run as unrelated
// command lines. See TestWriteWindowsCommandScript_PreservesEmbeddedNewline.
func Launch(cfg Config, workDir, claudeBin, prompt string) error {
	return LaunchWithArgs(cfg, workDir, claudeBin, nil, prompt)
}

// LaunchWithArgs is Launch with extra command-line arguments for claude,
// placed between claudeBin and the prompt -- the autopilot uses it to pass
// `--permission-mode <mode>` (DFLT-00142). Launch is LaunchWithArgs with no
// extra arguments, and produces exactly what it produced before they existed.
//
// Every extra argument is passed as one argument on every launch path, quoted
// by the same rule as the prompt: shellQuote for the `sh -c` template and the
// darwin .command script, powershellQuote for the windows .ps1, and a separate
// argv element for tmux (no shell involved). The values the autopilot passes
// are validated against an enum before they get here, but the quoting is not
// skipped on that account: it is what keeps a future argument from becoming
// an injection sink. claudeBin keeps its documented "shell fragment" contract
// (see Launch) -- only the extra arguments and the prompt are quoted.
func LaunchWithArgs(cfg Config, workDir, claudeBin string, extraArgs []string, prompt string) error {
	_, err := LaunchWithOptions(cfg, workDir, claudeBin, extraArgs, prompt, LaunchOptions{})
	return err
}

// LaunchOptions are the autopilot's additions to LaunchWithArgs (DFLT-00154).
// The zero value is LaunchWithArgs exactly.
type LaunchOptions struct {
	// AppleTerminalTTY is the tty of the Terminal.app tab whose window the
	// session should open in as a new tab (see DetectAppleTerminalTTY). ""
	// never tries a tab.
	AppleTerminalTTY string
	// SkipAppleTerminalTab never tries a tab, whatever AppleTerminalTTY is:
	// the run has already seen the tab path fail in a way that will not
	// improve by itself (see LaunchOutcome.DisableTab).
	SkipAppleTerminalTab bool
}

// LaunchOutcome says how LaunchWithOptions opened the session when it
// returned no error. It is the zero value on every path other than the
// Terminal.app tab path.
type LaunchOutcome struct {
	// UsedTab: the session opened as a new tab of the AppleTerminalTTY
	// window.
	UsedTab bool
	// TabError is why the tab could not be opened, when it was tried and the
	// session was opened in a new window instead; "" when no tab was tried
	// or the tab opened.
	TabError string
	// DisableTab: the tab failure is of a kind that will repeat (a missing
	// Automation/Accessibility permission, a timeout waiting on a permission
	// prompt, osascript not runnable, anything unexpected), so later
	// launches of the same run should not try a tab again. The quick,
	// passing failures -- a window that could not be found, a tab that did
	// not appear, Terminal not coming to the front, more than one new tab
	// at once, another process still opening a tab -- leave it false.
	DisableTab bool
}

// LaunchWithOptions is LaunchWithArgs with the autopilot's options. The
// error keeps LaunchWithArgs's meaning -- no terminal could be opened at
// all -- so a tab that fails and falls back to a new window is not an
// error: the outcome carries why the tab failed, and reporting it is the
// caller's job (this package logs nothing).
//
// The tab path is taken only on darwin with no terminalCommand, outside
// tmux, with SkipAppleTerminalTab unset and AppleTerminalTTY a valid
// /dev/ttysN; everything else goes through exactly the argv
// buildLaunchArgvWithArgs builds, as LaunchWithArgs always has.
func LaunchWithOptions(cfg Config, workDir, claudeBin string, extraArgs []string, prompt string, opts LaunchOptions) (LaunchOutcome, error) {
	if useAppleTerminalTab(cfg, opts) {
		return launchAppleTerminalTab(workDir, claudeBin, extraArgs, prompt, opts.AppleTerminalTTY)
	}
	name, args, err := buildLaunchArgvWithArgs(cfg, workDir, claudeBin, extraArgs, prompt)
	if err != nil {
		return LaunchOutcome{}, err
	}
	return LaunchOutcome{}, runLauncher(name, args)
}

// runCommand runs a launcher command and returns its combined output. A
// package variable so tests can fake osascript and open instead of starting
// real processes.
var runCommand = func(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// launcherTimeout bounds one launcher command (see Launch's doc comment). A
// variable so tests can shorten it.
var launcherTimeout = 10 * time.Second

// runLauncher runs the launcher argv under launcherTimeout, turning a
// failure into the error Launch has always returned.
func runLauncher(name string, args []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), launcherTimeout)
	defer cancel()

	output, err := runCommand(ctx, name, args...)
	if err != nil {
		msg := strings.TrimSpace(string(output))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("failed to open terminal: %s", msg)
	}
	return nil
}

// buildLaunchArgv picks the launch strategy and returns the argv to run it.
// Kept separate from Launch so the selection logic is unit-testable; the
// darwin and windows branches each have the one unavoidable side effect of
// writing a small script file (see writeCommandScript and
// writeWindowsCommandScript).
func buildLaunchArgv(cfg Config, workDir, claudeBin, prompt string) (string, []string, error) {
	return buildLaunchArgvWithArgs(cfg, workDir, claudeBin, nil, prompt)
}

// buildLaunchArgvWithArgs is buildLaunchArgv with claude's extra arguments
// (see LaunchWithArgs). With none, every path produces exactly what
// buildLaunchArgv produced before they existed.
func buildLaunchArgvWithArgs(cfg Config, workDir, claudeBin string, extraArgs []string, prompt string) (string, []string, error) {
	if cfg.TerminalCommand != "" {
		// claudeBin unquoted on purpose -- see Launch's doc comment
		// (DFLT-00023 C-8): it is a shell command fragment the user
		// configured, and `sh -c` below is meant to word-split it. The
		// extra arguments and the prompt are data, and are quoted.
		replaced := strings.NewReplacer(
			"{cwd}", shellQuote(workDir),
			"{command}", claudeBin+" "+quoteArgs(shellQuote, extraArgs)+shellQuote(prompt),
		).Replace(cfg.TerminalCommand)
		return "sh", []string{"-c", replaced}, nil
	}

	if os.Getenv("TMUX") != "" {
		// tmux treats more than one trailing argument as a literal argv to
		// exec directly (no shell involved), so no quoting is needed here:
		// each extra argument is its own argv element.
		argv := []string{"new-window", "-c", workDir, claudeBin}
		argv = append(argv, extraArgs...)
		argv = append(argv, prompt)
		return "tmux", argv, nil
	}

	if goos == "darwin" {
		// `open -a Terminal <script>` uses Launch Services, not AppleEvents,
		// so unlike `osascript ... tell application "Terminal" to do script`
		// it needs no Automation permission grant at all.
		scriptPath, err := writeCommandScriptWithArgs(workDir, claudeBin, extraArgs, prompt)
		if err != nil {
			return "", nil, fmt.Errorf("preparing terminal launch script: %w", err)
		}
		return "open", []string{"-a", "Terminal", scriptPath}, nil
	}

	if goos == "windows" {
		scriptPath, err := writeWindowsCommandScriptWithArgs(workDir, claudeBin, extraArgs, prompt)
		if err != nil {
			return "", nil, fmt.Errorf("preparing terminal launch script: %w", err)
		}
		// -NoExit keeps the PowerShell session interactive after the
		// script's claude invocation returns, mirroring the darwin script's
		// behaviour of leaving the window open rather than closing it out
		// from under the user. -NoProfile skips the user's profile script
		// (faster startup, and one less place for unrelated user
		// configuration to interfere). -ExecutionPolicy Bypass overrides
		// only this one process's policy so the generated, unsigned script
		// runs regardless of the machine's default (commonly Restricted),
		// without touching that machine-wide setting.
		psArgs := []string{"-NoExit", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", scriptPath}
		if _, err := lookPath("wt.exe"); err == nil {
			// Windows Terminal is on PATH: open a new tab/window running
			// PowerShell with the script.
			return "wt.exe", append([]string{"powershell"}, psArgs...), nil
		}
		// No Windows Terminal on PATH: fall back to a plain console host.
		// cmd.exe itself has no "open a new window" flag -- running it
		// directly would inherit this process's (invisible) console -- so
		// its own `start` builtin is used to spawn a detached window
		// running PowerShell with the script. `start` treats its first
		// quoted argument as the new window's title when more arguments
		// follow, hence the explicit "Claude Code" title rather than an
		// empty one.
		return "cmd.exe", append([]string{"/c", "start", "Claude Code", "powershell"}, psArgs...), nil
	}

	return "", nil, fmt.Errorf(
		"no terminal launch method available on %s: set \"terminalCommand\" in $HOME/.graph-ops/config.json "+
			"(a shell template using {cwd} and {command}, e.g. for iTerm/wezterm/Windows Terminal/VS Code/cmux)",
		goos,
	)
}

// writeCommandScript writes a small executable .command script that cds into
// workDir and runs claudeBin with prompt, for Terminal.app to run when opened
// via `open`. The file is intentionally left in place rather than cleaned up
// immediately: Terminal reads and executes it asynchronously, and there is no
// reliable signal here for when it's safe to delete (a stale file in the
// system temp dir is a fine trade-off for a low-volume local dev tool).
//
// Because the file outlives this call, its mode is 0o700 rather than 0o755:
// the script body embeds the prompt (ticket text) in plaintext, and the only
// thing that ever has to run it is Terminal.app running as this same user,
// so group/other need neither read nor execute. On macOS -- the only OS that
// reaches this path -- $TMPDIR is already a per-user 0700 directory, so this
// is defence in depth for the configurations where TMPDIR points at a shared
// directory such as /tmp instead.
func writeCommandScript(workDir, claudeBin, prompt string) (string, error) {
	return writeCommandScriptWithArgs(workDir, claudeBin, nil, prompt)
}

// writeCommandScriptWithArgs is writeCommandScript with claude's extra
// arguments, each shellQuote'd like the prompt (see LaunchWithArgs).
func writeCommandScriptWithArgs(workDir, claudeBin string, extraArgs []string, prompt string) (string, error) {
	f, err := os.CreateTemp("", "graph-engine-launch-*.command")
	if err != nil {
		return "", err
	}
	defer f.Close()

	// Same split as buildLaunchArgv: workDir and prompt are quoted, claudeBin
	// is interpolated raw so bash word-splits it (DFLT-00023 C-8).
	script := fmt.Sprintf("#!/bin/bash\ncd %s\n%s %s%s\n", shellQuote(workDir), claudeBin, quoteArgs(shellQuote, extraArgs), shellQuote(prompt))
	if _, err := f.WriteString(script); err != nil {
		return "", err
	}
	path := f.Name()
	if err := os.Chmod(path, 0o700); err != nil {
		return "", err
	}
	return filepath.Clean(path), nil
}

// shellQuote wraps s in single quotes, escaping any embedded single quote,
// producing a token that is safe to embed in a POSIX `sh -c` command
// regardless of s's contents.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// quoteArgs quotes each of args with quote and joins them, each followed by
// one space, so the result sits directly in front of the quoted prompt. No
// arguments give "", which keeps Launch's command line (no extra arguments)
// byte-for-byte what it was before extra arguments existed.
func quoteArgs(quote func(string) string, args []string) string {
	var b strings.Builder
	for _, a := range args {
		b.WriteString(quote(a))
		b.WriteByte(' ')
	}
	return b.String()
}

// utf8BOM is the UTF-8 encoding of U+FEFF (bytes EF BB BF), prepended to the
// generated .ps1 so Windows PowerShell 5.1 reads it as UTF-8 (see
// writeWindowsCommandScript).
const utf8BOM = "\ufeff"

// writeWindowsCommandScript writes a small PowerShell (.ps1) script that cds
// into workDir and runs claudeBin with prompt, for PowerShell to execute when
// opened via wt.exe or cmd.exe's `start` (see buildLaunchArgv). It is the
// windows counterpart to writeCommandScript and is left in place for the
// same reason: PowerShell/wt.exe read and run it asynchronously, and there
// is no reliable signal here for when it is safe to delete.
//
// A PowerShell script is used here rather than a cmd.exe batch script
// because cmd.exe's batch parser is line-oriented: a quoted token can never
// contain a literal newline, no matter how it's quoted, because the parser
// treats every physical line break as the end of the current command. Since
// prompt text routinely contains newlines (see withTicketContext in
// claude_launch.go), a batch script broke on exactly the input this
// function most needs to handle correctly -- see
// TestWriteWindowsCommandScript_PreservesEmbeddedNewline. PowerShell's
// single-quoted string literals, like POSIX sh's, may span multiple
// physical lines and are still parsed as one token, so this problem does
// not exist for the .ps1 form.
//
// Unlike writeCommandScript, the file's permissions are not tightened after
// creation: Go's os.CreateTemp on Windows has no POSIX mode bits to narrow,
// and ACLs are not touched here either -- os.CreateTemp under the per-user
// %TEMP% directory already inherits that directory's (owner-only-by-default)
// ACL, so no additional narrowing is done. Locking this down further would
// require an explicit ACL call (e.g. via golang.org/x/sys/windows), which is
// treated as out of scope for this change; the script body embeds the
// prompt (ticket text) in plaintext the same way the darwin script does, so
// this is a known, accepted gap rather than an oversight.
//
// The file is written as UTF-8 *with* a byte-order mark (utf8BOM). Windows
// PowerShell 5.1 -- which is what `powershell` always resolves to -- reads a
// BOM-less .ps1 in the system ANSI code page (CP932 on a Japanese-locale
// system) rather than UTF-8, so any non-ASCII text embedded in the script
// (the prompt, workDir, or a claudeBin install path) would be garbled unless
// the user enabled the OS-wide "Beta: Use Unicode UTF-8" setting, which
// breaks other applications. The BOM makes PowerShell read the script as
// UTF-8 regardless of the system locale; PowerShell 7 also honours it, so it
// is harmless there.
func writeWindowsCommandScript(workDir, claudeBin, prompt string) (string, error) {
	return writeWindowsCommandScriptWithArgs(workDir, claudeBin, nil, prompt)
}

// writeWindowsCommandScriptWithArgs is writeWindowsCommandScript with
// claude's extra arguments, each powershellQuote'd like the prompt (see
// LaunchWithArgs).
func writeWindowsCommandScriptWithArgs(workDir, claudeBin string, extraArgs []string, prompt string) (string, error) {
	f, err := os.CreateTemp("", "graph-engine-launch-*.ps1")
	if err != nil {
		return "", err
	}
	defer f.Close()

	// Same split as buildLaunchArgv: workDir and prompt are quoted (via
	// powershellQuote), claudeBin is interpolated raw so PowerShell
	// word-splits it (DFLT-00023 C-8). -LiteralPath (rather than the
	// positional form) tells Set-Location to treat workDir as a literal
	// path with no wildcard expansion, and switches drives automatically if
	// workDir is on a different drive than the one PowerShell started on.
	script := fmt.Sprintf("Set-Location -LiteralPath %s\r\n%s %s%s\r\n", powershellQuote(workDir), claudeBin, quoteArgs(powershellQuote, extraArgs), powershellQuote(prompt))
	if _, err := f.WriteString(utf8BOM + script); err != nil {
		return "", err
	}
	return filepath.Clean(f.Name()), nil
}

// powerShellSingleQuotes are every rune PowerShell's tokenizer accepts as a
// single quote: the ASCII apostrophe plus the four "smart quote" characters
// its IsSingleQuote() classifies alongside it -- U+2018 LEFT SINGLE
// QUOTATION MARK, U+2019 RIGHT SINGLE QUOTATION MARK, U+201A SINGLE LOW-9
// QUOTATION MARK and U+201B SINGLE HIGH-REVERSED-9 QUOTATION MARK.
//
// They are one class throughout: any of them opens a literal string, any of
// them closes it, and the doubling escape is "the next character is in this
// same class" rather than "the next character is the same character". That
// is why powershellQuote has to double all five and not just the ASCII one
// (finding CHK-02 of DFLT-00104): text containing a smart quote -- which
// prose routinely does, since editors and phones substitute one for an
// apostrophe automatically -- would otherwise close the quoted string this
// function opened, and everything after it in the prompt would be parsed as
// PowerShell rather than as data.
const powerShellSingleQuotes = "'‘’‚‛"

// powershellQuote wraps s in single quotes, escaping every embedded single
// quote (all five of them -- see powerShellSingleQuotes) by doubling it,
// producing a token that is safe to embed in a generated PowerShell script
// regardless of s's contents. It plays the same role shellQuote plays for
// the POSIX script, and for the same reason: a PowerShell single-quoted
// string is fully literal -- no character (not '%', '"', '&', '|', '<', '>',
// '^', '!', '$', a backtick, nor a literal newline) has any special meaning
// inside one, and the only escape rule is that a single quote is written as
// two. This is also why PowerShell was chosen over a cmd.exe batch script
// for this file: cmd.exe has no quoting construct that survives an embedded
// newline (see writeWindowsCommandScript's doc comment), while this one
// does.
//
// The opening and closing quotes are always the ASCII one. A doubled smart
// quote inside the literal is folded back to a single character of the same
// kind by PowerShell's own unescaping, so the text the prompt carries is
// preserved as well as contained.
//
// shellQuote, its POSIX counterpart, deliberately stays ASCII-only: `sh`
// gives the smart quotes no syntactic meaning at all, so doubling them there
// would corrupt the prompt rather than protect it.
func powershellQuote(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('\'')
	for _, r := range s {
		b.WriteRune(r)
		if strings.ContainsRune(powerShellSingleQuotes, r) {
			b.WriteRune(r)
		}
	}
	b.WriteByte('\'')
	return b.String()
}
