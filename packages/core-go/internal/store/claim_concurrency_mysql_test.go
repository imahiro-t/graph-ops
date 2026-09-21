// This file is package store_test, not package store, on purpose. It drives
// engine.GetExecutableNodes, and the engine imports the store -- an in-package
// test would be an import cycle. The external test package sits outside that
// loop (store_test -> engine -> store) and still lives in internal/store,
// which is one of the three packages dev/mysql/test.sh runs
// (./internal/store/ ./internal/httpserver/ ./cmd/graph-engine/ -- notably not
// ./internal/engine/). Putting it here is what gets it run against a real
// MySQL at all.
package store_test

import (
	"os"
	"strconv"
	"sync"
	"testing"

	"github.com/graph-ops/core-go/internal/config"
	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/store"
)

// mysqlRepoForClaimTest mirrors internal/store's own mysqlTestConfig, which is
// unexported and so out of reach from here. Same environment variables, same
// skip when no server is configured, same secure TLS default.
func mysqlRepoForClaimTest(t *testing.T) *store.MySQLRepository {
	t.Helper()
	host := os.Getenv("GRAPH_TEST_MYSQL_HOST")
	if host == "" {
		t.Skip("GRAPH_TEST_MYSQL_HOST not set; skipping the MySQL claim-concurrency test")
	}
	port := 3306
	if p := os.Getenv("GRAPH_TEST_MYSQL_PORT"); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			t.Fatalf("invalid GRAPH_TEST_MYSQL_PORT %q: %v", p, err)
		}
		port = n
	}
	repo, err := store.NewMySQLRepository(store.Config{
		Backend:        "mysql",
		MySQLHost:      host,
		MySQLPort:      port,
		MySQLDatabase:  os.Getenv("GRAPH_TEST_MYSQL_DATABASE"),
		MySQLUser:      os.Getenv("GRAPH_TEST_MYSQL_USER"),
		MySQLPassword:  os.Getenv("GRAPH_TEST_MYSQL_PASSWORD"),
		MySQLTLSMode:   os.Getenv("GRAPH_TEST_MYSQL_TLS"),
		MySQLTLSCAFile: os.Getenv("GRAPH_TEST_MYSQL_TLS_CA"),
	})
	if err != nil {
		t.Fatalf("NewMySQLRepository: %v", err)
	}
	if err := repo.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return repo
}

// TestGetExecutableNodes_ConcurrentCallsNeverHandOutTheSameNode is CHK-01 at
// the level it actually bites: process-ticket runs several subagents, each
// calling get-executable against the same ticket at the same moment. Before
// the claim became a compare-and-swap, they read one snapshot of the node
// statuses and then wrote to it separately, so more than one could be handed
// the same node -- two agents doing the same piece of work, with whichever
// finished last overwriting the other's result.
//
// Against MySQL specifically, because that is the backend where the calls
// really are concurrent (SQLite serializes statements behind
// SetMaxOpenConns(1), which narrows the window without closing it).
//
// The graph is seeded by one call before the racers start, so what is under
// test is the claim alone. Concurrent first-ever calls would also race to seed
// the graph, which is a separate problem this ticket deliberately leaves alone
// (deduplicating graph creation needs a (ticket_id, config_id) unique
// constraint, and this repository has no migration mechanism to add one to
// existing databases).
func TestGetExecutableNodes_ConcurrentCallsNeverHandOutTheSameNode(t *testing.T) {
	repo := mysqlRepoForClaimTest(t)
	eng := engine.New(repo)
	catalog, err := config.LoadWithRoots(t.TempDir(), t.TempDir(), "")
	if err != nil {
		t.Fatalf("config.LoadWithRoots: %v", err)
	}

	project, err := repo.CreateProject("Claim Race", "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	ticket, err := eng.CreateTicket(project.ID, "claim race", "")
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	// Seed the graph (and claim whatever it hands out) before the race.
	if _, err := eng.GetExecutableNodes(ticket.ID, catalog); err != nil {
		t.Fatalf("seeding GetExecutableNodes: %v", err)
	}

	// rounds*extraNodes has to stay under the 99-node cap a ticket has
	// (CreateNode mints <ticketId>-<seq:02d>), seed graph included.
	const (
		rounds     = 15
		callers    = 4
		extraNodes = 5
	)
	for round := 0; round < rounds; round++ {
		// A handful of independent, prerequisite-free nodes so each round
		// has several nodes up for grabs at once, the way a fan-out stage
		// of the workflow does.
		want := map[string]bool{}
		for i := 0; i < extraNodes; i++ {
			node, err := repo.CreateNode(domain.GraphNode{
				TicketID: ticket.ID, Name: "race", Type: domain.NodeTypeImplementation,
				Status: domain.NodeTODO, MaxIterations: 3,
			})
			if err != nil {
				t.Fatalf("round %d: CreateNode: %v", round, err)
			}
			want[node.ID] = true
		}

		start := make(chan struct{})
		var wg sync.WaitGroup
		handouts := make(chan []domain.GraphNode, callers)
		errs := make(chan error, callers)
		for c := 0; c < callers; c++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				nodes, err := eng.GetExecutableNodes(ticket.ID, catalog)
				if err != nil {
					errs <- err
					return
				}
				handouts <- nodes
			}()
		}
		close(start)
		wg.Wait()
		close(handouts)
		close(errs)

		for err := range errs {
			// Losing a claim is not an error -- it is reported by
			// leaving the node out of that call's result.
			t.Fatalf("round %d: GetExecutableNodes: %v", round, err)
		}
		seen := map[string]int{}
		total := 0
		for nodes := range handouts {
			for _, n := range nodes {
				seen[n.ID]++
				total++
			}
		}
		for id, count := range seen {
			if count > 1 {
				t.Fatalf("round %d: node %s was handed to %d callers at once", round, id, count)
			}
		}
		if total > len(seen) {
			t.Fatalf("round %d: %d handouts for %d distinct nodes", round, total, len(seen))
		}
		// Clear the field for the next round, so a leftover claimable node
		// cannot make a later round's count look right by accident.
		done := domain.NodeDone
		for id := range want {
			if _, err := repo.UpdateNode(id, store.NodePatch{Status: &done}); err != nil {
				t.Fatalf("round %d: parking node %s: %v", round, id, err)
			}
		}
	}
}
