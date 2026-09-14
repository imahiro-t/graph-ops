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
	cat, err := LoadWithRoots(t.TempDir(), t.TempDir(), t.TempDir(), "")
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

// TestLoadWithRoots_LanguagePriority covers the full "explicit override >
// team > user > unset" precedence chain (execution plan, section 1.4).
func TestLoadWithRoots_LanguagePriority(t *testing.T) {
	t.Run("user only", func(t *testing.T) {
		userDir := t.TempDir()
		writeLoadTestDocument(t, userDir, userConfigFile, "ja")
		cat, err := LoadWithRoots(t.TempDir(), userDir, t.TempDir(), "")
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

	t.Run("team overrides user", func(t *testing.T) {
		userDir := t.TempDir()
		teamDir := t.TempDir()
		writeLoadTestDocument(t, userDir, userConfigFile, "ja")
		// An unsupported code for "team" only proves team wins in terms of
		// *which tier* is consulted; ApplyLocale then silently no-ops for an
		// unrecognized code (see LocalizedDefault), so the node name falls
		// back to English even though team, not user, decided the language.
		writeLoadTestDocument(t, teamDir, teamConfigFile, "xx-unsupported")
		cat, err := LoadWithRoots(t.TempDir(), userDir, teamDir, "")
		if err != nil {
			t.Fatalf("LoadWithRoots: %v", err)
		}
		if cat.Language != "xx-unsupported" {
			t.Errorf("Catalog.Language = %q, want team's xx-unsupported to win over user's ja", cat.Language)
		}
		if got := implNodeName(t, cat); got != "Implementation" {
			t.Errorf("impl node name = %q, want the English default (unknown locale silently ignored)", got)
		}
	})

	t.Run("explicit override wins over both tiers", func(t *testing.T) {
		userDir := t.TempDir()
		teamDir := t.TempDir()
		writeLoadTestDocument(t, userDir, userConfigFile, "ja")
		writeLoadTestDocument(t, teamDir, teamConfigFile, "ja")
		cat, err := LoadWithRoots(t.TempDir(), userDir, teamDir, "")
		if err != nil {
			t.Fatalf("LoadWithRoots: %v", err)
		}
		if got := implNodeName(t, cat); got != "実装" {
			t.Errorf("sanity check failed: impl node name = %q, want 実装", got)
		}

		// Now pass an explicit override that differs from both tiers: per
		// ResolveLanguage's doc comment the override alone decides which
		// locale is APPLIED to the default document (here, an unsupported
		// code, so English node names), even though both tiers still set
		// Language: ja. Catalog.Language itself (populated by Merge purely
		// from each Document's own Language field -- see Merge's doc
		// comment) is unaffected by languageOverride, since the override is
		// never written into any Document; it only steers which locale
		// LocalizedDefault applies before Merge runs.
		catOverride, err := LoadWithRoots(t.TempDir(), userDir, teamDir, "xx-unsupported")
		if err != nil {
			t.Fatalf("LoadWithRoots: %v", err)
		}
		if catOverride.Language != "ja" {
			t.Errorf("Catalog.Language = %q, want ja (from the team/user tiers, unaffected by languageOverride)", catOverride.Language)
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

	cat, err := LoadWithRoots(t.TempDir(), userDir, t.TempDir(), "")
	if err != nil {
		t.Fatalf("LoadWithRoots: %v", err)
	}
	if got := cat.ReviewGates["code_review"].Name; got != "カスタムコードレビュー" {
		t.Errorf("code_review.Name = %q, want the user's explicit override to win over the ja locale's default", got)
	}
}
