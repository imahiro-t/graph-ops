package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
)

// newHandler serves the GraphOps HTTP data source protocol
// (docs/http-datasource/openapi.yaml) on top of store. Every request --
// GET /protocol included -- must carry "Authorization: Bearer <token>";
// anything else is answered 401 before Jira is contacted.
func newHandler(store *Store, token string, logger *log.Logger) http.Handler {
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
			h.reply(w, http.StatusCreated)(store.CreateTicket(r.PathValue("projectId"), in))
		}
	})
	mux.HandleFunc("GET /tickets/{ticketId}", func(w http.ResponseWriter, r *http.Request) {
		h.reply(w, http.StatusOK)(store.GetTicket(r.PathValue("ticketId")))
	})
	mux.HandleFunc("GET /tickets/{ticketId}/detail", func(w http.ResponseWriter, r *http.Request) {
		h.reply(w, http.StatusOK)(store.GetTicketDetail(r.PathValue("ticketId")))
	})
	mux.HandleFunc("GET /tickets", func(w http.ResponseWriter, r *http.Request) {
		h.reply(w, http.StatusOK)(store.ListTickets())
	})
	mux.HandleFunc("GET /projects/{projectId}/tickets", func(w http.ResponseWriter, r *http.Request) {
		h.reply(w, http.StatusOK)(store.ListTicketsByProject(r.PathValue("projectId")))
	})
	mux.HandleFunc("PATCH /tickets/{ticketId}", func(w http.ResponseWriter, r *http.Request) {
		var p TicketPatch
		if h.decode(w, r, &p) {
			h.reply(w, http.StatusOK)(store.UpdateTicket(r.PathValue("ticketId"), p))
		}
	})
	mux.HandleFunc("DELETE /tickets/{ticketId}", func(w http.ResponseWriter, r *http.Request) {
		h.noContent(w, store.DeleteTicket(r.PathValue("ticketId")))
	})

	// Nodes
	mux.HandleFunc("POST /tickets/{ticketId}/nodes", func(w http.ResponseWriter, r *http.Request) {
		var in GraphNode
		if h.decode(w, r, &in) {
			h.reply(w, http.StatusCreated)(store.CreateNode(r.PathValue("ticketId"), in))
		}
	})
	mux.HandleFunc("GET /nodes/{nodeId}", func(w http.ResponseWriter, r *http.Request) {
		h.reply(w, http.StatusOK)(store.GetNode(r.PathValue("nodeId")))
	})
	mux.HandleFunc("GET /tickets/{ticketId}/nodes", func(w http.ResponseWriter, r *http.Request) {
		h.reply(w, http.StatusOK)(store.ListNodesByTicket(r.PathValue("ticketId")))
	})
	mux.HandleFunc("PATCH /nodes/{nodeId}", func(w http.ResponseWriter, r *http.Request) {
		var p NodePatch
		if h.decode(w, r, &p) {
			h.reply(w, http.StatusOK)(store.UpdateNode(r.PathValue("nodeId"), p))
		}
	})
	mux.HandleFunc("DELETE /nodes/{nodeId}", func(w http.ResponseWriter, r *http.Request) {
		h.noContent(w, store.DeleteNode(r.PathValue("nodeId")))
	})

	// Edges
	mux.HandleFunc("POST /tickets/{ticketId}/edges", func(w http.ResponseWriter, r *http.Request) {
		var in GraphEdge
		if h.decode(w, r, &in) {
			h.reply(w, http.StatusCreated)(store.CreateEdge(r.PathValue("ticketId"), in))
		}
	})
	mux.HandleFunc("GET /tickets/{ticketId}/edges", func(w http.ResponseWriter, r *http.Request) {
		h.reply(w, http.StatusOK)(store.ListEdgesByTicket(r.PathValue("ticketId")))
	})
	mux.HandleFunc("DELETE /tickets/{ticketId}/edges", func(w http.ResponseWriter, r *http.Request) {
		h.noContent(w, store.ClearEdgesByTicket(r.PathValue("ticketId")))
	})

	// Artifacts
	mux.HandleFunc("POST /tickets/{ticketId}/artifacts", func(w http.ResponseWriter, r *http.Request) {
		var in Artifact
		if h.decode(w, r, &in) {
			h.reply(w, http.StatusCreated)(store.CreateArtifact(r.PathValue("ticketId"), in))
		}
	})
	mux.HandleFunc("GET /artifacts/{artifactId}", func(w http.ResponseWriter, r *http.Request) {
		h.reply(w, http.StatusOK)(store.GetArtifact(r.PathValue("artifactId")))
	})
	mux.HandleFunc("GET /tickets/{ticketId}/artifacts", func(w http.ResponseWriter, r *http.Request) {
		h.reply(w, http.StatusOK)(store.ListArtifactsByTicket(r.PathValue("ticketId")))
	})
	mux.HandleFunc("GET /nodes/{nodeId}/artifacts", func(w http.ResponseWriter, r *http.Request) {
		h.reply(w, http.StatusOK)(store.ListArtifactsByNode(r.PathValue("nodeId")))
	})

	// Projects
	mux.HandleFunc("POST /projects", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name   string `json:"name"`
			Prefix string `json:"prefix"`
		}
		if h.decode(w, r, &in) {
			h.reply(w, http.StatusCreated)(store.CreateProject(in.Name, in.Prefix))
		}
	})
	mux.HandleFunc("GET /projects/{projectId}", func(w http.ResponseWriter, r *http.Request) {
		h.reply(w, http.StatusOK)(store.GetProject(r.PathValue("projectId")))
	})
	mux.HandleFunc("GET /projects", func(w http.ResponseWriter, r *http.Request) {
		h.reply(w, http.StatusOK)(store.ListProjects())
	})
	mux.HandleFunc("PATCH /projects/{projectId}", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name *string `json:"name"`
		}
		if h.decode(w, r, &in) {
			h.reply(w, http.StatusOK)(store.UpdateProject(r.PathValue("projectId"), in.Name))
		}
	})
	mux.HandleFunc("DELETE /projects/{projectId}", func(w http.ResponseWriter, r *http.Request) {
		h.noContent(w, store.DeleteProject(r.PathValue("projectId")))
	})

	// Labels
	mux.HandleFunc("POST /projects/{projectId}/labels", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name  string `json:"name"`
			Color string `json:"color"`
		}
		if h.decode(w, r, &in) {
			h.reply(w, http.StatusCreated)(store.CreateLabel(r.PathValue("projectId"), in.Name, in.Color))
		}
	})
	mux.HandleFunc("GET /labels/{labelId}", func(w http.ResponseWriter, r *http.Request) {
		h.reply(w, http.StatusOK)(store.GetLabel(r.PathValue("labelId")))
	})
	mux.HandleFunc("GET /projects/{projectId}/labels", func(w http.ResponseWriter, r *http.Request) {
		h.reply(w, http.StatusOK)(store.ListLabelsByProject(r.PathValue("projectId")))
	})
	mux.HandleFunc("PATCH /labels/{labelId}", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name  *string `json:"name"`
			Color *string `json:"color"`
		}
		if h.decode(w, r, &in) {
			h.reply(w, http.StatusOK)(store.UpdateLabel(r.PathValue("labelId"), in.Name, in.Color))
		}
	})
	mux.HandleFunc("DELETE /labels/{labelId}", func(w http.ResponseWriter, r *http.Request) {
		n, err := store.DeleteLabel(r.PathValue("labelId"))
		h.reply(w, http.StatusOK)(map[string]int{"removed_from_tickets": n}, err)
	})

	// Current project
	mux.HandleFunc("GET /current-project", func(w http.ResponseWriter, r *http.Request) {
		id, err := store.GetCurrentProjectID()
		h.reply(w, http.StatusOK)(map[string]string{"project_id": id}, err)
	})
	mux.HandleFunc("PUT /current-project", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ProjectID string `json:"project_id"`
		}
		if h.decode(w, r, &in) {
			h.noContent(w, store.SetCurrentProjectID(in.ProjectID))
		}
	})

	return requireBearer(token, mux)
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
