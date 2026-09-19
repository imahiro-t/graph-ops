package main

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/store"
)

// DFLT-00093: refine-ticket's [description|-] goes through the same
// readDescriptionArg as create-ticket. Empty or whitespace-only stdin, a
// failed read, and a whitespace-only argument are errors that leave the
// ticket completely untouched (before this, empty stdin still marked it
// REFINED and whitespace-only stdin replaced the description with blanks).
// These tests swap the global os.Stdin, so none of them may use t.Parallel.

const refineOriginalDescription = "精査前の説明"

// refineStdinFixture is a ticket in the state the "nothing changes" checks
// compare against: a description, priority LOW, one label, status TODO
// (not REFINED) and refined_at nil.
type refineStdinFixture struct {
	repo   store.GraphRepository
	eng    *engine.GraphEngine
	before *domain.Ticket
}

func newRefineStdinFixture(t *testing.T) refineStdinFixture {
	t.Helper()
	repo, projectID := cliLabelSetup(t)
	eng := engine.New(repo)
	low := domain.TicketPriorityLow
	tk, err := eng.CreateTicketWithOptions(projectID, "A-1", refineOriginalDescription, engine.CreateTicketOptions{Priority: &low, LabelNames: []string{"バグ"}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	before, err := repo.GetTicket(tk.ID)
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if before.Status == domain.TicketRefined || before.RefinedAt != nil || before.Priority != low || cliLabelNames(before.Labels) != "バグ" {
		t.Fatalf("unexpected fixture ticket: %+v", before)
	}
	return refineStdinFixture{repo: repo, eng: eng, before: before}
}

// run calls cmdRefineTicket on the fixture ticket with args after the id and
// returns its error and stdout.
func (f refineStdinFixture) run(t *testing.T, args ...string) (error, string) {
	t.Helper()
	var runErr error
	out := captureStdout(t, func() {
		runErr = cmdRefineTicket(f.eng, append([]string{f.before.ID}, args...))
	})
	return runErr, out
}

func (f refineStdinFixture) stored(t *testing.T) *domain.Ticket {
	t.Helper()
	tk, err := f.repo.GetTicket(f.before.ID)
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	return tk
}

// assertUnchanged checks description, priority, labels, status, refined_at
// and updated_at against the state before the command ran.
func (f refineStdinFixture) assertUnchanged(t *testing.T) {
	t.Helper()
	got := f.stored(t)
	b := f.before
	if got.Description != b.Description || got.Priority != b.Priority ||
		cliLabelNames(got.Labels) != cliLabelNames(b.Labels) || got.Status != b.Status ||
		!equalStrPtr(got.RefinedAt, b.RefinedAt) || got.UpdatedAt != b.UpdatedAt {
		t.Errorf("the ticket must be unchanged:\n got %+v\nwant %+v", got, b)
	}
}

func equalStrPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// refineFlagCombos are the flag sets every rejection case is combined with:
// a rejected description must win over (and discard) a valid --priority and
// --label.
var refineFlagCombos = map[string][]string{
	"no flags":           nil,
	"--priority":         {"--priority", "HIGH"},
	"--label":            {"--label", "機能追加"},
	"--priority --label": {"--priority", "HIGH", "--label", "機能追加"},
}

func TestCmdRefineTicket_EmptyOrWhitespaceStdinChangesNothing(t *testing.T) {
	stdins := map[string]string{
		"empty":           "",
		"newline":         "\n",
		"whitespace only": "  \n\t\n",
	}
	for stdinName, content := range stdins {
		for flagName, flags := range refineFlagCombos {
			t.Run(stdinName+"/"+flagName, func(t *testing.T) {
				f := newRefineStdinFixture(t)
				withStdin(t, content)
				err, out := f.run(t, append([]string{"-"}, flags...)...)
				if err == nil {
					t.Fatal("expected an error")
				}
				for _, want := range []string{"stdin", "empty", refineTicketUsageLine} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q should mention %q", err, want)
					}
				}
				if out != "" {
					t.Errorf("nothing must be printed on stdout, got %q", out)
				}
				f.assertUnchanged(t)
			})
		}
	}
}

func TestCmdRefineTicket_EmptyStdinWithFlagsBeforeDash(t *testing.T) {
	f := newRefineStdinFixture(t)
	withStdin(t, "")
	err, out := f.run(t, "--priority", "HIGH", "--label", "機能追加", "-")
	if err == nil || !strings.Contains(err.Error(), "stdin") || !strings.Contains(err.Error(), "empty") ||
		!strings.Contains(err.Error(), refineTicketUsageLine) {
		t.Fatalf("expected the empty-stdin error, got %v", err)
	}
	if out != "" {
		t.Errorf("nothing must be printed on stdout, got %q", out)
	}
	f.assertUnchanged(t)
}

func TestCmdRefineTicket_StdinReadFailureChangesNothing(t *testing.T) {
	f := newRefineStdinFixture(t)
	withClosedStdin(t)
	err, out := f.run(t, "-", "--priority", "HIGH")
	if err == nil || !strings.HasPrefix(err.Error(), "reading description from stdin:") {
		t.Fatalf("expected a stdin read error, got %v", err)
	}
	if !errors.Is(err, os.ErrClosed) {
		t.Errorf("the underlying read error should be wrapped, got %v", err)
	}
	if out != "" {
		t.Errorf("nothing must be printed on stdout, got %q", out)
	}
	f.assertUnchanged(t)
}

func TestCmdRefineTicket_EmptyStdinWithUnregisteredLabelChangesNothing(t *testing.T) {
	f := newRefineStdinFixture(t)
	withStdin(t, "")
	err, out := f.run(t, "-", "--label", "未登録ラベル")
	if err == nil {
		t.Fatal("expected an error")
	}
	if out != "" {
		t.Errorf("nothing must be printed on stdout, got %q", out)
	}
	f.assertUnchanged(t)
}

func TestCmdRefineTicket_WhitespaceOnlyArgumentChangesNothing(t *testing.T) {
	args := map[string]string{
		"spaces":           "   ",
		"newline":          "\n",
		"tab":              "\t",
		"space newline sp": " \n ",
	}
	for argName, arg := range args {
		for flagName, flags := range refineFlagCombos {
			t.Run(argName+"/"+flagName, func(t *testing.T) {
				f := newRefineStdinFixture(t)
				// Content that would be accepted if stdin were (wrongly) read.
				withStdin(t, "stdin の内容")
				err, out := f.run(t, append([]string{arg}, flags...)...)
				if err == nil {
					t.Fatal("expected an error")
				}
				for _, want := range []string{"empty", refineTicketUsageLine} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q should mention %q", err, want)
					}
				}
				if strings.Contains(err.Error(), "stdin") {
					t.Errorf("a whitespace-only argument must not be reported as a stdin error: %v", err)
				}
				if out != "" {
					t.Errorf("nothing must be printed on stdout, got %q", out)
				}
				f.assertUnchanged(t)
			})
		}
	}
}

// An explicit "" keeps its pre-DFLT-00093 meaning: leave the description
// alone. The engine still marks the ticket REFINED (refine always does, out
// of scope here) but, since the description was not overwritten, refined_at
// stays nil.
func TestCmdRefineTicket_EmptyStringArgumentLeavesDescription(t *testing.T) {
	t.Run("alone", func(t *testing.T) {
		f := newRefineStdinFixture(t)
		withStdin(t, "stdin の内容")
		if err, _ := f.run(t, ""); err != nil {
			t.Fatalf("cmdRefineTicket: %v", err)
		}
		got := f.stored(t)
		if got.Description != refineOriginalDescription {
			t.Errorf("description = %q, want it unchanged", got.Description)
		}
		if got.RefinedAt != nil {
			t.Errorf("refined_at must stay nil, got %v", *got.RefinedAt)
		}
		if got.Status != domain.TicketRefined {
			t.Errorf("status = %s, want REFINED (refine always sets it)", got.Status)
		}
		if got.Priority != f.before.Priority || cliLabelNames(got.Labels) != cliLabelNames(f.before.Labels) {
			t.Errorf("priority/labels must be unchanged: %+v", got)
		}
	})
	t.Run("with --priority", func(t *testing.T) {
		f := newRefineStdinFixture(t)
		withStdin(t, "stdin の内容")
		if err, _ := f.run(t, "", "--priority", "HIGH"); err != nil {
			t.Fatalf("cmdRefineTicket: %v", err)
		}
		got := f.stored(t)
		if got.Priority != domain.TicketPriorityHigh {
			t.Errorf("priority = %s, want HIGH", got.Priority)
		}
		if got.Description != refineOriginalDescription || got.RefinedAt != nil || got.Status != domain.TicketRefined {
			t.Errorf("description unchanged, refined_at nil and status REFINED expected: %+v", got)
		}
	})
}

func TestCmdRefineTicket_StdinDescriptionSavedByteForByte(t *testing.T) {
	cases := []struct {
		name         string
		args         []string
		wantPriority domain.TicketPriority
		wantLabels   string
	}{
		{"dash only", []string{"-"}, domain.TicketPriorityLow, "バグ"},
		{"flags after dash", []string{"-", "--priority", "HIGH", "--label", "機能追加"}, domain.TicketPriorityHigh, "機能追加"},
		{"flags before dash", []string{"--priority", "HIGH", "--label", "機能追加", "-"}, domain.TicketPriorityHigh, "機能追加"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newRefineStdinFixture(t)
			withStdin(t, stdinMarkdown)
			err, out := f.run(t, c.args...)
			if err != nil {
				t.Fatalf("cmdRefineTicket: %v", err)
			}
			if tk := decodeCLITicket(t, out); tk.Description != stdinMarkdown {
				t.Errorf("stdout description mismatch:\n got %q\nwant %q", tk.Description, stdinMarkdown)
			}
			got := f.stored(t)
			if got.Description != stdinMarkdown {
				t.Errorf("stored description mismatch:\n got %q\nwant %q", got.Description, stdinMarkdown)
			}
			if got.Status != domain.TicketRefined || got.RefinedAt == nil {
				t.Errorf("status REFINED and refined_at set expected: %+v", got)
			}
			if got.Priority != c.wantPriority || cliLabelNames(got.Labels) != c.wantLabels {
				t.Errorf("priority/labels = %s/%s, want %s/%s", got.Priority, cliLabelNames(got.Labels), c.wantPriority, c.wantLabels)
			}
		})
	}
}

func TestCmdRefineTicket_OmittedDescriptionChangesOnlyFlags(t *testing.T) {
	f := newRefineStdinFixture(t)
	withStdin(t, "stdin の内容")
	if err, _ := f.run(t, "--priority", "HIGH", "--label", "機能追加"); err != nil {
		t.Fatalf("cmdRefineTicket: %v", err)
	}
	got := f.stored(t)
	if got.Priority != domain.TicketPriorityHigh || cliLabelNames(got.Labels) != "機能追加" {
		t.Errorf("priority/labels should change: %+v", got)
	}
	if got.Description != refineOriginalDescription {
		t.Errorf("description = %q, want it unchanged (stdin must not be read)", got.Description)
	}
}

func TestCmdRefineTicket_InvalidPriorityCheckedBeforeStdin(t *testing.T) {
	f := newRefineStdinFixture(t)
	// Empty stdin would itself be an error; getting the priority error
	// instead shows stdin was never consulted.
	withStdin(t, "")
	err, out := f.run(t, "-", "--priority", "URGENT")
	if err == nil || !strings.Contains(err.Error(), "URGENT") || strings.Contains(err.Error(), "stdin") {
		t.Fatalf("expected the priority error (not a stdin one), got %v", err)
	}
	if out != "" {
		t.Errorf("nothing must be printed on stdout, got %q", out)
	}
	f.assertUnchanged(t)
}

func TestCmdRefineTicket_DescriptionArgumentStillLiteral(t *testing.T) {
	for _, arg := range []string{"新しい説明", "- 箇条書き"} {
		t.Run(arg, func(t *testing.T) {
			f := newRefineStdinFixture(t)
			withStdin(t, "stdin の内容")
			if err, _ := f.run(t, arg); err != nil {
				t.Fatalf("cmdRefineTicket: %v", err)
			}
			if got := f.stored(t); got.Description != arg {
				t.Errorf("description = %q, want %q", got.Description, arg)
			}
		})
	}
}
