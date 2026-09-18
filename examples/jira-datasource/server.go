package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
)

// requestDeadline is how long the plugin works on one protocol request,
// Jira calls, retries and Retry-After waits included. graph-engine gives up
// on a request after 30 seconds and never retries it (see
// docs/http-datasource/openapi.yaml), so the plugin must finish -- or stop --
// well before that: an operation still running after graph-engine reported a
// timeout could complete a write that the user then repeats, creating a
// duplicate ticket, node or artifact.
const requestDeadline = 25 * time.Second

// slowRequestThreshold is the duration above which a request is logged.
const slowRequestThreshold = 5 * time.Second

// newHandler serves the GraphOps HTTP data source protocol
// (docs/http-datasource/openapi.yaml) on top of store. Every request --
// GET /protocol included -- must carry "Authorization: Bearer <token>";
// anything else is answered 401 before Jira is contacted.
//
// Each request's context -- cancelled when graph-engine disconnects, and
// bounded by requestDeadline -- is passed down to every Jira call, so an
// abandoned request stops touching Jira.
func newHandler(store *Store, token string, logger *log.Logger) http.Handler {
	return newHandlerWithDeadline(store, token, logger, requestDeadline)
}

func newHandlerWithDeadline(store *Store, token string, logger *log.Logger, deadline time.Duration) http.Handler {
	mux := http.NewServeMux()
	h := &handler{store: store, logger: logger}

	mux.HandleFunc("GET /protocol", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"protocol": protocolName, "version": protocolVersion})
	})
	mux.HandleFunc("POST /init", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })

	// Tickets
	mux.HandleFunc("POST /projects/{projectId}/tickets", func(w http.ResponseWriter, r *http.Request) {
		var in Ticket
		if h.decode(w, r, &in) {
			h.reply(w, http.StatusCreated)(store.CreateTicket(r.Context(), r.PathValue("projectId"), in))
		}
	})
	mux.HandleFunc("GET /tickets/{ticketId}", func(w http.ResponseWriter, r *http.Request) {
		h.reply(w, http.StatusOK)(store.GetTicket(r.Context(), r.PathValue("ticketId")))
	})
	mux.HandleFunc("GET /tickets/{ticketId}/detail", func(w http.ResponseWriter, r *http.Request) {
		h.reply(w, http.StatusOK)(store.GetTicketDetail(r.Context(), r.PathValue("ticketId")))
	})
	mux.HandleFunc("GET /tickets", func(w http.ResponseWriter, r *http.Request) {
		h.reply(w, http.StatusOK)(store.ListTickets(r.Context()))
	})
	mux.HandleFunc("GET /projects/{projectId}/tickets", func(w http.ResponseWriter, r *http.Request) {
		h.reply(w, http.StatusOK)(store.ListTicketsByProject(r.Context(), r.PathValue("projectId")))
	})
	mux.HandleFunc("PATCH /tickets/{ticketId}", func(w http.ResponseWriter, r *http.Request) {
		var p TicketPatch
		if h.decode(w, r, &p) {
			h.reply(w, http.StatusOK)(store.UpdateTicket(r.Context(), r.PathValue("ticketId"), p))
		}
	})
	mux.HandleFunc("DELETE /tickets/{ticketId}", func(w http.ResponseWriter, r *http.Request) {
		h.noContent(w, store.DeleteTicket(r.Context(), r.PathValue("ticketId")))
	})

	// Nodes
	mux.HandleFunc("POST /tickets/{ticketId}/nodes", func(w http.ResponseWriter, r *http.Request) {
		var in GraphNode
		if h.decode(w, r, &in) {
			h.reply(w, http.StatusCreated)(store.CreateNode(r.Context(), r.PathValue("ticketId"), in))
		}
	})
	mux.HandleFunc("GET /nodes/{nodeId}", func(w http.ResponseWriter, r *http.Request) {
		h.reply(w, http.StatusOK)(store.GetNode(r.Context(), r.PathValue("nodeId")))
	})
	mux.HandleFunc("GET /tickets/{ticketId}/nodes", func(w http.ResponseWriter, r *http.Request) {
		h.reply(w, http.StatusOK)(store.ListNodesByTicket(r.Context(), r.PathValue("ticketId")))
	})
	mux.HandleFunc("PATCH /nodes/{nodeId}", func(w http.ResponseWriter, r *http.Request) {
		var p NodePatch
		if h.decode(w, r, &p) {
			h.reply(w, http.StatusOK)(store.UpdateNode(r.Context(), r.PathValue("nodeId"), p))
		}
	})
	mux.HandleFunc("DELETE /nodes/{nodeId}", func(w http.ResponseWriter, r *http.Request) {
		h.noContent(w, store.DeleteNode(r.Context(), r.PathValue("nodeId")))
	})

	// Edges
	mux.HandleFunc("POST /tickets/{ticketId}/edges", func(w http.ResponseWriter, r *http.Request) {
		var in GraphEdge
		if h.decode(w, r, &in) {
			h.reply(w, http.StatusCreated)(store.CreateEdge(r.Context(), r.PathValue("ticketId"), in))
		}
	})
	mux.HandleFunc("GET /tickets/{ticketId}/edges", func(w http.ResponseWriter, r *http.Request) {
		h.reply(w, http.StatusOK)(store.ListEdgesByTicket(r.Context(), r.PathValue("ticketId")))
	})
	mux.HandleFunc("DELETE /tickets/{ticketId}/edges", func(w http.ResponseWriter, r *http.Request) {
		h.noContent(w, store.ClearEdgesByTicket(r.Context(), r.PathValue("ticketId")))
	})

	// Artifacts
	mux.HandleFunc("POST /tickets/{ticketId}/artifacts", func(w http.ResponseWriter, r *http.Request) {
		var in Artifact
		if h.decode(w, r, &in) {
			h.reply(w, http.StatusCreated)(store.CreateArtifact(r.Context(), r.PathValue("ticketId"), in))
		}
	})
	mux.HandleFunc("GET /artifacts/{artifactId}", func(w http.ResponseWriter, r *http.Request) {
		h.reply(w, http.StatusOK)(store.GetArtifact(r.Context(), r.PathValue("artifactId")))
	})
	mux.HandleFunc("GET /tickets/{ticketId}/artifacts", func(w http.ResponseWriter, r *http.Request) {
		h.reply(w, http.StatusOK)(store.ListArtifactsByTicket(r.Context(), r.PathValue("ticketId")))
	})
	mux.HandleFunc("GET /nodes/{nodeId}/artifacts", func(w http.ResponseWriter, r *http.Request) {
		h.reply(w, http.StatusOK)(store.ListArtifactsByNode(r.Context(), r.PathValue("nodeId")))
	})

	// Projects
	mux.HandleFunc("POST /projects", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name   string `json:"name"`
			Prefix string `json:"prefix"`
		}
		if h.decode(w, r, &in) {
			h.reply(w, http.StatusCreated)(store.CreateProject(r.Context(), in.Name, in.Prefix))
		}
	})
	mux.HandleFunc("GET /projects/{projectId}", func(w http.ResponseWriter, r *http.Request) {
		h.reply(w, http.StatusOK)(store.GetProject(r.Context(), r.PathValue("projectId")))
	})
	mux.HandleFunc("GET /projects", func(w http.ResponseWriter, r *http.Request) {
		h.reply(w, http.StatusOK)(store.ListProjects(r.Context()))
	})
	mux.HandleFunc("PATCH /projects/{projectId}", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name *string `json:"name"`
		}
		if h.decode(w, r, &in) {
			h.reply(w, http.StatusOK)(store.UpdateProject(r.Context(), r.PathValue("projectId"), in.Name))
		}
	})
	mux.HandleFunc("DELETE /projects/{projectId}", func(w http.ResponseWriter, r *http.Request) {
		h.noContent(w, store.DeleteProject(r.Context(), r.PathValue("projectId")))
	})

	// Labels
	mux.HandleFunc("POST /projects/{projectId}/labels", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name  string `json:"name"`
			Color string `json:"color"`
		}
		if h.decode(w, r, &in) {
			h.reply(w, http.StatusCreated)(store.CreateLabel(r.Context(), r.PathValue("projectId"), in.Name, in.Color))
		}
	})
	mux.HandleFunc("GET /labels/{labelId}", func(w http.ResponseWriter, r *http.Request) {
		h.reply(w, http.StatusOK)(store.GetLabel(r.Context(), r.PathValue("labelId")))
	})
	mux.HandleFunc("GET /projects/{projectId}/labels", func(w http.ResponseWriter, r *http.Request) {
		h.reply(w, http.StatusOK)(store.ListLabelsByProject(r.Context(), r.PathValue("projectId")))
	})
	mux.HandleFunc("PATCH /labels/{labelId}", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name  *string `json:"name"`
			Color *string `json:"color"`
		}
		if h.decode(w, r, &in) {
			h.reply(w, http.StatusOK)(store.UpdateLabel(r.Context(), r.PathValue("labelId"), in.Name, in.Color))
		}
	})
	mux.HandleFunc("DELETE /labels/{labelId}", func(w http.ResponseWriter, r *http.Request) {
		n, err := store.DeleteLabel(r.Context(), r.PathValue("labelId"))
		h.reply(w, http.StatusOK)(map[string]int{"removed_from_tickets": n}, err)
	})

	// Current project
	mux.HandleFunc("GET /current-project", func(w http.ResponseWriter, r *http.Request) {
		id, err := store.GetCurrentProjectID(r.Context())
		h.reply(w, http.StatusOK)(map[string]string{"project_id": id}, err)
	})
	mux.HandleFunc("PUT /current-project", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ProjectID string `json:"project_id"`
		}
		if h.decode(w, r, &in) {
			h.noContent(w, store.SetCurrentProjectID(r.Context(), in.ProjectID))
		}
	})

	return requireBearer(token, withDeadline(deadline, logger, mux))
}

// withDeadline bounds each request's context by deadline and logs requests
// slower than slowRequestThreshold.
func withDeadline(deadline time.Duration, logger *log.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), deadline)
		defer cancel()
		start := time.Now()
		next.ServeHTTP(w, r.WithContext(ctx))
		if took := time.Since(start); took > slowRequestThreshold && logger != nil {
			logger.Printf("slow request: %s %s took %s", r.Method, r.URL.Path, took.Round(time.Millisecond))
		}
	})
}

// requireBearer rejects any request whose bearer token is not token,
// comparing in constant time.
func requireBearer(token string, next http.Handler) http.Handler {
	want := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(got), want) != 1 {
			writeJSON(w, http.StatusUnauthorized, errorBody("UNAUTHORIZED", "missing or invalid bearer token"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

type handler struct {
	store  *Store
	logger *log.Logger
}

func errorBody(code, message string) map[string]map[string]string {
	return map[string]map[string]string{"error": {"code": code, "message": message}}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (h *handler) decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("VALIDATION_ERROR", "malformed JSON body: "+err.Error()))
		return false
	}
	return true
}

func (h *handler) writeError(w http.ResponseWriter, err error) {
	var apiErr *apiError
	if !errors.As(err, &apiErr) {
		apiErr = newAPIError(http.StatusInternalServerError, "INTERNAL_ERROR", "%v", err)
	}
	if apiErr.Status >= 500 && h.logger != nil {
		h.logger.Printf("error: %s", apiErr.Message)
	}
	writeJSON(w, apiErr.Status, errorBody(apiErr.Code, apiErr.Message))
}

// reply returns a function taking a (value, error) pair, so a Store call's
// results can be passed straight through.
func (h *handler) reply(w http.ResponseWriter, status int) func(any, error) {
	return func(v any, err error) {
		if err != nil {
			h.writeError(w, err)
			return
		}
		writeJSON(w, status, v)
	}
}

func (h *handler) noContent(w http.ResponseWriter, err error) {
	if err != nil {
		h.writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
