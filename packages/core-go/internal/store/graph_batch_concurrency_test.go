// Package store_test for the same reason as claim_concurrency_mysql_test.go:
// these tests drive the engine, which imports the store, and living in
// internal/store is what gets them run against a real MySQL by
// dev/mysql/test.sh.
package store_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/graph-ops/core-go/internal/config"
	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/store"
)

// DFLT-00328: several sessions starting the same ticket at once used to race
// through "no nodes yet -> create the seed" and "not expanded yet -> create
// the rest" with nothing holding the check true, so each could create its
// own copy of the graph. These tests start that race for real -- every
// caller has its own repository (its own connection pool, the way separate
// graph-engine processes have), released together -- and check that the
// graph exists exactly once.

const graphRaceCallers = 8

// graphRaceRepos opens n repositories on one database.
type graphRaceRepos func(t *testing.T, n int) []store.GraphRepository

func sqliteGraphRaceRepos(t *testing.T, n int) []store.GraphRepository {
	path := filepath.Join(t.TempDir(), "race.db")
	repos := make([]store.GraphRepository, n)
	for i := range repos {
		repo, err := store.NewSQLiteRepository(path)
		if err != nil {
			t.Fatalf("NewSQLiteRepository: %v", err)
		}
		if err := repo.Init(); err != nil {
			t.Fatalf("Init: %v", err)
		}
		repos[i] = repo
	}
	return repos
}

func mysqlGraphRaceRepos(t *testing.T, n int) []store.GraphRepository {
	repos := make([]store.GraphRepository, n)
	for i := range repos {
		repos[i] = mysqlRepoForClaimTest(t) // skips unless GRAPH_TEST_MYSQL_HOST is set
	}
	return repos
}

// race runs fn(i) for every caller at the same moment and returns their
// errors, indexed like the callers.
func race(n int, fn func(i int) error) []error {
	errs := make([]error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = fn(i)
		}()
	}
	close(start)
	wg.Wait()
	return errs
}

// checkGraphOnce fails unless the ticket's graph has no two nodes with the
// same config_id and no two edges between the same nodes with the same
// condition, and has exactly wantNodes nodes and wantEdges edges, with
// node IDs -01 .. -NN in listing order.
func checkGraphOnce(t *testing.T, repo store.GraphRepository, ticketID string, wantNodes, wantEdges int) []domain.GraphNode {
	t.Helper()
	nodes, err := repo.ListNodesByTicket(ticketID)
	if err != nil {
		t.Fatalf("ListNodesByTicket: %v", err)
	}
	edges, err := repo.ListEdgesByTicket(ticketID)
	if err != nil {
		t.Fatalf("ListEdgesByTicket: %v", err)
	}
	configIDs := map[string]bool{}
	for i, n := range nodes {
		if n.ConfigID == nil {
			t.Fatalf("node %s has no config_id", n.ID)
		}
		if configIDs[*n.ConfigID] {
			t.Fatalf("config_id %s was created twice", *n.ConfigID)
		}
		configIDs[*n.ConfigID] = true
		if want := fmt.Sprintf("%s-%02d", ticketID, i+1); n.ID != want {
			t.Fatalf("node %d is %s, want %s", i, n.ID, want)
		}
	}
	edgeKeys := map[string]bool{}
	for _, e := range edges {
		key := e.FromNodeID + ">" + e.ToNodeID + ":" + string(e.Condition)
		if edgeKeys[key] {
			t.Fatalf("edge %s was created twice", key)
		}
		edgeKeys[key] = true
	}
	if len(nodes) != wantNodes || len(edges) != wantEdges {
		t.Fatalf("the graph has %d nodes and %d edges, want %d and %d", len(nodes), len(edges), wantNodes, wantEdges)
	}
	return nodes
}

// completeSeed completes plan and plan_review, whoever claimed them.
func completeSeed(t *testing.T, eng *engine.GraphEngine, repo store.GraphRepository, ticketID string, catalog config.Catalog) {
	t.Helper()
	for _, configID := range []string{"plan", "plan_review"} {
		if _, err := eng.GetExecutableNodes(ticketID, catalog); err != nil {
			t.Fatalf("GetExecutableNodes: %v", err)
		}
		nodes, err := repo.ListNodesByTicket(ticketID)
		if err != nil {
			t.Fatalf("ListNodesByTicket: %v", err)
		}
		var id string
		for _, n := range nodes {
			if n.ConfigID != nil && *n.ConfigID == configID {
				id = n.ID
			}
		}
		if id == "" {
			t.Fatalf("no %s node", configID)
		}
		if _, err := eng.CompleteNode(id, true, nil); err != nil {
			t.Fatalf("CompleteNode(%s): %v", configID, err)
		}
	}
}

func testGraphCreationRace(t *testing.T, open graphRaceRepos) {
	repos := open(t, graphRaceCallers)
	engines := make([]*engine.GraphEngine, len(repos))
	for i, r := range repos {
		engines[i] = engine.New(r)
	}
	repo, eng := repos[0], engines[0]
	catalog, err := config.LoadWithRoots(t.TempDir(), t.TempDir(), "")
	if err != nil {
		t.Fatalf("config.LoadWithRoots: %v", err)
	}
	project, err := repo.CreateProject("Graph Race", "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// The reference: the same graph built by one session alone.
	ref, err := eng.CreateTicket(project.ID, "reference", "")
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if _, err := eng.GetExecutableNodes(ref.ID, catalog); err != nil {
		t.Fatalf("GetExecutableNodes(reference): %v", err)
	}
	seedNodes, _ := repo.ListNodesByTicket(ref.ID)
	seedEdges, _ := repo.ListEdgesByTicket(ref.ID)
	completeSeed(t, eng, repo, ref.ID, catalog)
	if err := eng.ExpandGraph(ref.ID, catalog, nil); err != nil {
		t.Fatalf("ExpandGraph(reference): %v", err)
	}
	fullNodes, _ := repo.ListNodesByTicket(ref.ID)
	fullEdges, _ := repo.ListEdgesByTicket(ref.ID)
	if len(seedNodes) == 0 || len(fullNodes) <= len(seedNodes) {
		t.Fatalf("reference graph: %d seed nodes, %d in all", len(seedNodes), len(fullNodes))
	}

	ticket, err := eng.CreateTicket(project.ID, "race", "")
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	// Everybody's first get-executable at once: one seed, and nobody fails.
	handedOut := map[string]int{}
	var mu sync.Mutex
	errs := race(len(engines), func(i int) error {
		nodes, err := engines[i].GetExecutableNodes(ticket.ID, catalog)
		mu.Lock()
		defer mu.Unlock()
		for _, n := range nodes {
			handedOut[n.ID]++
		}
		return err
	})
	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: GetExecutableNodes: %v", i, err)
		}
	}
	checkGraphOnce(t, repo, ticket.ID, len(seedNodes), len(seedEdges))
	for id, n := range handedOut {
		if n > 1 {
			t.Fatalf("node %s was handed out %d times", id, n)
		}
	}

	// Everybody's expand-graph at once: one expansion, everyone else is told
	// the graph has already been expanded.
	completeSeed(t, eng, repo, ticket.ID, catalog)
	errs = race(len(engines), func(i int) error {
		return engines[i].ExpandGraph(ticket.ID, catalog, nil)
	})
	won := 0
	for i, err := range errs {
		switch {
		case err == nil:
			won++
		case strings.Contains(err.Error(), "graph has already been expanded"):
		default:
			t.Fatalf("caller %d: ExpandGraph: %v", i, err)
		}
	}
	if won != 1 {
		t.Fatalf("%d callers expanded the graph, want exactly 1", won)
	}
	checkGraphOnce(t, repo, ticket.ID, len(fullNodes), len(fullEdges))
	got, err := repo.GetTicket(ticket.ID)
	if err != nil || got == nil || got.GraphExpandedAt == nil {
		t.Fatalf("graph_expanded_at is not set: %+v (%v)", got, err)
	}
}

func TestGraphCreation_ConcurrentSeedAndExpand_SQLite(t *testing.T) {
	testGraphCreationRace(t, sqliteGraphRaceRepos)
}

func TestGraphCreation_ConcurrentSeedAndExpand_MySQL(t *testing.T) {
	testGraphCreationRace(t, mysqlGraphRaceRepos)
}
