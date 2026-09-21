package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeExtensionFile(t *testing.T, root, subdir, name, content string) {
	t.Helper()
	dir := filepath.Join(root, extensionsSubdir, subdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestResolveNodeTypeContext_NoExtensionsFallsBackToDefault(t *testing.T) {
	roots := Roots{}
	got := ResolveNodeTypeContext(roots, "review")
	want := defaultNodeTypeContent("review")
	if got != want {
		t.Errorf("got %q, want plugin default %q", got, want)
	}
	if want == "" {
		t.Fatal("expected a non-empty plugin default for review")
	}
}

func TestResolveNodeTypeContext_UserLayerAppendsAfterDefault(t *testing.T) {
	userDir := t.TempDir()
	writeExtensionFile(t, userDir, NodeTypesSubdir, "review.md", "Always write monetary figures in JPY.")

	got := ResolveNodeTypeContext(Roots{UserDir: userDir}, "review")
	def := defaultNodeTypeContent("review")

	if !strings.Contains(got, def) {
		t.Errorf("missing plugin default in %q", got)
	}
	if !strings.Contains(got, "Always write monetary figures in JPY.") {
		t.Errorf("missing user extension text in %q", got)
	}
	if strings.Index(got, def) > strings.Index(got, "Always write monetary figures in JPY.") {
		t.Errorf("expected plugin default to appear before user text, got %q", got)
	}
}

func TestResolveNodeTypeContext_TeamLayerAppendsAfterUser(t *testing.T) {
	userDir, teamDir := t.TempDir(), t.TempDir()
	writeExtensionFile(t, userDir, NodeTypesSubdir, "report.md", "Always write monetary figures in JPY.")
	writeExtensionFile(t, teamDir, NodeTypesSubdir, "report.md", "Include a rollback plan section.")

	got := ResolveNodeTypeContext(Roots{UserDir: userDir, TeamDir: teamDir}, "report")
	def := defaultNodeTypeContent("report")

	iDef := strings.Index(got, def)
	iUser := strings.Index(got, "Always write monetary figures in JPY.")
	iTeam := strings.Index(got, "Include a rollback plan section.")
	if iDef < 0 || iUser < 0 || iTeam < 0 {
		t.Fatalf("expected all three layers present, got %q", got)
	}
	if !(iDef < iUser && iUser < iTeam) {
		t.Errorf("expected order default < user < team, got indices %d,%d,%d", iDef, iUser, iTeam)
	}
}

func TestResolveNodeTypeContext_UnknownTypeWithNoDefaultStillReturnsExtensionContent(t *testing.T) {
	teamDir := t.TempDir()
	writeExtensionFile(t, teamDir, NodeTypesSubdir, "custom_lint.md", "Run the internal linter and attach its output.")

	got := ResolveNodeTypeContext(Roots{TeamDir: teamDir}, "custom_lint")
	if !strings.Contains(got, "Run the internal linter and attach its output.") {
		t.Errorf("got %q", got)
	}
}

func TestResolveNodeTypeContext_MissingDirDoesNotError(t *testing.T) {
	roots := Roots{UserDir: filepath.Join(t.TempDir(), "does-not-exist")}
	got := ResolveNodeTypeContext(roots, "report")
	if got != defaultNodeTypeContent("report") {
		t.Errorf("expected default-only content, got %q", got)
	}
}

func TestResolveSkillContext_MergesUserAndTeam(t *testing.T) {
	teamDir := t.TempDir()
	writeExtensionFile(t, teamDir, SkillsSubdir, "process-ticket.md", "Always CC #eng-standards when opening a release node.")

	got := ResolveSkillContext(Roots{TeamDir: teamDir}, "process-ticket")
	if !strings.Contains(got, "Always CC #eng-standards when opening a release node.") {
		t.Errorf("got %q", got)
	}
}

func TestResolveSkillContext_NoExtensionsIsEmpty(t *testing.T) {
	got := ResolveSkillContext(Roots{}, "process-ticket")
	if got != "" {
		t.Errorf("expected empty string with no configured extensions, got %q", got)
	}
}

func TestResolveReportTemplate_DefaultHasRequiredMarkers(t *testing.T) {
	tmpl := ResolveReportTemplate(Roots{})
	if err := ValidateReportHTML(tmpl); err != nil {
		t.Errorf("default template should satisfy its own validation: %v", err)
	}
	if !strings.Contains(tmpl, `data-report-template="graph-ops-default"`) {
		t.Errorf("expected default template name in %q", tmpl)
	}
}

func TestResolveReportTemplate_TeamOverridesUserOverridesDefault(t *testing.T) {
	userDir, teamDir := t.TempDir(), t.TempDir()
	writeExtensionFile(t, userDir, ReportSubdir, ReportTemplateFile, `<html data-report-template="user-custom" data-report-version="1">`)
	writeExtensionFile(t, teamDir, ReportSubdir, ReportTemplateFile, `<html data-report-template="team-custom" data-report-version="1">`)

	got := ResolveReportTemplate(Roots{UserDir: userDir, TeamDir: teamDir})
	if !strings.Contains(got, `data-report-template="team-custom"`) {
		t.Errorf("expected team override to win, got %q", got)
	}
	if strings.Contains(got, "user-custom") {
		t.Errorf("did not expect user override text, got %q", got)
	}

	userOnly := ResolveReportTemplate(Roots{UserDir: userDir})
	if !strings.Contains(userOnly, `data-report-template="user-custom"`) {
		t.Errorf("expected user override when no team override present, got %q", userOnly)
	}
}

func TestValidateReportHTML_RejectsFreeFormHTML(t *testing.T) {
	err := ValidateReportHTML("<html><body><h1>Report</h1></body></html>")
	if err == nil {
		t.Fatal("expected a validation error for HTML missing the fixed template markers")
	}
}

func TestValidateReportHTML_AcceptsConformingOverride(t *testing.T) {
	html := `<html data-report-template="acme-team" data-report-version="1">
<header data-report-section="header">h</header>
<section data-report-section="summary">s</section>
<section data-report-section="results">r</section>
<footer data-report-section="footer">f</footer>
</html>`
	if err := ValidateReportHTML(html); err != nil {
		t.Errorf("expected conforming override to validate, got %v", err)
	}
}

func TestResolvePlanTemplate_DefaultHasFixedHeadings(t *testing.T) {
	tmpl := ResolvePlanTemplate(Roots{})
	for _, heading := range []string{"# Purpose", "# Steps", "# Impact", "# Risks / Notes"} {
		if !strings.Contains(tmpl, heading) {
			t.Errorf("expected default plan template to contain heading %q, got %q", heading, tmpl)
		}
	}
}

func TestResolvePlanTemplate_TeamOverridesUserOverridesDefault(t *testing.T) {
	userDir, teamDir := t.TempDir(), t.TempDir()
	writeExtensionFile(t, userDir, PlanSubdir, PlanTemplateFile, "# user-custom-plan")
	writeExtensionFile(t, teamDir, PlanSubdir, PlanTemplateFile, "# team-custom-plan")

	got := ResolvePlanTemplate(Roots{UserDir: userDir, TeamDir: teamDir})
	if !strings.Contains(got, "team-custom-plan") {
		t.Errorf("expected team override to win, got %q", got)
	}
	if strings.Contains(got, "user-custom-plan") {
		t.Errorf("did not expect user override text, got %q", got)
	}

	userOnly := ResolvePlanTemplate(Roots{UserDir: userDir})
	if !strings.Contains(userOnly, "user-custom-plan") {
		t.Errorf("expected user override when no team override present, got %q", userOnly)
	}

	teamOnly := ResolvePlanTemplate(Roots{UserDir: t.TempDir(), TeamDir: teamDir})
	if !strings.Contains(teamOnly, "team-custom-plan") {
		t.Errorf("expected team override when no user override present, got %q", teamOnly)
	}

	// With no team/user override, the English plugin default applies.
	defaultOnly := ResolvePlanTemplate(Roots{UserDir: t.TempDir(), TeamDir: t.TempDir()})
	if !strings.HasPrefix(defaultOnly, "# Purpose") {
		t.Errorf("expected the English default when no override is present, got %q", defaultOnly)
	}
}

func TestResolveReviewTemplate_DefaultHasFixedHeadingsAndVerdictWords(t *testing.T) {
	tmpl := ResolveReviewTemplate(Roots{})
	for _, heading := range []string{"# Verdict", "# Findings", "# Rationale", "# Conditions (if Conditionally Approved)"} {
		if !strings.Contains(tmpl, heading) {
			t.Errorf("expected default review template to contain heading %q, got %q", heading, tmpl)
		}
	}
	for _, word := range []string{"Approved", "Conditionally Approved", "Rejected"} {
		if !strings.Contains(tmpl, word) {
			t.Errorf("expected default review template to mention fixed verdict word %q, got %q", word, tmpl)
		}
	}
}

func TestResolveReviewTemplate_TeamOverridesUserOverridesDefault(t *testing.T) {
	userDir, teamDir := t.TempDir(), t.TempDir()
	writeExtensionFile(t, userDir, ReviewSubdir, ReviewTemplateFile, "# user-custom-review")
	writeExtensionFile(t, teamDir, ReviewSubdir, ReviewTemplateFile, "# team-custom-review")

	got := ResolveReviewTemplate(Roots{UserDir: userDir, TeamDir: teamDir})
	if !strings.Contains(got, "team-custom-review") {
		t.Errorf("expected team override to win, got %q", got)
	}
	if strings.Contains(got, "user-custom-review") {
		t.Errorf("did not expect user override text, got %q", got)
	}

	userOnly := ResolveReviewTemplate(Roots{UserDir: userDir})
	if !strings.Contains(userOnly, "user-custom-review") {
		t.Errorf("expected user override when no team override present, got %q", userOnly)
	}

	teamOnly := ResolveReviewTemplate(Roots{UserDir: t.TempDir(), TeamDir: teamDir})
	if !strings.Contains(teamOnly, "team-custom-review") {
		t.Errorf("expected team override when no user override present, got %q", teamOnly)
	}

	defaultOnly := ResolveReviewTemplate(Roots{UserDir: t.TempDir(), TeamDir: t.TempDir()})
	if !strings.HasPrefix(defaultOnly, "# Verdict") {
		t.Errorf("expected the English default when no override is present, got %q", defaultOnly)
	}
}

func TestPlanTemplateTierText_ReflectsOnlyThatTier(t *testing.T) {
	root := t.TempDir()
	if _, ok := PlanTemplateTierText(root); ok {
		t.Fatal("expected no override text before one is written")
	}
	writeExtensionFile(t, root, PlanSubdir, PlanTemplateFile, "# scoped-plan-override")
	text, ok := PlanTemplateTierText(root)
	if !ok || !strings.Contains(text, "scoped-plan-override") {
		t.Errorf("PlanTemplateTierText = (%q, %v), want scoped override text", text, ok)
	}
}

func TestReviewTemplateTierText_ReflectsOnlyThatTier(t *testing.T) {
	root := t.TempDir()
	if _, ok := ReviewTemplateTierText(root); ok {
		t.Fatal("expected no override text before one is written")
	}
	writeExtensionFile(t, root, ReviewSubdir, ReviewTemplateFile, "# scoped-review-override")
	text, ok := ReviewTemplateTierText(root)
	if !ok || !strings.Contains(text, "scoped-review-override") {
		t.Errorf("ReviewTemplateTierText = (%q, %v), want scoped override text", text, ok)
	}
}

func TestResolveRoots_ExplicitOverridesWinOverDefaults(t *testing.T) {
	userOverride := t.TempDir()
	teamOverride := t.TempDir()
	roots := ResolveRoots(userOverride, teamOverride)
	if roots.UserDir != userOverride || roots.TeamDir != teamOverride {
		t.Errorf("got %+v, want overrides to win", roots)
	}
}

// TestLoadWithRoots_TeamWorkflowYAMLMergesReviewGateOnly documents the split
// introduced when workflow.nodes/seed became fixed by the plugin default
// alone: a team workflow.yaml can still add/override a review_gates entry
// (design_review's criteria here), but a workflow.nodes entry it also
// declares is silently ignored -- process-ticket wires a gate like this into
// a ticket's graph itself via an expand-graph patch's gate_ref, it is never
// expected to come from a team-declared graph-template node.
func TestLoadWithRoots_TeamWorkflowYAMLMergesReviewGateOnly(t *testing.T) {
	teamDir := t.TempDir()
	workflowYAML := `
version: 1
review_gates:
  design_review:
    name: "Design System Review"
    criteria: |
      Verify all new UI matches the design-system token list.
    max_iterations: 2
workflow:
  nodes:
    - id: design_review
      type: review_gate
      gate: design_review
      depends_on: [impl]
      loop_back_to: impl
`
	if err := os.WriteFile(filepath.Join(teamDir, teamConfigFile), []byte(workflowYAML), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cat, err := LoadWithRoots("", teamDir, "")
	if err != nil {
		t.Fatalf("LoadWithRoots: %v", err)
	}
	gate, ok := cat.EnabledReviewGates()["design_review"]
	if !ok {
		t.Fatal("expected design_review gate in merged catalog")
	}
	if !strings.Contains(gate.Criteria, "design-system token list") {
		t.Errorf("criteria = %q", gate.Criteria)
	}

	for _, n := range cat.EnabledNodes() {
		if n.ID == "design_review" {
			t.Fatalf("team workflow.nodes entries are no longer merged in; found unexpected node %+v", n)
		}
	}
}

// (b) A nodeType/skillName containing path traversal or path separators
// must never escape the intended extensions/<subdir>/ directory. nodeType
// can originate from a team-shared workflow.yaml's custom node "type"
// field, so it is untrusted input -- see Security Review node-47c93a46,
// non-blocking finding #2.
func TestResolveNodeTypeContext_PathTraversalNodeTypeDoesNotEscapeExtensionsDir(t *testing.T) {
	teamDir := t.TempDir()

	// A file placed one level above the intended node-types directory --
	// escaping to it would prove the traversal worked.
	secretPath := filepath.Join(teamDir, "secret.md")
	if err := os.WriteFile(secretPath, []byte("TOP SECRET"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	for _, malicious := range []string{
		"../../../../../../secret",
		"../secret",
		"foo/../../secret",
		"/etc/passwd",
		"a/b",
	} {
		got := ResolveNodeTypeContext(Roots{TeamDir: teamDir}, malicious)
		if strings.Contains(got, "TOP SECRET") {
			t.Errorf("nodeType %q escaped the extensions dir and read %q: got %q", malicious, secretPath, got)
		}
	}
}

func TestResolveSkillContext_PathTraversalSkillNameDoesNotEscapeExtensionsDir(t *testing.T) {
	userDir := t.TempDir()
	secretPath := filepath.Join(userDir, "secret.md")
	if err := os.WriteFile(secretPath, []byte("TOP SECRET"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got := ResolveSkillContext(Roots{UserDir: userDir}, "../secret")
	if strings.Contains(got, "TOP SECRET") {
		t.Errorf("skillName traversal escaped the extensions dir: got %q", got)
	}
}

func TestIsSafeExtensionName(t *testing.T) {
	cases := map[string]bool{
		"review.md":        true,
		"custom_lint.md":   true,
		"":                 false,
		".":                false,
		"..":               false,
		"../review.md":     false,
		"a/b.md":           false,
		`a\b.md`:           false,
		"../../etc/passwd": false,
	}
	for name, want := range cases {
		if got := isSafeExtensionName(name); got != want {
			t.Errorf("isSafeExtensionName(%q) = %v, want %v", name, got, want)
		}
	}
}

// A legitimate nodeType/skillName (no traversal) must still work after the
// sanitization -- the fix must not break the normal case.
func TestResolveNodeTypeContext_NormalNodeTypeStillWorksAfterSanitization(t *testing.T) {
	teamDir := t.TempDir()
	writeExtensionFile(t, teamDir, NodeTypesSubdir, "custom_lint.md", "Run eslint --max-warnings=0.")

	got := ResolveNodeTypeContext(Roots{TeamDir: teamDir}, "custom_lint")
	if !strings.Contains(got, "Run eslint --max-warnings=0.") {
		t.Errorf("expected legitimate extension content, got %q", got)
	}
}
