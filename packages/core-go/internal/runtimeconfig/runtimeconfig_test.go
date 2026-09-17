package runtimeconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestIsEnvVarRef covers the "${ENV_VAR_NAME}" syntax completion criterion
// 7 relies on: a value must be recognized as an env var reference only when
// it, in its entirety, matches that shape -- a password that merely
// contains "${...}" as a substring (or has extra characters around it)
// must be treated as a plaintext secret, not misread as a reference.
func TestIsEnvVarRef(t *testing.T) {
	tests := []struct {
		raw     string
		wantOK  bool
		wantVar string
	}{
		{"${GRAPH_MYSQL_PASSWORD}", true, "GRAPH_MYSQL_PASSWORD"},
		{"${_leading_underscore}", true, "_leading_underscore"},
		{"plaintext-password", false, ""},
		{"", false, ""},
		{"prefix-${NOT_WHOLE_MATCH}", false, ""},
		{"${NOT_WHOLE_MATCH}-suffix", false, ""},
		{"${}", false, ""},
		{"${1STARTS_WITH_DIGIT}", false, ""},
	}
	for _, tt := range tests {
		gotVar, gotOK := IsEnvVarRef(tt.raw)
		if gotOK != tt.wantOK || gotVar != tt.wantVar {
			t.Errorf("IsEnvVarRef(%q) = (%q, %v), want (%q, %v)", tt.raw, gotVar, gotOK, tt.wantVar, tt.wantOK)
		}
	}
}

// TestResolveSecret_Plaintext covers a value that isn't a "${...}"
// reference: it must be returned completely unchanged, with no attempt to
// look anything up in the environment.
func TestResolveSecret_Plaintext(t *testing.T) {
	value, fromEnv, envVar, err := ResolveSecret("s3cret")
	if err != nil {
		t.Fatalf("ResolveSecret: %v", err)
	}
	if value != "s3cret" || fromEnv || envVar != "" {
		t.Errorf("ResolveSecret(%q) = (%q, %v, %q), want (%q, false, \"\")", "s3cret", value, fromEnv, envVar, "s3cret")
	}
}

// TestResolveSecret_FromEnv covers completion criterion 7: a
// "${ENV_VAR_NAME}"-shaped value resolves to that environment variable's
// actual value at connection time.
func TestResolveSecret_FromEnv(t *testing.T) {
	t.Setenv("GRAPH_TEST_SECRET_VAR", "the-real-password")
	value, fromEnv, envVar, err := ResolveSecret("${GRAPH_TEST_SECRET_VAR}")
	if err != nil {
		t.Fatalf("ResolveSecret: %v", err)
	}
	if value != "the-real-password" || !fromEnv || envVar != "GRAPH_TEST_SECRET_VAR" {
		t.Errorf("ResolveSecret = (%q, %v, %q), want (%q, true, %q)", value, fromEnv, envVar, "the-real-password", "GRAPH_TEST_SECRET_VAR")
	}
}

// TestResolveSecret_MissingEnvVarFailsLoudly covers the corresponding
// Gherkin scenario ("パスワード用の環境変数が未設定の場合は明確なエラーになる"):
// an unset referenced env var must be a hard error, never silently treated
// as an empty or literal "${...}" password.
func TestResolveSecret_MissingEnvVarFailsLoudly(t *testing.T) {
	// Deliberately not set by this test (nor, realistically, by anything
	// else) -- the point is that ResolveSecret errors when the referenced
	// variable is absent, not merely empty.
	const unset = "GRAPH_OPS_TEST_DEFINITELY_UNSET_VAR_DFLT_00020"
	value, fromEnv, envVar, err := ResolveSecret("${" + unset + "}")
	if err == nil {
		t.Fatalf("expected an error for an unset env var, got value=%q", value)
	}
	if !fromEnv || envVar != unset {
		t.Errorf("expected fromEnv=true, envVar=%q even on error, got fromEnv=%v, envVar=%q", unset, fromEnv, envVar)
	}
}

// TestRedactSecret covers the three shapes a stored secret can have, since
// only one of them (an actual plaintext password) must be withheld: "" has
// to stay distinguishable as "nothing configured", and a "${ENV_VAR}"
// reference is a variable name the settings UI needs in order to render its
// "read from env var X" hint.
func TestRedactSecret(t *testing.T) {
	for _, tc := range []struct{ name, raw, want string }{
		{"unset stays unset", "", ""},
		{"env var reference passes through", "${GRAPH_MYSQL_PASSWORD}", "${GRAPH_MYSQL_PASSWORD}"},
		{"plaintext is withheld", "hunter2", RedactedSecretPlaceholder},
		{"a value merely containing a reference is still plaintext", "x${GRAPH_MYSQL_PASSWORD}", RedactedSecretPlaceholder},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := RedactSecret(tc.raw); got != tc.want {
				t.Errorf("RedactSecret(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// TestIsLoopbackHost is the table DFLT-00023 items N-2/N-4 call for: one
// definition of "loopback" has to answer every shape a bind address or a
// Host header can arrive in. The 127.0.0.2 row is N-4's false positive (the
// old three-literal string comparison in cmd/graph-engine warned about it
// even though it is loopback), and the 0.0.0.0 / :: rows are the case that
// must stay false -- a wildcard bind is exactly what the exposure warning
// exists for.
func TestIsLoopbackHost(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"127.0.0.1", true},
		{"127.0.0.2", true},
		{"127.255.255.254", true},
		{"::1", true},
		{"[::1]", true}, // as it appears in a Host header
		{"localhost", true},
		{"LocalHost", true},  // DNS names are case-insensitive
		{"localhost.", true}, // fully qualified, single trailing dot
		{" localhost ", true},
		{"0.0.0.0", false},
		{"::", false},
		{"192.168.1.5", false},
		{"10.0.0.1", false},
		{"example.com", false},
		{"localhost.evil.example.com", false},
		{"", false},
		{"not an address", false},
		{"127.0.0.1:49173", false}, // a host:port pair is not a host
	}
	for _, tt := range tests {
		if got := IsLoopbackHost(tt.host); got != tt.want {
			t.Errorf("IsLoopbackHost(%q) = %v, want %v", tt.host, got, tt.want)
		}
	}
}

func TestIsWildcardHost(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"0.0.0.0", true},
		{"::", true},
		{"[::]", true},
		{"127.0.0.1", false},
		{"localhost", false},
		{"192.168.1.5", false},
		// Empty means "unset", which falls back to DefaultHost (loopback)
		// everywhere in this project -- never to a wildcard.
		{"", false},
	}
	for _, tt := range tests {
		if got := IsWildcardHost(tt.host); got != tt.want {
			t.Errorf("IsWildcardHost(%q) = %v, want %v", tt.host, got, tt.want)
		}
	}
}

// TestDisplayHost pins the mapping `graph-engine ui` builds its own base URL
// from (DFLT-00023 item N-1): a wildcard or unset bind address has to become
// something connectable, and a concrete address has to survive untouched --
// returning "localhost" for a concrete 192.168.1.5 is precisely the bug N-1
// reports.
func TestDisplayHost(t *testing.T) {
	tests := []struct {
		host string
		want string
	}{
		{"0.0.0.0", "localhost"},
		{"::", "localhost"},
		{"", "localhost"},
		{"127.0.0.1", "127.0.0.1"},
		{"127.0.0.2", "127.0.0.2"},
		{"192.168.1.5", "192.168.1.5"},
		{"::1", "::1"},
		{"localhost", "localhost"},
	}
	for _, tt := range tests {
		if got := DisplayHost(tt.host); got != tt.want {
			t.Errorf("DisplayHost(%q) = %q, want %q", tt.host, got, tt.want)
		}
	}
}

// TestLoad_MalformedJSONYieldsZeroConfig pins the contract C-5 tightened
// (DFLT-00023): when Load returns an error, the FileConfig it returns is the
// zero value, never whatever encoding/json managed to decode before hitting
// the syntax error. Every current caller checks err first, so this is not
// observable today -- which is exactly why it needs a test rather than a
// reader's goodwill to stay true.
func TestLoad_MalformedJSONYieldsZeroConfig(t *testing.T) {
	dir := t.TempDir()
	// "dbPath" parses fine and would be retained by a streaming decode; the
	// unterminated value after it is what makes the document invalid.
	const malformed = `{"dbPath": "/tmp/should-not-survive.db", "myName": }`
	if err := os.WriteFile(filepath.Join(dir, "graph-config.json"), []byte(malformed), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	cfg, path, err := Load(dir, t.TempDir())
	if err == nil {
		t.Fatal("Load returned nil error for malformed JSON")
	}
	if path == "" {
		t.Error("Load returned an empty path; the resolved path must be reported even on error")
	}
	if !reflect.DeepEqual(cfg, FileConfig{}) {
		t.Errorf("Load returned %+v on error, want the zero FileConfig", cfg)
	}
}

// TestLoad_MissingFileIsNotAnError keeps the other half of Load's contract
// explicit: an absent graph-config.json is the normal case (every setting
// unset), not a failure.
func TestLoad_MissingFileIsNotAnError(t *testing.T) {
	cfg, path, err := Load(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(cfg, FileConfig{}) {
		t.Errorf("Load returned %+v, want the zero FileConfig", cfg)
	}
	if path == "" {
		t.Error("Load returned an empty path")
	}
}
