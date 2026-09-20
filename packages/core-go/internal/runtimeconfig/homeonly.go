package runtimeconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// homeOnlyKeys are graph-config.json's four "home-only" keys: the settings
// that decide what this machine executes and who can reach it, and that are
// therefore read ONLY from the home config file ($HOME/.graph-ops/config.json,
// see HomeConfigPath) or an environment variable -- never from the
// graph-config.json sitting in whatever directory graph-engine happened to be
// started in (ticket DFLT-00104, report finding SEC-05).
//
// Why these four and not the rest:
//
//   - terminalCommand is handed to `sh -c` by internal/terminal.buildLaunchArgv
//     the moment the UI's "Claude を起動" button is pressed.
//   - claudeBinary is deliberately interpolated into that same command line
//     unquoted (a design decision of terminal/launch.go, so a user can write
//     `env FOO=1 claude`), which makes it a direct command-execution vector.
//   - host at "0.0.0.0" publishes an API with no authentication of any kind
//     to every machine on the LAN (see DefaultHost).
//   - artifactsDir at "/" removes any meaningful limit on what the artifact
//     endpoints will read back.
//
// Before this, `git clone`-ing someone's repository and running
// `graph-engine ui` inside it was enough to hand that repository's author all
// four, because ResolvePath stops at the FIRST candidate that exists and the
// cwd candidate comes first -- so the user's own home config was not merged
// in, it was skipped entirely. Nobody opening a third-party repository
// expects that to be a grant of command execution.
//
// Everything else in FileConfig (dbPath, workDir, the extension dirs, the
// MySQL/HTTP data source settings, port, paginationPageSize, myName,
// projectPaths) deliberately keeps the old "first file found wins"
// precedence: per-repository values there are a feature, and narrowing the
// blast radius of the four keys above is the whole of this change.
//
// The order of this slice is the order warnings list the keys in, and it is
// pinned against FileConfig's json tags by
// TestHomeOnlyKeys_MatchFileConfigJSONTags.
var homeOnlyKeys = []string{"terminalCommand", "claudeBinary", "host", "artifactsDir"}

// HomeOnlyKeys returns the json key names of the settings that are read only
// from the home config file or the environment, in the fixed order warnings
// list them in. The returned slice is a copy: callers cannot reorder or
// extend the definition by mutating it.
func HomeOnlyKeys() []string {
	out := make([]string, len(homeOnlyKeys))
	copy(out, homeOnlyKeys)
	return out
}

// HomeOnlyKeyList is the human-facing rendering of HomeOnlyKeys -- the same
// keys, in the same order, comma-separated. Warnings on both the CLI and the
// API side print the list, and they print it identically.
func HomeOnlyKeyList() string {
	return strings.Join(homeOnlyKeys, ", ")
}

// HomeConfigPath returns the one path the home-only keys may come from --
// $HOME/.graph-ops/config.json, i.e. the last of CandidatePaths -- or "" when
// home could not be resolved at all (os.UserHomeDir failing on a minimal
// container or CI image, which callers already pass through as ""). "The
// trusted location" is asked about in exactly one place, here, so the
// loading side and the writing side cannot drift apart about what it is.
func HomeConfigPath(home string) string {
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".graph-ops", "config.json")
}

// clearHomeOnly zeroes cfg's home-only fields and reports which of them
// actually held a value, in homeOnlyKeys order. An empty value is not
// reported: "the key is present but says nothing" is not something a user
// needs to be warned about, and warning about it would fire on a
// graph-config.json that merely round-tripped through a tool writing every
// key.
//
// This is the single place that maps the key names in homeOnlyKeys onto
// FileConfig's fields; adding a fifth home-only key means touching this
// function and that slice, and nothing else.
func clearHomeOnly(cfg *FileConfig) []string {
	var cleared []string
	if cfg.TerminalCommand != "" {
		cleared = append(cleared, "terminalCommand")
		cfg.TerminalCommand = ""
	}
	if cfg.ClaudeBinary != "" {
		cleared = append(cleared, "claudeBinary")
		cfg.ClaudeBinary = ""
	}
	if cfg.Host != "" {
		cleared = append(cleared, "host")
		cfg.Host = ""
	}
	if cfg.ArtifactsDir != "" {
		cleared = append(cleared, "artifactsDir")
		cfg.ArtifactsDir = ""
	}
	return cleared
}

// copyHomeOnly overwrites dst's home-only fields with src's -- the other half
// of clearHomeOnly, and deliberately adjacent to it so the two cannot fall
// out of step about which fields those are.
func copyHomeOnly(dst *FileConfig, src FileConfig) {
	dst.TerminalCommand = src.TerminalCommand
	dst.ClaudeBinary = src.ClaudeBinary
	dst.Host = src.Host
	dst.ArtifactsDir = src.ArtifactsDir
}

// HomeConfigReadError reports that the home config file exists but could not
// be read or parsed. Both places that open that file for its own sake --
// LoadEffective, consulting it for the home-only keys, and UpdateHome, which
// must read it before it may write it -- report the failure as this type, so
// a caller can tell "your home config is broken, here is which file" apart
// from any other I/O failure and say something the user can act on. The Web
// UI does exactly that: the same condition is a warning on a GET and a named
// error code on a PUT (DFLT-00104, non-functional review NF-2).
type HomeConfigReadError struct {
	Path string
	Err  error
}

func (e *HomeConfigReadError) Error() string {
	return fmt.Sprintf("cannot read %s: %v", e.Path, e.Err)
}

func (e *HomeConfigReadError) Unwrap() error { return e.Err }

// Effective is LoadEffective's result: the settings that actually apply,
// plus everything a caller needs in order to tell the user what was dropped
// on the way there.
//
// It is a struct rather than a fistful of return values because the
// "something was ignored" story has three independent parts -- which file,
// which keys, and whether the home config could be read at all -- and each
// of them is optional.
type Effective struct {
	// Config is the merged result: every non-home-only field exactly as
	// Load would have returned it, and the four home-only fields taken from
	// the home config file (or left empty, which makes the caller fall
	// through to an env var and then to a built-in default).
	Config FileConfig
	// Path is the file the non-home-only settings came from -- ResolvePath's
	// answer, unchanged. It keeps the meaning it always had ("the file a
	// Save targets, and the one the UI shows"), so nothing that already
	// displays it starts pointing somewhere else.
	Path string
	// HomeConfigPath is the file the home-only settings were taken from, or
	// "" when home could not be resolved.
	HomeConfigPath string
	// IgnoredKeys lists the home-only keys that were found in Path and
	// dropped, in homeOnlyKeys order. Empty in the common case: when Path is
	// the home config itself, or when it simply carries none of them.
	//
	// There is deliberately no file name beside it, and no room for a second
	// file: exactly one file is ever read and partly ignored here, and that
	// file is Path. A slice of (path, keys) pairs would have invited readers
	// to wonder which other files could turn up in it.
	IgnoredKeys []string
	// HomeConfigErr is non-nil when HomeConfigPath exists but could not be
	// read or parsed while it was being consulted for the home-only keys.
	// It is deliberately NOT an error return -- see LoadEffective.
	HomeConfigErr error
}

// LoadEffective is Load plus the home-only rule: the file ResolvePath picks
// still supplies every other setting, but terminalCommand, claudeBinary,
// host and artifactsDir are taken from the home config file (or from
// nothing) no matter which file that was. See homeOnlyKeys for why.
//
// Use this, not Load, anywhere a value is about to be *used* as a setting --
// that is what makes the protection hold. Load remains the raw read: use it
// only to see one file's literal contents, which is what the
// read-modify-write helpers (Update/UpdateHome, and the settings API's save
// path) need so they never write a merged value back into a file it did not
// come from.
//
// A read error on Path is returned as an error, exactly as Load does today:
// that file was already being read before this change, so a corrupt one
// failing startup is existing behaviour, and starting with settings a user
// believes they wrote but that were never parsed would be worse.
//
// A read error on the home config, however, is reported in
// Effective.HomeConfigErr and NOT returned as an error, because it is a
// genuinely new failure mode: while a working-directory graph-config.json
// exists the home config used not to be opened at all, so a corrupt one that
// has sat there harmlessly for months would suddenly stop every subcommand
// (add-artifact included) from starting. Completion criterion 2 of
// DFLT-00104 requires that this change not break existing users' startups;
// the caller warns and falls back to env vars and defaults instead. When the
// home config IS the resolved path there is no new failure mode, and the
// plain error above applies unchanged.
func LoadEffective(cwd, home string) (Effective, error) {
	cfg, path, err := Load(cwd, home)
	if err != nil {
		return Effective{Path: path, HomeConfigPath: HomeConfigPath(home)}, err
	}

	homePath := HomeConfigPath(home)
	eff := Effective{Config: cfg, Path: path, HomeConfigPath: homePath}
	if homePath != "" && filepath.Clean(path) == filepath.Clean(homePath) {
		// The trusted file is the one that was read: nothing to drop, and
		// nothing more to load.
		return eff, nil
	}

	eff.IgnoredKeys = clearHomeOnly(&eff.Config)
	if homePath == "" {
		// No trusted file to fall back to. The home-only fields stay empty,
		// which is the safe direction: the caller falls through to the env
		// vars and then to the built-in defaults, rather than to values from
		// a directory anyone can hand us.
		return eff, nil
	}
	homeCfg, err := loadFrom(homePath)
	if err != nil {
		eff.HomeConfigErr = &HomeConfigReadError{Path: homePath, Err: err}
		return eff, nil
	}
	copyHomeOnly(&eff.Config, homeCfg)
	return eff, nil
}

// UpdateHome is Update aimed unconditionally at the home config file, for the
// settings API's save of a home-only key: writing such a key to the resolved
// path would put it in a file LoadEffective is about to ignore, which is the
// "I changed it in the UI and nothing happened" bug that completion
// criterion 3 of DFLT-00104 exists to prevent.
//
// It takes the same fileMu as Update, so the two serialize against each other
// even in the common case where both target the very same file.
//
// A home that could not be resolved is an error rather than a silent no-op:
// the caller has to decide what to tell the user, and a save that quietly
// went nowhere is exactly the failure this function was added to remove.
func UpdateHome(home string, fn func(cfg *FileConfig) error) (FileConfig, string, error) {
	path := HomeConfigPath(home)
	if path == "" {
		return FileConfig{}, "", errors.New("cannot resolve the home directory, so there is no home config file to write")
	}

	fileMu.Lock()
	defer fileMu.Unlock()

	cfg, err := loadFrom(path)
	if err != nil {
		// A HomeConfigReadError, not a bare one: a home config that cannot
		// be parsed makes every save of a home-only key fail, for as long as
		// the file stays broken, and the user can only fix that if they are
		// told it is that file. The caller turns this into its own
		// user-facing message.
		return FileConfig{}, path, &HomeConfigReadError{Path: path, Err: err}
	}
	if err := fn(&cfg); err != nil {
		return cfg, path, err
	}
	if err := saveTo(path, cfg); err != nil {
		return cfg, path, err
	}
	return cfg, path, nil
}

// loadFrom reads one specific config file. A missing file is not an error
// (it yields the zero value, "nothing set"); a malformed one is, and the
// returned FileConfig is then always the zero value rather than a partial
// decode (see Load).
func loadFrom(path string) (FileConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return FileConfig{}, nil
		}
		return FileConfig{}, err
	}
	var cfg FileConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return FileConfig{}, err
	}
	return cfg, nil
}

// saveTo writes cfg to one specific config file, creating its parent
// directory 0o700 first. See Save for why that mode.
func saveTo(path string, cfg FileConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, raw)
}
