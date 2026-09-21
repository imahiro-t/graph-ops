package runtimeconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// WorkDirConfigFileName is the name of the graph-config.json that used to be
// read out of the process's working directory. Nothing reads that file any
// more (see LoadEffective); the name survives only so a stale copy can be
// DETECTED and named in the one warning that tells its owner it is no longer
// read. It is a constant rather than a literal because the detection and the
// tests that pin the warning must not be able to disagree about the spelling.
const WorkDirConfigFileName = "graph-config.json"

// HomeConfigPath returns the one configuration file this program reads --
// $HOME/.graph-ops/config.json -- or "" when home could not be resolved at
// all (os.UserHomeDir failing on a minimal container or CI image, which
// callers already pass through as ""). "Where do settings come from?" is
// asked in exactly one place, here, so the loading side and the writing side
// cannot drift apart about what it is.
//
// There is deliberately no second candidate and no per-key exception table:
// settings come from this file and from environment variables, and from
// nothing else (ticket DFLT-00124). Before that, a graph-config.json in
// whatever directory graph-engine happened to be started in came FIRST and
// won outright -- resolution stopped at the first file that existed, so the
// user's own home config was not merged in, it was skipped entirely.
// `git clone`-ing a third-party repository and running `graph-engine ui`
// inside it was therefore enough to hand that repository's author the
// database connection (dbBackend/dbPath/mysql*), the destination tickets and
// artifacts are sent to (httpDataSource*), and the directory agent
// instructions are read from (userExtensionsDir). DFLT-00104 had closed the
// same hole for four keys with a blocklist; this removes the exception table
// itself instead, so every field of FileConfig is protected by one rule.
func HomeConfigPath(home string) string {
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".graph-ops", "config.json")
}

// HomeConfigPathForMessage renders the home config file for a human-readable
// message: its path when home is resolvable, and the fixed phrase "the home
// config file" when it is not. Naming a path we do not have would be a lie,
// and there would be nothing there for the user to edit.
//
// It exists so that every user-facing place that has to say where settings
// come from -- the CLI's config warnings and its "cannot open the database"
// error -- words the home == "" case identically. The HTTP API deliberately
// does NOT use it: its config_path field is a machine-readable path, and
// answers "" so a client never displays a path that does not exist (see
// appSettingsResponse.ConfigPath).
func HomeConfigPathForMessage(home string) string {
	return homeConfigPathForMessage(HomeConfigPath(home))
}

func homeConfigPathForMessage(path string) string {
	if path != "" {
		return path
	}
	return "the home config file"
}

// HomeConfigReadError reports that the home config file exists but could not
// be read or parsed. Every place that opens that file -- LoadEffective,
// LoadHomeConfig, and UpdateHome, which must read it before it may write it
// -- reports the failure as this type, so a caller can tell "your home config
// is broken, here is which file" apart from any other I/O failure and say
// something the user can act on. The Web UI does exactly that: the same
// condition is a warning on a GET and a named error code on a PUT
// (DFLT-00104, non-functional review NF-2).
type HomeConfigReadError struct {
	Path string
	Err  error
}

func (e *HomeConfigReadError) Error() string {
	return fmt.Sprintf("cannot read %s: %v", e.Path, e.Err)
}

func (e *HomeConfigReadError) Unwrap() error { return e.Err }

// Effective is LoadEffective's result: the settings that actually apply, plus
// what a caller needs in order to warn the user about the two things that can
// be wrong on the way there.
//
// There is no "which file did each setting come from" bookkeeping any more,
// and no list of ignored keys: exactly one file is read, and either all of it
// applies or none of it does.
type Effective struct {
	// Config is the home config file's contents -- the zero value when that
	// file does not exist, and also when it could not be parsed (see
	// HomeConfigErr). Every field falls back to an environment variable and
	// then to a built-in default in cmd/graph-engine's loadRuntimeConfig,
	// which is the only place that applies that chain.
	Config FileConfig
	// HomeConfigPath is the file Config was read from, and the file a save
	// writes back to -- or "" when the home directory could not be resolved,
	// in which case there is no configuration file at all.
	HomeConfigPath string
	// HomeConfigErr is non-nil when HomeConfigPath exists but could not be
	// read or parsed. It is deliberately not an error return -- see
	// LoadEffective.
	HomeConfigErr error
	// StaleWorkDirConfigPath is the path of a leftover graph-config.json in
	// the process's working directory, or "" when there is none. It is not a
	// setting source: the file is detected with os.Stat and never opened, so
	// a syntactically broken one reaches this field exactly like a valid one.
	//
	// The only consumer is cmd/graph-engine's formatConfigWarnings, which
	// prints one stderr line for such a file. It is deliberately NOT surfaced
	// in any HTTP response: telling the Web UI about it would mean a new
	// warning code and a new pair of i18n strings, and this warning is a
	// migration aid with a fixed lifetime, not a permanent part of the API.
	// Migration is announced in the release notes instead (DFLT-00124, plan
	// review condition F-3). If some future change does want it in the UI,
	// that is a decision to take deliberately -- not something to slip in
	// because the value happens to be sitting here already.
	StaleWorkDirConfigPath string
}

// HomeConfigPathForMessage is the package-level function of the same name
// applied to the path this Effective was loaded from, for a caller that
// already holds an Effective and would otherwise have to carry the home
// directory alongside it just to word one message -- two values that must
// then be kept in agreement by hand.
func (e Effective) HomeConfigPathForMessage() string {
	return homeConfigPathForMessage(e.HomeConfigPath)
}

// LoadEffective reads the settings that apply: the home config file, and
// nothing else. cwd is used for one thing only -- spotting a leftover
// graph-config.json to warn about -- and never as a source of values.
//
// It does not return an error, on purpose (DFLT-00124, plan review condition
// F-1). Neither of the two things that can go wrong should stop a command
// from running:
//
//   - The home config cannot be read or parsed. That lands in HomeConfigErr
//     and the caller warns; values fall back to environment variables and
//     built-in defaults. This is the one rule for a broken home config, and
//     it applies regardless of what is in cwd -- before DFLT-00124 a
//     working-directory graph-config.json meant the home config was never
//     opened, so whether a broken one stopped your CLI depended on whether a
//     repository you had checked out happened to contain a config file
//     (completion criterion 4).
//   - A leftover graph-config.json sits in cwd. That lands in
//     StaleWorkDirConfigPath and the caller warns. Refusing to start over a
//     file shipped inside somebody else's repository is exactly the failure
//     mode this ticket exists to avoid, so the file is not even opened; a
//     broken one takes the same path as a valid one, with no special case to
//     write (completion criterion 3).
//
// Returning Effective alone also means no call site keeps an `if err != nil`
// branch that can never be taken.
func LoadEffective(cwd, home string) Effective {
	eff := Effective{HomeConfigPath: HomeConfigPath(home)}
	if eff.HomeConfigPath != "" {
		cfg, err := loadFrom(eff.HomeConfigPath)
		if err != nil {
			eff.HomeConfigErr = &HomeConfigReadError{Path: eff.HomeConfigPath, Err: err}
		} else {
			eff.Config = cfg
		}
	}
	if cwd != "" {
		stale := filepath.Join(cwd, WorkDirConfigFileName)
		if info, err := os.Stat(stale); err == nil && !info.IsDir() {
			eff.StaleWorkDirConfigPath = stale
		}
	}
	return eff
}

// LoadHomeConfig reads the home config file's literal contents -- the raw
// read, with no redaction and no fallback chain applied. An unresolvable home
// yields the zero value and no error, the same as a missing file: there is
// simply nothing configured.
//
// Use it only where the bytes on disk are themselves the point: the settings
// API's "is this submitted secret the one already saved?" comparison needs
// the stored value, unredacted, to compare against. Anywhere a setting is
// about to be USED, go through LoadEffective (or, in the server, the
// already-resolved runtimeConfig) instead.
//
// A malformed file is an error, and the returned FileConfig is then always
// the zero value rather than a partial decode.
func LoadHomeConfig(home string) (FileConfig, error) {
	path := HomeConfigPath(home)
	if path == "" {
		return FileConfig{}, nil
	}
	cfg, err := loadFrom(path)
	if err != nil {
		return FileConfig{}, &HomeConfigReadError{Path: path, Err: err}
	}
	return cfg, nil
}

// UpdateHome loads the home config file, lets fn edit it in memory, and saves
// the result -- all while holding fileMu, so concurrent writers in this
// process never lose each other's changes. If fn returns an error nothing is
// written and that error is returned unchanged. The returned FileConfig is
// what was saved (or, on an fn error, the loaded value as fn left it), and
// the path is the file written.
//
// Reads and writes are deliberately asymmetric about a broken home config
// (DFLT-00124, plan decision D-6). LoadEffective treats an unparseable file
// as "no settings" and carries on, because refusing to run helps nobody; a
// write, by contrast, fails here, because the alternative is overwriting a
// file whose contents we could not understand and destroying whatever the
// user has in it. Reading tolerates it, writing refuses it -- the same rule
// stated from both sides.
//
// A home that could not be resolved is likewise an error rather than a silent
// no-op: there is no file to write, and a save that quietly went nowhere is
// exactly what the caller must not report as success (completion criterion
// 10).
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
		// be parsed makes every save fail, for as long as the file stays
		// broken, and the user can only fix that if they are told it is that
		// file. The caller turns this into its own user-facing message.
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
// decode (DFLT-00023 C-5).
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
// directory 0o700 first: $HOME/.graph-ops is the same directory
// cmd/graph-engine's loadRuntimeConfig and store.NewSQLiteRepository create
// user-only, and the file itself is 0o600 (writeFileAtomic) because it can
// hold a MySQL password. Whichever of the three runs first in a fresh
// environment sets the mode, so they are kept in step.
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
