package main

import (
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/config"
)

// TestCmdGetPlanTemplate_PrintsPluginDefault covers the CLI wiring for
// `get-plan-template`: with no user/team override and no persistent language
// setting configured, it prints the plugin's fixed English Markdown plan
// template (marker checks for the template itself live in internal/config's
// TestResolvePlanTemplate_* tests).
func TestCmdGetPlanTemplate_PrintsPluginDefault(t *testing.T) {
	rc := runtimeConfig{UserExtensionsDir: t.TempDir(), TeamExtensionsDir: t.TempDir()}

	out := captureStdout(t, func() {
		if err := cmdGetPlanTemplate(rc); err != nil {
			t.Fatalf("cmdGetPlanTemplate: %v", err)
		}
	})

	for _, heading := range []string{"# Purpose", "# Steps", "# Impact", "# Risks / Notes"} {
		if !strings.Contains(out, heading) {
			t.Errorf("expected output to contain heading %q, got %q", heading, out)
		}
	}
}

// TestCmdGetPlanTemplate_JaLanguageSettingStillPrintsEnglishDefault covers
// the removal of language-based template switching: with `language: ja`
// persisted at the user or team tier and no override, `get-plan-template`
// still prints the English plugin default.
func TestCmdGetPlanTemplate_JaLanguageSettingStillPrintsEnglishDefault(t *testing.T) {
	for _, tier := range []string{"user", "team"} {
		t.Run(tier, func(t *testing.T) {
			rc := runtimeConfigWithJaLanguage(t, tier)

			out := captureStdout(t, func() {
				if err := cmdGetPlanTemplate(rc); err != nil {
					t.Fatalf("cmdGetPlanTemplate: %v", err)
				}
			})

			if !strings.HasPrefix(out, "# Purpose") {
				t.Errorf("expected the English default starting with # Purpose, got %q", out)
			}
			for _, heading := range []string{"# 目的", "# 手順"} {
				if strings.Contains(out, heading) {
					t.Errorf("did not expect Japanese heading %q, got %q", heading, out)
				}
			}
		})
	}
}

// TestCmdGetPlanTemplate_OverridePrecedence covers team -> user -> plugin
// default resolution through the CLI.
func TestCmdGetPlanTemplate_OverridePrecedence(t *testing.T) {
	assertTemplateOverridePrecedence(t, config.PlanSubdir, config.PlanTemplateFile, "# Purpose", cmdGetPlanTemplate)
}

// TestCmdGetReviewTemplate_PrintsPluginDefault covers the CLI wiring for
// `get-review-template`, shared by the review/review_gate node types: with
// no user/team override and no persistent language setting configured, it
// prints the plugin's fixed English Markdown review template, including the
// three fixed verdict words.
func TestCmdGetReviewTemplate_PrintsPluginDefault(t *testing.T) {
	rc := runtimeConfig{UserExtensionsDir: t.TempDir(), TeamExtensionsDir: t.TempDir()}

	out := captureStdout(t, func() {
		if err := cmdGetReviewTemplate(rc); err != nil {
			t.Fatalf("cmdGetReviewTemplate: %v", err)
		}
	})

	for _, heading := range []string{"# Verdict", "# Findings", "# Rationale", "# Conditions (if Conditionally Approved)"} {
		if !strings.Contains(out, heading) {
			t.Errorf("expected output to contain heading %q, got %q", heading, out)
		}
	}
	for _, word := range []string{"Approved", "Conditionally Approved", "Rejected"} {
		if !strings.Contains(out, word) {
			t.Errorf("expected output to mention fixed verdict word %q, got %q", word, out)
		}
	}
}

// TestCmdGetReviewTemplate_JaLanguageSettingStillPrintsEnglishDefault is
// TestCmdGetPlanTemplate_JaLanguageSettingStillPrintsEnglishDefault's review
// counterpart, also checking that no Japanese verdict word leaks in.
func TestCmdGetReviewTemplate_JaLanguageSettingStillPrintsEnglishDefault(t *testing.T) {
	for _, tier := range []string{"user", "team"} {
		t.Run(tier, func(t *testing.T) {
			rc := runtimeConfigWithJaLanguage(t, tier)

			out := captureStdout(t, func() {
				if err := cmdGetReviewTemplate(rc); err != nil {
					t.Fatalf("cmdGetReviewTemplate: %v", err)
				}
			})

			if !strings.HasPrefix(out, "# Verdict") {
				t.Errorf("expected the English default starting with # Verdict, got %q", out)
			}
			for _, word := range []string{"# 判定", "条件付き承認", "差し戻し"} {
				if strings.Contains(out, word) {
					t.Errorf("did not expect Japanese template text %q, got %q", word, out)
				}
			}
		})
	}
}

// TestCmdGetReviewTemplate_OverridePrecedence covers team -> user -> plugin
// default resolution through the CLI.
func TestCmdGetReviewTemplate_OverridePrecedence(t *testing.T) {
	assertTemplateOverridePrecedence(t, config.ReviewSubdir, config.ReviewTemplateFile, "# Verdict", cmdGetReviewTemplate)
}

// runtimeConfigWithJaLanguage returns a runtimeConfig with fresh user/team
// roots and `language: ja` persisted at the given tier ("user" or "team").
func runtimeConfigWithJaLanguage(t *testing.T, tier string) runtimeConfig {
	t.Helper()
	userDir, teamDir := t.TempDir(), t.TempDir()
	path := config.UserDocumentPath(userDir)
	if tier == "team" {
		path = config.TeamDocumentPath(teamDir)
	}
	if err := config.SaveDocumentAt(path, config.Document{Language: "ja"}); err != nil {
		t.Fatalf("SaveDocumentAt: %v", err)
	}
	return runtimeConfig{UserExtensionsDir: userDir, TeamExtensionsDir: teamDir}
}

// assertTemplateOverridePrecedence walks one template command through every
// tier combination: user only, then user + team (team wins), then team only,
// then neither (English default, whose first heading is defaultHeading).
func assertTemplateOverridePrecedence(t *testing.T, subdir, file, defaultHeading string, cmd func(runtimeConfig) error) {
	t.Helper()
	userDir, teamDir := t.TempDir(), t.TempDir()
	rc := runtimeConfig{UserExtensionsDir: userDir, TeamExtensionsDir: teamDir}
	run := func() string {
		t.Helper()
		return strings.TrimSpace(captureStdout(t, func() {
			if err := cmd(rc); err != nil {
				t.Fatalf("command: %v", err)
			}
		}))
	}
	write := func(root, text string) {
		t.Helper()
		if err := config.WriteExtensionText(root, subdir, file, text); err != nil {
			t.Fatalf("WriteExtensionText: %v", err)
		}
	}

	write(userDir, "# user-override")
	if got := run(); got != "# user-override" {
		t.Errorf("user override only: got %q", got)
	}
	write(teamDir, "# team-override")
	if got := run(); got != "# team-override" {
		t.Errorf("user + team overrides: expected team to win, got %q", got)
	}
	write(userDir, "")
	if got := run(); got != "# team-override" {
		t.Errorf("team override only: got %q", got)
	}
	write(teamDir, "")
	if got := run(); !strings.HasPrefix(got, defaultHeading) {
		t.Errorf("no overrides: expected English default starting with %q, got %q", defaultHeading, got)
	}
}
