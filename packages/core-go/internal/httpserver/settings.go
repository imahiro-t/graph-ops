package httpserver

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/graph-ops/core-go/internal/config"
	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
)

// settingsScope is the resolved (validated) user-/team-tier roots one
// settings API request operates against, plus which tier ("global" or
// "project") the request's document/text edits actually target.
//
//   - scope "global" edits the user tier ($HOME/.graph-ops, or
//     GRAPH_USER_EXTENSIONS_DIR) -- team is still resolved (so mergedCatalog
//     previews can include it when a project happens to be given) but
//     writes never touch it.
//   - scope "project" edits the team tier resolved from the given DB
//     Project's work_dir -- see internal/config.ResolveRootsForProjectWorkDir
//     and the execution plan's "重要な設計課題" section: this is
//     deliberately NOT the server process's own cwd, so the settings UI's
//     "project-scoped" tier tracks whichever DB Project is selected in the
//     UI, not wherever the server binary happens to be running.
type settingsScope struct {
	Scope     string // "global" | "project"
	UserRoot  string
	TeamRoot  string // "" if not resolvable (no project selected / no $HOME)
	ProjectID string
}

// resolveSettingsScope validates scope/project_id query (or body) values and
// resolves the corresponding roots. teamDirOverride (GRAPH_TEAM_EXTENSIONS_DIR
// / graph-config.json) always wins over a project's work_dir, mirroring
// ResolveRoots' own precedence, so an operator's explicit configuration is
// never silently bypassed by picking a different project in the UI.
func (s *Server) resolveSettingsScope(scope, projectID string) (settingsScope, error) {
	if scope != "global" && scope != "project" {
		return settingsScope{}, domain.NewAPIError(domain.ErrCodeInvalidScope, "scope must be \"global\" or \"project\", got %q", scope)
	}

	baseRoots, err := s.resolveRoots()
	if err != nil {
		return settingsScope{}, err
	}
	out := settingsScope{Scope: scope, UserRoot: baseRoots.UserDir, ProjectID: projectID}

	if scope == "project" {
		if projectID == "" {
			return settingsScope{}, domain.NewAPIError(domain.ErrCodeInvalidScope, "project_id is required for scope=project")
		}
		if s.cfg.TeamExtensionsDir != "" {
			out.TeamRoot = s.cfg.TeamExtensionsDir
		} else {
			project, err := s.repo.GetProject(projectID)
			if err != nil {
				return settingsScope{}, err
			}
			if project == nil {
				return settingsScope{}, domain.NewAPIError(domain.ErrCodeProjectNotFound, "project not found: %s", projectID)
			}
			teamRoot, err := config.ProjectTeamRoot(project.WorkDir)
			if err != nil {
				return settingsScope{}, err
			}
			out.TeamRoot = teamRoot
		}
	}

	return out, nil
}

// mergedDocuments returns def plus every tier up to and including this
// scope: user only for "global", user+team for "project". This is the
// settings UI's "継承後のプレビュー" -- what an agent would actually see if
// this scope's document/text were exactly what's currently on disk.
//
// def is supplied by the caller (already resolved/localized via
// config.ResolveLanguage + config.LocalizedDefault against exactly the
// Document set this scope can see -- see handleGetSettingsCatalog) rather
// than fetched in here as a bare config.DefaultDocument(): unlike
// LoadWithRoots' callers, this package doesn't go through LoadWithRoots at
// all, so language resolution has to be done explicitly at each call site
// (execution plan, section 1.3/1.6).
func (sc settingsScope) mergedDocuments(def, userDoc, teamDoc config.Document) []config.Document {
	docs := []config.Document{def, userDoc}
	if sc.Scope == "project" {
		docs = append(docs, teamDoc)
	}
	return docs
}

// tierDocumentPath returns the path this scope's own Document file (the one
// GET returns as tier_document / PUT overwrites) lives at.
func (sc settingsScope) tierDocumentPath() string {
	if sc.Scope == "global" {
		return config.UserDocumentPath(sc.UserRoot)
	}
	return config.TeamDocumentPath(sc.TeamRoot)
}

// tierExtensionRoot returns the root this scope's extensions/ (node-type
// overrides, report template override) writes should target.
func (sc settingsScope) tierExtensionRoot() string {
	if sc.Scope == "global" {
		return sc.UserRoot
	}
	return sc.TeamRoot
}

// mergeRoots are the roots every "merged preview" in this file resolves
// against: this scope's user root, plus its team root only when the scope is
// "project" (a global-scope preview never depends on any one project's team
// tier).
func (sc settingsScope) mergeRoots() config.Roots {
	roots := config.Roots{UserDir: sc.UserRoot}
	if sc.Scope == "project" {
		roots.TeamDir = sc.TeamRoot
	}
	return roots
}

func scopeAndProjectFromQuery(r *http.Request) (string, string) {
	return r.URL.Query().Get("scope"), r.URL.Query().Get("project_id")
}

// listScopeRoots resolves the roots the two "list everything" endpoints
// (node types, skills) need: the user root always, plus the team root of an
// optional project_id query parameter. Unlike the tier-specific endpoints
// these two take no scope -- an absent project_id simply means "no project
// context", which yields an empty team root rather than an error. It writes
// the error response itself and reports ok=false when the caller should stop.
func (s *Server) listScopeRoots(w http.ResponseWriter, r *http.Request) (userRoot, teamRoot string, ok bool) {
	if projectID := r.URL.Query().Get("project_id"); projectID != "" {
		sc, err := s.resolveSettingsScope("project", projectID)
		if err != nil {
			writeError(w, statusForError(err, http.StatusBadRequest), err)
			return "", "", false
		}
		teamRoot = sc.TeamRoot
	}
	roots, err := s.resolveRoots()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return "", "", false
	}
	return roots.UserDir, teamRoot, true
}

// handleGetSettingsCatalog returns this scope's own raw Document
// (tier_document), the fully-merged catalog through this scope
// (merged_catalog, a preview of what an agent actually sees), and the
// catalog merged through every tier BELOW this scope (inherited_catalog, so
// the UI can show "what you'd fall back to if you cleared this scope's
// override").
func (s *Server) handleGetSettingsCatalog(w http.ResponseWriter, r *http.Request) {
	scopeStr, projectID := scopeAndProjectFromQuery(r)
	sc, err := s.resolveSettingsScope(scopeStr, projectID)
	if err != nil {
		writeError(w, statusForError(err, http.StatusBadRequest), err)
		return
	}

	tierDoc, err := config.LoadDocumentAt(sc.tierDocumentPath())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	userDoc, err := config.LoadDocumentAt(config.UserDocumentPath(sc.UserRoot))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	var teamDoc config.Document
	if sc.TeamRoot != "" {
		teamDoc, err = config.LoadDocumentAt(config.TeamDocumentPath(sc.TeamRoot))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}

	// The language that localizes merged_catalog's fixed skeleton/default
	// review-gate names must be resolved from exactly the Document set this
	// scope can see (execution plan, section 1.6's design principle): global
	// sees only userDoc, project sees userDoc+teamDoc (team winning, same
	// "later argument wins" convention as config.Merge/ResolveLanguage).
	var mergedLang string
	if sc.Scope == "project" {
		mergedLang = config.ResolveLanguage("", userDoc, teamDoc)
	} else {
		mergedLang = config.ResolveLanguage("", userDoc)
	}
	mergedDef, err := config.LocalizedDefault(mergedLang)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	merged := config.Merge(sc.mergedDocuments(mergedDef, userDoc, teamDoc)...)

	// Every tier strictly BELOW this scope: the plugin default alone for
	// "global" (no language resolution -- there is nothing below it to
	// resolve one from), plus the user tier for "project".
	var inheritedDocs []config.Document
	if sc.Scope == "global" {
		def, err := config.DefaultDocument()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		inheritedDocs = []config.Document{def}
	} else {
		inheritedLang := config.ResolveLanguage("", userDoc)
		inheritedDef, err := config.LocalizedDefault(inheritedLang)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		inheritedDocs = []config.Document{inheritedDef, userDoc}
	}
	inherited := config.Merge(inheritedDocs...)

	writeJSON(w, http.StatusOK, map[string]any{
		"tier_document":      tierDoc,
		"merged_catalog":     merged,
		"inherited_catalog":  inherited,
		"scope":              sc.Scope,
		"project_id":         sc.ProjectID,
		"team_root_resolved": sc.TeamRoot != "",
		"resolved_language":  mergedLang,
	})
}

// handlePutSettingsCatalog validates the submitted document against the
// other tier(s) before writing anything: engine.ValidateCatalog runs on the
// candidate full merge (exactly what GetExecutableNodes/ExpandGraph would
// build from), so a broken edit (duplicate id, dangling depends_on/
// loop_back_to/gate reference, a depends_on cycle) is rejected with no
// write to disk at all.
func (s *Server) handlePutSettingsCatalog(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Scope     string          `json:"scope"`
		ProjectID string          `json:"project_id"`
		Document  config.Document `json:"document"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	sc, err := s.resolveSettingsScope(body.Scope, body.ProjectID)
	if err != nil {
		writeError(w, statusForError(err, http.StatusBadRequest), err)
		return
	}
	if err := validateMaxIterations(body.Document); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := validateNoWorkflowOverride(body.Document); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	userDoc, err := config.LoadDocumentAt(config.UserDocumentPath(sc.UserRoot))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	var teamDoc config.Document
	if sc.Scope == "project" {
		teamDoc = body.Document
	} else if sc.TeamRoot != "" {
		teamDoc, err = config.LoadDocumentAt(config.TeamDocumentPath(sc.TeamRoot))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}
	if sc.Scope == "global" {
		userDoc = body.Document
	}

	// Resolved from userDoc/teamDoc AFTER the scope-appropriate substitution
	// of body.Document above, so a submitted language change takes effect in
	// this same response's merged_catalog (execution plan, section 1.6's
	// handlePutSettingsCatalog row) rather than requiring a follow-up GET.
	lang := config.ResolveLanguage("", userDoc, teamDoc)
	def, err := config.LocalizedDefault(lang)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	candidate := config.Merge(def, userDoc, teamDoc)
	if err := engine.ValidateCatalog(candidate); err != nil {
		apiErr := classifyCatalogError(err)
		writeError(w, http.StatusBadRequest, apiErr)
		return
	}

	if err := config.SaveDocumentAt(sc.tierDocumentPath(), body.Document); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"merged_catalog": candidate})
}

// validateMaxIterations enforces "1 or more" for every review gate's
// max_iterations in doc (nil is fine -- it means "inherit"; 0 or negative is
// not, since GraphEngine.CompleteNode would otherwise block a ticket after
// its very first loop-back iteration, which is never what a UI-entered "0"
// or a stray negative number was meant to express).
func validateMaxIterations(doc config.Document) error {
	for id, gate := range doc.ReviewGates {
		if gate.MaxIterations != nil && *gate.MaxIterations < 1 {
			return domain.NewAPIError(domain.ErrCodeInvalidMaxIterations,
				"review gate %q: max_iterations must be 1 or greater, got %d", id, *gate.MaxIterations)
		}
	}
	return nil
}

// validateNoWorkflowOverride rejects a submitted document that sets
// workflow.nodes or workflow.seed: the skeleton graph and its seed are fixed
// by the plugin default only (see config.Merge), so accepting either field
// here would silently save an override that never takes effect. Document.
// Language is deliberately NOT checked here -- unlike workflow.nodes/seed it
// is a normal, always-overridable field at either tier (execution plan,
// section 1.1), so a submitted language change is always accepted.
func validateNoWorkflowOverride(doc config.Document) error {
	if len(doc.Workflow.Nodes) > 0 || len(doc.Workflow.Seed) > 0 {
		return domain.NewAPIError(domain.ErrCodeWorkflowNodesLocked,
			"workflow.nodes and workflow.seed are fixed by the plugin default and can no longer be overridden at any tier")
	}
	return nil
}

// classifyCatalogError maps an engine.ValidateCatalog error (a plain Go
// error from the shared buildPlan validation -- see internal/engine/dag.go)
// to a specific domain.ErrorCode by inspecting its message, so the frontend
// can show a distinct localized message for "cycle" vs. "unknown reference"
// vs. "duplicate id" instead of one generic validation-failed string (see
// the Gherkin spec's dedicated scenarios for each). buildPlan doesn't (and
// per its own doc comment, deliberately can't -- internal/engine has no
// domain-error dependency) return typed sentinel errors, so this is a
// best-effort text classification rather than an errors.As switch; any
// message that doesn't match a known pattern still surfaces as a 400 with
// the generic catalog-invalid code, never silently swallowed.
func classifyCatalogError(err error) *domain.APIError {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "cycle"):
		return domain.NewAPIError(domain.ErrCodeCatalogCycleDetected, "%s", msg)
	case strings.Contains(msg, "duplicate node id"):
		return domain.NewAPIError(domain.ErrCodeCatalogDuplicateNode, "%s", msg)
	case strings.Contains(msg, "unknown node") || strings.Contains(msg, "unknown review gate"):
		return domain.NewAPIError(domain.ErrCodeCatalogUnknownReference, "%s", msg)
	default:
		return domain.NewAPIError(domain.ErrCodeCatalogInvalidDocument, "%s", msg)
	}
}

// handleListSettingsNodeTypes returns every known node type (built-in plus
// any custom type appearing in the merged catalog) alongside, for each, the
// resolved has_default/has_user_override/has_team_override flags -- the
// left-hand list in the settings UI's ノードタブ. project_id is optional
// here (unlike the tier-specific endpoints below): when supplied,
// has_team_override reflects that project's team tier; when omitted,
// has_team_override is always false (there is no project context to check).
func (s *Server) handleListSettingsNodeTypes(w http.ResponseWriter, r *http.Request) {
	userRoot, teamRoot, ok := s.listScopeRoots(w, r)
	if !ok {
		return
	}

	// userDoc/teamDoc must be read BEFORE resolving def: unlike the other
	// three handlers in this file, this one used to read the plugin default
	// first, which made it impossible to resolve a language from
	// userDoc/teamDoc before they existed. See the execution plan's section
	// 1.6/2.2 note on this handler needing a genuine reordering, not just a
	// drop-in two-line replacement like handleGetSettingsCatalog/
	// handlePutSettingsCatalog got.
	userDoc, err := config.LoadDocumentAt(config.UserDocumentPath(userRoot))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	var teamDoc config.Document
	if teamRoot != "" {
		teamDoc, err = config.LoadDocumentAt(config.TeamDocumentPath(teamRoot))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}
	lang := config.ResolveLanguage("", userDoc, teamDoc)
	def, err := config.LocalizedDefault(lang)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	catalog := config.Merge(def, userDoc, teamDoc)

	type typeInfo struct {
		Type            string `json:"type"`
		HasDefault      bool   `json:"has_default"`
		HasUserOverride bool   `json:"has_user_override"`
		HasTeamOverride bool   `json:"has_team_override"`
	}
	types := config.ListKnownNodeTypes(catalog)
	// Union in any type that has an override file but isn't (yet) referenced
	// by a catalog node -- a brand-new custom type created purely through
	// the settings UI's ノード種別 tab (no plugin default, no workflow node
	// using it yet) would otherwise vanish from this list on the very next
	// reload even though its instructions file still exists on disk. See
	// ListNodeTypeOverrideNames' doc comment.
	seen := make(map[string]bool, len(types))
	for _, t := range types {
		seen[t] = true
	}
	for _, t := range config.ListNodeTypeOverrideNames(userRoot) {
		if !seen[t] {
			seen[t] = true
			types = append(types, t)
		}
	}
	if teamRoot != "" {
		for _, t := range config.ListNodeTypeOverrideNames(teamRoot) {
			if !seen[t] {
				seen[t] = true
				types = append(types, t)
			}
		}
	}
	out := make([]typeInfo, 0, len(types))
	for _, t := range types {
		_, hasUser := config.NodeTypeTierText(userRoot, t)
		var hasTeam bool
		if teamRoot != "" {
			_, hasTeam = config.NodeTypeTierText(teamRoot, t)
		}
		out = append(out, typeInfo{
			Type:            t,
			HasDefault:      config.ResolveNodeTypeContext(config.Roots{}, t) != "",
			HasUserOverride: hasUser,
			HasTeamOverride: hasTeam,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"types": out})
}

// handleGetSettingsNodeType returns this scope's own override text
// (tier_text) for one node type plus the merged preview through this scope
// (merged_text) -- the ノードタブ's right-hand editor pane.
func (s *Server) handleGetSettingsNodeType(w http.ResponseWriter, r *http.Request) {
	nodeType := r.PathValue("type")
	scopeStr, projectID := scopeAndProjectFromQuery(r)
	sc, err := s.resolveSettingsScope(scopeStr, projectID)
	if err != nil {
		writeError(w, statusForError(err, http.StatusBadRequest), err)
		return
	}

	tierText, _ := config.NodeTypeTierText(sc.tierExtensionRoot(), nodeType)
	mergedText := config.ResolveNodeTypeContext(sc.mergeRoots(), nodeType)

	writeJSON(w, http.StatusOK, map[string]any{
		"type":        nodeType,
		"tier_text":   tierText,
		"merged_text": mergedText,
	})
}

// handlePutSettingsNodeType saves (or, given empty text, clears -- see
// config.WriteExtensionText) this scope's override text for one node type.
func (s *Server) handlePutSettingsNodeType(w http.ResponseWriter, r *http.Request) {
	nodeType := r.PathValue("type")
	var body struct {
		Scope     string `json:"scope"`
		ProjectID string `json:"project_id"`
		Text      string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	sc, err := s.resolveSettingsScope(body.Scope, body.ProjectID)
	if err != nil {
		writeError(w, statusForError(err, http.StatusBadRequest), err)
		return
	}

	if err := config.WriteExtensionText(sc.tierExtensionRoot(), config.NodeTypesSubdir, nodeType+".md", body.Text); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	tierText, _ := config.NodeTypeTierText(sc.tierExtensionRoot(), nodeType)
	mergedText := config.ResolveNodeTypeContext(sc.mergeRoots(), nodeType)
	writeJSON(w, http.StatusOK, map[string]any{
		"type":        nodeType,
		"tier_text":   tierText,
		"merged_text": mergedText,
	})
}

// handleListSettingsSkills returns the fixed set of plugin skills
// (config.ListKnownSkills) alongside, for each, whether this user/team tier
// has a supplementary-instruction override set -- the left-hand list in the
// settings UI's スキル tab. project_id is optional (unlike the tier-specific
// endpoints below): when supplied, has_team_override reflects that
// project's team tier; when omitted, has_team_override is always false.
func (s *Server) handleListSettingsSkills(w http.ResponseWriter, r *http.Request) {
	userRoot, teamRoot, ok := s.listScopeRoots(w, r)
	if !ok {
		return
	}

	type skillInfo struct {
		Name            string `json:"name"`
		HasUserOverride bool   `json:"has_user_override"`
		HasTeamOverride bool   `json:"has_team_override"`
	}
	names := config.ListKnownSkills()
	out := make([]skillInfo, 0, len(names))
	for _, name := range names {
		_, hasUser := config.SkillTierText(userRoot, name)
		var hasTeam bool
		if teamRoot != "" {
			_, hasTeam = config.SkillTierText(teamRoot, name)
		}
		out = append(out, skillInfo{
			Name:            name,
			HasUserOverride: hasUser,
			HasTeamOverride: hasTeam,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"skills": out})
}

// handleGetSettingsSkill returns this scope's own override text (tier_text)
// for one skill plus the merged preview through this scope (merged_text) --
// the スキル tab's right-hand editor pane.
func (s *Server) handleGetSettingsSkill(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	scopeStr, projectID := scopeAndProjectFromQuery(r)
	sc, err := s.resolveSettingsScope(scopeStr, projectID)
	if err != nil {
		writeError(w, statusForError(err, http.StatusBadRequest), err)
		return
	}

	tierText, _ := config.SkillTierText(sc.tierExtensionRoot(), name)
	mergedText := config.ResolveSkillContext(sc.mergeRoots(), name)

	writeJSON(w, http.StatusOK, map[string]any{
		"name":        name,
		"tier_text":   tierText,
		"merged_text": mergedText,
	})
}

// handlePutSettingsSkill saves (or, given empty text, clears -- see
// config.WriteExtensionText) this scope's supplementary-instruction override
// for one skill.
func (s *Server) handlePutSettingsSkill(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct {
		Scope     string `json:"scope"`
		ProjectID string `json:"project_id"`
		Text      string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	sc, err := s.resolveSettingsScope(body.Scope, body.ProjectID)
	if err != nil {
		writeError(w, statusForError(err, http.StatusBadRequest), err)
		return
	}

	if err := config.WriteExtensionText(sc.tierExtensionRoot(), config.SkillsSubdir, name+".md", body.Text); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	tierText, _ := config.SkillTierText(sc.tierExtensionRoot(), name)
	mergedText := config.ResolveSkillContext(sc.mergeRoots(), name)
	writeJSON(w, http.StatusOK, map[string]any{
		"name":        name,
		"tier_text":   tierText,
		"merged_text": mergedText,
	})
}

// handleGetSettingsReportTemplate returns this scope's own report-template
// override HTML (tier_text, "" if unset) plus the merged/resolved template
// (merged_text -- team override, else user override, else the plugin
// default; see config.ResolveReportTemplate) -- the レポートテンプレート
// tab's editor pane.
func (s *Server) handleGetSettingsReportTemplate(w http.ResponseWriter, r *http.Request) {
	scopeStr, projectID := scopeAndProjectFromQuery(r)
	sc, err := s.resolveSettingsScope(scopeStr, projectID)
	if err != nil {
		writeError(w, statusForError(err, http.StatusBadRequest), err)
		return
	}

	tierText, _ := config.ReportTemplateTierText(sc.tierExtensionRoot())
	mergedText := config.ResolveReportTemplate(sc.mergeRoots())

	writeJSON(w, http.StatusOK, map[string]any{
		"tier_text":   tierText,
		"merged_text": mergedText,
	})
}

// handlePutSettingsReportTemplate validates and saves (or, given empty html,
// clears) this scope's report-template override. The body field is named
// "html" (not "text") to make it clear in the API shape itself -- matching
// the "保存すると丸ごと置き換わる（追記ではない）" requirement -- that this
// is a full replace, unlike the append-by-default node-type/skill overrides.
// A validation failure (config.ValidateReportHTML) is rejected with 400 and
// nothing is written to disk.
func (s *Server) handlePutSettingsReportTemplate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Scope     string `json:"scope"`
		ProjectID string `json:"project_id"`
		HTML      string `json:"html"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	sc, err := s.resolveSettingsScope(body.Scope, body.ProjectID)
	if err != nil {
		writeError(w, statusForError(err, http.StatusBadRequest), err)
		return
	}

	if body.HTML != "" {
		if err := config.ValidateReportHTML(body.HTML); err != nil {
			apiErr := domain.NewAPIError(domain.ErrCodeInvalidReportTemplate, "%s", err)
			writeError(w, statusForError(apiErr, http.StatusBadRequest), apiErr)
			return
		}
	}

	if err := config.WriteExtensionText(sc.tierExtensionRoot(), config.ReportSubdir, config.ReportTemplateFile, body.HTML); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	tierText, _ := config.ReportTemplateTierText(sc.tierExtensionRoot())
	mergedText := config.ResolveReportTemplate(sc.mergeRoots())
	writeJSON(w, http.StatusOK, map[string]any{
		"tier_text":   tierText,
		"merged_text": mergedText,
	})
}
