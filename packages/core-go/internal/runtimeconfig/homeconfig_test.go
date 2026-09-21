package runtimeconfig

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeWorkDirConfig writes a graph-config.json into cwd -- the file a cloned
// repository would be carrying, and which used to win outright over the
// user's own home config.
func writeWorkDirConfig(t *testing.T, cwd string, raw string) string {
	t.Helper()
	path := filepath.Join(cwd, WorkDirConfigFileName)
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}
	return path
}

// writeHomeConfig writes $HOME/.graph-ops/config.json, the only file settings
// are ever read from.
func writeHomeConfig(t *testing.T, home string, raw string) string {
	t.Helper()
	path := HomeConfigPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("os.MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}
	return path
}

// --- completion criterion 1: nothing is read from the working directory ----

// TestLoadEffective_WorkDirDBSettingsAreIgnored is the DB group completion
// criterion 1 asks to check on its own: a repository's graph-config.json must
// not be able to point this process at another database -- neither a
// different sqlite file nor a MySQL server of its author's choosing.
func TestLoadEffective_WorkDirDBSettingsAreIgnored(t *testing.T) {
	cwd, home := t.TempDir(), t.TempDir()
	writeHomeConfig(t, home, `{
		"dbBackend": "sqlite",
		"dbPath": "/home/.graph-ops/graph.db"
	}`)
	writeWorkDirConfig(t, cwd, `{
		"dbBackend": "mysql",
		"dbPath": "/work/repo.db",
		"mysqlHost": "attacker.example.com",
		"mysqlPort": 3307,
		"mysqlDatabase": "stolen",
		"mysqlUser": "root",
		"mysqlPassword": "p@ss",
		"mysqlTls": "skip-verify",
		"mysqlTlsCa": "/work/ca.pem"
	}`)

	cfg := LoadEffective(cwd, home).Config
	if cfg.DBBackend != "sqlite" {
		t.Errorf("DBBackend = %q, want %q", cfg.DBBackend, "sqlite")
	}
	if cfg.DBPath != "/home/.graph-ops/graph.db" {
		t.Errorf("DBPath = %q, want the home config's", cfg.DBPath)
	}
	if cfg.MySQLHost != "" || cfg.MySQLPort != 0 || cfg.MySQLDatabase != "" ||
		cfg.MySQLUser != "" || cfg.MySQLPassword != "" || cfg.MySQLTLS != "" || cfg.MySQLTLSCA != "" {
		t.Errorf("MySQL settings leaked from the working directory: %+v", cfg)
	}
}

// TestLoadEffective_WorkDirHTTPDataSourceIsIgnored is the httpDataSource*
// group: these decide where tickets and artifacts are sent, so a repository
// being able to set them is data exfiltration.
func TestLoadEffective_WorkDirHTTPDataSourceIsIgnored(t *testing.T) {
	cwd, home := t.TempDir(), t.TempDir()
	writeWorkDirConfig(t, cwd, `{
		"httpDataSourceUrl": "https://attacker.example.com/api",
		"httpDataSourceToken": "leaked-token"
	}`)

	cfg := LoadEffective(cwd, home).Config
	if cfg.HTTPDataSourceURL != "" {
		t.Errorf("HTTPDataSourceURL = %q, want \"\"", cfg.HTTPDataSourceURL)
	}
	if cfg.HTTPDataSourceToken != "" {
		t.Errorf("HTTPDataSourceToken = %q, want \"\"", cfg.HTTPDataSourceToken)
	}
}

// TestLoadEffective_WorkDirUserExtensionsDirIsIgnored is the
// userExtensionsDir group: it decides where the agent instructions an agent
// is handed come from.
func TestLoadEffective_WorkDirUserExtensionsDirIsIgnored(t *testing.T) {
	cwd, home := t.TempDir(), t.TempDir()
	writeHomeConfig(t, home, `{"userExtensionsDir": "/home/shared/graph-ops"}`)
	writeWorkDirConfig(t, cwd, `{"userExtensionsDir": "/work/.repo-extensions"}`)

	if got := LoadEffective(cwd, home).Config.UserExtensionsDir; got != "/home/shared/graph-ops" {
		t.Errorf("UserExtensionsDir = %q, want the home config's", got)
	}
}

// TestLoadEffective_NoFileConfigKeyIsReadFromTheWorkDir walks every json tag
// of FileConfig and checks that a working-directory config setting it changes
// nothing at all. This is what makes "there is no exception table" testable:
// the table is the struct itself, so a field added later is covered the day
// it is added, without anyone remembering to list it.
func TestLoadEffective_NoFileConfigKeyIsReadFromTheWorkDir(t *testing.T) {
	tags := fileConfigJSONTags(t)
	if len(tags) == 0 {
		t.Fatal("FileConfig has no json tags")
	}
	// A value per json type, so each key is set to something that would be
	// visible in the result if it were read.
	value := func(tag string) any {
		switch tag {
		case "port", "paginationPageSize", "mysqlPort":
			return 65000
		case "projectPaths":
			return map[string]string{"proj-x": "/work/x"}
		default:
			return "from-the-work-dir-" + tag
		}
	}

	for _, tag := range tags {
		t.Run(tag, func(t *testing.T) {
			cwd, home := t.TempDir(), t.TempDir()
			raw, err := json.Marshal(map[string]any{tag: value(tag)})
			if err != nil {
				t.Fatalf("json.Marshal: %v", err)
			}
			writeWorkDirConfig(t, cwd, string(raw))

			withFile := LoadEffective(cwd, home).Config
			withoutFile := LoadEffective(t.TempDir(), home).Config
			if !reflect.DeepEqual(withFile, withoutFile) {
				t.Errorf("setting %q in the working directory changed the effective config:\n with = %+v\n without = %+v",
					tag, withFile, withoutFile)
			}
			if !reflect.DeepEqual(withFile, FileConfig{}) {
				t.Errorf("setting %q in the working directory produced %+v, want the zero value", tag, withFile)
			}
		})
	}
}

// fileConfigJSONTags returns FileConfig's json key names, in declaration
// order.
func fileConfigJSONTags(t *testing.T) []string {
	t.Helper()
	typ := reflect.TypeOf(FileConfig{})
	tags := make([]string, 0, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if name == "" || name == "-" {
			t.Fatalf("field %s has no usable json tag %q", typ.Field(i).Name, tag)
		}
		tags = append(tags, name)
	}
	return tags
}

// TestLoadEffective_HomeConfigIsRead is the other half: the home config's
// values do arrive, so the tests above are not passing because nothing is
// read at all.
func TestLoadEffective_HomeConfigIsRead(t *testing.T) {
	cwd, home := t.TempDir(), t.TempDir()
	path := writeHomeConfig(t, home, `{
		"dbPath": "/home/.graph-ops/graph.db",
		"terminalCommand": "open -a Terminal {cwd}",
		"paginationPageSize": 25,
		"projectPaths": {"proj-a": "/home/a"}
	}`)

	eff := LoadEffective(cwd, home)
	if eff.HomeConfigPath != path {
		t.Errorf("HomeConfigPath = %q, want %q", eff.HomeConfigPath, path)
	}
	if eff.HomeConfigErr != nil {
		t.Errorf("HomeConfigErr = %v, want nil", eff.HomeConfigErr)
	}
	if eff.Config.DBPath != "/home/.graph-ops/graph.db" ||
		eff.Config.TerminalCommand != "open -a Terminal {cwd}" ||
		eff.Config.PaginationPageSize != 25 ||
		eff.Config.ProjectPaths["proj-a"] != "/home/a" {
		t.Errorf("Config = %+v, want the home config's values", eff.Config)
	}
}

func TestLoadEffective_UnresolvableHomeYieldsNoSettings(t *testing.T) {
	cwd := t.TempDir()
	writeWorkDirConfig(t, cwd, `{"dbBackend": "mysql", "host": "0.0.0.0"}`)

	eff := LoadEffective(cwd, "")
	if eff.HomeConfigPath != "" {
		t.Errorf("HomeConfigPath = %q, want \"\"", eff.HomeConfigPath)
	}
	if !reflect.DeepEqual(eff.Config, FileConfig{}) {
		t.Errorf("Config = %+v, want the zero value", eff.Config)
	}
}

// --- completion criterion 2: no per-key exception table --------------------

// TestNoHomeOnlyExceptionTableRemains pins the absence of the blocklist and
// of any replacement for it. The old API (HomeOnlyKeys / HomeOnlyKeyList /
// clearHomeOnly / copyHomeOnly, and the homeOnlyKeys slice behind them)
// cannot be referenced here -- removing it is a compile-time fact, and a test
// that named those identifiers would not build. What can drift is the
// principle: somebody adding a "only these keys are special" list back. The
// test above (every FileConfig tag behaving identically) is what makes that
// impossible to do unnoticed, and this test states the intent next to it.
func TestNoHomeOnlyExceptionTableRemains(t *testing.T) {
	cwd, home := t.TempDir(), t.TempDir()
	// The four keys DFLT-00104 singled out, and four that it did not: after
	// DFLT-00124 they are read by one code path with no distinction at all.
	writeHomeConfig(t, home, `{
		"terminalCommand": "t", "claudeBinary": "c", "host": "h", "artifactsDir": "/a",
		"dbPath": "/d", "port": 1234, "myName": "me", "userExtensionsDir": "/u"
	}`)
	cfg := LoadEffective(cwd, home).Config
	for name, got := range map[string]string{
		"terminalCommand":   cfg.TerminalCommand,
		"claudeBinary":      cfg.ClaudeBinary,
		"host":              cfg.Host,
		"artifactsDir":      cfg.ArtifactsDir,
		"dbPath":            cfg.DBPath,
		"myName":            cfg.MyName,
		"userExtensionsDir": cfg.UserExtensionsDir,
	} {
		if got == "" {
			t.Errorf("%s came back empty; every key is read the same way", name)
		}
	}
	if cfg.Port != 1234 {
		t.Errorf("port = %d, want 1234", cfg.Port)
	}
}

// --- completion criterion 3: detecting the stale working-directory file ----

func TestLoadEffective_StaleWorkDirConfigIsDetected(t *testing.T) {
	cwd, home := t.TempDir(), t.TempDir()
	stale := writeWorkDirConfig(t, cwd, `{"dbBackend": "mysql"}`)

	if got := LoadEffective(cwd, home).StaleWorkDirConfigPath; got != stale {
		t.Errorf("StaleWorkDirConfigPath = %q, want %q", got, stale)
	}
}

func TestLoadEffective_NoStaleFileMeansNoPath(t *testing.T) {
	cwd, home := t.TempDir(), t.TempDir()
	writeHomeConfig(t, home, `{"dbPath": "/home/graph.db"}`)

	if got := LoadEffective(cwd, home).StaleWorkDirConfigPath; got != "" {
		t.Errorf("StaleWorkDirConfigPath = %q, want \"\"", got)
	}
}

// TestLoadEffective_StaleWorkDirConfigIsNeverParsed is decision D-2: the file
// is detected with os.Stat and never opened, so broken JSON reaches the same
// field, by the same path, as valid JSON. There is deliberately no special
// case for a malformed one to get wrong.
func TestLoadEffective_StaleWorkDirConfigIsNeverParsed(t *testing.T) {
	home := t.TempDir()
	valid, broken := t.TempDir(), t.TempDir()
	validPath := writeWorkDirConfig(t, valid, `{"dbBackend": "mysql"}`)
	brokenPath := writeWorkDirConfig(t, broken, `{ "dbBackend": "mysql",`)

	validEff := LoadEffective(valid, home)
	brokenEff := LoadEffective(broken, home)
	if validEff.StaleWorkDirConfigPath != validPath {
		t.Errorf("valid file: StaleWorkDirConfigPath = %q, want %q", validEff.StaleWorkDirConfigPath, validPath)
	}
	if brokenEff.StaleWorkDirConfigPath != brokenPath {
		t.Errorf("broken file: StaleWorkDirConfigPath = %q, want %q", brokenEff.StaleWorkDirConfigPath, brokenPath)
	}
	if brokenEff.HomeConfigErr != nil {
		t.Errorf("broken working-directory file reported as an error: %v", brokenEff.HomeConfigErr)
	}
	if !reflect.DeepEqual(validEff.Config, brokenEff.Config) {
		t.Error("a broken working-directory file produced a different config from a valid one")
	}
}

// TestLoadEffective_WorkDirGraphOpsDirectoryIsNotAFile guards the os.Stat
// check: a directory that happens to be named graph-config.json is not a
// leftover config file to warn about.
func TestLoadEffective_WorkDirGraphOpsDirectoryIsNotAFile(t *testing.T) {
	cwd, home := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(cwd, WorkDirConfigFileName), 0o755); err != nil {
		t.Fatalf("os.Mkdir: %v", err)
	}
	if got := LoadEffective(cwd, home).StaleWorkDirConfigPath; got != "" {
		t.Errorf("StaleWorkDirConfigPath = %q, want \"\" for a directory", got)
	}
}

// TestLoadEffective_StaleFileInHomeConfigDirIsStillReported settles the case
// the Gherkin review (F-1) corrected: the two file names differ
// (graph-config.json vs config.json), so cwd being $HOME/.graph-ops does not
// make them the same file. A graph-config.json there is as unread as one
// anywhere else, and is reported as such -- there is no same-file check to
// write, and writing one would be a branch that can never be taken.
func TestLoadEffective_StaleFileInHomeConfigDirIsStillReported(t *testing.T) {
	home := t.TempDir()
	homePath := writeHomeConfig(t, home, `{"dbPath": "/home/graph.db"}`)
	cwd := filepath.Dir(homePath) // $HOME/.graph-ops
	stale := writeWorkDirConfig(t, cwd, `{"dbBackend": "mysql"}`)
	if stale == homePath {
		t.Fatal("the two config file names must differ")
	}

	eff := LoadEffective(cwd, home)
	if eff.StaleWorkDirConfigPath != stale {
		t.Errorf("StaleWorkDirConfigPath = %q, want %q", eff.StaleWorkDirConfigPath, stale)
	}
	if eff.Config.DBPath != "/home/graph.db" {
		t.Errorf("DBPath = %q, want the home config's", eff.Config.DBPath)
	}
}

// --- completion criterion 4: one rule for a broken home config -------------

// TestLoadEffective_BrokenHomeConfigIsNonFatal is decision D-1. The two
// sub-cases are written as a pair on purpose: the whole point is that the
// presence of a file in the working directory changes nothing about how a
// broken home config is treated. Before this, a working-directory config
// meant the home config was never opened, so "does my CLI start?" depended on
// whether a repository happened to carry one.
func TestLoadEffective_BrokenHomeConfigIsNonFatal(t *testing.T) {
	for _, tc := range []struct {
		name     string
		workFile bool
	}{
		{"with a working-directory config", true},
		{"without a working-directory config", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cwd, home := t.TempDir(), t.TempDir()
			homePath := writeHomeConfig(t, home, `{ "dbPath": "/home/graph.db",`)
			if tc.workFile {
				writeWorkDirConfig(t, cwd, `{"dbBackend": "mysql"}`)
			}

			eff := LoadEffective(cwd, home)
			if eff.HomeConfigErr == nil {
				t.Fatal("HomeConfigErr = nil, want the read failure")
			}
			var readErr *HomeConfigReadError
			if !errors.As(eff.HomeConfigErr, &readErr) || readErr.Path != homePath {
				t.Errorf("HomeConfigErr = %v, want a *HomeConfigReadError naming %s", eff.HomeConfigErr, homePath)
			}
			if !reflect.DeepEqual(eff.Config, FileConfig{}) {
				t.Errorf("Config = %+v, want the zero value (never a partial decode)", eff.Config)
			}
		})
	}
}

// --- HomeConfigPathForMessage (completion criterion 11) --------------------

func TestHomeConfigPathForMessage(t *testing.T) {
	home := t.TempDir()
	if got, want := HomeConfigPathForMessage(home), HomeConfigPath(home); got != want {
		t.Errorf("HomeConfigPathForMessage(%q) = %q, want %q", home, got, want)
	}
	if got, want := HomeConfigPathForMessage(""), "the home config file"; got != want {
		t.Errorf("HomeConfigPathForMessage(\"\") = %q, want %q", got, want)
	}
}

// --- LoadHomeConfig --------------------------------------------------------

// TestLoadHomeConfig_ReadsTheStoredValueUnredacted covers what the settings
// API's "is this a resend of the stored secret?" check needs: the literal
// bytes on disk, from the home config and nowhere else.
func TestLoadHomeConfig_ReadsTheStoredValueUnredacted(t *testing.T) {
	cwd, home := t.TempDir(), t.TempDir()
	writeHomeConfig(t, home, `{"mysqlPassword": "stored-secret"}`)
	writeWorkDirConfig(t, cwd, `{"mysqlPassword": "attacker-secret"}`)

	cfg, err := LoadHomeConfig(home)
	if err != nil {
		t.Fatalf("LoadHomeConfig: %v", err)
	}
	if cfg.MySQLPassword != "stored-secret" {
		t.Errorf("MySQLPassword = %q, want the home config's", cfg.MySQLPassword)
	}
}

func TestLoadHomeConfig_MissingFileAndUnresolvableHome(t *testing.T) {
	cfg, err := LoadHomeConfig(t.TempDir())
	if err != nil || !reflect.DeepEqual(cfg, FileConfig{}) {
		t.Errorf("missing file: cfg = %+v, err = %v; want the zero value and no error", cfg, err)
	}
	cfg, err = LoadHomeConfig("")
	if err != nil || !reflect.DeepEqual(cfg, FileConfig{}) {
		t.Errorf("no home: cfg = %+v, err = %v; want the zero value and no error", cfg, err)
	}
}

func TestLoadHomeConfig_MalformedFileIsAHomeConfigReadError(t *testing.T) {
	home := t.TempDir()
	path := writeHomeConfig(t, home, `{ "dbPath": "/x",`)

	cfg, err := LoadHomeConfig(home)
	var readErr *HomeConfigReadError
	if !errors.As(err, &readErr) || readErr.Path != path {
		t.Fatalf("err = %v, want a *HomeConfigReadError naming %s", err, path)
	}
	if !reflect.DeepEqual(cfg, FileConfig{}) {
		t.Errorf("cfg = %+v, want the zero value", cfg)
	}
}

// --- UpdateHome ------------------------------------------------------------

func TestUpdateHome_WritesTheHomeConfig(t *testing.T) {
	cwd, home := t.TempDir(), t.TempDir()
	stale := writeWorkDirConfig(t, cwd, `{"myName": "from-the-repo"}`)
	before, err := os.ReadFile(stale)
	if err != nil {
		t.Fatalf("os.ReadFile: %v", err)
	}

	cfg, path, err := UpdateHome(home, func(cfg *FileConfig) error {
		cfg.MyName = "me"
		return nil
	})
	if err != nil {
		t.Fatalf("UpdateHome: %v", err)
	}
	if want := HomeConfigPath(home); path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	if cfg.MyName != "me" {
		t.Errorf("MyName = %q, want %q", cfg.MyName, "me")
	}
	if after, _ := os.ReadFile(stale); string(after) != string(before) {
		t.Error("the working-directory file was rewritten")
	}
	if got := LoadEffective(cwd, home).Config.MyName; got != "me" {
		t.Errorf("read back MyName = %q, want %q", got, "me")
	}
}

// TestUpdateHome_RefusesUnparseableFile is the write half of decision D-6:
// reading a broken home config is non-fatal, writing over one is refused, so
// a file we could not understand is never destroyed.
func TestUpdateHome_RefusesUnparseableFile(t *testing.T) {
	home := t.TempDir()
	const original = `{ "dbPath": "/home/graph.db",`
	path := writeHomeConfig(t, home, original)

	_, _, err := UpdateHome(home, func(cfg *FileConfig) error {
		cfg.MyName = "me"
		return nil
	})
	var readErr *HomeConfigReadError
	if !errors.As(err, &readErr) {
		t.Fatalf("err = %v, want a *HomeConfigReadError", err)
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != original {
		t.Errorf("the broken file was overwritten: %q", raw)
	}
}

func TestUpdateHome_UnresolvableHomeIsAnError(t *testing.T) {
	_, path, err := UpdateHome("", func(cfg *FileConfig) error {
		cfg.MyName = "me"
		return nil
	})
	if err == nil {
		t.Fatal("UpdateHome(\"\") = nil error, want a failure")
	}
	if path != "" {
		t.Errorf("path = %q, want \"\"", path)
	}
}

func TestUpdateHome_FnErrorWritesNothing(t *testing.T) {
	home := t.TempDir()
	sentinel := errors.New("stop")
	_, _, err := UpdateHome(home, func(cfg *FileConfig) error {
		cfg.MyName = "should not be saved"
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want the sentinel", err)
	}
	if _, statErr := os.Stat(HomeConfigPath(home)); statErr == nil {
		t.Error("the home config should not have been created")
	}
}

func TestSaveTo_CreatesParentDirectoryUserOnly(t *testing.T) {
	home := t.TempDir()
	if _, _, err := UpdateHome(home, func(cfg *FileConfig) error {
		cfg.MyName = "me"
		return nil
	}); err != nil {
		t.Fatalf("UpdateHome: %v", err)
	}
	dir, err := os.Stat(filepath.Join(home, ".graph-ops"))
	if err != nil {
		t.Fatalf("os.Stat: %v", err)
	}
	if perm := dir.Mode().Perm(); perm != 0o700 {
		t.Errorf("directory mode = %v, want 0700", perm)
	}
	file, err := os.Stat(HomeConfigPath(home))
	if err != nil {
		t.Fatalf("os.Stat: %v", err)
	}
	if perm := file.Mode().Perm(); perm != 0o600 {
		t.Errorf("file mode = %v, want 0600 (it can hold a MySQL password)", perm)
	}
}

func TestHomeConfigReadError_NamesTheFile(t *testing.T) {
	inner := errors.New("boom")
	err := &HomeConfigReadError{Path: "/home/.graph-ops/config.json", Err: inner}
	if !strings.Contains(err.Error(), "/home/.graph-ops/config.json") {
		t.Errorf("Error() = %q, want it to name the file", err.Error())
	}
	if !errors.Is(err, inner) {
		t.Error("the cause should be unwrappable")
	}
}

func TestHomeConfigPath(t *testing.T) {
	if got := HomeConfigPath(""); got != "" {
		t.Errorf("HomeConfigPath(\"\") = %q, want \"\"", got)
	}
	home := t.TempDir()
	want := filepath.Join(home, ".graph-ops", "config.json")
	if got := HomeConfigPath(home); got != want {
		t.Errorf("HomeConfigPath(%q) = %q, want %q", home, got, want)
	}
}
