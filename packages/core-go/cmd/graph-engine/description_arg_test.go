package main

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/engine"
)

// DFLT-00093: readDescriptionArg is the one place create-ticket and
// refine-ticket resolve [description|-]. These tests swap the global
// os.Stdin, so none of them may use t.Parallel.

func TestReadDescriptionArg(t *testing.T) {
	const usage = "usage: X"
	const unreadStdin = "stdin の内容"
	type want struct {
		value       string
		errContains []string // nil -> no error expected
		errLacks    []string
		errPrefix   string
		errIs       error
	}
	cases := []struct {
		name  string
		arg   string
		stdin *string // nil -> closed stdin (read fails)
		want  want
	}{
		{"empty string is left as-is", "", strp(unreadStdin), want{value: ""}},
		{"dash with empty stdin", "-", strp(""), want{errContains: []string{usage, "stdin", "empty"}}},
		{"dash with whitespace stdin", "-", strp("  \n\t\n"), want{errContains: []string{usage, "stdin", "empty"}}},
		{"dash with content", "-", strp("# 見出し\n本文\n"), want{value: "# 見出し\n本文\n"}},
		{"dash with failed read", "-", nil, want{errPrefix: "reading description from stdin:", errIs: os.ErrClosed}},
		{"spaces", "   ", strp(unreadStdin), want{errContains: []string{usage, "empty"}, errLacks: []string{"stdin"}}},
		{"newline", "\n", strp(unreadStdin), want{errContains: []string{usage, "empty"}, errLacks: []string{"stdin"}}},
		{"plain text", "新しい説明", strp(unreadStdin), want{value: "新しい説明"}},
		{"leading dash is literal", "- item", strp(unreadStdin), want{value: "- item"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.stdin == nil {
				withClosedStdin(t)
			} else {
				withStdin(t, *c.stdin)
			}
			got, err := readDescriptionArg(c.arg, usage)
			wantErr := c.want.errContains != nil || c.want.errPrefix != ""
			if !wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got != c.want.value {
					t.Errorf("got %q, want %q", got, c.want.value)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected an error, got value %q", got)
			}
			if got != "" {
				t.Errorf("an error must come with an empty value, got %q", got)
			}
			for _, s := range c.want.errContains {
				if !strings.Contains(err.Error(), s) {
					t.Errorf("error %q should contain %q", err, s)
				}
			}
			for _, s := range c.want.errLacks {
				if strings.Contains(err.Error(), s) {
					t.Errorf("error %q must not contain %q", err, s)
				}
			}
			if c.want.errPrefix != "" && !strings.HasPrefix(err.Error(), c.want.errPrefix) {
				t.Errorf("error %q should start with %q", err, c.want.errPrefix)
			}
			if c.want.errIs != nil && !errors.Is(err, c.want.errIs) {
				t.Errorf("error %v should wrap %v", err, c.want.errIs)
			}
		})
	}
}

func strp(s string) *string { return &s }

// Both commands report the same description errors; only the usage prefix
// differs. The read-failure error carries no usage and is covered by
// TestReadDescriptionArg instead.
func TestDescriptionErrors_SameWordingForCreateAndRefine(t *testing.T) {
	cases := []struct {
		name  string
		arg   string
		stdin string
	}{
		{"empty stdin", "-", ""},
		{"whitespace-only stdin", "-", "  \n\t\n"},
		{"whitespace-only argument", "   ", "stdin の内容"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo, projectID := cliLabelSetup(t)
			eng := engine.New(repo)
			tk, err := eng.CreateTicket(projectID, "A-1", "元の説明")
			if err != nil {
				t.Fatalf("create: %v", err)
			}

			var createErr, refineErr error
			withStdin(t, c.stdin)
			captureStdout(t, func() {
				createErr = cmdCreateTicket(eng, repo, sandboxRC(t), []string{"T", c.arg, "--project", projectID})
			})
			withStdin(t, c.stdin)
			captureStdout(t, func() {
				refineErr = cmdRefineTicket(eng, []string{tk.ID, c.arg})
			})
			if createErr == nil || refineErr == nil {
				t.Fatalf("both commands must fail: create=%v refine=%v", createErr, refineErr)
			}
			if !strings.Contains(createErr.Error(), createTicketUsageLine) || !strings.Contains(refineErr.Error(), refineTicketUsageLine) {
				t.Fatalf("each error should carry its command's usage:\n create: %v\n refine: %v", createErr, refineErr)
			}
			e1 := strings.Replace(createErr.Error(), createTicketUsageLine, "", 1)
			e2 := strings.Replace(refineErr.Error(), refineTicketUsageLine, "", 1)
			if e1 != e2 {
				t.Errorf("wording differs beyond the usage:\n create: %q\n refine: %q", e1, e2)
			}
		})
	}
}

// Structural guard for the de-duplication: cmdCreateTicket and
// cmdRefineTicket both call readDescriptionArg and neither reads stdin
// itself; readDescriptionArg holds the only description io.ReadAll(os.Stdin).
func TestDescriptionStdinReadLivesOnlyInReadDescriptionArg(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}
	funcs := map[string]*ast.FuncDecl{}
	for _, d := range file.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil {
			funcs[fd.Name.Name] = fd
		}
	}
	calls := func(fd *ast.FuncDecl) (readsStdin, callsHelper bool) {
		ast.Inspect(fd, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fn := call.Fun.(type) {
			case *ast.Ident:
				if fn.Name == "readDescriptionArg" {
					callsHelper = true
				}
			case *ast.SelectorExpr:
				if pkg, ok := fn.X.(*ast.Ident); ok && pkg.Name == "io" && fn.Sel.Name == "ReadAll" && len(call.Args) == 1 {
					if arg, ok := call.Args[0].(*ast.SelectorExpr); ok {
						if x, ok := arg.X.(*ast.Ident); ok && x.Name == "os" && arg.Sel.Name == "Stdin" {
							readsStdin = true
						}
					}
				}
			}
			return true
		})
		return
	}
	for _, name := range []string{"cmdCreateTicket", "cmdRefineTicket"} {
		fd := funcs[name]
		if fd == nil {
			t.Fatalf("%s not found in main.go", name)
		}
		readsStdin, callsHelper := calls(fd)
		if readsStdin {
			t.Errorf("%s must not read stdin itself; use readDescriptionArg", name)
		}
		if !callsHelper {
			t.Errorf("%s must resolve its description with readDescriptionArg", name)
		}
	}
	helper := funcs["readDescriptionArg"]
	if helper == nil {
		t.Fatal("readDescriptionArg not found in main.go")
	}
	if readsStdin, _ := calls(helper); !readsStdin {
		t.Error("readDescriptionArg should be where stdin is read")
	}
}

func TestPrintUsage_DocumentsDescriptionErrors(t *testing.T) {
	out := captureStdout(t, printUsage)
	// Compared with whitespace collapsed so re-wrapping the help does not
	// break the test.
	flat := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	create := flat(usageSection(t, out, "create-ticket <title>"))
	refine := flat(usageSection(t, out, "refine-ticket <ticketId>"))
	for _, want := range []string{
		"empty or whitespace-only stdin, or a failed read, is an error and no ticket is created",
		"description argument that is only whitespace",
	} {
		if !strings.Contains(create, want) {
			t.Errorf("create-ticket help should mention %q:\n%s", want, create)
		}
	}
	for _, want := range []string{
		`Description "-" -> read from stdin and saved byte for byte`,
		"Empty or whitespace-only stdin, a failed read, or a whitespace-only description argument",
		"changes nothing",
		"does not become REFINED",
	} {
		if !strings.Contains(refine, want) {
			t.Errorf("refine-ticket help should mention %q:\n%s", want, refine)
		}
	}
}

// usageSection returns the help text from the line starting with head up to
// the next command line (a line indented by exactly two spaces).
func usageSection(t *testing.T, out, head string) string {
	t.Helper()
	start := strings.Index(out, "  "+head)
	if start < 0 {
		t.Fatalf("help has no %q entry", head)
	}
	lines := strings.Split(out[start:], "\n")
	var b strings.Builder
	for i, l := range lines {
		if i > 0 && strings.HasPrefix(l, "  ") && !strings.HasPrefix(l, "   ") {
			break
		}
		b.WriteString(l)
		b.WriteString("\n")
	}
	return b.String()
}
