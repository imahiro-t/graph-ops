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
// graph-config.json's "claudeBinary" -- both local, both outside the HTTP
// API's editable surface (the app-settings tab does not expose it) -- so
// this is a configuration contract, not an injection sink. Anyone who can
// write those already runs code as this user.
//
// The tmux path is the one exception and cannot honor the fragment form:
// tmux execs the trailing argv directly with no shell, so claudeBin is
// passed there as a single argv element and only a plain executable name or
// path works. Changing any of this changes existing users' configs, so it
// is a deliberate spec decision, not an oversight.
func Launch(cfg Config, workDir, claudeBin, prompt string) error {
	name, args, err := buildLaunchArgv(cfg, workDir, claudeBin, prompt)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
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
// darwin branch has the one unavoidable side effect of writing a small
// script file (see writeCommandScript).
func buildLaunchArgv(cfg Config, workDir, claudeBin, prompt string) (string, []string, error) {
	if cfg.TerminalCommand != "" {
		// claudeBin unquoted on purpose -- see Launch's doc comment
		// (DFLT-00023 C-8): it is a shell command fragment the user
		// configured, and `sh -c` below is meant to word-split it.
		replaced := strings.NewReplacer(
			"{cwd}", shellQuote(workDir),
			"{command}", claudeBin+" "+shellQuote(prompt),
		).Replace(cfg.TerminalCommand)
		return "sh", []string{"-c", replaced}, nil
	}

	if os.Getenv("TMUX") != "" {
		// tmux treats more than one trailing argument as a literal argv to
		// exec directly (no shell involved), so no quoting is needed here.
		return "tmux", []string{"new-window", "-c", workDir, claudeBin, prompt}, nil
	}

	if goos == "darwin" {
		// `open -a Terminal <script>` uses Launch Services, not AppleEvents,
		// so unlike `osascript ... tell application "Terminal" to do script`
		// it needs no Automation permission grant at all.
		scriptPath, err := writeCommandScript(workDir, claudeBin, prompt)
		if err != nil {
			return "", nil, fmt.Errorf("preparing terminal launch script: %w", err)
		}
		return "open", []string{"-a", "Terminal", scriptPath}, nil
	}

	return "", nil, fmt.Errorf(
		"no terminal launch method available on %s: set \"terminalCommand\" in graph-config.json "+
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
	f, err := os.CreateTemp("", "graph-engine-launch-*.command")
	if err != nil {
		return "", err
	}
	defer f.Close()

	// Same split as buildLaunchArgv: workDir and prompt are quoted, claudeBin
	// is interpolated raw so bash word-splits it (DFLT-00023 C-8).
	script := fmt.Sprintf("#!/bin/bash\ncd %s\n%s %s\n", shellQuote(workDir), claudeBin, shellQuote(prompt))
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
