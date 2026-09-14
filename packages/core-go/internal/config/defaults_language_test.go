package config

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
)

// containsJapanese reports whether s has any Hiragana, Katakana, or Han rune.
func containsJapanese(s string) bool {
	for _, r := range s {
		if unicode.In(r, unicode.Hiragana, unicode.Katakana, unicode.Han) {
			return true
		}
	}
	return false
}

// TestDefaults_NoJapaneseOutsideJaLocale keeps the English plugin defaults
// free of Japanese text. `npm run sync:defaults` (run first by `npm test`)
// mirrors packages/plugin/defaults into ./defaults, so walking the mirror
// also checks the hand-edited source.
func TestDefaults_NoJapaneseOutsideJaLocale(t *testing.T) {
	const root = "defaults"
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == "locales/ja" {
				return filepath.SkipDir
			}
			return nil
		}
		if rel == "locales/ja.yaml" {
			return nil
		}
		f, openErr := os.Open(path)
		if openErr != nil {
			return openErr
		}
		defer f.Close()
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		line := 0
		for scanner.Scan() {
			line++
			if containsJapanese(scanner.Text()) {
				t.Errorf("%s:%d contains Japanese text; put Japanese content under defaults/locales/ja/ or defaults/locales/ja.yaml instead: %q", rel, line, scanner.Text())
			}
		}
		return scanner.Err()
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
}

// TestPlanAndReviewTemplates_HaveNoHTMLComment guards against reintroducing
// an instruction comment into the Markdown templates: agents copied such a
// comment into saved artifacts, so a template must be the output's shape
// itself, starting at its first heading.
func TestPlanAndReviewTemplates_HaveNoHTMLComment(t *testing.T) {
	cases := map[string]string{
		"plan (default)":   ResolvePlanTemplate(Roots{}, ""),
		"plan (ja)":        ResolvePlanTemplate(Roots{}, "ja"),
		"review (default)": ResolveReviewTemplate(Roots{}, ""),
		"review (ja)":      ResolveReviewTemplate(Roots{}, "ja"),
	}
	for name, tmpl := range cases {
		if strings.Contains(tmpl, "<!--") {
			t.Errorf("%s template must not contain an HTML comment, got %q", name, tmpl)
		}
		if !strings.HasPrefix(tmpl, "# ") {
			t.Errorf("%s template must start at its first heading, got %q", name, tmpl)
		}
	}
}

// templateHeadings returns the text of every top-level "# " heading in tmpl.
func templateHeadings(tmpl string) []string {
	var headings []string
	for _, line := range strings.Split(tmpl, "\n") {
		if strings.HasPrefix(line, "# ") {
			headings = append(headings, strings.TrimSpace(strings.TrimPrefix(line, "# ")))
		}
	}
	return headings
}

// forbiddenTemplateWords lists heading names and verdict words that belong to
// a specific language's plan/review template and so must not be hardcoded in
// agent-facing instructions. Headings are derived from the resolved templates
// themselves so the list follows template changes; a heading is forbidden in
// its "# X" form, and also bare when it cannot be mistaken for ordinary
// English prose (non-ASCII text, or containing punctuation such as "/" or
// "("). Verdict words are listed explicitly because they live inside a
// placeholder line rather than a heading.
func forbiddenTemplateWords(t *testing.T) []string {
	t.Helper()
	var words []string
	for _, tmpl := range []string{
		ResolvePlanTemplate(Roots{}, ""),
		ResolvePlanTemplate(Roots{}, "ja"),
		ResolveReviewTemplate(Roots{}, ""),
		ResolveReviewTemplate(Roots{}, "ja"),
	} {
		headings := templateHeadings(tmpl)
		if len(headings) == 0 {
			t.Fatalf("expected headings in template, got %q", tmpl)
		}
		for _, h := range headings {
			words = append(words, "# "+h)
			if containsJapanese(h) || strings.ContainsAny(h, "/(") {
				words = append(words, h)
			}
		}
	}
	return append(words,
		"Approved", "Conditionally Approved", "Rejected",
		"承認", "条件付き承認", "差し戻し",
	)
}

// TestNodeTypeDefaults_PlanAndReviewDoNotHardcodeTemplateWords keeps the
// plan/review/review_gate node-type defaults language-neutral: they must tell
// the agent to use whatever headings and verdict words the fetched template
// gives, never name one language's words (which contradicts the template
// whenever the other language resolves).
func TestNodeTypeDefaults_PlanAndReviewDoNotHardcodeTemplateWords(t *testing.T) {
	forbidden := forbiddenTemplateWords(t)
	for _, nodeType := range []string{"plan", "review", "review_gate"} {
		content := defaultNodeTypeContent(nodeType)
		if content == "" {
			t.Fatalf("expected a non-empty plugin default for %s", nodeType)
		}
		for _, w := range forbidden {
			if strings.Contains(content, w) {
				t.Errorf("node-types/%s.md hardcodes template word %q; refer to the fetched template's headings/verdict words by role instead", nodeType, w)
			}
		}
	}
}

// TestGraphNodeAgent_DoesNotHardcodeTemplateWords applies the same check to
// the plugin's graph-node-agent definition, which also tells plan/review
// agents how to fill in those templates.
func TestGraphNodeAgent_DoesNotHardcodeTemplateWords(t *testing.T) {
	path := filepath.Join("..", "..", "..", "plugin", "agents", "graph-node-agent.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("graph-node-agent.md not available at %s: %v", path, err)
	}
	content := string(data)
	for _, w := range forbiddenTemplateWords(t) {
		if strings.Contains(content, w) {
			t.Errorf("graph-node-agent.md hardcodes template word %q; refer to the fetched template's headings/verdict words by role instead", w)
		}
	}
}
