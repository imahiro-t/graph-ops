package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
)

// DFLT-00092: create-ticket "<title>" - reads the description from stdin.
// These tests swap the global os.Stdin, so none of them may use t.Parallel.

// withStdinFile points os.Stdin at f for the rest of the test.
func withStdinFile(t *testing.T, f *os.File) {
	t.Helper()
	orig := os.Stdin
	os.Stdin = f
	t.Cleanup(func() { os.Stdin = orig })
}

// withStdin makes os.Stdin yield content (via a temp file) for the rest of
// the test.
func withStdin(t *testing.T, content string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stdin")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	withStdinFile(t, f)
}

// withClosedStdin makes every read from os.Stdin fail.
func withClosedStdin(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stdin")
	if err := os.WriteFile(path, []byte("unreachable"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	f.Close()
	withStdinFile(t, f)
}

const stdinMarkdown = "## Why（背景）\n" +
	"- \"quoted\" and 'single' text\n" +
	"- $HOME and ${PATH} must not expand\n" +
	"- `backquoted` and ```fenced``` code\n" +
	"- back\\slash \\n literal\n" +
	"\n" +
	"  indented line with trailing spaces  \n"

func TestCmdCreateTicket_StdinDescriptionSavedByteForByte(t *testing.T) {
	repo, projectID := cliLabelSetup(t)
	eng := engine.New(repo)
	withStdin(t, stdinMarkdown)

	var runErr error
	out := captureStdout(t, func() {
		runErr = cmdCreateTicket(eng, repo, runtimeConfig{}, []string{"stdin ticket", "-", "--project", projectID})
	})
	if runErr != nil {
		t.Fatalf("cmdCreateTicket: %v", runErr)
	}
	tk := decodeCLITicket(t, out)
	if tk.Description != stdinMarkdown {
		t.Errorf("stdout description mismatch:\n got %q\nwant %q", tk.Description, stdinMarkdown)
	}
	if tk.Description == "-" {
		t.Errorf(`"-" must not be saved as the description`)
	}
	stored, err := repo.GetTicket(tk.ID)
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if stored.Description != stdinMarkdown {
		t.Errorf("stored description mismatch:\n got %q\nwant %q", stored.Description, stdinMarkdown)
	}
}

func TestCmdCreateTicket_StdinWithFlagsInAnyOrder(t *testing.T) {
	cases := [][]string{
		{"T", "-", "--priority", "HIGH", "--label", "バグ", "--label", "UI", "--project", "PROJECT"},
		{"--priority", "HIGH", "--label", "バグ", "--project", "PROJECT", "--label", "UI", "T", "-"},
		{"T", "--label", "バグ", "-", "--priority", "HIGH", "--project", "PROJECT", "--label", "UI"},
		{"--project", "PROJECT", "T", "--priority", "HIGH", "-", "--label", "UI", "--label", "バグ"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			repo, projectID := cliLabelSetup(t)
			eng := engine.New(repo)
			withStdin(t, stdinMarkdown)
			resolved := make([]string, len(args))
			for i, a := range args {
				if a == "PROJECT" {
					a = projectID
				}
				resolved[i] = a
			}
			var runErr error
			out := captureStdout(t, func() {
				runErr = cmdCreateTicket(eng, repo, runtimeConfig{}, resolved)
			})
			if runErr != nil {
				t.Fatalf("cmdCreateTicket: %v", runErr)
			}
			tk := decodeCLITicket(t, out)
			if tk.Title != "T" || tk.Description != stdinMarkdown || tk.Priority != "HIGH" || cliLabelNames(tk.Labels) != "UI,バグ" {
				t.Errorf("unexpected ticket: %+v", tk)
			}
		})
	}
}

func TestCmdCreateTicket_EmptyStdinIsAnError(t *testing.T) {
	for name, content := range map[string]string{
		"empty":           "",
		"whitespace only": "  \n\t\n",
	} {
		t.Run(name, func(t *testing.T) {
			repo, projectID := cliLabelSetup(t)
			eng := engine.New(repo)
			withStdin(t, content)
			before := countProjectTickets(t, repo, projectID)

			var runErr error
			out := captureStdout(t, func() {
				runErr = cmdCreateTicket(eng, repo, runtimeConfig{}, []string{"T", "-", "--project", projectID})
			})
			if runErr == nil {
				t.Fatal("expected an error for empty stdin")
			}
			for _, want := range []string{"stdin", "empty", createTicketUsage} {
				if !strings.Contains(runErr.Error(), want) {
					t.Errorf("error %q should mention %q", runErr, want)
				}
			}
			if out != "" {
				t.Errorf("nothing must be printed on stdout, got %q", out)
			}
			if after := countProjectTickets(t, repo, projectID); after != before {
				t.Errorf("no ticket may be created: %d -> %d", before, after)
			}
		})
	}
}

func TestCmdCreateTicket_StdinReadFailureIsAnError(t *testing.T) {
	repo, projectID := cliLabelSetup(t)
	eng := engine.New(repo)
	withClosedStdin(t)
	before := countProjectTickets(t, repo, projectID)

	var runErr error
	out := captureStdout(t, func() {
		runErr = cmdCreateTicket(eng, repo, runtimeConfig{}, []string{"T", "-", "--project", projectID})
	})
	if runErr == nil || !strings.Contains(runErr.Error(), "reading description from stdin") {
		t.Fatalf("expected a stdin read error, got %v", runErr)
	}
	if !errors.Is(runErr, os.ErrClosed) {
		t.Errorf("the underlying read error should be wrapped, got %v", runErr)
	}
	if out != "" {
		t.Errorf("nothing must be printed on stdout, got %q", out)
	}
	if after := countProjectTickets(t, repo, projectID); after != before {
		t.Errorf("no ticket may be created: %d -> %d", before, after)
	}
}

func TestCmdCreateTicket_DescriptionArgumentStillLiteral(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"plain argument", []string{"T", "説明本文"}, "説明本文"},
		{"argument starting with a dash", []string{"T", "- 箇条書き"}, "- 箇条書き"},
		{"omitted", []string{"T"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo, projectID := cliLabelSetup(t)
			eng := engine.New(repo)
			// stdin carries content that must NOT end up in the ticket: only
			// an exact "-" reads it.
			withStdin(t, "from stdin\n")
			var runErr error
			out := captureStdout(t, func() {
				runErr = cmdCreateTicket(eng, repo, runtimeConfig{}, append(c.args, "--project", projectID))
			})
			if runErr != nil {
				t.Fatalf("cmdCreateTicket: %v", runErr)
			}
			if tk := decodeCLITicket(t, out); tk.Description != c.want {
				t.Errorf("description = %q, want %q", tk.Description, c.want)
			}
		})
	}
}

func TestCmdCreateTicket_StdinWithUnknownLabelCreatesNothing(t *testing.T) {
	repo, projectID := cliLabelSetup(t)
	eng := engine.New(repo)
	before := countProjectTickets(t, repo, projectID)

	run := func(args []string) (error, string) {
		var runErr error
		out := captureStdout(t, func() {
			runErr = cmdCreateTicket(eng, repo, runtimeConfig{}, args)
		})
		return runErr, out
	}

	withStdin(t, stdinMarkdown)
	stdinErr, stdinOut := run([]string{"T", "-", "--label", "未登録", "--project", projectID})
	argErr, _ := run([]string{"T", "D", "--label", "未登録", "--project", projectID})

	for name, err := range map[string]error{"stdin": stdinErr, "argument": argErr} {
		var apiErr *domain.APIError
		if err == nil || !errors.As(err, &apiErr) || apiErr.Code != domain.ErrCodeLabelNotFound {
			t.Fatalf("%s: expected LABEL_NOT_FOUND, got %v", name, err)
		}
	}
	if stdinErr.Error() != argErr.Error() {
		t.Errorf("reading stdin must not change the error:\n stdin: %v\n   arg: %v", stdinErr, argErr)
	}
	if stdinOut != "" {
		t.Errorf("nothing must be printed on stdout, got %q", stdinOut)
	}
	if after := countProjectTickets(t, repo, projectID); after != before {
		t.Errorf("no ticket may be created: %d -> %d", before, after)
	}
}

func TestCmdCreateTicket_InvalidPriorityCheckedBeforeStdin(t *testing.T) {
	repo, projectID := cliLabelSetup(t)
	eng := engine.New(repo)
	// Empty stdin would itself be an error; getting the priority error
	// instead shows stdin was never consulted.
	withStdin(t, "")
	before := countProjectTickets(t, repo, projectID)

	var runErr error
	captureStdout(t, func() {
		runErr = cmdCreateTicket(eng, repo, runtimeConfig{}, []string{"T", "-", "--priority", "URGENT", "--project", projectID})
	})
	if runErr == nil || !strings.Contains(runErr.Error(), "URGENT") || strings.Contains(runErr.Error(), "stdin") {
		t.Fatalf("expected the priority error (not a stdin one), got %v", runErr)
	}
	if after := countProjectTickets(t, repo, projectID); after != before {
		t.Errorf("no ticket may be created: %d -> %d", before, after)
	}
}
