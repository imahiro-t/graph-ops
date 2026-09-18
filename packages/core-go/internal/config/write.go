package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// builtinNodeTypes is the fixed set of node types the plugin ships
// instructions/behavior for (see ResolveNodeTypeContext's callers and
// nodeTypeDefaultFile). It seeds ListKnownNodeTypes so the settings UI's node
// type picker always offers every built-in type, even one that happens not
// to appear in any catalog node yet.
//
// Keep this in sync with the files the plugin actually ships under
// defaults/node-types/ (mirrored into this package's own defaults/ by
// `npm run sync:defaults`): a type with a default instructions file that is
// missing here stays invisible in the picker until some catalog node
// happens to use it, which is the bug "documentation" had.
var builtinNodeTypes = []string{
	"plan", "review", "gherkin_spec", "implementation", "review_gate",
	"approval_gate", "gherkin_test", "report", "investigation",
	"documentation", "release",
}

// builtinSkills is the fixed set of plugin skills that accept supplementary
// instructions via ResolveSkillContext (packages/plugin/skills/*). Unlike
// node types there is no "custom skill" concept a team workflow.yaml could
// introduce, so this list is exhaustive -- ListKnownSkills always returns
// exactly these four, in this order.
var builtinSkills = []string{"create-ticket", "refine-ticket", "process-ticket", "onboarding"}

// ListKnownSkills returns the fixed list of skill names the settings UI's
// skill picker should offer, in builtinSkills' order.
func ListKnownSkills() []string {
	out := make([]string, len(builtinSkills))
	copy(out, builtinSkills)
	return out
}

// LoadDocumentAt is a public wrapper around loadOptionalDocument: it reads
// and parses the Document at path, returning the zero Document (not an
// error) if the file doesn't exist. It's the read-only counterpart to
// SaveDocumentAt, used by the settings API to show a scope's own raw
// override content (as opposed to Load/LoadWithRoots, which always returns
// the fully-merged Catalog across all three tiers).
func LoadDocumentAt(path string) (Document, error) {
	return loadOptionalDocument(path)
}

// atomicWriteFile writes data to path via a temp file in path's own directory
// followed by a rename, creating the directory if it doesn't exist yet. The
// rename is what makes the write atomic: a reader never observes a
// partially-written file, and a crash mid-write leaves the previous, still
// valid content in place. The temp file is removed on every failure path so a
// failed write never litters the directory.
func atomicWriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("renaming into place: %w", err)
	}
	return nil
}

// SaveDocumentAt serializes doc as YAML and writes it to path, creating any
// missing parent directories. The write is atomic -- see atomicWriteFile.
func SaveDocumentAt(path string, doc Document) error {
	if path == "" {
		return fmt.Errorf("SaveDocumentAt: empty path")
	}
	raw, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("encoding document: %w", err)
	}
	return atomicWriteFile(path, raw)
}

// WriteExtensionText writes text to <root>/extensions/<subdir>/<name>,
// creating parent directories as needed. Per readExtensionText's "always
// optional, best-effort overlay" contract, an empty (after trimming) text
// deletes the file instead of writing an empty one -- see
// DeleteExtensionText -- so a scope can be returned to "no override,
// inherit from the layer below" by saving an empty string. name is
// validated with isSafeExtensionName the same way the read path is, so this
// can never be used to escape the extensions directory.
func WriteExtensionText(root, subdir, name, text string) error {
	if root == "" {
		return fmt.Errorf("WriteExtensionText: empty root")
	}
	if !isSafeExtensionName(name) {
		return fmt.Errorf("WriteExtensionText: unsafe extension name %q", name)
	}
	if text == "" {
		return DeleteExtensionText(root, subdir, name)
	}
	return atomicWriteFile(filepath.Join(root, extensionsSubdir, subdir, name), []byte(text))
}

// DeleteExtensionText removes <root>/extensions/<subdir>/<name> if it
// exists. A missing file is not an error -- deleting an override that was
// never set (or was already cleared) is a no-op, matching the idempotent
// "fall back to the layer below" semantics WriteExtensionText's empty-text
// case relies on.
func DeleteExtensionText(root, subdir, name string) error {
	if root == "" {
		return nil
	}
	if !isSafeExtensionName(name) {
		return fmt.Errorf("DeleteExtensionText: unsafe extension name %q", name)
	}
	path := filepath.Join(root, extensionsSubdir, subdir, name)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing %s: %w", path, err)
	}
	return nil
}

// ListKnownNodeTypes returns every node type the settings UI's node-type
// picker should offer: the fixed built-in set (builtinNodeTypes) plus any
// custom type name appearing in catalog.Nodes, deduplicated and with the
// built-ins listed first (in their fixed order) followed by custom types in
// first-seen order.
func ListKnownNodeTypes(catalog Catalog) []string {
	seen := make(map[string]bool, len(builtinNodeTypes)+len(catalog.Nodes))
	out := make([]string, 0, len(builtinNodeTypes)+len(catalog.Nodes))
	for _, t := range builtinNodeTypes {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	for _, n := range catalog.Nodes {
		if n.Type != "" && !seen[n.Type] {
			seen[n.Type] = true
			out = append(out, n.Type)
		}
	}
	return out
}

// ListNodeTypeOverrideNames returns the node type names that have an override
// file directly under <root>/extensions/node-types/ (basenames with the
// ".md" suffix stripped). This is what lets a brand-new custom node type
// created purely through the settings UI's ノード種別 tab (an override file
// with no plugin default and, yet, no node in any catalog referencing it)
// keep showing up in ListKnownNodeTypes' caller (handleListSettingsNodeTypes)
// across reloads -- without this, such a type would vanish the moment no
// catalog node happened to use it, even though its instructions file still
// exists on disk. A missing/unreadable directory (root not configured yet,
// no override ever saved) yields an empty slice rather than an error, same
// "always-optional overlay" contract as readExtensionText.
func ListNodeTypeOverrideNames(root string) []string {
	if root == "" {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(root, extensionsSubdir, NodeTypesSubdir))
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".md") {
			out = append(out, strings.TrimSuffix(name, ".md"))
		}
	}
	return out
}

// ResolveRootsForProjectWorkDir is ResolveRoots, but always resolves the
// team-tier root starting from projectDir (a project's local path)
// rather than the caller's own process cwd -- see the execution plan's
// section on why the settings UI's "project-scoped" tier must be tied to the
// selected DB Project, not wherever the server/CLI process happens to be
// running. teamDirOverride still takes precedence when non-empty, exactly as
// in ResolveRoots, so an operator's explicit GRAPH_TEAM_EXTENSIONS_DIR
// configuration is never silently bypassed by a project-scoped settings
// edit.
func ResolveRootsForProjectWorkDir(projectDir, userDirOverride, teamDirOverride string) (Roots, error) {
	return ResolveRoots(projectDir, userDirOverride, teamDirOverride)
}

// UserDocumentPath returns the path of the user-tier override Document file
// (config.yaml) under userRoot (a Roots.UserDir value). It exists so callers
// outside this package (the settings HTTP API) can build the same path
// LoadWithRoots resolves internally without hardcoding "config.yaml"
// themselves.
func UserDocumentPath(userRoot string) string {
	return filepath.Join(userRoot, userConfigFile)
}

// TeamDocumentPath returns the path of the team-tier override Document file
// (workflow.yaml) under teamRoot (a Roots.TeamDir value, or
// ProjectTeamRoot's result) -- the PUT-side counterpart to
// FindProjectConfigPath/ProjectTeamConfigPath's read-oriented resolution.
func TeamDocumentPath(teamRoot string) string {
	return filepath.Join(teamRoot, teamConfigFile)
}

// ProjectTeamRoot returns the directory a project-scoped ("team-tier")
// settings write should target for projectDir: the nearest existing
// .graph-ops directory found by walking up from projectDir, or
// (if none exists yet) a new .graph-ops directly under
// projectDir. Unlike ResolveRootsForProjectWorkDir's Roots.TeamDir
// (which is "" when no such directory exists yet, matching the read-only
// "extension content is always optional" contract), this never returns ""
// -- the settings UI always needs somewhere to write project-scoped
// overrides, even for a project that has never had one before.
//
// Like findProjectDir, the walk skips $HOME/.graph-ops, so a project under
// $HOME with no .graph-ops of its own gets <projectDir>/.graph-ops rather
// than the user tier's root (DFLT-00068). When projectDir is $HOME itself,
// that new path would be the user tier's default root, so
// ErrTeamRootIsUserRoot is returned instead. Only the default user root
// ($HOME/.graph-ops, see DefaultUserRoot) is checked here: this function
// doesn't know about a GRAPH_USER_EXTENSIONS_DIR/graph-config.json user-root
// override, so a caller that has one must compare against it itself (see
// SameDir).
func ProjectTeamRoot(projectDir string) (string, error) {
	dir, err := findProjectDir(projectDir)
	if err != nil {
		return "", err
	}
	if dir != "" {
		return dir, nil
	}
	abs, err := filepath.Abs(projectDir)
	if err != nil {
		return "", err
	}
	root := filepath.Join(abs, configDirName)
	if SameDir(root, DefaultUserRoot()) {
		return "", ErrTeamRootIsUserRoot
	}
	return root, nil
}

// ErrTeamRootIsUserRoot is returned by ProjectTeamRoot (and so
// ProjectTeamConfigPath) when the project's team root would be the user
// tier's root -- i.e. the project's local path is the home directory
// itself. Such a project has no team tier of its own: read-only callers
// should treat it as "no team root", and writers must refuse rather than
// silently overwrite the user tier.
var ErrTeamRootIsUserRoot = errors.New("project team root would be the user tier's root ($HOME/.graph-ops)")

// ProjectTeamConfigPath returns the workflow.yaml path under
// ProjectTeamRoot(projectDir) -- see its doc comment.
func ProjectTeamConfigPath(projectDir string) (string, error) {
	dir, err := ProjectTeamRoot(projectDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, teamConfigFile), nil
}
