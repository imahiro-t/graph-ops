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
// covers a node that has never run. A loop-back (DFLT-00119) resets the loop
// target *and* its whole forward closure over success edges -- every node
// reachable from the target, whatever state it was in (DONE, IN PROGRESS,
// IN REVIEW, AWAITING FIX, REJECTED), not just the finished ones and not just
// the ones on a path to the node that failed. So the sibling gates that had
// already passed, and the ones still being judged at that moment, go back to
// TODO as well. This node is re-claimed (as IN REVIEW) once its own non-loop
// prerequisites are DONE again; in the default shape, where every gate
// depends only on the implementation node, that is the same get-executable
// call that re-offers its rewound siblings.
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

// A ticket's priority (DFLT-00048): one of three fixed levels. There is no
// unset state (DFLT-00083) -- a ticket created without one is MEDIUM.
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
  // whichever name was configured in "アプリ設定" (see AppSettingsFile.myName)
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
  // Priority (DFLT-00048): always present and one of HIGH/MEDIUM/LOW --
  // MEDIUM when none was given at creation, with pre-existing NULL rows
  // backfilled by the backend's migration (DFLT-00083). Changed via PATCH
  // /api/tickets/{id}'s "priority" field from the ticket header (see
  // PrioritySelect.tsx; it can't be cleared), and used to filter the ticket
  // list (see priorityMeta.ts and App.tsx's filterPriorities).
  priority: TicketPriority;
  // The project labels attached to this ticket (DFLT-00084), sorted by name
  // case-insensitively. The backend always sends an array ([] when none);
  // it is optional here only so a response from an older graph-engine (or a
  // hand-built fixture) without the key is still a valid Ticket -- read it
  // as `ticket.labels ?? []`.
  labels?: Label[];
}

// A label's color is one of a fixed palette (DFLT-00084). The backend stores
// only the key; how each key looks lives in labelMeta.ts.
export type LabelColor =
  | 'gray'
  | 'red'
  | 'orange'
  | 'amber'
  | 'green'
  | 'teal'
  | 'blue'
  | 'indigo'
  | 'purple'
  | 'pink';

// Every LabelColor in palette display order -- must match the Go side's
// domain.LabelColors.
export const LABEL_COLORS: readonly LabelColor[] = [
  'gray',
  'red',
  'orange',
  'amber',
  'green',
  'teal',
  'blue',
  'indigo',
  'purple',
  'pink'
];

// The longest label name the server accepts (Go's domain.MaxLabelNameLength,
// counted there in runes after trimming).
export const LABEL_NAME_MAX_LENGTH = 50;

// A project-scoped label (DFLT-00084). Tickets reference labels by id, so a
// rename or recolor shows on every ticket carrying it.
export interface Label {
  id: string;
  project_id: string;
  name: string;
  color: LabelColor;
  created_at: string;
  updated_at: string;
}

// GET /api/projects/{id}/labels' element: a label plus how many tickets
// carry it (shown in settings and in the in-use delete confirmation).
export interface LabelUsage extends Label {
  ticket_count: number;
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

// GET /api/tickets' element (backend: domain.TicketGraph): a ticket plus its
// execution graph, with no artifacts at all -- not even an empty array, so a
// mistaken read is a type error rather than a silently empty list.
//
// DFLT-00112: the 15s poll used to fetch this list and then one
// GET /api/tickets/{id} per listed ticket, which made a poll "1 + N"
// requests and re-sent every text artifact's whole body although only an
// expanded ticket's panel reads them. Nodes/edges are needed by the
// collapsed cards too (progress bar, node chips, approval-gate highlight,
// the dashboard totals), so they ride along here; artifacts are fetched only
// for expanded tickets, via TicketDetail.
export interface TicketGraph extends Ticket {
  nodes: GraphNode[];
  edges: GraphEdge[];
}

// GET /api/tickets/{id}'s response, and the shape App keeps in state: a
// TicketGraph plus the artifacts. For a ticket nobody has expanded, the
// artifacts are simply an empty array (nothing has been fetched for it yet)
// -- see App.tsx's mergeTicketSummaries.
export interface TicketDetail extends TicketGraph {
  artifacts: Artifact[];
}

// A project scopes a set of tickets to one prefix-based ID namespace (see
// GET/POST /api/projects, backed by packages/core-go/internal/domain.Project).
// prefix is fixed at creation time -- there is no UI or API surface to change
// it afterward.
//
// local_path is where the project lives on *this* environment's disk
// (DFLT-00080). It is not stored in the (possibly team-shared) DB but in the
// server's own home config file, $HOME/.graph-ops/config.json (projectPaths),
// so each member sees their own value here. '' means it is not set in this
// environment ("未設定").
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

// One review gate definition (a reusable review perspective a review_gate
// node can reference by id). additional_criteria is appended to (not
// replacing) whatever criteria was inherited from a lower-priority layer;
// criteria, when set, replaces it outright.
//
// There is no per-gate max_iterations any more (DFLT-00140): the review
// iteration limit is the workflow-wide SettingsDocument.max_iterations.
export interface ReviewGateDef {
  name?: string;
  criteria?: string;
  additional_criteria?: string;
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

// The shape of the user tier's own override file -- what GET
// /api/settings/catalog's tier_document returns, and what PUT's body submits
// back.
//
// max_iterations is the workflow-wide review iteration limit (the maximum
// number of review rounds, counting the first review): 3, 4 or 5. Absent or
// null means "inherit" (the plugin default, 3).
export interface SettingsDocument {
  version: number;
  review_gates?: Record<string, ReviewGateDef>;
  workflow?: WorkflowDef;
  max_iterations?: number | null;
}

// A merged, ready-to-use catalog: the plugin default plus the user tier
// (merged_catalog), or the plugin default alone -- what the settings screen
// would fall back to if the user tier's override were cleared
// (inherited_catalog). Neither includes a teamExtensionsDir team tier, which
// an agent does additionally see; see the server's handleGetSettingsCatalog.
export interface SettingsCatalog {
  review_gates: Record<string, ReviewGateDef>;
  nodes: NodeDef[];
  seed?: string[];
  max_iterations?: number | null;
}

// The warning codes GET /api/settings/catalog can put in `warnings`. Both are
// packages/core-go/internal/config/schema.go's Warn* constants, and each has
// a `settings.reviewGates.warning*` message beside it in the translation
// catalogues: the server never sends a sentence, so the screen shows its own
// wording in the UI's language (DFLT-00140).
export const SETTINGS_CATALOG_WARNINGS = {
  // A review gate (gate_id) still sets the retired per-gate max_iterations,
  // which is ignored. Saving from the screen drops it.
  legacyGateMaxIterations: 'LEGACY_GATE_MAX_ITERATIONS',
  // The user tier's own top-level max_iterations (value) is not 3, 4 or 5 --
  // a hand edit. Agents refuse to load such a file; choosing a valid value
  // (or inherit) and saving fixes it.
  maxIterationsOutOfRange: 'MAX_ITERATIONS_OUT_OF_RANGE'
} as const;

// One problem found in the user tier: a code plus the details its message
// needs. A code the client does not know is simply not shown.
export interface SettingsCatalogWarning {
  code: string;
  gate_id?: string;
  value?: number;
}

// warnings lists what is wrong with the user tier -- see
// SETTINGS_CATALOG_WARNINGS for the codes.
export interface SettingsCatalogResponse {
  tier_document: SettingsDocument;
  merged_catalog: SettingsCatalog;
  inherited_catalog: SettingsCatalog;
  warnings?: SettingsCatalogWarning[];
}

export interface SettingsNodeTypeInfo {
  type: string;
  has_default: boolean;
  has_user_override: boolean;
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
}

// GET/PUT /api/settings/skills/{name} -- mirrors SettingsNodeTypeTextResponse.
export interface SettingsSkillTextResponse {
  name: string;
  tier_text: string;
  merged_text: string;
}

// GET/PUT /api/settings/{plan,review,report}-template. tier_text is the user
// tier's own override ("" if unset: Markdown for plan/review, HTML for
// report); merged_text is the resolved template (user override -> plugin
// default -- a full replace, not an append, unlike node-type/skill context).
export interface SettingsTemplateTextResponse {
  tier_text: string;
  merged_text: string;
}

// The report template's response has the same shape; the name is kept for
// existing references.
export type SettingsReportTemplateResponse = SettingsTemplateTextResponse;

// --- App settings (GET/PUT /api/settings/app) ---
// The server/CLI's own operational settings ($HOME/.graph-ops/config.json),
// edited from the app-settings tab -- distinct from the
// node-type/workflow/review-gate Settings UI above. None of these fields
// take effect for the already-running server; they're read once at startup,
// so `effective` (this server's actual current values) will keep showing
// the pre-edit values until it's restarted.

// AppSettingsFile mirrors packages/core-go/internal/runtimeconfig.FileConfig
// (the home config file's shape) -- only the fields this tab edits are listed
// here; the others (port/claudeBinary/terminalCommand/workDir/
// teamExtensionsDir/projectPaths) are preserved server-side but never
// surfaced in this UI (projectPaths is edited through the project API as
// Project.local_path instead). An empty string means "not set, falls back to an env var or a
// hardcoded default".
// 'http' is the HTTP custom data source (DFLT-00088): a plugin server that
// implements docs/http-datasource/openapi.yaml.
export type DBBackend = 'sqlite' | 'mysql' | 'http';

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
  // HTTP custom data source (dbBackend 'http', DFLT-00088). The URL is not a
  // secret. The token follows mysqlPassword's rules exactly: the server
  // sends '', a '${ENV_VAR_NAME}' reference, or REDACTED_SECRET_PLACEHOLDER,
  // and sending the placeholder (or the reference) back means "keep the
  // stored token" -- but only while httpDataSourceUrl is unchanged.
  httpDataSourceUrl?: string;
  httpDataSourceToken?: string;
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
  // Present only when dbBackend is 'http'. The token is never included.
  httpDataSourceUrl?: string;
  artifactsDir: string;
  userExtensionsDir: string;
  paginationPageSize: number;
}

// The warning codes GET/PUT /api/settings/app can put in `warnings`, spelled
// once here rather than at each comparison. Both are
// packages/core-go/internal/httpserver/app_settings.go's constants of the
// same name, and each has a `settings.appSettings.*` message beside it in
// the translation catalogues -- a code is never a sentence, so a code with
// no message would show the user nothing at all.
export const APP_SETTINGS_WARNINGS = {
  // $HOME/.graph-ops/config.json exists but could not be read or parsed, so
  // every setting on this page is coming from nowhere -- not into this
  // response and not into the next startup either. A save cannot fix it:
  // repairing or removing that file is the only way out, which is why the
  // same condition is an error code (not a warning) on a PUT.
  //
  // It is the only warning code left. HOME_CONFIG_UNAVAILABLE used to be
  // another, meaning "saved, except the one field we had nowhere to put";
  // with one destination for every field there is no such partial outcome,
  // so it became an error code instead (DFLT-00124).
  homeConfigUnreadable: 'HOME_CONFIG_UNREADABLE'
} as const;

export type AppSettingsWarning = (typeof APP_SETTINGS_WARNINGS)[keyof typeof APP_SETTINGS_WARNINGS];

export interface AppSettingsResponse {
  file: AppSettingsFile;
  effective: EffectiveAppSettings;
  // The one file this page reads and writes: $HOME/.graph-ops/config.json.
  // '' when the home directory could not be resolved -- the server answers
  // an empty string rather than inventing a path that does not exist, and
  // the form shows its own wording for that case.
  config_path: string;
  // Fixed codes for things the server did not do on a request it still
  // completed -- see AppSettingsWarning for the ones defined so far. A code
  // the client does not know is simply not shown, so this stays string[].
  warnings?: string[];
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
