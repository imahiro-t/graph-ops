package config

import (
	"testing"
)

// TestLoadLocale_JaReturnsExpectedTranslations checks a representative
// sample of the embedded Japanese locale's Nodes/ReviewGates -- a full
// element-by-element diff against defaults/locales/ja.yaml would just
// duplicate that file; the point here is that LoadLocale actually reads and
// parses the embedded file into the expected shape.
func TestLoadLocale_JaReturnsExpectedTranslations(t *testing.T) {
	locale, ok, err := LoadLocale("ja")
	if err != nil {
		t.Fatalf("LoadLocale: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true for a known locale code")
	}
	if got := locale.Nodes["impl"]; got != "実装" {
		t.Errorf("nodes[impl] = %q, want 実装", got)
	}
	if got := locale.Nodes["report"]; got == "" {
		t.Errorf("nodes[report] should be non-empty")
	}
	if got := locale.ReviewGates["code_review"]; got != "コードレビュー" {
		t.Errorf("review_gates[code_review] = %q, want コードレビュー", got)
	}
	if len(locale.Nodes) != 12 {
		t.Errorf("expected 12 translated nodes, got %d", len(locale.Nodes))
	}
	if len(locale.ReviewGates) != 6 {
		t.Errorf("expected 6 translated review gates, got %d", len(locale.ReviewGates))
	}
}

func TestLoadLocale_UnknownCodeReturnsNotOK(t *testing.T) {
	locale, ok, err := LoadLocale("xx-unsupported")
	if err != nil {
		t.Fatalf("LoadLocale: %v", err)
	}
	if ok {
		t.Fatal("expected ok=false for an unknown locale code")
	}
	if locale.Nodes != nil || locale.ReviewGates != nil {
		t.Errorf("expected a zero Locale for an unknown code, got %+v", locale)
	}
}

func TestLoadLocale_EmptyCodeReturnsNotOK(t *testing.T) {
	_, ok, err := LoadLocale("")
	if err != nil {
		t.Fatalf("LoadLocale: %v", err)
	}
	if ok {
		t.Fatal("expected ok=false for an empty locale code")
	}
}

func TestSupportedLocales_IncludesJa(t *testing.T) {
	codes := SupportedLocales()
	found := false
	for _, c := range codes {
		if c == "ja" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected \"ja\" in SupportedLocales(), got %v", codes)
	}
}

// TestApplyLocale_OnlyReplacesNameFields checks that ApplyLocale leaves
// every other NodeDef/ReviewGateDef field untouched and does not mutate its
// input doc.
func TestApplyLocale_OnlyReplacesNameFields(t *testing.T) {
	doc := Document{
		Version: 1,
		Workflow: WorkflowDef{
			Seed: []string{"plan"},
			Nodes: []NodeDef{
				{ID: "plan", Name: "Plan Creation", Type: "plan"},
				{ID: "impl", Name: "Implementation", Type: "implementation", DependsOn: []string{"plan"}, IsManual: true, Enabled: boolPtr(true)},
				{ID: "custom_step", Name: "Custom Step", Type: "documentation"},
			},
		},
		ReviewGates: map[string]ReviewGateDef{
			"code_review": {Name: "Code Review", Criteria: "quality", MaxIterations: intPtr(3), Enabled: boolPtr(true)},
			"custom_gate": {Name: "Custom Gate", Criteria: "custom"},
		},
	}
	locale := Locale{
		Nodes:       map[string]string{"plan": "計画作成", "impl": "実装"},
		ReviewGates: map[string]string{"code_review": "コードレビュー"},
	}

	out := ApplyLocale(doc, locale)

	// Translated ids get the new Name, other fields untouched.
	byID := func(nodes []NodeDef, id string) NodeDef {
		for _, n := range nodes {
			if n.ID == id {
				return n
			}
		}
		t.Fatalf("node %q not found", id)
		return NodeDef{}
	}
	plan := byID(out.Workflow.Nodes, "plan")
	if plan.Name != "計画作成" {
		t.Errorf("plan.Name = %q, want 計画作成", plan.Name)
	}
	impl := byID(out.Workflow.Nodes, "impl")
	if impl.Name != "実装" {
		t.Errorf("impl.Name = %q, want 実装", impl.Name)
	}
	if !impl.IsManual || impl.Enabled == nil || !*impl.Enabled || len(impl.DependsOn) != 1 || impl.DependsOn[0] != "plan" {
		t.Errorf("impl's non-Name fields must be untouched, got %+v", impl)
	}
	// An id absent from the locale keeps its original (English) Name.
	custom := byID(out.Workflow.Nodes, "custom_step")
	if custom.Name != "Custom Step" {
		t.Errorf("custom_step.Name should be untouched, got %q", custom.Name)
	}

	gate := out.ReviewGates["code_review"]
	if gate.Name != "コードレビュー" {
		t.Errorf("code_review.Name = %q, want コードレビュー", gate.Name)
	}
	if gate.Criteria != "quality" || gate.MaxIterations == nil || *gate.MaxIterations != 3 || gate.Enabled == nil || !*gate.Enabled {
		t.Errorf("code_review's non-Name fields must be untouched, got %+v", gate)
	}
	customGate := out.ReviewGates["custom_gate"]
	if customGate.Name != "Custom Gate" {
		t.Errorf("custom_gate.Name should be untouched, got %q", customGate.Name)
	}

	// The original doc must not be mutated.
	if doc.Workflow.Nodes[0].Name != "Plan Creation" {
		t.Errorf("ApplyLocale must not mutate its input doc, got %q", doc.Workflow.Nodes[0].Name)
	}
	if doc.ReviewGates["code_review"].Name != "Code Review" {
		t.Errorf("ApplyLocale must not mutate its input doc's review gates, got %q", doc.ReviewGates["code_review"].Name)
	}
}

func TestResolveLanguage_ExplicitOverrideAlwaysWins(t *testing.T) {
	userDoc := Document{Language: "ja"}
	teamDoc := Document{Language: "en"}
	if got := ResolveLanguage("fr", userDoc, teamDoc); got != "fr" {
		t.Errorf("expected explicit override to win, got %q", got)
	}
}

func TestResolveLanguage_LaterTierWinsOverEarlier(t *testing.T) {
	userDoc := Document{Language: "ja"}
	teamDoc := Document{Language: "en"}
	if got := ResolveLanguage("", userDoc, teamDoc); got != "en" {
		t.Errorf("expected teamDoc (later argument) to win over userDoc, got %q", got)
	}
	if got := ResolveLanguage("", userDoc, Document{}); got != "ja" {
		t.Errorf("expected userDoc to win when teamDoc is unset, got %q", got)
	}
}

func TestResolveLanguage_NothingSetReturnsEmpty(t *testing.T) {
	if got := ResolveLanguage("", Document{}, Document{}); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestLocalizedDefault_KnownCodeAppliesLocale(t *testing.T) {
	doc, err := LocalizedDefault("ja")
	if err != nil {
		t.Fatalf("LocalizedDefault: %v", err)
	}
	var implName string
	for _, n := range doc.Workflow.Nodes {
		if n.ID == "impl" {
			implName = n.Name
		}
	}
	if implName != "実装" {
		t.Errorf("impl node name = %q, want 実装", implName)
	}
	if doc.ReviewGates["code_review"].Name != "コードレビュー" {
		t.Errorf("code_review name = %q, want コードレビュー", doc.ReviewGates["code_review"].Name)
	}
}

func TestLocalizedDefault_EmptyOrUnknownMatchesRawDefault(t *testing.T) {
	def, err := DefaultDocument()
	if err != nil {
		t.Fatalf("DefaultDocument: %v", err)
	}

	empty, err := LocalizedDefault("")
	if err != nil {
		t.Fatalf("LocalizedDefault(\"\"): %v", err)
	}
	if len(empty.Workflow.Nodes) != len(def.Workflow.Nodes) {
		t.Fatalf("node count mismatch")
	}
	for i, n := range empty.Workflow.Nodes {
		if n.Name != def.Workflow.Nodes[i].Name {
			t.Errorf("node %q name = %q, want unmodified default %q", n.ID, n.Name, def.Workflow.Nodes[i].Name)
		}
	}

	unknown, err := LocalizedDefault("xx-unsupported")
	if err != nil {
		t.Fatalf("LocalizedDefault(unknown): %v", err)
	}
	for i, n := range unknown.Workflow.Nodes {
		if n.Name != def.Workflow.Nodes[i].Name {
			t.Errorf("unknown-locale node %q name = %q, want unmodified default %q", n.ID, n.Name, def.Workflow.Nodes[i].Name)
		}
	}
}
