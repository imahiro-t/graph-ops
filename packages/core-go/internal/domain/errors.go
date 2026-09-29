package domain

import "fmt"

// ErrorCode is a machine-readable identifier for an API error. The REST API
// returns it alongside a developer-facing English message so the frontend
// can resolve a localized (ja/en) user-facing message from the code instead
// of parsing or displaying the raw English text (see packages/web/src/i18n).
type ErrorCode string

const (
	// ErrCodeTitleRequired: ticket creation was attempted without a title.
	ErrCodeTitleRequired ErrorCode = "TITLE_REQUIRED"
	// ErrCodeTicketNotFound: the requested ticket does not exist.
	ErrCodeTicketNotFound ErrorCode = "TICKET_NOT_FOUND"
	// ErrCodeNodeNotFound: the requested graph node does not exist.
	ErrCodeNodeNotFound ErrorCode = "NODE_NOT_FOUND"
	// ErrCodeArtifactNotFound: the requested artifact does not exist, or (for
	// GET /api/artifacts/{id}/content) exists but has no stored content.
	ErrCodeArtifactNotFound ErrorCode = "ARTIFACT_NOT_FOUND"
	// ErrCodeValidation: the request failed validation for a reason not
	// covered by a more specific code above (e.g. malformed JSON body).
	ErrCodeValidation ErrorCode = "VALIDATION_ERROR"
	// ErrCodeInternal: an unclassified server-side failure. This is the
	// fallback used for any error that isn't a *APIError.
	ErrCodeInternal ErrorCode = "INTERNAL_ERROR"
	// ErrCodeInvalidPrefix: an explicitly-supplied project prefix isn't
	// 1-5 alphanumeric characters.
	ErrCodeInvalidPrefix ErrorCode = "INVALID_PREFIX"
	// ErrCodePrefixTaken: an explicitly-supplied project prefix collides
	// (case-insensitively) with an existing project's prefix.
	ErrCodePrefixTaken ErrorCode = "PREFIX_TAKEN"
	// ErrCodeProjectNotFound: the requested project does not exist.
	ErrCodeProjectNotFound ErrorCode = "PROJECT_NOT_FOUND"
	// ErrCodeNoCurrentProject: a ticket was created without an explicit
	// project_id and no current project is selected to default to.
	ErrCodeNoCurrentProject ErrorCode = "NO_CURRENT_PROJECT"
	// ErrCodeCSRFHeaderRequired: a state-changing request (anything but
	// GET/HEAD/OPTIONS) arrived without the required same-origin marker
	// header. See internal/httpserver's withCORS -- this is the CSRF
	// mitigation for the server's no-auth/wildcard-CORS/all-interfaces
	// configuration (Security review node-5f79e568).
	ErrCodeCSRFHeaderRequired ErrorCode = "CSRF_HEADER_REQUIRED"
	// ErrCodeHostNotAllowed: the request's Host header does not name an
	// address this server answers to, so it was rejected before reaching any
	// handler. In practice this means a DNS rebinding attempt -- a domain
	// the attacker controls, re-resolved to this machine -- rather than
	// anything a legitimate client does; see internal/httpserver's
	// allowedHost.
	ErrCodeHostNotAllowed ErrorCode = "HOST_NOT_ALLOWED"
	// ErrCodeCatalogCycleDetected: a settings catalog (workflow) save was
	// rejected because the candidate merged catalog's depends_on edges
	// contain a cycle. See internal/engine.ValidateCatalog.
	ErrCodeCatalogCycleDetected ErrorCode = "CATALOG_CYCLE_DETECTED"
	// ErrCodeCatalogUnknownReference: a settings catalog save was rejected
	// because some node's depends_on/loop_back_to/gate referenced a node or
	// review gate id that doesn't exist in the candidate merged catalog.
	ErrCodeCatalogUnknownReference ErrorCode = "CATALOG_UNKNOWN_REFERENCE"
	// ErrCodeCatalogDuplicateNode: a settings catalog save was rejected
	// because two nodes in the submitted document share the same id.
	ErrCodeCatalogDuplicateNode ErrorCode = "CATALOG_DUPLICATE_NODE_ID"
	// ErrCodeCatalogInvalidDocument: a settings catalog save was rejected by
	// internal/engine.ValidateCatalog for a reason not covered by a more
	// specific code above.
	ErrCodeCatalogInvalidDocument ErrorCode = "CATALOG_INVALID_DOCUMENT"
	// ErrCodeInvalidMaxIterations: the settings catalog's workflow-wide
	// max_iterations was not 3, 4 or 5, or a node PATCH set a node's
	// max_iterations to a value less than 1.
	ErrCodeInvalidMaxIterations ErrorCode = "INVALID_MAX_ITERATIONS"
	// ErrCodeInvalidReportTemplate: a settings report-template save was
	// rejected because the submitted HTML is missing one of the fixed
	// template's required structural markers. See
	// internal/config.ValidateReportHTML.
	ErrCodeInvalidReportTemplate ErrorCode = "INVALID_REPORT_TEMPLATE"
	// ErrCodeInvalidPaginationPageSize: the app-settings "pagination page
	// size" was set to a value less than 1.
	ErrCodeInvalidPaginationPageSize ErrorCode = "INVALID_PAGINATION_PAGE_SIZE"
	// ErrCodeProjectCreatedLocalPathNotSaved: POST /api/projects inserted the
	// project into the DB but then failed to save its local path to the home
	// config file (DFLT-00080). Returned with a 500. Distinct from
	// INTERNAL_ERROR so the Web UI can tell the project already exists
	// (re-fetch the list, stop offering "create" again) instead of letting
	// the user retry into a duplicate project in the shared DB.
	ErrCodeProjectCreatedLocalPathNotSaved ErrorCode = "PROJECT_CREATED_LOCAL_PATH_NOT_SAVED"
	// ErrCodeHomeConfigUnavailable: PUT /api/settings/app had nowhere to
	// save to, because the home directory could not be resolved and the home
	// config file ($HOME/.graph-ops/config.json) is the only file settings
	// are ever written to (DFLT-00124, completion criterion 10). Returned
	// with a 500. It used to be a warning on a 200 response -- "saved, minus
	// the one field we had nowhere to put" -- which is exactly the shape
	// this code exists to remove: now that every field goes to that one
	// file, a response that cannot name a file it wrote must not look like a
	// successful save.
	ErrCodeHomeConfigUnavailable ErrorCode = "HOME_CONFIG_UNAVAILABLE"
	// ErrCodeHomeConfigUnreadable: the home config file
	// ($HOME/.graph-ops/config.json) exists but could not be read or parsed.
	// Returned with a 500 from PUT /api/settings/app -- a file we could not
	// parse must not be overwritten, since that would discard whatever the
	// user has in it -- and used as a warning code by GET, which still
	// succeeds and shows the settings falling back to environment variables
	// and built-in defaults. Distinct from INTERNAL_ERROR because the cause
	// is a specific file the user has to repair or delete: every retry fails
	// identically until they do.
	ErrCodeHomeConfigUnreadable ErrorCode = "HOME_CONFIG_UNREADABLE"
	// ErrCodeWorkflowNodesLocked: a settings catalog save set workflow.nodes
	// or workflow.seed. The skeleton graph (plan/plan_review/approval gates/
	// release) and its seed are fixed by the plugin default only -- no
	// tier may override them; only review_gates remain overridable. See
	// internal/config.Merge.
	ErrCodeWorkflowNodesLocked ErrorCode = "WORKFLOW_NODES_LOCKED"
	// ErrCodeMySQLPasswordRetypeRequired: an app-settings save or
	// connection test submitted a resend of the stored MySQL secret -- the
	// "keep the stored password" placeholder, or (since DFLT-00036) the
	// stored "${ENV_VAR}" reference's own text -- together with a MySQL
	// host/port/database/user that is not the one the stored secret was
	// saved for. The stored secret is only ever resolved for its own
	// connection, so it (the password, or the "${ENV_VAR}" reference) has to
	// be retyped before it can be used against a different destination.
	ErrCodeMySQLPasswordRetypeRequired ErrorCode = "MYSQL_PASSWORD_RETYPE_REQUIRED"
	// ErrCodeHTTPDataSourceTokenRetypeRequired: an app-settings save resent
	// the stored HTTP data source token -- the "keep the stored secret"
	// placeholder, or the stored "${ENV_VAR}" reference's own text -- together
	// with an httpDataSourceUrl that is not the one the token was saved for
	// (DFLT-00088). Same rule, and same reason, as
	// ErrCodeMySQLPasswordRetypeRequired: a stored secret is only ever sent
	// to the destination it was saved for, so pointing it at a new URL
	// requires typing it again.
	ErrCodeHTTPDataSourceTokenRetypeRequired ErrorCode = "HTTP_DATASOURCE_TOKEN_RETYPE_REQUIRED"
	// ErrCodeLabelNotFound: the requested label does not exist, or it
	// belongs to a different project than the ticket it was to be attached
	// to, or (CLI --label) no label with that name is registered in the
	// ticket's project (DFLT-00084).
	ErrCodeLabelNotFound ErrorCode = "LABEL_NOT_FOUND"
	// ErrCodeLabelNameTaken: another label in the same project already has
	// this name, compared case-insensitively after trimming.
	ErrCodeLabelNameTaken ErrorCode = "LABEL_NAME_TAKEN"
	// ErrCodeInvalidLabelName: a label name is empty after trimming, or
	// longer than domain.MaxLabelNameLength characters.
	ErrCodeInvalidLabelName ErrorCode = "INVALID_LABEL_NAME"
	// ErrCodeInvalidLabelColor: a label color is not one of the fixed
	// palette keys (domain.LabelColors).
	ErrCodeInvalidLabelColor ErrorCode = "INVALID_LABEL_COLOR"
	// ErrCodeAPIRouteNotFound: the request named an /api/ path this server
	// has no route for (DFLT-00103). Returned with a 404 and distinct from
	// TICKET_NOT_FOUND, which means "the route exists, the row doesn't": the
	// two call for completely different fixes on the caller's side.
	ErrCodeAPIRouteNotFound ErrorCode = "API_ROUTE_NOT_FOUND"
	// ErrCodeRequestBodyTooLarge: the request body exceeded the server's cap
	// (internal/httpserver's maxRequestBodyBytes, DFLT-00103). Returned with
	// a 413, never a 5xx: the request is the caller's to fix, not a server
	// failure. Distinct from VALIDATION_ERROR so the Web UI can say "this
	// artifact is too big to upload" rather than "check your input".
	ErrCodeRequestBodyTooLarge ErrorCode = "REQUEST_BODY_TOO_LARGE"
	// ErrCodeInvalidNodeState: a node was asked to complete from a status it
	// cannot complete from -- one that was never claimed, or one that has
	// already finished (DFLT-00102 / BUG-04). Returned with a 409, not a
	// 400: the request is well-formed and would have been accepted a moment
	// earlier or later, so what has to change is the node's state, not the
	// call. Recovery is unstick-node (for a node stuck at IN PROGRESS /
	// IN REVIEW) or reopen-nodes (to redo a completed one).
	ErrCodeInvalidNodeState ErrorCode = "INVALID_NODE_STATE"
	// ErrCodeRouteNotFound: the request named a non-/api/ route this server
	// deliberately no longer serves (DFLT-00103 removed /artifacts-static/).
	// Separate from API_ROUTE_NOT_FOUND so "an endpoint you called is gone"
	// stays distinguishable from "you misspelled an API path", and from
	// TICKET_NOT_FOUND, which is about a missing row rather than a missing
	// route.
	ErrCodeRouteNotFound ErrorCode = "ROUTE_NOT_FOUND"
	// ErrCodeParentTicketUnsupported: a ticket was to be created with a
	// parent (DFLT-00142) on an HTTP data source that speaks protocol 1.0,
	// which has no parent_ticket_id. It is raised before anything is sent,
	// so the parent is never silently dropped. Returned with a 400.
	ErrCodeParentTicketUnsupported ErrorCode = "PARENT_TICKET_UNSUPPORTED"
	// ErrCodeAutopilotSettingLocked: a PUT of a project's autopilot settings
	// (DFLT-00142) named at least one key the team settings file
	// (<teamExtensionsDir>/autopilot.yaml) fixes for that project. The whole
	// request is refused and nothing is saved -- even when the submitted
	// value equals the team's -- so a caller never mistakes "ignored" or
	// "saved but not in effect" for "saved". Details carries "keys", the
	// sorted list of offending keys. Returned with a 400.
	ErrCodeAutopilotSettingLocked ErrorCode = "AUTOPILOT_SETTING_LOCKED"
	// ErrCodeNodeClaimedByOther: unstick-node was asked to release a node
	// that another processing session claimed and is still working on --
	// its session (or autopilot run) has shown a heartbeat within its lease
	// (DFLT-00327). Nothing is written. Details names who holds it and
	// since when; --force releases it anyway, which is only right once a
	// person has made sure that nobody is working on the node. Returned
	// with a 409.
	ErrCodeNodeClaimedByOther ErrorCode = "NODE_CLAIMED_BY_OTHER"
	// ErrCodeGraphChanged: an HTTP data source (protocol 1.2) refused a
	// POST /tickets/{ticketId}/graph because the ticket's graph no longer
	// matches what the batch was planned against -- another session created
	// the seed or expanded the graph first (DFLT-00328). Nothing was
	// written. Returned by the plugin with a 409; graph-engine absorbs it
	// (the seed carries on with the existing graph, an expansion ends with
	// "already been expanded"), so it never reaches the CLI or the Web UI.
	ErrCodeGraphChanged ErrorCode = "GRAPH_CHANGED"
	// ErrCodeTicketStatusChanged: a ticket status write carried the status
	// it was derived from (store.TicketPatch.IfStatus, DFLT-00329) and the
	// stored status no longer matched -- somebody else changed it first,
	// e.g. closed the ticket. Nothing was written. Returned with a 409 by an
	// HTTP data source (protocol 1.2); the engine absorbs it (it re-reads
	// and derives again), so it does not normally reach the CLI or the Web
	// UI.
	ErrCodeTicketStatusChanged ErrorCode = "TICKET_STATUS_CHANGED"
	// ErrCodeTicketChanged: a ticket write carried the updated_at of the
	// version it was based on (store.TicketPatch.IfUpdatedAt, the
	// if_updated_at of PATCH /api/tickets/{id} and --if-updated-at of
	// update-ticket / refine-ticket, DFLT-00330) and the stored updated_at
	// no longer matched -- somebody else wrote the ticket after it was
	// read. Nothing was written; read the ticket again and redo the change.
	// Not to be confused with TICKET_STATUS_CHANGED, which answers a
	// mismatched if_status. Returned with a 409.
	ErrCodeTicketChanged ErrorCode = "TICKET_CHANGED"
	// ErrCodeConcurrentWriteConflict: a write kept colliding with another
	// write to the same ticket (on MySQL, a deadlock that was still there
	// after the retries, DFLT-00329). Nothing was written, so the same call
	// can simply be made again -- unlike INVALID_NODE_STATE, which says the
	// state has changed and the same call will be refused again. Returned
	// with a 409.
	ErrCodeConcurrentWriteConflict ErrorCode = "CONCURRENT_WRITE_CONFLICT"
	// ErrCodeClientTooOld: the SQLite/MySQL database records a minimum
	// client schema version this graph-engine does not meet -- a newer
	// graph-engine has migrated it (DFLT-00331; see internal/store's
	// schema_version.go). Nothing was written: Init stops before it touches
	// the schema. The fix is to update graph-engine (the GraphOps plugin)
	// and, for the Web UI, restart its server. The CLI exits non-zero; a
	// running Web UI server answers its /api/ requests (except health and
	// settings) with a 503, since the server itself is what is out of date.
	// Details carries db_schema_version, min_client_schema_version and
	// client_schema_version.
	ErrCodeClientTooOld ErrorCode = "CLIENT_TOO_OLD"
)

// APIError pairs a machine-readable Code with a developer-facing English
// Message. The message is for logs/debugging only -- it is never shown to
// end users directly; the frontend looks up a localized string using Code.
//
// Details is optional structured context for the caller (e.g. {"keys": [...]}
// for AUTOPILOT_SETTING_LOCKED or a per-key VALIDATION_ERROR). The HTTP API
// sends it as the error payload's "details" object, omitted when nil.
type APIError struct {
	Code    ErrorCode
	Message string
	Details map[string]any
}

func (e *APIError) Error() string {
	return e.Message
}

// NewAPIError builds an *APIError, formatting Message like fmt.Errorf.
func NewAPIError(code ErrorCode, format string, args ...any) *APIError {
	return &APIError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// WithDetails sets Details and returns e, for chaining onto NewAPIError.
func (e *APIError) WithDetails(details map[string]any) *APIError {
	e.Details = details
	return e
}
