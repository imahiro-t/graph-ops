package terminal

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
	"unicode/utf8"
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

func TestBuildLaunchArgv_WindowsUsesWtExeWhenAvailable(t *testing.T) {
	t.Setenv("TMUX", "")
	oldGoos, oldLookPath := goos, lookPath
	goos = "windows"
	lookPath = func(file string) (string, error) {
		if file == "wt.exe" {
			return `C:\Program Files\WindowsApps\wt.exe`, nil
		}
		return "", exec.ErrNotFound
	}
	defer func() { goos, lookPath = oldGoos, oldLookPath }()

	name, args, err := buildLaunchArgv(Config{}, `C:\proj`, "claude", `say "hi" for me`)
	if err != nil {
		t.Fatalf("buildLaunchArgv: %v", err)
	}
	wantPrefix := []string{"powershell", "-NoExit", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File"}
	if name != "wt.exe" || len(args) != len(wantPrefix)+1 {
		t.Fatalf("expected wt.exe powershell ... -File <script>, got %s %v", name, args)
	}
	for i := range wantPrefix {
		if args[i] != wantPrefix[i] {
			t.Errorf("args[%d] = %q, want %q", i, args[i], wantPrefix[i])
		}
	}
	scriptPath := args[len(args)-1]
	defer os.Remove(scriptPath)

	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("reading generated script: %v", err)
	}
	assertUTF8BOM(t, content)
	got := string(content)
	if !strings.Contains(got, `Set-Location -LiteralPath 'C:\proj'`) {
		t.Errorf("script missing quoted cwd: %q", got)
	}
	if !strings.Contains(got, `claude 'say "hi" for me'`) {
		t.Errorf("script missing quoted claude invocation: %q", got)
	}
}

func TestBuildLaunchArgv_WindowsFallsBackToCmdExeWithoutWtExe(t *testing.T) {
	t.Setenv("TMUX", "")
	oldGoos, oldLookPath := goos, lookPath
	goos = "windows"
	lookPath = func(file string) (string, error) { return "", exec.ErrNotFound }
	defer func() { goos, lookPath = oldGoos, oldLookPath }()

	name, args, err := buildLaunchArgv(Config{}, `C:\proj`, "claude", "do the thing")
	if err != nil {
		t.Fatalf("buildLaunchArgv: %v", err)
	}
	if name != "cmd.exe" {
		t.Fatalf("expected cmd.exe, got %s", name)
	}
	want := []string{"/c", "start", "Claude Code", "powershell", "-NoExit", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File"}
	if len(args) != len(want)+1 {
		t.Fatalf("args = %v, want %d elements", args, len(want)+1)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, args[i], want[i])
		}
	}
	scriptPath := args[len(args)-1]
	defer os.Remove(scriptPath)
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("expected generated script to exist: %v", err)
	}
	assertUTF8BOM(t, content)
}

func TestBuildLaunchArgv_WindowsDoesNotOverrideTerminalCommand(t *testing.T) {
	oldGoos := goos
	goos = "windows"
	defer func() { goos = oldGoos }()
	cfg := Config{TerminalCommand: "wt.exe -d {cwd} -- cmd /k {command}"}

	name, args, err := buildLaunchArgv(cfg, `C:\proj`, "claude", "hello")
	if err != nil {
		t.Fatalf("buildLaunchArgv: %v", err)
	}
	if name != "sh" || len(args) != 2 || args[0] != "-c" {
		t.Fatalf("expected terminalCommand to take precedence via sh -c wrapper, got %s %v", name, args)
	}
}

func TestBuildLaunchArgv_WindowsDoesNotOverrideTmux(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-1/default,123,0")
	oldGoos := goos
	goos = "windows"
	defer func() { goos = oldGoos }()

	name, args, err := buildLaunchArgv(Config{}, "/proj", "claude", "do the thing")
	if err != nil {
		t.Fatalf("buildLaunchArgv: %v", err)
	}
	if name != "tmux" {
		t.Fatalf("expected tmux to take precedence over the windows branch, got %s %v", name, args)
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

// TestWriteCommandScript_HasNoBOMBeforeShebang guards the darwin path against
// the DFLT-00089 Windows fix leaking into it: the .command script must start
// with the "#!/bin/bash" shebang itself. A UTF-8 BOM (or anything else) in
// front of "#!" stops the kernel from recognising the shebang, so the script
// would run under whatever shell opened it and print a line-1 error.
func TestWriteCommandScript_HasNoBOMBeforeShebang(t *testing.T) {
	path, err := writeCommandScript(t.TempDir(), "claude", "チケットの指示です。")
	if err != nil {
		t.Fatalf("writeCommandScript: %v", err)
	}
	t.Cleanup(func() { os.Remove(path) })

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading generated script: %v", err)
	}
	if bytes.HasPrefix(content, []byte(utf8BOM)) {
		t.Fatalf("darwin .command script must not start with a UTF-8 BOM: % x", content[:min(len(content), 8)])
	}
	if !bytes.HasPrefix(content, []byte("#!/bin/bash\n")) {
		t.Fatalf("darwin .command script must start with the #!/bin/bash shebang, got %q", content[:min(len(content), 16)])
	}
}

// TestPowershellQuote_EscapesSpecialCharacters checks powershellQuote's
// escaping rule in isolation (there is no PowerShell available in this
// dev/CI environment to round-trip through, unlike TestShellQuote's
// real-shell check): a PowerShell single-quoted string is fully literal, so
// the only characters that need escaping are the embedded single quotes
// (doubled); everything else -- including '%', '"', '&', '|', '^', '!', a
// backtick, and a literal embedded newline -- must survive unescaped.
//
// "Single quote" means all five of them -- the ASCII apostrophe and the four
// smart quotes PowerShell's tokenizer treats as the same thing (finding
// CHK-02; see powerShellSingleQuotes). The expectations below are written
// out as fixed strings rather than derived from a tokenizer reimplemented in
// the test, which would only restate the assumption instead of checking it.
func TestPowershellQuote_EscapesSpecialCharacters(t *testing.T) {
	cases := []struct{ in, want string }{
		{"hello", `'hello'`},
		{`say "hi" for me`, `'say "hi" for me'`},
		{"it's a test", `'it''s a test'`},
		{"100%", `'100%'`},
		{"a & b | c ^ d < e > f ! g $h `i", "'a & b | c ^ d < e > f ! g $h `i'"},
		{"line1\nline2", "'line1\nline2'"},
		{"", `''`},
		{"'''", `''''''''`},
		// The injection attempt from CHK-02, once per smart quote: each one
		// would have closed the literal had it not been doubled, leaving
		// "; calc; " to be parsed as PowerShell.
		{"'; calc; '", `'''; calc; '''`},
		{"\u2018; calc; \u2018", "'\u2018\u2018; calc; \u2018\u2018'"},
		{"\u2019; calc; \u2019", "'\u2019\u2019; calc; \u2019\u2019'"},
		{"\u201a; calc; \u201a", "'\u201a\u201a; calc; \u201a\u201a'"},
		{"\u201b; calc; \u201b", "'\u201b\u201b; calc; \u201b\u201b'"},
		// Mixed kinds: each is doubled as itself, never converted.
		{"a\u2019b'c\u201bd", "'a\u2019\u2019b''c\u201b\u201bd'"},
		// A double quote and the other quotation marks PowerShell does not
		// treat as single quotes stay exactly as they are.
		{"\u201c\u201d\u00ab\u00bb", "'\u201c\u201d\u00ab\u00bb'"},
	}
	for _, c := range cases {
		if got := powershellQuote(c.in); got != c.want {
			t.Errorf("powershellQuote(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestWriteWindowsCommandScript_SmartQuotePromptStaysLiteral is CHK-02 at
// the level the finding is actually about: the whole generated .ps1 for a
// prompt carrying the smart-quote spelling of "'; calc; '". The expectation
// is the complete file contents, so nothing about the quoting can drift
// without this failing.
func TestWriteWindowsCommandScript_SmartQuotePromptStaysLiteral(t *testing.T) {
	path, err := writeWindowsCommandScript(`C:\work`, "claude", "\u2019; calc; \u2019")
	if err != nil {
		t.Fatalf("writeWindowsCommandScript: %v", err)
	}
	t.Cleanup(func() { os.Remove(path) })

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading generated script: %v", err)
	}
	want := utf8BOM + "Set-Location -LiteralPath 'C:\\work'\r\nclaude '\u2019\u2019; calc; \u2019\u2019'\r\n"
	if got := string(content); got != want {
		t.Errorf("script =\n%q\nwant\n%q", got, want)
	}
}

// TestShellQuote_LeavesSmartQuotesAlone is the other side of CHK-02: `sh`
// gives U+2018..U+201B no syntactic meaning, so doubling them there would
// corrupt the prompt rather than protect it. Only the ASCII apostrophe is
// escaped, and by sh's own rule rather than PowerShell's.
func TestShellQuote_LeavesSmartQuotesAlone(t *testing.T) {
	if got, want := shellQuote("it's \u2019 ok"), `'it'\''s `+"\u2019"+` ok'`; got != want {
		t.Errorf("shellQuote = %q, want %q", got, want)
	}
}

// TestWriteWindowsCommandScript_ContentAndLineEndings verifies the generated
// PowerShell script cds into workDir and runs claudeBin with the
// (powershell-quoted) prompt.
func TestWriteWindowsCommandScript_ContentAndLineEndings(t *testing.T) {
	path, err := writeWindowsCommandScript(`C:\proj`, "claude", `say "hi" for me`)
	if err != nil {
		t.Fatalf("writeWindowsCommandScript: %v", err)
	}
	t.Cleanup(func() { os.Remove(path) })

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading generated script: %v", err)
	}
	got := string(content)
	if !strings.Contains(got, `Set-Location -LiteralPath 'C:\proj'`) {
		t.Errorf("script missing quoted cwd: %q", got)
	}
	if !strings.Contains(got, `claude 'say "hi" for me'`) {
		t.Errorf("script missing quoted claude invocation: %q", got)
	}
}

// TestWriteWindowsCommandScript_PreservesEmbeddedNewline is the regression
// test for the QA/security review finding on this ticket (art-2ec8a62a,
// art-33fee083): a prompt containing a newline -- the normal shape of a
// ticket-derived prompt via withTicketContext in claude_launch.go, not an
// edge case -- must survive as a single argument to claudeBin instead of
// being split across cmd.exe batch "lines" (which, unlike a PowerShell
// single-quoted string, has no way to keep a quoted token intact across a
// line break, letting the remainder run as unrelated, attacker-influenced
// commands -- OWASP A03 Injection).
func TestWriteWindowsCommandScript_PreservesEmbeddedNewline(t *testing.T) {
	prompt := "This instruction concerns ticket DFLT-00099.\n\nFix the bug."
	path, err := writeWindowsCommandScript(`C:\proj`, "claude", prompt)
	if err != nil {
		t.Fatalf("writeWindowsCommandScript: %v", err)
	}
	t.Cleanup(func() { os.Remove(path) })

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading generated script: %v", err)
	}
	got := string(content)

	// The whole multi-line prompt must appear intact, still wrapped in the
	// single quotes that make it one PowerShell token, immediately after the
	// claude invocation -- not truncated at the first embedded newline.
	wantInvocation := "claude '" + prompt + "'"
	if !strings.Contains(got, wantInvocation) {
		t.Errorf("script does not contain the embedded-newline prompt as one quoted token.\ngot:\n%s\nwant substring:\n%s", got, wantInvocation)
	}

	// A single quote is the only character powershellQuote escapes, so the
	// total count of "'" in the script must be even -- an odd count would
	// mean some quote in the prompt (there are none here) or in the
	// generated script broke out of the intended single-quoted token.
	if n := strings.Count(got, "'"); n%2 != 0 {
		t.Errorf("script has an unbalanced number of single quotes (%d), suggesting the prompt broke out of its quoted token:\n%s", n, got)
	}
}

// assertUTF8BOM fails the test unless content starts with exactly one UTF-8
// byte-order mark (EF BB BF). The generated .ps1 needs it so Windows
// PowerShell 5.1 reads the script as UTF-8 instead of the system ANSI code
// page (DFLT-00089).
func assertUTF8BOM(t *testing.T, content []byte) {
	t.Helper()
	bom := []byte{0xEF, 0xBB, 0xBF}
	if !bytes.HasPrefix(content, bom) {
		t.Fatalf("generated script does not start with a UTF-8 BOM: % x", content[:min(len(content), 8)])
	}
	if bytes.HasPrefix(content[len(bom):], bom) {
		t.Fatalf("generated script starts with more than one UTF-8 BOM")
	}
}

// TestWriteWindowsCommandScript_WritesUTF8WithBOM is the regression test for
// DFLT-00089: Windows PowerShell 5.1 reads a BOM-less .ps1 in the system
// ANSI code page (CP932 on a Japanese-locale system), which garbled Japanese
// prompts and working-directory paths. The script must be UTF-8 with a single
// leading BOM, followed by the unchanged script body with the Japanese text
// stored as UTF-8.
func TestWriteWindowsCommandScript_WritesUTF8WithBOM(t *testing.T) {
	workDir := `C:\作業\プロジェクト`
	prompt := "チケット DFLT-00089 の指示です。\n\n文字化けを直してください。"
	path, err := writeWindowsCommandScript(workDir, "claude", prompt)
	if err != nil {
		t.Fatalf("writeWindowsCommandScript: %v", err)
	}
	t.Cleanup(func() { os.Remove(path) })

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading generated script: %v", err)
	}
	assertUTF8BOM(t, content)

	body := bytes.TrimPrefix(content, []byte(utf8BOM))
	if !utf8.Valid(body) {
		t.Fatalf("script body after the BOM is not valid UTF-8: % x", body)
	}
	want := "Set-Location -LiteralPath '" + workDir + "'\r\nclaude '" + prompt + "'\r\n"
	if string(body) != want {
		t.Errorf("script body = %q, want %q", body, want)
	}
}
