package httpserver

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/graph-ops/core-go/internal/config"
	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
)

// settingsScope is the one tier the settings API reads and writes: the user
// tier's root ($HOME/.graph-ops, or whatever userExtensionsDir /
// GRAPH_USER_EXTENSIONS_DIR names).
//
// It used to carry a scope ("global" | "project"), a team root and a project
// id, because a request could ask to edit a per-project team tier resolved
// from that project's local path (DFLT-00080). That whole mechanism is gone
// (DFLT-00124, completion criterion 7): settings live in one place per user,
// and a team shares them by pointing teamExtensionsDir at a shared directory
// -- a directory this API deliberately does not edit, since it is shared
// state that belongs to whoever curates it, not to whoever happens to open
// the settings screen.
//
// The type survives the collapse to one field because it is still what says,
// at every call site, WHICH root a read or a write is aimed at; a bare string
// passed around the eight handler pairs would not.
type settingsScope struct {
	UserRoot string
}

// settingsUserScope resolves the user-tier root this request operates on.
//
// It cannot fail: config.ResolveRoots is pure string handling over the two
// configured overrides (DFLT-00124), so there is no error for the handlers
// to turn into a 500 -- and hence no ok/error dance at the top of each of
// them.
func (s *Server) settingsUserScope() settingsScope {
	return settingsScope{UserRoot: s.resolveRoots().UserDir}
}

// mergedDocuments returns def plus the user tier -- the settings UI's
// "継承後のプレビュー".
//
// It shows the plugin default merged with the user tier, and nothing else.
// A team tier configured through teamExtensionsDir does take effect for
// agents, but it is not previewed here: this screen edits the user tier, and
// a preview that silently folded in a shared directory would show edits the
// user cannot make from this screen. See handleGetSettingsCatalog.
//
// def is supplied by the caller (already resolved/localized via
// config.ResolveLanguage + config.LocalizedDefault against exactly the
// Document set this preview covers -- see handleGetSettingsCatalog) rather
// than fetched in here as a bare config.DefaultDocument(): unlike
// LoadWithRoots' callers, this package doesn't go through LoadWithRoots at
// all, so language resolution has to be done explicitly at each call site
// (execution plan, section 1.3/1.6).
func mergedDocuments(def, userDoc config.Document) []config.Document {
	return []config.Document{def, userDoc}
}

// tierDocumentPath returns the path this scope's own Document file (the one
// GET returns as tier_document / PUT overwrites) lives at.
func (sc settingsScope) tierDocumentPath() string {
	return config.UserDocumentPath(sc.UserRoot)
}

// tierExtensionRoot returns the root this scope's extensions/ (node-type
// overrides, report template override) writes should target.
func (sc settingsScope) tierExtensionRoot() string {
	return sc.UserRoot
}

// mergeRoots are the roots every "merged preview" in this file resolves
// against: the user root alone. The team root is deliberately left out --
// see mergedDocuments for why this screen previews only what it can edit.
func (sc settingsScope) mergeRoots() config.Roots {
	return config.Roots{UserDir: sc.UserRoot}
}

// handleGetSettingsCatalog returns the user tier's own raw Document
// (tier_document), that tier merged onto the plugin default
// (merged_catalog), and the plugin default alone (inherited_catalog, so the
// UI can show "what you'd fall back to if you cleared this override").
//
// merged_catalog is a preview of the plugin default plus the user tier -- NOT
// of everything an agent sees. When teamExtensionsDir is configured, agents
// additionally get that team tier merged on top (see config.LoadWithRoots),
// and it is not shown here: this endpoint edits the user tier, and the team
// tier is a shared directory the settings screen deliberately does not touch
// (DFLT-00124, plan review condition F-2).
func (s *Server) handleGetSettingsCatalog(w http.ResponseWriter, r *http.Request) {
	sc := s.settingsUserScope()

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
	// The language that localizes merged_catalog's fixed skeleton/default
	// review-gate names is resolved from exactly the Document set this
	// preview covers (execution plan, section 1.6's design principle): the
	// user tier.
	mergedLang := config.ResolveLanguage("", userDoc)
	mergedDef, err := config.LocalizedDefault(mergedLang)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	merged := config.Merge(mergedDocuments(mergedDef, userDoc)...)

	// Everything strictly BELOW the user tier: the plugin default alone (no
	// language resolution -- there is nothing below it to resolve one from).
	def, err := config.DefaultDocument()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	inherited := config.Merge(def)

	writeJSON(w, http.StatusOK, map[string]any{
		"tier_document":     tierDoc,
		"merged_catalog":    merged,
		"inherited_catalog": inherited,
		"resolved_language": mergedLang,
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
		Document config.Document `json:"document"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	sc := s.settingsUserScope()
	if err := validateMaxIterations(body.Document); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := validateNoWorkflowOverride(body.Document); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	// Resolved from the submitted document, so a submitted language change
	// takes effect in this same response's merged_catalog (execution plan,
	// section 1.6's handlePutSettingsCatalog row) rather than requiring a
	// follow-up GET.
	userDoc := body.Document
	lang := config.ResolveLanguage("", userDoc)
	def, err := config.LocalizedDefault(lang)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	candidate := config.Merge(def, userDoc)
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
// resolved has_default/has_user_override flags -- the left-hand list in the
// settings UI's ノードタブ.
func (s *Server) handleListSettingsNodeTypes(w http.ResponseWriter, r *http.Request) {
	sc := s.settingsUserScope()
	userRoot := sc.UserRoot

	// userDoc must be read BEFORE resolving def: unlike the other three
	// handlers in this file, this one used to read the plugin default first,
	// which made it impossible to resolve a language from userDoc before it
	// existed. See the execution plan's section 1.6/2.2 note on this handler
	// needing a genuine reordering, not just a drop-in two-line replacement
	// like handleGetSettingsCatalog/handlePutSettingsCatalog got.
	userDoc, err := config.LoadDocumentAt(config.UserDocumentPath(userRoot))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	lang := config.ResolveLanguage("", userDoc)
	def, err := config.LocalizedDefault(lang)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	catalog := config.Merge(def, userDoc)

	type typeInfo struct {
		Type            string `json:"type"`
		HasDefault      bool   `json:"has_default"`
		HasUserOverride bool   `json:"has_user_override"`
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
	out := make([]typeInfo, 0, len(types))
	for _, t := range types {
		_, hasUser := config.NodeTypeTierText(userRoot, t)
		out = append(out, typeInfo{
			Type:            t,
			HasDefault:      config.ResolveNodeTypeContext(config.Roots{}, t) != "",
			HasUserOverride: hasUser,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"types": out})
}

// handleGetSettingsNodeType returns this scope's own override text
// (tier_text) for one node type plus the merged preview through this scope
// (merged_text) -- the ノードタブ's right-hand editor pane.
func (s *Server) handleGetSettingsNodeType(w http.ResponseWriter, r *http.Request) {
	nodeType := r.PathValue("type")
	sc := s.settingsUserScope()

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
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	sc := s.settingsUserScope()

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
// (config.ListKnownSkills) alongside, for each, whether the user tier has a
// supplementary-instruction override set -- the left-hand list in the
// settings UI's スキル tab.
func (s *Server) handleListSettingsSkills(w http.ResponseWriter, r *http.Request) {
	sc := s.settingsUserScope()

	type skillInfo struct {
		Name            string `json:"name"`
		HasUserOverride bool   `json:"has_user_override"`
	}
	names := config.ListKnownSkills()
	out := make([]skillInfo, 0, len(names))
	for _, name := range names {
		_, hasUser := config.SkillTierText(sc.UserRoot, name)
		out = append(out, skillInfo{
			Name:            name,
			HasUserOverride: hasUser,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"skills": out})
}

// handleGetSettingsSkill returns this scope's own override text (tier_text)
// for one skill plus the merged preview through this scope (merged_text) --
// the スキル tab's right-hand editor pane.
func (s *Server) handleGetSettingsSkill(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	sc := s.settingsUserScope()

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
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	sc := s.settingsUserScope()

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
// override HTML (tier_text, "" if unset) plus the resolved template
// (merged_text -- the user override, else the plugin default; see
// config.ResolveReportTemplate) -- the editor pane for the テンプレート tab's
// レポート entry.
func (s *Server) handleGetSettingsReportTemplate(w http.ResponseWriter, r *http.Request) {
	sc := s.settingsUserScope()

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
		HTML string `json:"html"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	sc := s.settingsUserScope()

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

// markdownTemplateTarget describes one of the fixed Markdown templates (plan,
// review) the settings UI's テンプレート tab edits. The two differ only in
// where the override file lives and which config accessors read it back, so
// one get/put handler pair serves both. The report template keeps its own
// handlers above because it validates its content and names the body field
// "html".
type markdownTemplateTarget struct {
	subdir   string
	file     string
	tierText func(root string) (string, bool)
	resolve  func(roots config.Roots) string
}

var (
	planTemplateTarget = markdownTemplateTarget{
		subdir:   config.PlanSubdir,
		file:     config.PlanTemplateFile,
		tierText: config.PlanTemplateTierText,
		resolve:  config.ResolvePlanTemplate,
	}
	reviewTemplateTarget = markdownTemplateTarget{
		subdir:   config.ReviewSubdir,
		file:     config.ReviewTemplateFile,
		tierText: config.ReviewTemplateTierText,
		resolve:  config.ResolveReviewTemplate,
	}
)

// handleGetSettingsPlanTemplate returns this scope's own plan-template
// override Markdown (tier_text, "" if unset) plus the resolved template
// (merged_text -- the user override, else the plugin's English default; see
// config.ResolvePlanTemplate) -- the editor pane for
// the テンプレート tab's 実行計画 entry. The same file get-plan-template reads.
func (s *Server) handleGetSettingsPlanTemplate(w http.ResponseWriter, r *http.Request) {
	s.getSettingsMarkdownTemplate(w, r, planTemplateTarget)
}

// handlePutSettingsPlanTemplate saves (or, given empty text, clears) this
// scope's plan-template override. See putSettingsMarkdownTemplate.
func (s *Server) handlePutSettingsPlanTemplate(w http.ResponseWriter, r *http.Request) {
	s.putSettingsMarkdownTemplate(w, r, planTemplateTarget)
}

// handleGetSettingsReviewTemplate is handleGetSettingsPlanTemplate's
// counterpart for the review template shared by the review/review_gate node
// types (the テンプレート tab's レビュー entry; get-review-template).
func (s *Server) handleGetSettingsReviewTemplate(w http.ResponseWriter, r *http.Request) {
	s.getSettingsMarkdownTemplate(w, r, reviewTemplateTarget)
}

// handlePutSettingsReviewTemplate saves (or, given empty text, clears) this
// scope's review-template override. See putSettingsMarkdownTemplate.
func (s *Server) handlePutSettingsReviewTemplate(w http.ResponseWriter, r *http.Request) {
	s.putSettingsMarkdownTemplate(w, r, reviewTemplateTarget)
}

func (s *Server) getSettingsMarkdownTemplate(w http.ResponseWriter, r *http.Request, target markdownTemplateTarget) {
	sc := s.settingsUserScope()
	s.writeMarkdownTemplateState(w, sc, target)
}

// putSettingsMarkdownTemplate writes body.text as this scope's override for
// target, as a full replace. Unlike the report template the content is not
// validated -- any Markdown is accepted -- because nothing in the engine
// parses a plan/review artifact's headings or verdict words. Empty text
// deletes the override file so resolution falls back to the layer below.
// The body field is "text", matching the node-type/skill PUTs.
func (s *Server) putSettingsMarkdownTemplate(w http.ResponseWriter, r *http.Request, target markdownTemplateTarget) {
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	sc := s.settingsUserScope()

	if err := config.WriteExtensionText(sc.tierExtensionRoot(), target.subdir, target.file, body.Text); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.writeMarkdownTemplateState(w, sc, target)
}

func (s *Server) writeMarkdownTemplateState(w http.ResponseWriter, sc settingsScope, target markdownTemplateTarget) {
	tierText, _ := target.tierText(sc.tierExtensionRoot())
	writeJSON(w, http.StatusOK, map[string]any{
		"tier_text":   tierText,
		"merged_text": target.resolve(sc.mergeRoots()),
	})
}
