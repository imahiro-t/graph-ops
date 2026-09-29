package httpserver

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/config"
	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/identity"
	"github.com/graph-ops/core-go/internal/store"
)

// The Web UI's two reads -- the list it polls and a ticket's detail -- show
// who holds a claimed node and whether they are still at it (DFLT-00327),
// and never the claim token.
func TestTicketAPIsShowClaimsButNotTokens(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	tk, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Status: domain.TicketTODO, AutoExecutable: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateNode(domain.GraphNode{TicketID: tk.ID, Name: "impl", Type: domain.NodeTypeImplementation, Status: domain.NodeTODO, MaxIterations: 3}); err != nil {
		t.Fatal(err)
	}
	eng := engine.New(repo)
	sess, err := eng.BeginSession(tk.ID, "", identity.Actor{Name: "bob@host", NameIsFallback: true, MachineID: "m"})
	if err != nil {
		t.Fatal(err)
	}
	exec, err := eng.GetExecutableNodesAs(tk.ID, config.Catalog{}, engine.Claimer{Name: "bob@host", NameIsFallback: true, SessionID: sess.SessionID})
	if err != nil || len(exec) != 1 {
		t.Fatalf("GetExecutableNodesAs = %+v, %v", exec, err)
	}
	token := *exec[0].ClaimToken

	for _, path := range []string{"/api/tickets?project_id=" + projectID, "/api/tickets/" + tk.ID} {
		rec := doJSON(t, s, http.MethodGet, path, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d", path, rec.Code)
		}
		body := rec.Body.String()
		if strings.Contains(body, token) || strings.Contains(body, "claim_token") {
			t.Errorf("GET %s leaks the claim token: %s", path, body)
		}
		for _, want := range []string{`"claimed_by_name":"bob@host"`, `"claimed_by_name_is_fallback":true`, `"claimed_at":`, `"claim_heartbeat":`, `"claim_lease":"live"`} {
			if !strings.Contains(body, want) {
				t.Errorf("GET %s lacks %s: %s", path, want, body)
			}
		}
	}
}

func TestNodeClaimedByOtherIs409(t *testing.T) {
	err := domain.NewAPIError(domain.ErrCodeNodeClaimedByOther, "x")
	if got := statusForError(err, http.StatusBadRequest); got != http.StatusConflict {
		t.Fatalf("status = %d, want 409", got)
	}
}

// failingSessionsRepo is a SQLite repository whose processing sessions
// cannot be read, standing in for a data source that keeps failing.
type failingSessionsRepo struct{ *store.SQLiteRepository }

func (failingSessionsRepo) ListProcessingSessionsByTickets([]string) ([]domain.ProcessingSession, error) {
	return nil, errors.New("data source unreachable")
}

// The engine's warnings reach the server's structured log (event
// node_claims) rather than bare stderr, and a failure that repeats on every
// poll is logged once, not once per poll (DFLT-00327).
func TestClaimWarningsGoToTheServerLogThrottled(t *testing.T) {
	sqlite, err := store.NewSQLiteRepository(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlite.Init(); err != nil {
		t.Fatal(err)
	}
	proj, err := sqlite.CreateProject("P", "TEST")
	if err != nil {
		t.Fatal(err)
	}
	tk, err := sqlite.CreateTicket(proj.ID, domain.Ticket{Title: "t", Status: domain.TicketTODO, AutoExecutable: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqlite.CreateNode(domain.GraphNode{TicketID: tk.ID, Name: "impl", Type: domain.NodeTypeImplementation, Status: domain.NodeTODO, MaxIterations: 3}); err != nil {
		t.Fatal(err)
	}
	plain := engine.New(sqlite)
	sess, err := plain.BeginSession(tk.ID, "", identity.Actor{Name: "bob", MachineID: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plain.GetExecutableNodesAs(tk.ID, config.Catalog{}, engine.Claimer{Name: "bob", SessionID: sess.SessionID}); err != nil {
		t.Fatal(err)
	}

	repo := failingSessionsRepo{sqlite}
	var logged bytes.Buffer
	cfg := Config{ArtifactsDir: t.TempDir(), HomeDir: t.TempDir(),
		Logger: slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelWarn}))}
	s := New(repo, engine.New(repo), cfg)
	for i := 0; i < 3; i++ {
		for _, path := range []string{"/api/tickets?project_id=" + proj.ID, "/api/tickets/" + tk.ID} {
			if rec := doJSON(t, s, http.MethodGet, path, nil); rec.Code != http.StatusOK {
				t.Fatalf("GET %s = %d: %s", path, rec.Code, rec.Body.String())
			}
		}
	}
	out := logged.String()
	if n := strings.Count(out, "event=node_claims"); n != 1 {
		t.Fatalf("want the failure logged once with event=node_claims, got %d times:\n%s", n, out)
	}
	if !strings.Contains(out, "data source unreachable") {
		t.Errorf("the log line lacks the cause: %s", out)
	}
}
