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

// TestCmdGetPlanTemplate_JaLanguageSettingPrintsJapaneseLocaleTemplate covers
// the CLI wiring's language resolution: with `language: ja` persisted at the
// user tier, `get-plan-template` prints the Japanese locale template instead
// of the English default.
func TestCmdGetPlanTemplate_JaLanguageSettingPrintsJapaneseLocaleTemplate(t *testing.T) {
	userDir := t.TempDir()
	if err := config.SaveDocumentAt(config.UserDocumentPath(userDir), config.Document{Language: "ja"}); err != nil {
		t.Fatalf("SaveDocumentAt: %v", err)
	}
	rc := runtimeConfig{UserExtensionsDir: userDir, TeamExtensionsDir: t.TempDir()}

	out := captureStdout(t, func() {
		if err := cmdGetPlanTemplate(rc); err != nil {
			t.Fatalf("cmdGetPlanTemplate: %v", err)
		}
	})

	for _, heading := range []string{"# 目的", "# 手順", "# 影響範囲", "# リスク・留意事項"} {
		if !strings.Contains(out, heading) {
			t.Errorf("expected output to contain heading %q, got %q", heading, out)
		}
	}
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

// TestCmdGetReviewTemplate_JaLanguageSettingPrintsJapaneseLocaleTemplate
// covers the CLI wiring's language resolution: with `language: ja` persisted
// at the team tier, `get-review-template` prints the Japanese locale
// template (headings and verdict words) instead of the English default.
func TestCmdGetReviewTemplate_JaLanguageSettingPrintsJapaneseLocaleTemplate(t *testing.T) {
	teamDir := t.TempDir()
	if err := config.SaveDocumentAt(config.TeamDocumentPath(teamDir), config.Document{Language: "ja"}); err != nil {
		t.Fatalf("SaveDocumentAt: %v", err)
	}
	rc := runtimeConfig{UserExtensionsDir: t.TempDir(), TeamExtensionsDir: teamDir}

	out := captureStdout(t, func() {
		if err := cmdGetReviewTemplate(rc); err != nil {
			t.Fatalf("cmdGetReviewTemplate: %v", err)
		}
	})

	for _, heading := range []string{"# 判定", "# 指摘事項", "# 判断理由", "# 条件付き承認の場合の条件"} {
		if !strings.Contains(out, heading) {
			t.Errorf("expected output to contain heading %q, got %q", heading, out)
		}
	}
	for _, word := range []string{"承認", "条件付き承認", "差し戻し"} {
		if !strings.Contains(out, word) {
			t.Errorf("expected output to mention fixed verdict word %q, got %q", word, out)
		}
	}
}
