package terminal

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// DFLT-00142: LaunchWithArgs passes extra claude arguments (the autopilot's
// --permission-mode) on all four launch paths, each quoted like the prompt.

// hostileArgs are extra arguments that would break out of naive quoting: an
// ASCII single quote, spaces, a shell metacharacter and a PowerShell smart
// quote.
var hostileArgs = []string{"--permission-mode", "it's a; `mode` $x", "\u2019; calc; \u2019"}

const hostilePrompt = "/graph-ops:autopilot-worker run-1 T-1 --role work"

// argvEcho is a stand-in for claude: a shell script that prints each argument
// it receives on its own NUL-terminated record, so a test can tell exactly how
// many arguments arrived and what each one was.
func argvEcho(t *testing.T) (bin, outFile string) {
	t.Helper()
	dir := t.TempDir()
	outFile = filepath.Join(dir, "argv.out")
	bin = filepath.Join(dir, "fake-claude")
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\0' \"$a\"; done > " + shellQuote(outFile) + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, outFile
}

func readArgv(t *testing.T, outFile string) []string {
	t.Helper()
	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("reading argv: %v", err)
	}
	parts := strings.Split(string(data), "\x00")
	return parts[:len(parts)-1]
}

func wantArgv() []string {
	return append(append([]string{}, hostileArgs...), hostilePrompt)
}

func assertArgv(t *testing.T, got []string) {
	t.Helper()
	want := wantArgv()
	if len(got) != len(want) {
		t.Fatalf("argv = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("argv[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestLaunchWithArgs_TerminalCommandQuotesEachArgThroughRealShell(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	bin, out := argvEcho(t)
	// The template runs {command} directly, so the real shell parses exactly
	// what buildLaunchArgvWithArgs produced.
	cfg := Config{TerminalCommand: "cd {cwd} && {command}"}
	if err := LaunchWithArgs(cfg, t.TempDir(), shellQuote(bin), hostileArgs, hostilePrompt); err != nil {
		t.Fatalf("LaunchWithArgs: %v", err)
	}
	assertArgv(t, readArgv(t, out))
}

func TestLaunchWithArgs_TmuxPassesEachArgAsArgvElement(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-1/default,123,0")
	name, args, err := buildLaunchArgvWithArgs(Config{}, "/proj", "claude", hostileArgs, hostilePrompt)
	if err != nil {
		t.Fatal(err)
	}
	if name != "tmux" {
		t.Fatalf("name = %s", name)
	}
	want := append([]string{"new-window", "-c", "/proj", "claude"}, wantArgv()...)
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("args = %q, want %q", args, want)
	}
}

func TestLaunchWithArgs_DarwinScriptQuotesEachArgThroughRealShell(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	t.Setenv("TMUX", "")
	old := goos
	goos = "darwin"
	defer func() { goos = old }()

	bin, out := argvEcho(t)
	name, args, err := buildLaunchArgvWithArgs(Config{}, t.TempDir(), shellQuote(bin), hostileArgs, hostilePrompt)
	if err != nil {
		t.Fatal(err)
	}
	if name != "open" || len(args) != 3 {
		t.Fatalf("argv = %s %q", name, args)
	}
	script := args[2]
	t.Cleanup(func() { os.Remove(script) })
	// Run the generated .command script the way Terminal.app would.
	if output, err := exec.Command("bash", script).CombinedOutput(); err != nil {
		t.Fatalf("running script: %v\n%s", err, output)
	}
	assertArgv(t, readArgv(t, out))
}

func TestLaunchWithArgs_WindowsScriptPowershellQuotesEachArg(t *testing.T) {
	t.Setenv("TMUX", "")
	old := goos
	goos = "windows"
	defer func() { goos = old }()
	oldLook := lookPath
	lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	defer func() { lookPath = oldLook }()

	name, args, err := buildLaunchArgvWithArgs(Config{}, `C:\proj`, "claude", hostileArgs, hostilePrompt)
	if err != nil {
		t.Fatal(err)
	}
	if name != "cmd.exe" {
		t.Fatalf("name = %s", name)
	}
	script := args[len(args)-1]
	t.Cleanup(func() { os.Remove(script) })
	content, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	assertUTF8BOM(t, content)
	body := strings.TrimPrefix(string(content), utf8BOM)
	var quoted []string
	for _, a := range wantArgv() {
		quoted = append(quoted, powershellQuote(a))
	}
	want := "Set-Location -LiteralPath 'C:\\proj'\r\nclaude " + strings.Join(quoted, " ") + "\r\n"
	if body != want {
		t.Fatalf("script = %q\nwant     %q", body, want)
	}
	// The smart quote in the argument is doubled, so it cannot close the
	// literal the quoting opened.
	if !strings.Contains(body, "'\u2019\u2019; calc; \u2019\u2019'") {
		t.Errorf("smart quote not doubled: %q", body)
	}
}

// TestLaunch_UnchangedWithoutExtraArgs pins that Launch (no extra arguments)
// produces exactly the command/script it produced before LaunchWithArgs.
func TestLaunch_UnchangedWithoutExtraArgs(t *testing.T) {
	t.Run("terminalCommand", func(t *testing.T) {
		_, args, err := buildLaunchArgv(Config{TerminalCommand: "x {cwd} {command}"}, "/p", "claude", "hi")
		if err != nil || args[1] != "x '/p' claude 'hi'" {
			t.Fatalf("args = %q, err = %v", args, err)
		}
	})
	t.Run("tmux", func(t *testing.T) {
		t.Setenv("TMUX", "x")
		_, args, err := buildLaunchArgv(Config{}, "/p", "claude", "hi")
		if err != nil || strings.Join(args, " ") != "new-window -c /p claude hi" {
			t.Fatalf("args = %q, err = %v", args, err)
		}
	})
	t.Run("darwin", func(t *testing.T) {
		path, err := writeCommandScript("/p", "claude", "hi")
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(path)
		b, _ := os.ReadFile(path)
		if string(b) != "#!/bin/bash\ncd '/p'\nclaude 'hi'\n" {
			t.Fatalf("script = %q", b)
		}
	})
	t.Run("windows", func(t *testing.T) {
		path, err := writeWindowsCommandScript(`C:\p`, "claude", "hi")
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(path)
		b, _ := os.ReadFile(path)
		if string(b) != utf8BOM+"Set-Location -LiteralPath 'C:\\p'\r\nclaude 'hi'\r\n" {
			t.Fatalf("script = %q", b)
		}
	})
}
