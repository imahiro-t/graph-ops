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

// configDirName is the directory name used for both tiers' root: the
// per-user root is $HOME/.graph-ops, the per-team (formerly called
// "project") root is a .graph-ops directory found by walking up from
// the working directory. Both roots share one internal layout (see Roots'
// doc comment): a workflow-override file, plus an extensions/ subtree for
// skill/node-type/report content. One root per tier, not two parallel path
// systems.
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

// findProjectDir searches startDir and its ancestors for a directory named
// ".graph-ops", returning "" if none is found. Unlike the old
// file-existence check this used to be, it looks for the directory itself,
// so a team root that only has extensions/ content (no workflow.yaml yet)
// still gets discovered.
func findProjectDir(startDir string) (string, error) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(dir, configDirName)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil
		}
		dir = parent
	}
}

// FindProjectConfigPath searches startDir and its ancestors for
// .graph-ops/workflow.yaml, returning "" if none is found.
func FindProjectConfigPath(startDir string) (string, error) {
	dir, err := findProjectDir(startDir)
	if err != nil || dir == "" {
		return "", err
	}
	return filepath.Join(dir, teamConfigFile), nil
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
// call. Either may be "" (no home dir resolvable / no team root found or
// configured), in which case that tier contributes nothing.
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
// userDirOverride/teamDirOverride are explicit configuration (e.g. from
// GRAPH_USER_EXTENSIONS_DIR / GRAPH_TEAM_EXTENSIONS_DIR or graph-config.json)
// and take precedence when non-empty; otherwise the user root defaults to
// $HOME/.graph-ops and the team root defaults to the nearest
// .graph-ops directory found by walking up from startDir (typically
// the process cwd) -- exactly today's hardcoded locations.
func ResolveRoots(startDir, userDirOverride, teamDirOverride string) (Roots, error) {
	userDir := userDirOverride
	if userDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			userDir = filepath.Join(home, configDirName)
		}
	}

	teamDir := teamDirOverride
	if teamDir == "" {
		dir, err := findProjectDir(startDir)
		if err != nil {
			return Roots{}, err
		}
		teamDir = dir
	}

	return Roots{UserDir: userDir, TeamDir: teamDir}, nil
}

// Load resolves and merges the three config layers (plugin default -> user
// -> team, team winning) into a single Catalog, using each tier's default
// root (see ResolveRoots with no overrides) and no explicit language
// override (see LoadWithRoots).
func Load(startDir string) (Catalog, error) {
	return LoadWithRoots(startDir, "", "", "")
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
func LoadWithRoots(startDir, userDirOverride, teamDirOverride, languageOverride string) (Catalog, error) {
	roots, err := ResolveRoots(startDir, userDirOverride, teamDirOverride)
	if err != nil {
		return Catalog{}, err
	}

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
