package main

import (
	"fmt"
	"net/http"
)

// Protocol entity shapes (docs/http-datasource/openapi.yaml, protocol 1.0).
// A plugin is an independent program, so these are defined here rather than
// imported from graph-engine's internal packages.

const (
	protocolName    = "graph-ops-datasource"
	protocolVersion = "1.0"
)

type Label struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
	Color     string `json:"color"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type LabelUsage struct {
	Label
	TicketCount int `json:"ticket_count"`
}

type Project struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Prefix    string `json:"prefix"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type Ticket struct {
	ID              string  `json:"id"`
	ProjectID       string  `json:"project_id"`
	Title           string  `json:"title"`
	Description     string  `json:"description"`
	Status          string  `json:"status"`
	AutoExecutable  bool    `json:"auto_executable"`
	Blocked         bool    `json:"blocked"`
	CreatedAt       string  `json:"created_at"`
	UpdatedAt       string  `json:"updated_at"`
	RefinedAt       *string `json:"refined_at,omitempty"`
	ClosedReason    *string `json:"closed_reason,omitempty"`
	Assignee        *string `json:"assignee,omitempty"`
	GraphExpandedAt *string `json:"graph_expanded_at,omitempty"`
	Priority        string  `json:"priority"`
	Labels          []Label `json:"labels"`
}

type GraphNode struct {
	ID             string  `json:"id"`
	TicketID       string  `json:"ticket_id"`
	Name           string  `json:"name"`
	Type           string  `json:"type"`
	Status         string  `json:"status"`
	IterationCount int     `json:"iteration_count"`
	MaxIterations  int     `json:"max_iterations"`
	Assignee       *string `json:"assignee,omitempty"`
	IsManual       bool    `json:"is_manual"`
	GateID         *string `json:"gate_id,omitempty"`
	Criteria       *string `json:"criteria,omitempty"`
	ConfigID       *string `json:"config_id,omitempty"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
}

type GraphEdge struct {
	ID         string `json:"id"`
	TicketID   string `json:"ticket_id"`
	FromNodeID string `json:"from_node_id"`
	ToNodeID   string `json:"to_node_id"`
	Condition  string `json:"condition"`
	CreatedAt  string `json:"created_at"`
}

type Artifact struct {
	ID         string  `json:"id"`
	TicketID   string  `json:"ticket_id"`
	NodeID     string  `json:"node_id"`
	Name       string  `json:"name"`
	Type       string  `json:"type"`
	Content    *string `json:"content,omitempty"`
	FilePath   *string `json:"file_path,omitempty"`
	Metadata   *string `json:"metadata,omitempty"`
	HasContent bool    `json:"has_content"`
	CreatedAt  string  `json:"created_at"`
}

type TicketDetail struct {
	Ticket
	Nodes     []GraphNode `json:"nodes"`
	Edges     []GraphEdge `json:"edges"`
	Artifacts []Artifact  `json:"artifacts"`
}

// optString decodes a PATCH field that may be absent, null or a string
// (the protocol's three-state "assignee").
type optString struct {
	Set   bool
	Value *string
}

func (o *optString) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(b) == "null" {
		o.Value = nil
		return nil
	}
	var s string
	if err := jsonUnmarshal(b, &s); err != nil {
		return err
	}
	o.Value = &s
	return nil
}

type TicketPatch struct {
	Title           *string   `json:"title"`
	Description     *string   `json:"description"`
	Status          *string   `json:"status"`
	AutoExecutable  *bool     `json:"auto_executable"`
	Blocked         *bool     `json:"blocked"`
	RefinedAt       *string   `json:"refined_at"`
	ClosedReason    *string   `json:"closed_reason"`
	Assignee        optString `json:"assignee"`
	GraphExpandedAt *string   `json:"graph_expanded_at"`
	Priority        *string   `json:"priority"`
	LabelIDs        *[]string `json:"label_ids"`
}

type NodePatch struct {
	Name           *string   `json:"name"`
	Type           *string   `json:"type"`
	Status         *string   `json:"status"`
	IterationCount *int      `json:"iteration_count"`
	MaxIterations  *int      `json:"max_iterations"`
	Assignee       optString `json:"assignee"`
	IsManual       *bool     `json:"is_manual"`
	GateID         *string   `json:"gate_id"`
	Criteria       *string   `json:"criteria"`
}

// apiError is an error answer in the protocol's ErrorResponse shape.
type apiError struct {
	Status  int
	Code    string
	Message string
}

func (e *apiError) Error() string { return e.Code + ": " + e.Message }

func newAPIError(status int, code, format string, args ...any) *apiError {
	return &apiError{Status: status, Code: code, Message: fmt.Sprintf(format, args...)}
}

func notFound(code, what, id string) *apiError {
	return newAPIError(http.StatusNotFound, code, "%s %s not found", what, id)
}

func validationError(format string, args ...any) *apiError {
	return newAPIError(http.StatusBadRequest, "VALIDATION_ERROR", format, args...)
}
