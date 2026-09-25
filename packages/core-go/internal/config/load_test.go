package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeLoadTestDocument writes a minimal Document YAML with only a language
// field set, at <dir>/<file> -- used below to build user (config.yaml) and
// team (workflow.yaml) tier fixtures for LoadWithRoots.
func writeLoadTestDocument(t *testing.T, dir, file, language string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := "version: 1\n"
	if language != "" {
		content += "language: " + language + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", file, err)
	}
}

func implNodeName(t *testing.T, cat Catalog) string {
	t.Helper()
	for _, n := range cat.Nodes {
		if n.ID == "impl" {
			return n.Name
		}
	}
	t.Fatal("impl node not found in catalog")
	return ""
}

// TestLoadWithRoots_UnsetLanguageMatchesEnglishDefault is the regression
// check the execution plan's section 3.1 calls for: with no language
// anywhere, LoadWithRoots' output must be byte-for-byte the same node
// names/Language as before this ticket's changes.
func TestLoadWithRoots_UnsetLanguageMatchesEnglishDefault(t *testing.T) {
	cat, err := LoadWithRoots(t.TempDir(), t.TempDir(), "")
	if err != nil {
		t.Fatalf("LoadWithRoots: %v", err)
	}
	if cat.Language != "" {
		t.Errorf("expected empty Catalog.Language, got %q", cat.Language)
	}
	if got := implNodeName(t, cat); got != "Implementation" {
		t.Errorf("impl node name = %q, want the English default", got)
	}
}

// countWarnings returns how many of warnings have the given code.
func countWarnings(warnings []Warning, code string) int {
	n := 0
	for _, w := range warnings {
		if w.Code == code {
			n++
		}
	}
	return n
}

// TestLoadWithRoots_LanguagePriority covers the "explicit override > user >
// unset" precedence chain (execution plan, section 1.4). The team tier's
// language is not part of it: the working language is a personal setting
// (DFLT-00153), so a team workflow.yaml's language is ignored and reported as
// a WarnTeamLanguageIgnored warning.
func TestLoadWithRoots_LanguagePriority(t *testing.T) {
	t.Run("user only", func(t *testing.T) {
		userDir := t.TempDir()
		writeLoadTestDocument(t, userDir, userConfigFile, "ja")
		cat, err := LoadWithRoots(userDir, t.TempDir(), "")
		if err != nil {
			t.Fatalf("LoadWithRoots: %v", err)
		}
		if cat.Language != "ja" {
			t.Errorf("Catalog.Language = %q, want ja", cat.Language)
		}
		if got := implNodeName(t, cat); got != "実装" {
			t.Errorf("impl node name = %q, want 実装", got)
		}
	})

	t.Run("team language is ignored", func(t *testing.T) {
		userDir := t.TempDir()
		teamDir := t.TempDir()
		writeLoadTestDocument(t, userDir, userConfigFile, "ja")
		writeLoadTestDocument(t, teamDir, teamConfigFile, "xx-unsupported")
		cat, err := LoadWithRoots(userDir, teamDir, "")
		if err != nil {
			t.Fatalf("LoadWithRoots: %v", err)
		}
		if cat.Language != "ja" {
			t.Errorf("Catalog.Language = %q, want the user tier's ja (the team tier's language is ignored)", cat.Language)
		}
		if got := implNodeName(t, cat); got != "実装" {
			t.Errorf("impl node name = %q, want 実装 (localized by the user tier's ja)", got)
		}
		if n := countWarnings(cat.Warnings, WarnTeamLanguageIgnored); n != 1 {
			t.Fatalf("got %d %s warnings, want exactly 1: %+v", n, WarnTeamLanguageIgnored, cat.Warnings)
		}
		for _, w := range cat.Warnings {
			if w.Code == WarnTeamLanguageIgnored && w.Language != "xx-unsupported" {
				t.Errorf("warning Language = %q, want xx-unsupported", w.Language)
			}
		}
	})

	t.Run("team language alone leaves the English default", func(t *testing.T) {
		teamDir := t.TempDir()
		writeLoadTestDocument(t, teamDir, teamConfigFile, "ja")
		cat, err := LoadWithRoots(t.TempDir(), teamDir, "")
		if err != nil {
			t.Fatalf("LoadWithRoots: %v", err)
		}
		if cat.Language != "" {
			t.Errorf("Catalog.Language = %q, want empty (only the team tier sets one)", cat.Language)
		}
		if got := implNodeName(t, cat); got != "Implementation" {
			t.Errorf("impl node name = %q, want the English default", got)
		}
		if n := countWarnings(cat.Warnings, WarnTeamLanguageIgnored); n != 1 {
			t.Errorf("got %d %s warnings, want exactly 1: %+v", n, WarnTeamLanguageIgnored, cat.Warnings)
		}
	})

	t.Run("no warning without a team language", func(t *testing.T) {
		userDir := t.TempDir()
		teamDir := t.TempDir()
		writeLoadTestDocument(t, userDir, userConfigFile, "ja")
		writeLoadTestDocument(t, teamDir, teamConfigFile, "")
		cat, err := LoadWithRoots(userDir, teamDir, "")
		if err != nil {
			t.Fatalf("LoadWithRoots: %v", err)
		}
		if n := countWarnings(cat.Warnings, WarnTeamLanguageIgnored); n != 0 {
			t.Errorf("got %d %s warnings, want none: %+v", n, WarnTeamLanguageIgnored, cat.Warnings)
		}
	})

	t.Run("explicit override wins over the user tier", func(t *testing.T) {
		userDir := t.TempDir()
		teamDir := t.TempDir()
		writeLoadTestDocument(t, userDir, userConfigFile, "ja")
		writeLoadTestDocument(t, teamDir, teamConfigFile, "ja")
		cat, err := LoadWithRoots(userDir, teamDir, "")
		if err != nil {
			t.Fatalf("LoadWithRoots: %v", err)
		}
		if got := implNodeName(t, cat); got != "実装" {
			t.Errorf("sanity check failed: impl node name = %q, want 実装", got)
		}
		if n := countWarnings(cat.Warnings, WarnTeamLanguageIgnored); n != 1 {
			t.Errorf("got %d %s warnings, want exactly 1 for the team tier's ja", n, WarnTeamLanguageIgnored)
		}

		// Now pass an explicit override that differs from the user tier: per
		// ResolveLanguage's doc comment the override alone decides which
		// locale is APPLIED to the default document (here, an unsupported
		// code, so English node names), even though the user tier still sets
		// Language: ja. Catalog.Language itself (populated by Merge purely
		// from each Document's own Language field -- see Merge's doc
		// comment) is unaffected by languageOverride, since the override is
		// never written into any Document; it only steers which locale
		// LocalizedDefault applies before Merge runs.
		catOverride, err := LoadWithRoots(userDir, teamDir, "xx-unsupported")
		if err != nil {
			t.Fatalf("LoadWithRoots: %v", err)
		}
		if catOverride.Language != "ja" {
			t.Errorf("Catalog.Language = %q, want ja (from the user tier, unaffected by languageOverride)", catOverride.Language)
		}
		if got := implNodeName(t, catOverride); got != "Implementation" {
			t.Errorf("impl node name = %q, want the English default (override's locale is unsupported)", got)
		}
	})
}

// TestLoadWithRoots_ExistingUserReviewGateNameOverrideStillWins is the
// backward-compatibility check called for in the execution plan's section
// 3.1/3.2: a pre-DFLT-00051 onboarding-style user override that hand-writes
// a translated review_gates[].name must still take effect even once the
// user tier also sets language: ja -- the locale layer sits BELOW user/team
// overrides in Merge's priority order, never above them.
func TestLoadWithRoots_ExistingUserReviewGateNameOverrideStillWins(t *testing.T) {
	userDir := t.TempDir()
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := `version: 1
language: ja
review_gates:
  code_review:
    name: "カスタムコードレビュー"
`
	if err := os.WriteFile(filepath.Join(userDir, userConfigFile), []byte(content), 0o644); err != nil {
		t.Fatalf("write config.yaml: %v", err)
	}

	cat, err := LoadWithRoots(userDir, t.TempDir(), "")
	if err != nil {
		t.Fatalf("LoadWithRoots: %v", err)
	}
	if got := cat.ReviewGates["code_review"].Name; got != "カスタムコードレビュー" {
		t.Errorf("code_review.Name = %q, want the user's explicit override to win over the ja locale's default", got)
	}
}
