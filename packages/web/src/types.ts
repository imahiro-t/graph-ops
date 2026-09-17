// CLOSED (DFLT-00043) marks a ticket withdrawn without being completed, as
// opposed to DONE, which means the work actually finished. It can be reached
// from any other status (close-ticket) and reopened back into the normal
// flow (reopen-ticket); while CLOSED, the engine never changes its status on
// its own and never hands out executable nodes for it.
export type TicketStatus = 'TODO' | 'REFINED' | 'IN PROGRESS' | 'IN REVIEW' | 'IN RELEASE' | 'DONE' | 'CLOSED';

// Every TicketStatus in workflow/display order. Kept next to the type so the
// ticket-list status filter's options can't drift from it. Readonly so a
// consumer (e.g. a useState initial value) can't mutate the shared constant
// in place -- copy it first.
export const TICKET_STATUSES: readonly TicketStatus[] = ['TODO', 'REFINED', 'IN PROGRESS', 'IN REVIEW', 'IN RELEASE', 'DONE', 'CLOSED'];

// REJECTED (DFLT-00016) applies only to approval_gate nodes: a human
// explicitly rejected it (with a reason, saved as a "rejection_reason"
// artifact on the node), as opposed to TODO, which also covers a gate
// nobody has judged yet.
//
// AWAITING FIX (DFLT-00042) marks a review/review_gate node that failed and
// looped back along its iteration_loop edge: it is waiting for the loop
// target (reset to TODO) to be reworked, as opposed to TODO, which also
// covers a node that has never run. The engine re-claims it (as IN REVIEW)
// once its prerequisites are DONE again -- which, when the loop target isn't
// a direct prerequisite, can be almost immediately.
export type NodeStatus = 'TODO' | 'IN PROGRESS' | 'IN REVIEW' | 'DONE' | 'REJECTED' | 'AWAITING FIX';

export type NodeType =
  | 'plan'
  | 'review'
  | 'gherkin_spec'
  | 'implementation'
  | 'review_gate'
  | 'approval_gate'
  | 'gherkin_test'
  | 'report'
  | 'release'
  | 'investigation'
  // 'documentation' has no domain.NodeType* constant on the Go side (the
  // engine runs it as an opaque step), but it is a plugin-shipped default
  // node type -- it has its own defaults/node-types/documentation.md and,
  // since DFLT-00023 C-1, its own entry in config.builtinNodeTypes, so the
  // settings node-type picker always offers it. Naming it here is what gives
  // it a badge of its own in nodeTypeMeta.ts instead of the unmodeled-type
  // fallback.
  | 'documentation'
  | 'custom';

export type ArtifactType = 'text' | 'gherkin' | 'html' | 'image' | 'json';

// A ticket's optional priority (DFLT-00048): one of three fixed levels.
// There is no 'UNSET' member here -- absence is represented by
// Ticket.priority being null/undefined, the same design as assignee.
export type TicketPriority = 'HIGH' | 'MEDIUM' | 'LOW';

// Every TicketPriority in display order (high to low). Kept next to the type
// so the ticket detail's priority selector and the list's priority filter
// can't drift from it -- see TICKET_STATUSES above for the same pattern.
export const TICKET_PRIORITIES: readonly TicketPriority[] = ['HIGH', 'MEDIUM', 'LOW'];

export interface Ticket {
  id: string;
  project_id: string;
  title: string;
  description: string;
  status: TicketStatus;
  auto_executable: boolean;
  blocked: boolean;
  refined_at?: string | null;
  created_at: string;
  updated_at: string;
  // The free-text reason passed to close-ticket, if any (DFLT-00043).
  // Overwritten every time the ticket is closed again; left untouched by
  // reopen-ticket, so it stays visible as history until the next close.
  closed_reason?: string;
  // The ticket's only form of assignment: the display name of whoever last
  // pressed "assign to me" (null/absent when unassigned), taken verbatim from
  // whichever name was configured in "全体設定" (see AppSettingsFile.myName)
  // at the moment they pressed it -- not necessarily the current viewer's own
  // myName. DFLT-00024 replaced the original free-text assignee with a
  // boolean assigned_to_me flag; DFLT-00047 replaced that flag with this
  // field because a shared backend can't tell "I assigned this" from
  // "someone else assigned this" from a bare boolean, which let another
  // person's assignment render (and be cleared) as if it were the viewer's
  // own.
  assignee?: string | null;
  // Set the moment engine.ExpandGraph first succeeds for this ticket
  // (DFLT-00046) -- nil/absent until the graph has ever been expanded beyond
  // its plan/plan_review seed. Not used for display; kept here only so this
  // type stays in sync with domain.Ticket.
  graph_expanded_at?: string | null;
  // Optional priority (DFLT-00048): null/absent means unset -- always a
  // valid, ordinary state (e.g. every ticket created before this field
  // existed), never treated as an error. Set/changed/cleared via PATCH
  // /api/tickets/{id}'s "priority" field from the ticket detail view (see
  // TicketItem.tsx), and used to filter the ticket list (see priorityMeta.ts
  // and App.tsx's filterPriorities).
  priority?: TicketPriority | null;
}

export interface GraphNode {
  id: string;
  ticket_id: string;
  name: string;
  type: NodeType;
  status: NodeStatus;
  iteration_count: number;
  max_iterations: number;
  assignee?: string | null;
  is_manual: boolean;
  gate_id?: string | null;
  criteria?: string | null;
  config_id?: string | null;
  created_at: string;
  updated_at: string;
}

export interface GraphEdge {
  id: string;
  ticket_id: string;
  from_node_id: string;
  to_node_id: string;
  condition?: string;
  created_at: string;
}

export interface Artifact {
  id: string;
  ticket_id: string;
  node_id: string;
  name: string;
  type: ArtifactType;
  content?: string | null;
  file_path?: string | null;
  metadata?: string | null;
  // True when this artifact has bytes stored in the DB, even when `content`
  // itself was omitted from this response (ticket list/detail responses
  // omit it for html/image rows to keep polling payloads small -- see
  // DFLT-00006's non-functional review). GET /api/artifacts/{id}/content is
  // always the source of truth for the actual bytes.
  has_content?: boolean;
  created_at: string;
}

export interface TicketDetail extends Ticket {
  nodes: GraphNode[];
  edges: GraphEdge[];
  artifacts: Artifact[];
}

// A project scopes a set of tickets to one prefix-based ID namespace (see
// GET/POST /api/projects, backed by packages/core-go/internal/domain.Project).
// prefix is fixed at creation time -- there is no UI or API surface to change
// it afterward.
//
// local_path is where the project lives on *this* environment's disk
// (DFLT-00080). It is not stored in the (possibly team-shared) DB but in the
// server's own graph-config.json (projectPaths), so each member sees their
// own value here. '' means it is not set in this environment ("未設定").
export interface Project {
  id: string;
  name: string;
  prefix: string;
  local_path: string;
  created_at: string;
  updated_at: string;
}

// --- Settings UI (workflow/node-type/review-gate configuration) ---
// Mirrors packages/core-go/internal/config's Document/NodeDef/ReviewGateDef/
// Catalog shapes (see GET/PUT /api/settings/catalog,
// GET/PUT /api/settings/node-types(/{type})).

export type SettingsScope = 'global' | 'project';

// One review gate definition (a reusable review perspective a review_gate
// node can reference by id). additional_criteria is appended to (not
// replacing) whatever criteria was inherited from a lower-priority layer;
// criteria, when set, replaces it outright.
export interface ReviewGateDef {
  name?: string;
  criteria?: string;
  additional_criteria?: string;
  max_iterations?: number | null;
  enabled?: boolean | null;
}

// One node in the workflow template. gate only applies to type:
// "review_gate" nodes (a reference to a ReviewGateDef by id).
export interface NodeDef {
  id: string;
  name?: string;
  type: string;
  gate?: string;
  depends_on?: string[];
  loop_back_to?: string;
  is_manual?: boolean;
  enabled?: boolean | null;
}

export interface WorkflowDef {
  nodes?: NodeDef[];
  seed?: string[];
}

// The shape of one config layer (a scope's own override file -- e.g. what
// GET /api/settings/catalog's tier_document returns, and what PUT's body
// submits back).
export interface SettingsDocument {
  version: number;
  review_gates?: Record<string, ReviewGateDef>;
  workflow?: WorkflowDef;
}

// The fully-merged, ready-to-use result of every applicable tier -- what an
// agent actually sees (merged_catalog), or what a scope would fall back to
// if its own override were cleared (inherited_catalog).
export interface SettingsCatalog {
  review_gates: Record<string, ReviewGateDef>;
  nodes: NodeDef[];
  seed?: string[];
}

export interface SettingsCatalogResponse {
  tier_document: SettingsDocument;
  merged_catalog: SettingsCatalog;
  inherited_catalog: SettingsCatalog;
  scope: SettingsScope;
  project_id: string;
  team_root_resolved: boolean;
}

export interface SettingsNodeTypeInfo {
  type: string;
  has_default: boolean;
  has_user_override: boolean;
  has_team_override: boolean;
}

export interface SettingsNodeTypeTextResponse {
  type: string;
  tier_text: string;
  merged_text: string;
}

// One plugin skill's supplementary-instruction override state (GET
// /api/settings/skills' left-hand list -- see
// internal/httpserver/settings.go's handleListSettingsSkills).
export interface SettingsSkillInfo {
  name: string;
  has_user_override: boolean;
  has_team_override: boolean;
}

// GET/PUT /api/settings/skills/{name} -- mirrors SettingsNodeTypeTextResponse.
export interface SettingsSkillTextResponse {
  name: string;
  tier_text: string;
  merged_text: string;
}

// GET/PUT /api/settings/{plan,review,report}-template. tier_text is this
// scope's own override ("" if unset: Markdown for plan/review, HTML for
// report); merged_text is the fully-resolved template (team override -> user
// override -> plugin default -- a full replace, not an append, unlike
// node-type/skill context).
export interface SettingsTemplateTextResponse {
  tier_text: string;
  merged_text: string;
}

// The report template's response has the same shape; the name is kept for
// existing references.
export type SettingsReportTemplateResponse = SettingsTemplateTextResponse;

// --- App settings (GET/PUT /api/settings/app) ---
// The server/CLI's own operational settings (graph-config.json), edited from
// the "全体設定" scope's app-settings tab -- distinct from the
// node-type/workflow/review-gate Settings UI above. None of these fields
// take effect for the already-running server; they're read once at startup,
// so `effective` (this server's actual current values) will keep showing
// the pre-edit values until it's restarted.

// AppSettingsFile mirrors packages/core-go/internal/runtimeconfig.FileConfig
// (graph-config.json's shape) -- only the fields this tab edits are listed
// here; the others (port/claudeBinary/terminalCommand/workDir/
// teamExtensionsDir/projectPaths) are preserved server-side but never
// surfaced in this UI (projectPaths is edited through the project API as
// Project.local_path instead). An empty string means "not set, falls back to an env var or a
// hardcoded default".
export type DBBackend = 'sqlite' | 'mysql';

// Mirrors packages/core-go/internal/store's MySQLTLSVerifyFull/VerifyCA/
// Disabled constants exactly -- no "preferred"/"skip-verify" is offered
// (see store.NormalizeMySQLTLSMode's doc comment for why: this ticket's
// (DFLT-00037) whole point is that there is no fallback-to-plaintext mode
// and no unverified-TLS mode, so this UI must not be able to construct
// either).
export type MySQLTLSMode = 'verify-full' | 'verify-ca' | 'disabled';

// Mirrors packages/core-go/internal/runtimeconfig.RedactedSecretPlaceholder
// exactly. The server never sends a stored plaintext secret to this UI; it
// sends this token instead, and accepts it back to mean "the user did not
// retype the secret, keep the stored one". Keeping the token in the form
// state (rather than blanking the field) is what makes saving an unrelated
// field preserve the password instead of clearing it.
export const REDACTED_SECRET_PLACEHOLDER = '__GRAPH_OPS_SECRET_UNCHANGED__';

export interface AppSettingsFile {
  dbBackend?: DBBackend;
  dbPath?: string;
  mysqlHost?: string;
  mysqlPort?: number;
  mysqlDatabase?: string;
  mysqlUser?: string;
  // Never the real stored password: the server redacts it on the way out
  // (packages/core-go/internal/runtimeconfig.RedactSecret). It is either ''
  // (nothing stored), a '${ENV_VAR_NAME}' reference (a variable name, not a
  // secret), or REDACTED_SECRET_PLACEHOLDER. Send whichever value the form
  // holds straight back: the placeholder means "keep the stored password".
  mysqlPassword?: string;
  // TLS mode for the MySQL connection above; '' (unset) means the
  // verify-full default -- see MySQLTLSMode and store.NormalizeMySQLTLSMode.
  // Unlike mysqlPassword, this is never a secret and is never redacted.
  mysqlTls?: MySQLTLSMode | '';
  // Absolute path to a PEM CA file, required when mysqlTls is 'verify-ca'.
  mysqlTlsCa?: string;
  artifactsDir?: string;
  userExtensionsDir?: string;
  paginationPageSize?: number;
  // The viewer's own display name, used by the per-ticket "assign to
  // me"/"unassign" buttons (see Ticket.assignee). Unlike every other
  // field here, it takes effect immediately -- no server restart needed --
  // and an empty value simply hides those buttons.
  myName?: string;
}

export interface EffectiveAppSettings {
  dbBackend: DBBackend;
  dbPath: string;
  artifactsDir: string;
  userExtensionsDir: string;
  paginationPageSize: number;
}

export interface AppSettingsResponse {
  file: AppSettingsFile;
  effective: EffectiveAppSettings;
  config_path: string;
}

// POST /api/settings/app/test-mysql-connection's response. See
// packages/core-go/internal/httpserver/app_settings.go's
// handleTestMySQLConnection: a failed connection attempt is still HTTP 200
// (ok: false, error: <driver's error text>) since it's an expected, common
// outcome of the "接続テスト" button, not a request error.
export interface TestMySQLConnectionResult {
  ok: boolean;
  error?: string;
}
