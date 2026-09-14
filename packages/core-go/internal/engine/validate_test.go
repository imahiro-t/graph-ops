package engine

import (
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/config"
)

func TestValidateCatalog_DefaultCatalogIsValid(t *testing.T) {
	if err := ValidateCatalog(baseCatalog(t)); err != nil {
		t.Fatalf("ValidateCatalog(default catalog): %v", err)
	}
}

func TestValidateCatalog_RejectsCycle(t *testing.T) {
	catalog := config.Catalog{
		Nodes: []config.NodeDef{
			{ID: "a", Type: "implementation", DependsOn: []string{"b"}},
			{ID: "b", Type: "implementation", DependsOn: []string{"a"}},
		},
	}
	err := ValidateCatalog(catalog)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected a cycle error, got %v", err)
	}
}

func TestValidateCatalog_RejectsUnknownDependsOn(t *testing.T) {
	catalog := config.Catalog{
		Nodes: []config.NodeDef{
			{ID: "a", Type: "implementation", DependsOn: []string{"missing"}},
		},
	}
	err := ValidateCatalog(catalog)
	if err == nil || !strings.Contains(err.Error(), "unknown node") {
		t.Fatalf("expected an unknown-node error, got %v", err)
	}
}

func TestValidateCatalog_RejectsUnknownReviewGateReference(t *testing.T) {
	catalog := config.Catalog{
		Nodes: []config.NodeDef{
			{ID: "a", Type: "review_gate", Gate: "does-not-exist"},
		},
	}
	err := ValidateCatalog(catalog)
	if err == nil || !strings.Contains(err.Error(), "unknown review gate") {
		t.Fatalf("expected an unknown-review-gate error, got %v", err)
	}
}

func TestValidateCatalog_RejectsDuplicateID(t *testing.T) {
	catalog := config.Catalog{
		Nodes: []config.NodeDef{
			{ID: "a", Type: "implementation"},
			{ID: "a", Type: "review"},
		},
	}
	err := ValidateCatalog(catalog)
	if err == nil || !strings.Contains(err.Error(), "duplicate node id") {
		t.Fatalf("expected a duplicate-id error, got %v", err)
	}
}
