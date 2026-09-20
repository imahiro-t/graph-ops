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
	// ErrCodeInvalidScope: a settings API request's scope parameter wasn't
	// "global" or "project", or was "project" without a resolvable
	// project_id. See internal/httpserver/settings.go.
	ErrCodeInvalidScope ErrorCode = "INVALID_SETTINGS_SCOPE"
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
	// ErrCodeInvalidMaxIterations: a review gate's max_iterations was set to
	// a value less than 1.
	ErrCodeInvalidMaxIterations ErrorCode = "INVALID_MAX_ITERATIONS"
	// ErrCodeInvalidReportTemplate: a settings report-template save was
	// rejected because the submitted HTML is missing one of the fixed
	// template's required structural markers. See
	// internal/config.ValidateReportHTML.
	ErrCodeInvalidReportTemplate ErrorCode = "INVALID_REPORT_TEMPLATE"
	// ErrCodeInvalidPaginationPageSize: the app-settings "pagination page
	// size" was set to a value less than 1.
	ErrCodeInvalidPaginationPageSize ErrorCode = "INVALID_PAGINATION_PAGE_SIZE"
	// ErrCodeProjectLocalPathNotSet: a scope=project settings request named a
	// project that has no local path in this environment's graph-config.json
	// (projectPaths), so there is no directory to read the team tier from or
	// write it to (DFLT-00080). Falling back to the server's own cwd would
	// silently read/write the wrong place, so this is reported instead.
	ErrCodeProjectLocalPathNotSet ErrorCode = "PROJECT_LOCAL_PATH_NOT_SET"
	// ErrCodeProjectTeamRootIsUserRoot: a scope=project settings request
	// resolved the project's team tier to the same directory as the user
	// tier -- the project's local path is the home directory itself, or an
	// explicit team-root override points at the user root (DFLT-00068).
	// Reading or writing there would edit the global settings under the
	// project's name, so the project has no team tier to edit.
	ErrCodeProjectTeamRootIsUserRoot ErrorCode = "PROJECT_TEAM_ROOT_IS_USER_ROOT"
	// ErrCodeProjectCreatedLocalPathNotSaved: POST /api/projects inserted the
	// project into the DB but then failed to save its local path to
	// graph-config.json (DFLT-00080). Returned with a 500. Distinct from
	// INTERNAL_ERROR so the Web UI can tell the project already exists
	// (re-fetch the list, stop offering "create" again) instead of letting
	// the user retry into a duplicate project in the shared DB.
	ErrCodeProjectCreatedLocalPathNotSaved ErrorCode = "PROJECT_CREATED_LOCAL_PATH_NOT_SAVED"
	// ErrCodeAppSettingsArtifactsDirNotSaved: PUT /api/settings/app saved
	// every setting it owns except artifactsDir, which is a home-only key
	// and therefore goes to $HOME/.graph-ops/config.json in a second write
	// (DFLT-00104) -- and that write failed. Returned with a 500. Distinct
	// from INTERNAL_ERROR because the two say opposite things to the user:
	// "nothing happened, try again later" would be wrong here, since the
	// other settings ARE saved and only this one field has to be set again.
	ErrCodeAppSettingsArtifactsDirNotSaved ErrorCode = "APP_SETTINGS_ARTIFACTS_DIR_NOT_SAVED"
	// ErrCodeHomeConfigUnreadable: the home config file
	// ($HOME/.graph-ops/config.json) exists but could not be read or parsed,
	// so the home-only keys could not be saved -- and will not be read at
	// the next startup either (DFLT-00104). Returned with a 500 from PUT
	// /api/settings/app, and used as a warning code by GET, which still
	// succeeds. Distinct from ErrCodeAppSettingsArtifactsDirNotSaved because
	// the cause is a specific file the user has to repair or delete: every
	// retry fails identically until they do.
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
)

// APIError pairs a machine-readable Code with a developer-facing English
// Message. The message is for logs/debugging only -- it is never shown to
// end users directly; the frontend looks up a localized string using Code.
type APIError struct {
	Code    ErrorCode
	Message string
}

func (e *APIError) Error() string {
	return e.Message
}

// NewAPIError builds an *APIError, formatting Message like fmt.Errorf.
func NewAPIError(code ErrorCode, format string, args ...any) *APIError {
	return &APIError{Code: code, Message: fmt.Sprintf(format, args...)}
}
