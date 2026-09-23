package config

import (
	"strings"
	"testing"
)

func boolPtr(b bool) *bool { return &b }
func intPtr(i int) *int    { return &i }

func TestMergeReviewGates_NewGateIsAdded(t *testing.T) {
	base := map[string]ReviewGateDef{
		"code_review": {Name: "Code", Criteria: "quality"},
	}
	overlay := map[string]ReviewGateDef{
		"perf_review": {Name: "Perf", Criteria: "N+1"},
	}
	merged := mergeReviewGates(base, overlay)
	if len(merged) != 2 {
		t.Fatalf("expected 2 gates, got %d", len(merged))
	}
	if merged["perf_review"].Criteria != "N+1" {
		t.Errorf("perf_review criteria = %q", merged["perf_review"].Criteria)
	}
	if merged["code_review"].Criteria != "quality" {
		t.Errorf("base gate should be untouched, got %q", merged["code_review"].Criteria)
	}
}

func TestMergeReviewGates_CriteriaReplaces(t *testing.T) {
	base := map[string]ReviewGateDef{
		"security_review": {Name: "Security", Criteria: "old text", Enabled: boolPtr(true)},
	}
	overlay := map[string]ReviewGateDef{
		"security_review": {Criteria: "new text"},
	}
	merged := mergeReviewGates(base, overlay)
	if merged["security_review"].Criteria != "new text" {
		t.Errorf("criteria = %q, want full replace", merged["security_review"].Criteria)
	}
	if e := merged["security_review"].Enabled; e == nil || !*e {
		t.Errorf("enabled should be inherited when overlay omits it")
	}
}

func TestMergeReviewGates_AdditionalCriteriaAppends(t *testing.T) {
	base := map[string]ReviewGateDef{
		"security_review": {Criteria: "base text"},
	}
	overlay := map[string]ReviewGateDef{
		"security_review": {AdditionalCriteria: "extra text"},
	}
	merged := mergeReviewGates(base, overlay)
	got := merged["security_review"].Criteria
	if !strings.Contains(got, "base text") || !strings.Contains(got, "extra text") {
		t.Errorf("criteria = %q, want both base and additional text present", got)
	}
}

func TestMergeReviewGates_DisableViaEnabled(t *testing.T) {
	base := map[string]ReviewGateDef{
		"qa_review": {Name: "QA", Criteria: "x"},
	}
	overlay := map[string]ReviewGateDef{
		"qa_review": {Enabled: boolPtr(false)},
	}
	merged := mergeReviewGates(base, overlay)
	cat := Catalog{ReviewGates: merged}
	if _, ok := cat.EnabledReviewGates()["qa_review"]; ok {
		t.Errorf("qa_review should be filtered out once disabled")
	}
}

func TestLoad_DefaultOnlyProducesFullCatalog(t *testing.T) {
	// Empty user/team roots keep this assertion on the plugin defaults alone;
	// Load would resolve the user tier to $HOME/.graph-ops and merge whatever
	// the developer has configured there.
	cat, err := LoadWithRoots(t.TempDir(), t.TempDir(), "")
	if err != nil {
		t.Fatalf("LoadWithRoots: %v", err)
	}
	// code/qa/security/non_functional (the standard implementation review
	// cluster) plus investigation_review and accessibility_review (each
	// used only via an expand-graph patch when process-ticket judges a
	// ticket actually needs them, not part of the default nodes list below).
	if len(cat.EnabledReviewGates()) != 6 {
		t.Errorf("expected 6 default review gates, got %d", len(cat.EnabledReviewGates()))
	}
	if len(cat.EnabledNodes()) != 16 {
		t.Errorf("expected 16 default nodes (including plan_approval/release_approval -- DFLT-00016), got %d", len(cat.EnabledNodes()))
	}
	if len(cat.Seed) != 2 || cat.Seed[0] != "plan" || cat.Seed[1] != "plan_review" {
		t.Errorf("expected default seed [plan, plan_review], got %v", cat.Seed)
	}
}

func TestMerge_WorkflowAlwaysComesFromFirstDoc(t *testing.T) {
	base := Document{Workflow: WorkflowDef{
		Seed:  []string{"plan", "plan_review"},
		Nodes: []NodeDef{{ID: "plan", Type: "plan"}},
	}}
	cat := Merge(base, Document{})
	if len(cat.Seed) != 2 || cat.Seed[0] != "plan" || cat.Seed[1] != "plan_review" {
		t.Errorf("expected seed from the first (plugin default) doc, got %v", cat.Seed)
	}
	if len(cat.Nodes) != 1 || cat.Nodes[0].ID != "plan" {
		t.Errorf("expected nodes from the first (plugin default) doc, got %v", cat.Nodes)
	}
}

func TestMerge_WorkflowIgnoresLaterDocs(t *testing.T) {
	base := Document{Workflow: WorkflowDef{
		Seed:  []string{"plan", "plan_review"},
		Nodes: []NodeDef{{ID: "plan", Type: "plan"}},
	}}
	overlay := Document{Workflow: WorkflowDef{
		Seed:  []string{"plan"},
		Nodes: []NodeDef{{ID: "extra", Type: "implementation"}},
	}}
	cat := Merge(base, overlay)
	if len(cat.Seed) != 2 || cat.Seed[0] != "plan" || cat.Seed[1] != "plan_review" {
		t.Errorf("a later doc's seed must be ignored entirely, got %v", cat.Seed)
	}
	if len(cat.Nodes) != 1 || cat.Nodes[0].ID != "plan" {
		t.Errorf("a later doc's nodes must be ignored entirely, got %v", cat.Nodes)
	}
}

// TestMerge_LanguageLastNonEmptyWins covers Merge's "team > user > default"
// Language precedence (execution plan, section 1.1/1.3): unlike
// Workflow.Nodes/Seed (always docs[0]) and unlike ReviewGates (per-key
// merge), Language is a scalar that the last document to set it wins,
// scanning every document including the plugin default (which in practice
// never sets one -- see DefaultDocument).
func TestMerge_LanguageLastNonEmptyWins(t *testing.T) {
	def := Document{}
	user := Document{Language: "ja"}
	team := Document{Language: "en"}

	if got := Merge(def, user, team).Language; got != "en" {
		t.Errorf("team should win over user, got %q", got)
	}
	if got := Merge(def, user, Document{}).Language; got != "ja" {
		t.Errorf("user should win when team is unset, got %q", got)
	}
	if got := Merge(def, Document{}, Document{}).Language; got != "" {
		t.Errorf("expected empty Language when no tier sets one, got %q", got)
	}
	if got := Merge(def, Document{}, team).Language; got != "en" {
		t.Errorf("team should win even when user is unset, got %q", got)
	}
}
