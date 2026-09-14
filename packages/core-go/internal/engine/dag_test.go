package engine

import (
	"testing"

	"github.com/graph-ops/core-go/internal/config"
)

// baseCatalog loads the plugin default catalog alone. The user and team roots
// are pointed at empty temp dirs so the result never picks up the developer's
// own ~/.graph-ops/config.yaml (config.Load would resolve the user tier to
// $HOME, making every assertion on node counts environment-dependent).
func baseCatalog(t *testing.T) config.Catalog {
	t.Helper()
	cat, err := config.LoadWithRoots(t.TempDir(), t.TempDir(), t.TempDir(), "")
	if err != nil {
		t.Fatalf("config.LoadWithRoots: %v", err)
	}
	return cat
}

func TestBuildPlan_DefaultCatalogIsValid(t *testing.T) {
	planned, err := buildPlan(baseCatalog(t), nil)
	if err != nil {
		t.Fatalf("buildPlan: %v", err)
	}
	if len(planned) != 16 {
		t.Fatalf("expected 16 planned nodes, got %d", len(planned))
	}
	var gateNodes int
	for _, n := range planned {
		if n.Type == "review_gate" {
			gateNodes++
			if n.GateID == nil || n.Criteria == nil || *n.Criteria == "" {
				t.Errorf("review_gate node %q missing resolved gate/criteria: %+v", n.ConfigID, n)
			}
		}
	}
	if gateNodes != 4 {
		t.Errorf("expected 4 review_gate nodes, got %d", gateNodes)
	}
}

// TestBuildPlan_LocalizedCatalogProducesLocalizedNames confirms a locale
// applied to the plugin default (config.LocalizedDefault -- see
// DFLT-00051's execution plan, section 3.1) flows all the way through
// buildPlanFromNodeDefs' planned node Names, for both a fixed-skeleton node
// (impl, report) and a review_gate node (whose Name comes from the gate's
// own Name, not the NodeDef's -- see dag.go's inline-gate/gate-id
// resolution).
func TestBuildPlan_LocalizedCatalogProducesLocalizedNames(t *testing.T) {
	def, err := config.LocalizedDefault("ja")
	if err != nil {
		t.Fatalf("config.LocalizedDefault: %v", err)
	}
	cat := config.Merge(def)

	planned, err := buildPlan(cat, nil)
	if err != nil {
		t.Fatalf("buildPlan: %v", err)
	}

	byConfigID := make(map[string]plannedNode, len(planned))
	for _, n := range planned {
		byConfigID[n.ConfigID] = n
	}

	if got := byConfigID["impl"].Name; got != "実装" {
		t.Errorf("impl planned node Name = %q, want 実装", got)
	}
	if got := byConfigID["report"].Name; got == "" || got == "Test Report Creation (HTML/Captures)" {
		t.Errorf("report planned node Name = %q, want a localized (Japanese) name", got)
	}
	if got := byConfigID["code_review"].Name; got != "コードレビュー" {
		t.Errorf("code_review planned node Name = %q, want コードレビュー", got)
	}
}

func TestBuildPlan_PatchWithExistingGateRef(t *testing.T) {
	patch := &Patch{ExtraNodes: []ExtraNode{
		{ID: "extra_security", Type: "review_gate", GateRef: "security_review", DependsOn: []string{"impl"}, LoopBackTo: "impl"},
	}}
	planned, err := buildPlan(baseCatalog(t), patch)
	if err != nil {
		t.Fatalf("buildPlan: %v", err)
	}
	if len(planned) != 17 {
		t.Fatalf("expected 17 planned nodes, got %d", len(planned))
	}
}

func TestBuildPlan_PatchWithInlineGate(t *testing.T) {
	patch := &Patch{ExtraNodes: []ExtraNode{
		{
			ID:         "pci_review",
			Type:       "review_gate",
			Gate:       &ExtraGateDef{Name: "PCI-DSS Review", Criteria: "Verify that card data is not persisted."},
			DependsOn:  []string{"impl"},
			LoopBackTo: "impl",
		},
	}}
	planned, err := buildPlan(baseCatalog(t), patch)
	if err != nil {
		t.Fatalf("buildPlan: %v", err)
	}
	var found bool
	for _, n := range planned {
		if n.ConfigID == "pci_review" {
			found = true
			if n.GateID == nil || *n.GateID != "pci_review" {
				t.Errorf("expected synthesized gate id 'pci_review', got %+v", n.GateID)
			}
			if n.Criteria == nil || *n.Criteria != "Verify that card data is not persisted." {
				t.Errorf("unexpected criteria: %+v", n.Criteria)
			}
			if n.Name != "PCI-DSS Review" {
				t.Errorf("expected name inherited from inline gate, got %q", n.Name)
			}
		}
	}
	if !found {
		t.Fatalf("patched node not found in plan")
	}
}

func TestBuildPlan_RejectsUnknownGateRef(t *testing.T) {
	patch := &Patch{ExtraNodes: []ExtraNode{
		{ID: "x", Type: "review_gate", GateRef: "does_not_exist", DependsOn: []string{"impl"}},
	}}
	if _, err := buildPlan(baseCatalog(t), patch); err == nil {
		t.Fatal("expected error for unknown gate reference")
	}
}

func TestBuildPlan_RejectsDuplicateID(t *testing.T) {
	patch := &Patch{ExtraNodes: []ExtraNode{
		{ID: "impl", Type: "custom"},
	}}
	if _, err := buildPlan(baseCatalog(t), patch); err == nil {
		t.Fatal("expected error for duplicate node id")
	}
}

func TestBuildPlan_RejectsDanglingDependsOn(t *testing.T) {
	patch := &Patch{ExtraNodes: []ExtraNode{
		{ID: "x", Type: "custom", DependsOn: []string{"does_not_exist"}},
	}}
	if _, err := buildPlan(baseCatalog(t), patch); err == nil {
		t.Fatal("expected error for dangling depends_on")
	}
}

func TestBuildPlan_RejectsDanglingLoopBackTo(t *testing.T) {
	patch := &Patch{ExtraNodes: []ExtraNode{
		{ID: "x", Type: "custom", DependsOn: []string{"impl"}, LoopBackTo: "does_not_exist"},
	}}
	if _, err := buildPlan(baseCatalog(t), patch); err == nil {
		t.Fatal("expected error for dangling loop_back_to")
	}
}

func TestBuildPlan_RejectsCycle(t *testing.T) {
	patch := &Patch{ExtraNodes: []ExtraNode{
		{ID: "a", Type: "custom", DependsOn: []string{"b"}},
		{ID: "b", Type: "custom", DependsOn: []string{"a"}},
	}}
	if _, err := buildPlan(baseCatalog(t), patch); err == nil {
		t.Fatal("expected error for cyclic depends_on graph")
	}
}

func TestBuildPlan_LoopBackDoesNotCountAsCycle(t *testing.T) {
	// The default catalog is full of loop_back_to edges pointing "backward";
	// this must NOT be flagged as a depends_on cycle.
	if _, err := buildPlan(baseCatalog(t), nil); err != nil {
		t.Fatalf("default catalog's loop_back_to edges should not trigger cycle detection: %v", err)
	}
}

// TestBuildPlanFromNodeDefs_ExternalIDDependencyIsNotACycle guards against a
// real bug found while wiring ExpandGraph's patch-only mode: a node whose
// only dependency is an externalID (e.g. the seed's "plan_review", already
// persisted in an earlier phase) has nothing in THIS batch that ever
// resolves that dependency, so counting it toward in-degree left the node
// permanently unreachable by Kahn's algorithm -- falsely reported as a
// cycle, even though a single node depending on one external id obviously
// isn't one.
func TestBuildPlanFromNodeDefs_ExternalIDDependencyIsNotACycle(t *testing.T) {
	patch := &Patch{ExtraNodes: []ExtraNode{
		{ID: "investigation", Type: "investigation", DependsOn: []string{"plan_review"}},
	}}
	externalIDs := map[string]bool{"plan_review": true}

	planned, err := buildPlanFromNodeDefs(nil, nil, patch, externalIDs)
	if err != nil {
		t.Fatalf("expected a dependency on an external id to be valid and acyclic, got: %v", err)
	}
	if len(planned) != 1 || planned[0].ConfigID != "investigation" {
		t.Fatalf("unexpected plan: %+v", planned)
	}
}

func TestBuildPlanFromNodeDefs_RejectsDependencyOnUnknownExternalID(t *testing.T) {
	patch := &Patch{ExtraNodes: []ExtraNode{
		{ID: "investigation", Type: "investigation", DependsOn: []string{"not_a_real_node"}},
	}}
	if _, err := buildPlanFromNodeDefs(nil, nil, patch, map[string]bool{"plan_review": true}); err == nil {
		t.Fatal("expected an error for a dependency that is neither in this batch nor in externalIDs")
	}
}
