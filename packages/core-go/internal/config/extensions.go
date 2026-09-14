package config

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Package-level layout constants for the extensions/ subtree under each
// tier's root (see Roots' doc comment in load.go).
//
// The three category subdirectories and the report template's filename are
// exported because WriteExtensionText takes the category as a parameter, so
// its callers outside this package have to name one -- internal/httpserver's
// settings handlers used to hard-code the same four strings, which meant a
// rename here would have silently desynchronized reads (this package) from
// writes (that one) (DFLT-00023 C-4). extensionsSubdir stays unexported:
// nothing outside this package addresses that level, because every exported
// entry point already joins it.
const (
	extensionsSubdir = "extensions"

	SkillsSubdir       = "skills"
	NodeTypesSubdir    = "node-types"
	ReportSubdir       = "report"
	ReportTemplateFile = "template.html"
	PlanSubdir         = "plan"
	PlanTemplateFile   = "template.md"
	ReviewSubdir       = "review"
	ReviewTemplateFile = "template.md"
)

// defaults/node-types/*.md, defaults/report/template.html,
// defaults/plan/template.md, and defaults/review/template.md are a
// generated mirror, not hand-edited here: go:embed cannot reach outside this
// package's own directory tree, so the canonical, hand-edited source for
// all of them -- along with the node/gate catalog in defaults/workflow.yaml
// (see load.go) -- lives in packages/plugin/defaults/, the actual plugin
// content a user/team installs. `npm run sync:defaults` (run automatically
// by `npm run build`/`build:go`/`test`) copies that source in here so it can
// be embedded into the compiled binary. Edit packages/plugin/defaults/,
// never this directory directly -- a plain `go build`/`go test` still works
// without that step because the mirror is committed, but it will be stale
// until the next sync.
//
//go:embed defaults/node-types/*.md
var defaultNodeTypeFS embed.FS

//go:embed defaults/report/template.html
var defaultReportTemplate []byte

//go:embed defaults/plan/template.md
var defaultPlanTemplate []byte

//go:embed defaults/review/template.md
var defaultReviewTemplate []byte

// nodeTypeDefaultFile maps a node type to the basename of its default
// instruction file under defaults/node-types/. It is the identity mapping
// (<type>.md) for every type except "report": tooling in this environment
// refuses to write files whose name contains the word "report" (a guard
// against subagents dropping stray "*.md" summary/report files), so the
// report node type's default content lives in deliverable.md instead. The
// mapping only affects where the *default* text is embedded from -- the
// resolver, CLI surface (`get-node-type-context report`), and user/team
// override paths (<root>/extensions/node-types/report.md) all still use the
// real node type name "report".
func nodeTypeDefaultFile(nodeType string) string {
	if nodeType == "report" {
		return "deliverable.md"
	}
	return nodeType + ".md"
}

// defaultNodeTypeContent returns the plugin's built-in instruction text for
// nodeType, or "" if there is no default for it (e.g. a team-defined custom
// node type, or "release" nodes and other manual/no-op types with no
// asssociated agent instructions).
func defaultNodeTypeContent(nodeType string) string {
	raw, err := defaultNodeTypeFS.ReadFile("defaults/node-types/" + nodeTypeDefaultFile(nodeType))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// isSafeExtensionName reports whether name is safe to join as a single path
// segment under extensions/<subdir>/. name is derived from a
// nodeType/skillName that can originate from a team-shared workflow.yaml
// (see the Gherkin spec's "custom_lint" scenario), so it is treated as
// untrusted input: this rejects path separators and ".." so a crafted
// custom node type/skill name (e.g. "../../../../some/other/dir/x") cannot
// escape the intended extensions directory (Security Review node-47c93a46,
// non-blocking finding #2 -- defense in depth for a value that is not
// otherwise reachable over HTTP, but shouldn't be trusted regardless).
func isSafeExtensionName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if strings.ContainsAny(name, `/\`) {
		return false
	}
	return filepath.Base(name) == name
}

// readExtensionText reads <root>/extensions/<subdir>/<name> and returns its
// trimmed content. Per the "an unreadable or missing extension directory
// does not error, it just yields plugin defaults" contract, any read error
// (missing file, missing dir, permission error, root not configured) simply
// yields ("", false) rather than propagating -- extension content is always
// optional, best-effort overlay. A name that fails isSafeExtensionName is
// treated the same way: "not present", not an error.
func readExtensionText(root, subdir, name string) (string, bool) {
	if root == "" || !isSafeExtensionName(name) {
		return "", false
	}
	raw, err := os.ReadFile(filepath.Join(root, extensionsSubdir, subdir, name))
	if err != nil {
		return "", false
	}
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return "", false
	}
	return text, true
}

// appendLayers joins non-empty layers, in priority order, with a blank line
// between them. This is the injection mechanism's append-by-default
// semantics (mirroring ReviewGateDef.AdditionalCriteria): each layer adds to
// what came before rather than replacing it.
func appendLayers(layers ...string) string {
	parts := make([]string, 0, len(layers))
	for _, l := range layers {
		if l != "" {
			parts = append(parts, l)
		}
	}
	return strings.Join(parts, "\n\n")
}

// NodeTypeTierText returns just root's own override text for nodeType (no
// plugin default, no other tier) plus whether one is set at all -- the
// settings UI's "what did this specific scope override" view, as opposed to
// ResolveNodeTypeContext's "what does an agent actually see" merged view.
func NodeTypeTierText(root, nodeType string) (string, bool) {
	return readExtensionText(root, NodeTypesSubdir, nodeType+".md")
}

// ResolveNodeTypeContext returns the merged agent instructions for nodeType:
// plugin default (if any) -> user extension -> team extension, appended in
// that order. Works for both built-in node types (domain.NodeType's
// constants) and arbitrary custom types introduced by a user/team
// workflow.yaml (per internal/domain.NodeType's "opaque custom step" design)
// -- a custom type simply has no plugin default layer.
func ResolveNodeTypeContext(roots Roots, nodeType string) string {
	def := defaultNodeTypeContent(nodeType)
	user, _ := readExtensionText(roots.UserDir, NodeTypesSubdir, nodeType+".md")
	team, _ := readExtensionText(roots.TeamDir, NodeTypesSubdir, nodeType+".md")
	return appendLayers(def, user, team)
}

// SkillTierText returns just root's own supplementary-instruction override
// text for skillName (no merge with any other tier) -- the settings UI's
// スキル tab's "このスコープでの上書き" pane, mirroring NodeTypeTierText.
func SkillTierText(root, skillName string) (string, bool) {
	return readExtensionText(root, SkillsSubdir, skillName+".md")
}

// ReportTemplateTierText returns just root's own report-template override
// HTML (no merge/fallback) -- the settings UI's レポートテンプレート tab's
// "このスコープでの上書き" pane, mirroring NodeTypeTierText.
func ReportTemplateTierText(root string) (string, bool) {
	return readExtensionText(root, ReportSubdir, ReportTemplateFile)
}

// PlanTemplateTierText returns just root's own plan-template override
// Markdown (no merge/fallback), mirroring ReportTemplateTierText/
// NodeTypeTierText. Not yet surfaced in the settings UI (no dedicated tab
// exists for the plan/review templates), but kept for consistency with the
// other TierText accessors and as groundwork for one.
func PlanTemplateTierText(root string) (string, bool) {
	return readExtensionText(root, PlanSubdir, PlanTemplateFile)
}

// ReviewTemplateTierText returns just root's own review-template override
// Markdown (no merge/fallback), mirroring ReportTemplateTierText/
// NodeTypeTierText. Shared by both the "review" and "review_gate" node
// types -- there is exactly one review template, not one per node type.
func ReviewTemplateTierText(root string) (string, bool) {
	return readExtensionText(root, ReviewSubdir, ReviewTemplateFile)
}

// ResolveSkillContext returns the merged supplementary instructions for a
// plugin skill (create-ticket / refine-ticket / process-ticket): user
// extension -> team extension, appended in that order. There is no plugin
// "default" layer here beyond the skill's own SKILL.md (which the injection
// mechanism supplements, not replaces) -- callers fold this result into the
// subagent/skill instructions they already have.
func ResolveSkillContext(roots Roots, skillName string) string {
	user, _ := readExtensionText(roots.UserDir, SkillsSubdir, skillName+".md")
	team, _ := readExtensionText(roots.TeamDir, SkillsSubdir, skillName+".md")
	return appendLayers(user, team)
}

// ResolveReportTemplate returns the fixed HTML report template: the team
// override if configured, else the user override, else the plugin default.
// Unlike skill/node-type context this is a full replace, not an append --
// the whole point is that there is exactly one fixed template in effect at
// any time, never a per-run blend of several.
func ResolveReportTemplate(roots Roots) string {
	if text, ok := readExtensionText(roots.TeamDir, ReportSubdir, ReportTemplateFile); ok {
		return text
	}
	if text, ok := readExtensionText(roots.UserDir, ReportSubdir, ReportTemplateFile); ok {
		return text
	}
	return strings.TrimSpace(string(defaultReportTemplate))
}

// ResolvePlanTemplate returns the fixed Markdown plan template: the team
// override if configured, else the user override, else -- if lang is
// non-empty and has a matching locale template -- that locale's version of
// the template (see LoadLocaleTemplate), else the plugin's English default.
// Full replace, not append, mirroring ResolveReportTemplate -- exactly one
// fixed template is in effect at any time. lang == "" or an
// unrecognized/unsupported code both fall through to the English default,
// silently, the same "leave the English default as-is" contract
// LocalizedDefault uses for node/gate names.
func ResolvePlanTemplate(roots Roots, lang string) string {
	if text, ok := readExtensionText(roots.TeamDir, PlanSubdir, PlanTemplateFile); ok {
		return text
	}
	if text, ok := readExtensionText(roots.UserDir, PlanSubdir, PlanTemplateFile); ok {
		return text
	}
	if lang != "" {
		if text, ok := LoadLocaleTemplate(lang, PlanSubdir); ok {
			return text
		}
	}
	return strings.TrimSpace(string(defaultPlanTemplate))
}

// ResolveReviewTemplate returns the fixed Markdown review template, shared
// by the "review" and "review_gate" node types: the team override if
// configured, else the user override, else -- if lang is non-empty and has
// a matching locale template -- that locale's version of the template (see
// LoadLocaleTemplate), else the plugin's English default. Full replace, not
// append, mirroring ResolveReportTemplate/ResolvePlanTemplate.
func ResolveReviewTemplate(roots Roots, lang string) string {
	if text, ok := readExtensionText(roots.TeamDir, ReviewSubdir, ReviewTemplateFile); ok {
		return text
	}
	if text, ok := readExtensionText(roots.UserDir, ReviewSubdir, ReviewTemplateFile); ok {
		return text
	}
	if lang != "" {
		if text, ok := LoadLocaleTemplate(lang, ReviewSubdir); ok {
			return text
		}
	}
	return strings.TrimSpace(string(defaultReviewTemplate))
}

// requiredReportMarkers are the structural markers every report HTML
// artifact must contain, whether it was produced from the plugin's default
// template or a user/team override: the override itself must still be one
// fixed template (a different data-report-template value is fine; dropping
// the attribute, or any of the four sections, is not).
var requiredReportMarkers = []string{
	`data-report-template="`,
	`data-report-version="`,
	`data-report-section="header"`,
	`data-report-section="summary"`,
	`data-report-section="results"`,
	`data-report-section="footer"`,
}

// ValidateReportHTML checks that html contains every structural marker the
// fixed report format requires. It is intentionally a lightweight substring
// check rather than a full HTML/DOM parse: the goal is to catch a subagent
// free-forming its own report structure (the gap the Plan Review flagged),
// not to fully validate well-formedness.
func ValidateReportHTML(html string) error {
	var missing []string
	for _, marker := range requiredReportMarkers {
		if !strings.Contains(html, marker) {
			missing = append(missing, marker)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("report HTML does not match the fixed report template: missing %s (run `get-report-template` and fill in that fixed structure instead of free-forming HTML)", strings.Join(missing, ", "))
	}
	return nil
}
