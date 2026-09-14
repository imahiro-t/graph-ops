package main

import "testing"

// A help request must never reach a command. "create-ticket --help" used to
// take "--help" as the ticket title and create a ticket, so the cases below
// pin both the help words and the per-command flag.
func TestIsHelpRequest(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{"no args", nil, false},
		{"help word", []string{"help"}, true},
		{"long flag alone", []string{"--help"}, true},
		{"short flag alone", []string{"-h"}, true},
		{"flag after command", []string{"create-ticket", "--help"}, true},
		{"short flag after command", []string{"create-ticket", "-h"}, true},
		{"flag in a later position", []string{"add-artifact", "T", "N", "name", "--help"}, true},
		{"plain command", []string{"list-tickets"}, false},
		{"command with real args", []string{"create-ticket", "title", "description"}, false},
		{"help only as a substring", []string{"create-ticket", "--help-wanted"}, false},
		{"help only inside a value", []string{"create-ticket", "needs --help text"}, false},
		{"a dash that is not a help flag", []string{"add-artifact", "T", "N", "name", "text", "-"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isHelpRequest(tc.args); got != tc.want {
				t.Errorf("isHelpRequest(%q) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}
