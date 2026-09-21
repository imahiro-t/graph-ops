package config

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// defaults/workflow.yaml is a generated mirror of
// packages/plugin/defaults/workflow.yaml (the canonical, hand-edited plugin
// default catalog) -- see the doc comment on defaultNodeTypeFS in
// extensions.go for why this package can't embed that file directly and how
// the mirror is kept in sync.
//
//go:embed defaults/workflow.yaml
var defaultYAML []byte

// configDirName is the directory name used for the user tier's default
// root, $HOME/.graph-ops. The team tier has no default location at all: it
// is whatever teamExtensionsDir / GRAPH_TEAM_EXTENSIONS_DIR names, and
// nothing when neither is set (DFLT-00124 -- see ResolveRoots). Both roots
// share one internal layout (see Roots' doc comment): a workflow-override
// file, plus an extensions/ subtree for skill/node-type/report content. One
// root per tier, not two parallel path systems.
const (
	configDirName  = ".graph-ops"
	userConfigFile = "config.yaml"
	teamConfigFile = "workflow.yaml"

	userConfigRelPath = configDirName + "/" + userConfigFile
)

func parseDocument(raw []byte) (Document, error) {
	var doc Document
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return Document{}, fmt.Errorf("parsing config: %w", err)
	}
	return doc, nil
}

// DefaultDocument returns the plugin's built-in workflow/review-gate catalog.
func DefaultDocument() (Document, error) {
	return parseDocument(defaultYAML)
}

// UserConfigPath returns where the per-user override file lives by default
// (no explicit override configured), honoring $HOME. It does not check that
// the file exists.
func UserConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, userConfigRelPath), nil
}

// DefaultUserRoot returns the user tier's default root, $HOME/.graph-ops,
// or "" when no home directory is resolvable. It ignores any explicit
// user-root override (GRAPH_USER_EXTENSIONS_DIR / the home config file's
// userExtensionsDir) on purpose: this location is reserved for the user tier
// (it also holds the DB, config.json and saved artifacts).
func DefaultUserRoot() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, configDirName)
}

// canonicalDir normalizes path for SameDir: absolute and cleaned, then with
// symlinks resolved when that succeeds (it fails for a path that doesn't
// exist yet, in which case the cleaned absolute path is used as is).
func canonicalDir(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	abs = filepath.Clean(abs)
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

// SameDir reports whether a and b name the same directory, ignoring
// relative-vs-absolute spelling, trailing separators and symlinks (e.g.
// macOS's /var vs /private/var). An empty path is never the same as
// anything. Case-insensitive file systems are not special-cased.
func SameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return canonicalDir(a) == canonicalDir(b)
}

// loadOptionalDocument reads and parses path if it exists; a missing file is
// not an error and yields the zero Document.
func loadOptionalDocument(path string) (Document, error) {
	if path == "" {
		return Document{}, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Document{}, nil
		}
		return Document{}, fmt.Errorf("reading %s: %w", path, err)
	}
	return parseDocument(raw)
}

// Roots holds the resolved user- and team-tier extension directories for one
// call. Either may be "" (no home dir resolvable / no team root configured /
// team root is the same directory as the user root), in which case that tier
// contributes nothing. ResolveRoots never returns the same directory for
// both.
//
// Both roots share the same internal layout:
//
//	<root>/config.yaml or workflow.yaml   the tier's node/review-gate overrides
//	                                       (Document shape); user tier keeps the
//	                                       historical "config.yaml" name, team
//	                                       tier keeps "workflow.yaml"
//	<root>/extensions/skills/<skill>.md      appended to that skill's instructions
//	<root>/extensions/node-types/<type>.md   appended to that node type's agent instructions
//	<root>/extensions/report/template.html   overrides the fixed default report template
//
// This is deliberately one directory root per tier with subpaths underneath,
// not a separate parallel path system alongside the pre-existing
// ~/.graph-ops/config.yaml / .graph-ops/workflow.yaml tiers:
// by default the roots resolve to exactly those same directories, so
// existing setups keep working unchanged and simply gain the extensions/
// subtree for free.
type Roots struct {
	UserDir string
	TeamDir string
}

// ResolveRoots determines the user- and team-tier root directories.
// userDirOverride/teamDirOverride are explicit configuration (from
// GRAPH_USER_EXTENSIONS_DIR / GRAPH_TEAM_EXTENSIONS_DIR or the home config
// file's userExtensionsDir / teamExtensionsDir). The user root falls back to
// $HOME/.graph-ops; the team root has no fallback -- an empty
// teamDirOverride means there is no team tier.
//
// That is the whole of the team tier's resolution, on purpose (DFLT-00124,
// completion criterion 6). It used to default to the nearest .graph-ops
// directory found by walking up from the process's working directory, which
// meant a repository that merely contained a .graph-ops/workflow.yaml or
// extensions/node-types/*.md supplied agent instructions and workflow
// definitions to whoever ran graph-engine inside it, with no setting written
// anywhere. Sharing across a team is done by pointing teamExtensionsDir at a
// shared directory -- explicitly, from the user's own home config -- never by
// what happens to sit in the current directory's ancestry.
//
// If both roots end up naming the same directory (e.g. both overrides set to
// one path), that directory is kept as the user root only and TeamDir is "",
// so it is never read twice (DFLT-00068).
//
// There is no error return: resolution is now pure string handling over the
// two overrides, with no filesystem access that could fail. It used to walk
// up from the working directory looking for a .graph-ops, which could fail;
// with that walk gone the only error the signature could ever have carried
// was a nil one, and every caller would have had to keep a branch that
// cannot be taken. Dropping it is the same call this ticket made for
// runtimeconfig.LoadEffective.
func ResolveRoots(userDirOverride, teamDirOverride string) Roots {
	userDir := userDirOverride
	if userDir == "" {
		userDir = DefaultUserRoot()
	}

	teamDir := teamDirOverride
	if SameDir(userDir, teamDir) {
		teamDir = ""
	}

	return Roots{UserDir: userDir, TeamDir: teamDir}
}

// Load resolves and merges the config layers (plugin default -> user ->
// team, team winning) into a single Catalog with no overrides at all -- so
// the user tier is $HOME/.graph-ops and there is no team tier (see
// ResolveRoots) -- and no explicit language override (see LoadWithRoots).
func Load() (Catalog, error) {
	return LoadWithRoots("", "", "")
}

// LoadWithRoots is Load, but with explicit user-/team-root overrides (see
// Roots and ResolveRoots) rather than always using the default locations,
// plus languageOverride: a single call's explicit language choice (e.g. the
// CLI's --language flag), which -- per ResolveLanguage's doc comment and the
// execution plan's section 1.4 -- outranks both tiers' persistent
// Document.Language when non-empty. Passing "" reproduces the old
// behavior exactly: the resolved language then comes solely from
// userDoc/teamDoc, and an unset/unsupported one leaves the plugin default's
// English names untouched (see LocalizedDefault).
func LoadWithRoots(userDirOverride, teamDirOverride, languageOverride string) (Catalog, error) {
	roots := ResolveRoots(userDirOverride, teamDirOverride)

	var userPath string
	if roots.UserDir != "" {
		userPath = filepath.Join(roots.UserDir, userConfigFile)
	}
	userDoc, err := loadOptionalDocument(userPath)
	if err != nil {
		return Catalog{}, err
	}

	var teamPath string
	if roots.TeamDir != "" {
		teamPath = filepath.Join(roots.TeamDir, teamConfigFile)
	}
	teamDoc, err := loadOptionalDocument(teamPath)
	if err != nil {
		return Catalog{}, err
	}

	lang := ResolveLanguage(languageOverride, userDoc, teamDoc)
	def, err := LocalizedDefault(lang)
	if err != nil {
		return Catalog{}, err
	}

	return Merge(def, userDoc, teamDoc), nil
}
