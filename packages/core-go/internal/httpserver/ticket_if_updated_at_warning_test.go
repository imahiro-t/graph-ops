package httpserver

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/store"
	"github.com/graph-ops/core-go/internal/store/httpdatasourcetest"
)

// DFLT-00330: on an HTTP data source older than 1.2, if_updated_at is checked
// with a non-atomic GET -> compare -> PATCH, and the one-time warning about it
// goes to the Web UI server's structured log (event=data_source), not bare
// onto stderr.
func TestUpdateTicket_IfUpdatedAt_HTTP11WarningGoesToServerLog(t *testing.T) {
	plugin := httpdatasourcetest.New("")
	plugin.Version = "1.1"
	srv := httptest.NewServer(plugin)
	t.Cleanup(srv.Close)
	repo, err := store.Open(store.Config{Backend: "http", HTTPURL: srv.URL})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	proj, err := repo.CreateProject("Test Project", "TEST")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	tk, err := repo.CreateTicket(proj.ID, domain.Ticket{Title: "t", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	s := New(repo, engine.New(repo), Config{ArtifactsDir: t.TempDir(), HomeDir: t.TempDir(), Logger: logger})

	cur := tk.UpdatedAt
	for i := 0; i < 3; i++ {
		rec := doJSON(t, s, http.MethodPatch, "/api/tickets/"+tk.ID, map[string]any{"priority": "HIGH", "if_updated_at": cur})
		expectStatus(t, rec, http.StatusOK)
		got, err := repo.GetTicket(tk.ID)
		if err != nil || got == nil {
			t.Fatalf("GetTicket: %v", err)
		}
		cur = got.UpdatedAt
	}

	out := logBuf.String()
	if n := strings.Count(out, "event=data_source"); n != 1 {
		t.Fatalf("server log has %d data_source warnings, want exactly 1:\n%s", n, out)
	}
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "not atomic") {
		t.Fatalf("server log lacks the non-atomic WARN line:\n%s", out)
	}
}
