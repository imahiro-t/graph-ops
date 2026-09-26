package terminal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// DFLT-00154: the Terminal.app tab path. Every test fakes runCommand (and,
// for detection, getenv/getpid/psOutput), so no osascript, open or ps is
// ever started.

type commandCall struct {
	Name string
	Args []string
}

// fakeCommands replaces runCommand for the test: respond decides each
// command's output and error, and every call is recorded.
func fakeCommands(t *testing.T, respond func(ctx context.Context, name string, args []string) ([]byte, error)) *[]commandCall {
	t.Helper()
	var calls []commandCall
	old := runCommand
	runCommand = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, commandCall{Name: name, Args: append([]string(nil), args...)})
		if respond == nil {
			return nil, nil
		}
		return respond(ctx, name, args)
	}
	t.Cleanup(func() { runCommand = old })
	return &calls
}

// onDarwin fakes macOS outside tmux, with the generated scripts in a
// per-test temp dir.
func onDarwin(t *testing.T) {
	t.Helper()
	t.Setenv("TMUX", "")
	t.Setenv("TMPDIR", t.TempDir())
	old := goos
	goos = "darwin"
	t.Cleanup(func() { goos = old })
}

const testTTY = "/dev/ttys003"

func launchTab(t *testing.T, cfg Config, opts LaunchOptions) (LaunchOutcome, error) {
	t.Helper()
	return LaunchWithOptions(cfg, "/proj dir", "claude", []string{"--permission-mode", "acceptEdits"}, "hi", opts)
}

// openScriptOf returns the script path of an `open -a Terminal <script>`
// call.
func openScriptOf(t *testing.T, c commandCall) string {
	t.Helper()
	if c.Name != "open" || len(c.Args) != 3 || c.Args[0] != "-a" || c.Args[1] != "Terminal" {
		t.Fatalf("expected open -a Terminal <script>, got %s %q", c.Name, c.Args)
	}
	return c.Args[2]
}

func TestLaunchWithOptions_TabOpensWithoutOpen(t *testing.T) {
	onDarwin(t)
	calls := fakeCommands(t, nil)
	out, err := launchTab(t, Config{}, LaunchOptions{AppleTerminalTTY: testTTY})
	if err != nil {
		t.Fatal(err)
	}
	if out != (LaunchOutcome{UsedTab: true}) {
		t.Fatalf("outcome = %+v", out)
	}
	if len(*calls) != 1 {
		t.Fatalf("calls = %+v, want only osascript", *calls)
	}
	c := (*calls)[0]
	if c.Name != "osascript" || len(c.Args) != 4 || c.Args[0] != "-e" || c.Args[1] != appleTerminalTabScript || c.Args[2] != testTTY {
		t.Fatalf("osascript call = %q", c.Args)
	}
	scriptPath := strings.Trim(c.Args[3], "'")
	if c.Args[3] != shellQuote(scriptPath) {
		t.Fatalf("command argument %q is not the shell-quoted script path", c.Args[3])
	}
	body, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if want := "#!/bin/bash\ncd '/proj dir'\nclaude '--permission-mode' 'acceptEdits' 'hi'\n"; string(body) != want {
		t.Fatalf("script = %q, want %q", body, want)
	}
	// Nothing variable is spliced into the AppleScript source.
	if strings.Contains(appleTerminalTabScript, testTTY) || strings.Contains(appleTerminalTabScript, scriptPath) {
		t.Fatal("the tty or the script path was embedded in the AppleScript source")
	}
}

func TestLaunchWithOptions_TabRouteSelection(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		tmux string
		goos string
		opts LaunchOptions
		// wantName is the one command that must run ("" for none).
		wantName string
		wantErr  bool
	}{
		{name: "terminalCommand wins", cfg: Config{TerminalCommand: "true {cwd} {command}"}, goos: "darwin", opts: LaunchOptions{AppleTerminalTTY: testTTY}, wantName: "sh"},
		{name: "tmux wins", tmux: "/tmp/tmux-1/default,1,0", goos: "darwin", opts: LaunchOptions{AppleTerminalTTY: testTTY}, wantName: "tmux"},
		{name: "windows unchanged", goos: "windows", opts: LaunchOptions{AppleTerminalTTY: testTTY}, wantName: "cmd.exe"},
		{name: "linux still an error", goos: "linux", opts: LaunchOptions{AppleTerminalTTY: testTTY}, wantErr: true},
		{name: "no tty", goos: "darwin", opts: LaunchOptions{}, wantName: "open"},
		{name: "tab disabled for the run", goos: "darwin", opts: LaunchOptions{AppleTerminalTTY: testTTY, SkipAppleTerminalTab: true}, wantName: "open"},
		{name: "tty with shell syntax", goos: "darwin", opts: LaunchOptions{AppleTerminalTTY: "/dev/ttys1; rm -rf ~"}, wantName: "open"},
		{name: "tty without /dev/", goos: "darwin", opts: LaunchOptions{AppleTerminalTTY: "ttys003"}, wantName: "open"},
		{name: "linux pts", goos: "darwin", opts: LaunchOptions{AppleTerminalTTY: "/dev/pts/1"}, wantName: "open"},
		{name: "tty with a newline", goos: "darwin", opts: LaunchOptions{AppleTerminalTTY: "/dev/ttys003\n"}, wantName: "open"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TMUX", tc.tmux)
			t.Setenv("TMPDIR", t.TempDir())
			oldGoos, oldLook := goos, lookPath
			goos = tc.goos
			lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
			defer func() { goos, lookPath = oldGoos, oldLook }()
			calls := fakeCommands(t, nil)

			out, err := launchTab(t, tc.cfg, tc.opts)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if out != (LaunchOutcome{}) {
				t.Fatalf("outcome = %+v, want the zero value", out)
			}
			var names []string
			for _, c := range *calls {
				names = append(names, c.Name)
			}
			want := []string{}
			if tc.wantName != "" {
				want = []string{tc.wantName}
			}
			if strings.Join(names, ",") != strings.Join(want, ",") {
				t.Fatalf("commands = %v, want %v", names, want)
			}
		})
	}
}

// exitFailure is what a non-zero osascript looks like to runCommand.
func exitFailure(stderr string) func(ctx context.Context, name string, args []string) ([]byte, error) {
	return func(ctx context.Context, name string, args []string) ([]byte, error) {
		if name == "osascript" {
			return []byte(stderr), errors.New("exit status 1")
		}
		return nil, nil
	}
}

func TestLaunchWithOptions_TabFailureFallsBackToTheSameScript(t *testing.T) {
	cases := []struct {
		name        string
		respond     func(ctx context.Context, name string, args []string) ([]byte, error)
		wantDisable bool
		wantInError string
	}{
		{name: "window not found", respond: exitFailure("0:1: execution error: graph-ops: no Terminal window has a tab on /dev/ttys003 (9101)"), wantDisable: false, wantInError: "9101"},
		{name: "tab did not appear", respond: exitFailure("0:1: execution error: graph-ops: no new tab appeared in the Terminal window of /dev/ttys003 (9102)"), wantDisable: false, wantInError: "9102"},
		{name: "no automation permission", respond: exitFailure("execution error: Not authorized to send Apple events to Terminal. (-1743)"), wantDisable: true, wantInError: "-1743"},
		{name: "no accessibility permission", respond: exitFailure("execution error: System Events got an error: osascript is not allowed to send keystrokes. (-25211)"), wantDisable: true, wantInError: "-25211"},
		{name: "assistive access", respond: exitFailure("execution error: System Events got an error: osascript is not allowed assistive access. (-1719)"), wantDisable: true, wantInError: "-1719"},
		{name: "osascript missing", respond: func(ctx context.Context, name string, args []string) ([]byte, error) {
			if name == "osascript" {
				return nil, &exec.Error{Name: "osascript", Err: exec.ErrNotFound}
			}
			return nil, nil
		}, wantDisable: true, wantInError: "executable file not found"},
		{name: "unexpected failure", respond: exitFailure("something else went wrong"), wantDisable: true, wantInError: "something else"},
		{name: "a number that merely contains 9101", respond: exitFailure("execution error: (-91010)"), wantDisable: true, wantInError: "-91010"},
		{name: "timeout", respond: func(ctx context.Context, name string, args []string) ([]byte, error) {
			if name == "osascript" {
				<-ctx.Done()
				return nil, ctx.Err()
			}
			return nil, nil
		}, wantDisable: true, wantInError: "did not finish"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			onDarwin(t)
			oldTimeout := tabScriptTimeout
			tabScriptTimeout = 20 * time.Millisecond
			defer func() { tabScriptTimeout = oldTimeout }()
			calls := fakeCommands(t, tc.respond)

			out, err := launchTab(t, Config{}, LaunchOptions{AppleTerminalTTY: testTTY})
			if err != nil {
				t.Fatalf("the fallback succeeded, so no error was expected: %v", err)
			}
			if out.UsedTab || out.DisableTab != tc.wantDisable || !strings.Contains(out.TabError, tc.wantInError) {
				t.Fatalf("outcome = %+v, want DisableTab=%v and %q in TabError", out, tc.wantDisable, tc.wantInError)
			}
			if len(*calls) != 2 || (*calls)[0].Name != "osascript" {
				t.Fatalf("calls = %+v, want osascript then open", *calls)
			}
			if got, want := openScriptOf(t, (*calls)[1]), strings.Trim((*calls)[0].Args[3], "'"); got != want {
				t.Fatalf("open ran %q, the tab would have run %q", got, want)
			}
		})
	}
}

func TestLaunchWithOptions_ErrorOnlyWhenTheFallbackFailsToo(t *testing.T) {
	onDarwin(t)
	fakeCommands(t, func(ctx context.Context, name string, args []string) ([]byte, error) {
		if name == "osascript" {
			return []byte("Not authorized to send Apple events to Terminal. (-1743)"), errors.New("exit status 1")
		}
		return []byte("LSOpenURLsWithRole() failed"), errors.New("exit status 1")
	})
	out, err := launchTab(t, Config{}, LaunchOptions{AppleTerminalTTY: testTTY})
	if err == nil {
		t.Fatal("expected an error when neither a tab nor a window opened")
	}
	if !strings.Contains(err.Error(), "LSOpenURLsWithRole") || !strings.Contains(err.Error(), "-1743") {
		t.Fatalf("error %q should carry both the open failure and the tab failure", err)
	}
	if !out.DisableTab || out.TabError == "" {
		t.Fatalf("outcome = %+v", out)
	}
}

func TestLaunchWithOptions_TabErrorIsCapped(t *testing.T) {
	onDarwin(t)
	fakeCommands(t, exitFailure(strings.Repeat("x", 5000)))
	out, err := launchTab(t, Config{}, LaunchOptions{AppleTerminalTTY: testTTY})
	if err != nil {
		t.Fatal(err)
	}
	if n := len([]rune(out.TabError)); n > maxTabErrorLen+3 {
		t.Fatalf("TabError is %d runes long", n)
	}
}

// The command osascript is given is exactly shellQuote of the script path,
// and the script itself still quotes awkward working directories and
// prompts as the new-window path does.
func TestLaunchWithOptions_TabQuotesAwkwardInput(t *testing.T) {
	onDarwin(t)
	calls := fakeCommands(t, nil)
	workDir := "/tmp/it's a \"dir\" with \\ back"
	prompt := "line one\nit's \"two\" \\ $HOME `x`"
	if _, err := LaunchWithOptions(Config{}, workDir, "claude", nil, prompt, LaunchOptions{AppleTerminalTTY: testTTY}); err != nil {
		t.Fatal(err)
	}
	c := (*calls)[0]
	// Recover the path the way the shell would read the quoted argument.
	path := strings.ReplaceAll(strings.TrimSuffix(strings.TrimPrefix(c.Args[3], "'"), "'"), `'\''`, "'")
	if c.Args[3] != shellQuote(path) {
		t.Fatalf("command %q != shellQuote(%q)", c.Args[3], path)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("#!/bin/bash\ncd %s\nclaude %s\n", shellQuote(workDir), shellQuote(prompt)); string(body) != want {
		t.Fatalf("script = %q, want %q", body, want)
	}
}

// TestAppleTerminalTabScript_Structure pins the script's shape: arguments
// through `on run argv`, the window by id, a new tab by Cmd+T, the two
// retryable errors, and `do script` as the very last statement (so a failure
// never falls back after the session already started).
//
// For a manual check on a Mac, set GRAPH_OPS_TAB_SCRIPT_OUT to a file path:
// the test writes the script there, to be run from a Terminal.app tab as
// `osascript <file> "$(tty)" "echo hello"`.
func TestAppleTerminalTabScript_Structure(t *testing.T) {
	for _, want := range []string{
		"on run argv",
		"item 1 of argv",
		"item 2 of argv",
		"window id targetID",
		`keystroke "t" using command down`,
		"number 9101",
		"number 9102",
		"do script shellCommand in (selected tab of window id targetID)",
	} {
		if !strings.Contains(appleTerminalTabScript, want) {
			t.Errorf("the script lacks %q", want)
		}
	}
	lines := strings.Split(strings.TrimSpace(appleTerminalTabScript), "\n")
	if len(lines) < 2 || strings.TrimSpace(lines[len(lines)-1]) != "end run" ||
		!strings.Contains(lines[len(lines)-2], "do script") {
		t.Errorf("do script must be the last statement; the script ends with %q", lines[len(lines)-2:])
	}
	if strings.Count(appleTerminalTabScript, "do script") != 1 {
		t.Error("do script must appear exactly once")
	}
	if tabErrWindowNotFound != 9101 || tabErrTabNotOpened != 9102 {
		t.Error("the Go constants must match the script's error numbers")
	}
	if out := os.Getenv("GRAPH_OPS_TAB_SCRIPT_OUT"); out != "" {
		if err := os.WriteFile(out, []byte(appleTerminalTabScript+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// fakeProcessTree fakes the environment and ps for DetectAppleTerminalTTY:
// tree maps a pid to its ps line ("<tty> <ppid>"); a pid not in it makes ps
// fail.
func fakeProcessTree(t *testing.T, env map[string]string, self int, tree map[int]string) *[]int {
	t.Helper()
	var asked []int
	oldGoos, oldGetenv, oldGetpid, oldPS := goos, getenv, getpid, psOutput
	goos = "darwin"
	getenv = func(k string) string { return env[k] }
	getpid = func() int { return self }
	psOutput = func(pid int) (string, error) {
		asked = append(asked, pid)
		line, ok := tree[pid]
		if !ok {
			return "", errors.New("ps: operation not permitted")
		}
		return line, nil
	}
	t.Cleanup(func() { goos, getenv, getpid, psOutput = oldGoos, oldGetenv, oldGetpid, oldPS })
	return &asked
}

var appleTerminalEnv = map[string]string{"TERM_PROGRAM": "Apple_Terminal"}

func TestDetectAppleTerminalTTY_WalksUpToTheFirstTTY(t *testing.T) {
	asked := fakeProcessTree(t, appleTerminalEnv, 400, map[int]string{
		400: "?? 300\n",
		300: "   ??   200",
		200: "ttys003 100\n",
		100: "ttys003 1",
	})
	if got := DetectAppleTerminalTTY(); got != "/dev/ttys003" {
		t.Fatalf("got %q", got)
	}
	if fmt.Sprint(*asked) != "[400 300 200]" {
		t.Fatalf("ps asked for %v", *asked)
	}
}

func TestDetectAppleTerminalTTY_EmptyCases(t *testing.T) {
	deep := map[int]string{}
	for pid := 1000; pid > 900; pid-- {
		deep[pid] = fmt.Sprintf("?? %d", pid-1)
	}
	cases := []struct {
		name string
		env  map[string]string
		goos string
		tree map[int]string
		// wantAsks is how many times ps may run (-1: do not check).
		wantAsks int
	}{
		{name: "iTerm", env: map[string]string{"TERM_PROGRAM": "iTerm.app"}, tree: map[int]string{1000: "ttys001 1"}, wantAsks: 0},
		{name: "no TERM_PROGRAM", env: map[string]string{}, tree: map[int]string{1000: "ttys001 1"}, wantAsks: 0},
		{name: "tmux", env: map[string]string{"TERM_PROGRAM": "Apple_Terminal", "TMUX": "/tmp/tmux,1,0"}, tree: map[int]string{1000: "ttys001 1"}, wantAsks: 0},
		{name: "not macOS", env: appleTerminalEnv, goos: "linux", tree: map[int]string{1000: "ttys001 1"}, wantAsks: 0},
		{name: "ps refused (sandbox)", env: appleTerminalEnv, tree: map[int]string{}, wantAsks: 1},
		{name: "no tty up to pid 1", env: appleTerminalEnv, tree: map[int]string{1000: "?? 999", 999: "?? 1"}, wantAsks: 2},
		{name: "hop limit", env: appleTerminalEnv, tree: deep, wantAsks: maxTTYParentHops},
		{name: "unexpected tty form", env: appleTerminalEnv, tree: map[int]string{1000: "console 1"}, wantAsks: 1},
		{name: "garbled ps output", env: appleTerminalEnv, tree: map[int]string{1000: "ttys001"}, wantAsks: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asked := fakeProcessTree(t, tc.env, 1000, tc.tree)
			if tc.goos != "" {
				goos = tc.goos
			}
			if got := DetectAppleTerminalTTY(); got != "" {
				t.Fatalf("got %q, want \"\"", got)
			}
			if tc.wantAsks >= 0 && len(*asked) != tc.wantAsks {
				t.Fatalf("ps ran %d times, want %d", len(*asked), tc.wantAsks)
			}
		})
	}
}
