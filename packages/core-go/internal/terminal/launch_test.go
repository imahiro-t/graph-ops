package terminal

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestBuildLaunchArgv_TerminalCommandTakesPrecedence(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-1/default,123,0") // even with tmux active...
	cfg := Config{TerminalCommand: "wezterm cli spawn --cwd {cwd} -- sh -c {command}"}

	name, args, err := buildLaunchArgv(cfg, "/proj", "claude", "hello")
	if err != nil {
		t.Fatalf("buildLaunchArgv: %v", err)
	}
	if name != "sh" || len(args) != 2 || args[0] != "-c" {
		t.Fatalf("expected sh -c wrapper, got %s %v", name, args)
	}
	if !strings.Contains(args[1], "wezterm cli spawn") || !strings.Contains(args[1], "/proj") {
		t.Errorf("template substitution missing expected content: %q", args[1])
	}
}

func TestBuildLaunchArgv_TmuxWhenActive(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-1/default,123,0")

	name, args, err := buildLaunchArgv(Config{}, "/proj", "claude", "do the thing")
	if err != nil {
		t.Fatalf("buildLaunchArgv: %v", err)
	}
	if name != "tmux" {
		t.Fatalf("expected tmux, got %s", name)
	}
	want := []string{"new-window", "-c", "/proj", "claude", "do the thing"}
	if len(args) != len(want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, args[i], want[i])
		}
	}
}

func TestBuildLaunchArgv_DarwinUsesOpenNotAppleEvents(t *testing.T) {
	t.Setenv("TMUX", "")
	old := goos
	goos = "darwin"
	defer func() { goos = old }()

	name, args, err := buildLaunchArgv(Config{}, "/proj", "claude", `say "hi" for me`)
	if err != nil {
		t.Fatalf("buildLaunchArgv: %v", err)
	}
	// `open -a Terminal <script>` uses Launch Services, not AppleEvents, so
	// it needs no macOS Automation permission grant (unlike the osascript
	// `tell application "Terminal" to do script` approach this replaced).
	if name != "open" || len(args) != 3 || args[0] != "-a" || args[1] != "Terminal" {
		t.Fatalf("expected open -a Terminal <script>, got %s %v", name, args)
	}
	scriptPath := args[2]
	defer os.Remove(scriptPath)

	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("reading generated script: %v", err)
	}
	got := string(content)
	if !strings.Contains(got, "cd '/proj'") {
		t.Errorf("script missing quoted cwd: %q", got)
	}
	if !strings.Contains(got, `claude 'say "hi" for me'`) {
		t.Errorf("script missing quoted claude invocation: %q", got)
	}

	info, err := os.Stat(scriptPath)
	if err != nil {
		t.Fatalf("stat script: %v", err)
	}
	if info.Mode()&0o100 == 0 {
		t.Errorf("expected script to be executable, mode = %v", info.Mode())
	}
}

func TestBuildLaunchArgv_NoStrategyAvailable(t *testing.T) {
	t.Setenv("TMUX", "")
	old := goos
	goos = "linux"
	defer func() { goos = old }()

	_, _, err := buildLaunchArgv(Config{}, "/proj", "claude", "hi")
	if err == nil {
		t.Fatal("expected an error when no launch strategy is available")
	}
	if !strings.Contains(err.Error(), "terminalCommand") {
		t.Errorf("expected error to mention terminalCommand, got: %v", err)
	}
}

// TestLaunch_SurfacesLauncherFailure guards against the exact bug that shipped
// initially: Launch used cmd.Start() and ignored the launcher's exit code/
// stderr, so a real failure (e.g. macOS "not permitted to send Apple Events
// to Terminal") was reported to the caller as success.
func TestLaunch_SurfacesLauncherFailure(t *testing.T) {
	cfg := Config{TerminalCommand: `sh -c 'echo custom-launcher-failure >&2; exit 1'`}
	err := Launch(cfg, "/proj", "claude", "hi")
	if err == nil {
		t.Fatal("expected Launch to return an error when the launcher command fails")
	}
	if !strings.Contains(err.Error(), "custom-launcher-failure") {
		t.Errorf("expected the launcher's stderr to surface in the error, got: %v", err)
	}
}

// TestShellQuote_RoundTripsThroughRealShell feeds a set of adversarial
// strings (embedded quotes, spaces, shell metacharacters, unicode) through
// shellQuote and a real `sh -c 'printf %s ...'` to confirm the shell receives
// exactly the original string back, not something it reinterprets.
func TestShellQuote_RoundTripsThroughRealShell(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	cases := []string{
		"hello",
		"it's a test",
		`"double quoted"`,
		"a $VAR `backtick` \\ backslash",
		"line1\nline2",
		"日本語のプロンプト「テスト」",
		"",
		"'''",
	}
	for _, s := range cases {
		quoted := shellQuote(s)
		cmd := exec.Command("sh", "-c", "printf %s "+quoted)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("shell command failed for input %q: %v", s, err)
		}
		if string(out) != s {
			t.Errorf("round-trip mismatch: input %q, quoted %q, got back %q", s, quoted, string(out))
		}
	}
}

// TestWriteCommandScript_IsOwnerOnly is the regression test for security
// finding S-3 (art-991dfc4a): the .command script is deliberately never
// cleaned up (Terminal.app reads it asynchronously), and its body embeds the
// ticket prompt in plaintext, so what is left lying in the temp directory
// must not be readable or executable by anyone but the user who launched it.
func TestWriteCommandScript_IsOwnerOnly(t *testing.T) {
	path, err := writeCommandScript(t.TempDir(), "claude", "a prompt with ticket text in it")
	if err != nil {
		t.Fatalf("writeCommandScript: %v", err)
	}
	t.Cleanup(func() { os.Remove(path) })

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Errorf("expected mode 0700 (owner only), got %#o", got)
	}
}
