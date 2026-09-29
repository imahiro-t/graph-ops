package httpserver

import (
	"net/http"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/config"
	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/identity"
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
