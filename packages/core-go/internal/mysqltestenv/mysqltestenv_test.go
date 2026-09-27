package mysqltestenv

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

func TestCheckDatabase(t *testing.T) {
	for _, tc := range []struct {
		name    string
		wantErr bool
	}{
		{"", true},
		{"graph_ops", true},
		{"GRAPH_OPS", true},
		{"Graph_Ops", true},
		{"graph_ops_ci", false},
		{"graph_ops_test_1_2", false},
	} {
		err := CheckDatabase(tc.name)
		if (err != nil) != tc.wantErr {
			t.Errorf("CheckDatabase(%q) = %v, want error: %v", tc.name, err, tc.wantErr)
		}
	}
}

// fakeTB records a Skip or Fatal instead of acting on the real test, and
// stops the calling goroutine the way the real ones do, so nothing after
// the call runs. Embedding testing.TB satisfies its unexported method;
// only the methods Load calls are overridden.
type fakeTB struct {
	testing.TB
	skipped string
	fatal   string
}

func (f *fakeTB) Helper() {}

func (f *fakeTB) Skipf(format string, args ...any) {
	f.skipped = fmt.Sprintf(format, args...)
	runtime.Goexit()
}

func (f *fakeTB) Fatalf(format string, args ...any) {
	f.fatal = fmt.Sprintf(format, args...)
	runtime.Goexit()
}

// runLoad calls Load against a fakeTB on its own goroutine (Goexit must not
// end the real test's goroutine) and reports what happened.
func runLoad(t *testing.T) (Settings, *fakeTB, bool) {
	t.Helper()
	tb := &fakeTB{TB: t}
	var got Settings
	returned := false
	done := make(chan struct{})
	go func() {
		defer close(done)
		got = Load(tb, "the tests under test")
		returned = true
	}()
	<-done
	return got, tb, returned
}

func setEnv(t *testing.T, env map[string]string) {
	t.Helper()
	for _, k := range []string{
		"GRAPH_TEST_MYSQL_HOST", "GRAPH_TEST_MYSQL_PORT", "GRAPH_TEST_MYSQL_DATABASE",
		"GRAPH_TEST_MYSQL_USER", "GRAPH_TEST_MYSQL_PASSWORD",
		"GRAPH_TEST_MYSQL_TLS", "GRAPH_TEST_MYSQL_TLS_CA",
	} {
		t.Setenv(k, env[k])
	}
}

func TestLoad_SkipsWithoutHost(t *testing.T) {
	// Even a working-database name is not an error when no server is
	// configured: nothing would connect to it.
	setEnv(t, map[string]string{"GRAPH_TEST_MYSQL_DATABASE": "graph_ops"})
	_, tb, returned := runLoad(t)
	if returned || tb.skipped == "" || tb.fatal != "" {
		t.Fatalf("returned=%v skipped=%q fatal=%q; want a skip only", returned, tb.skipped, tb.fatal)
	}
	if !strings.Contains(tb.skipped, "the tests under test") {
		t.Errorf("skip message %q does not name the tests", tb.skipped)
	}
}

func TestLoad_RefusesUnsafeDatabase(t *testing.T) {
	for _, db := range []string{"", "graph_ops", "GRAPH_OPS"} {
		t.Run(fmt.Sprintf("%q", db), func(t *testing.T) {
			setEnv(t, map[string]string{
				"GRAPH_TEST_MYSQL_HOST":     "127.0.0.1",
				"GRAPH_TEST_MYSQL_DATABASE": db,
			})
			_, tb, returned := runLoad(t)
			if returned || tb.fatal == "" || tb.skipped != "" {
				t.Fatalf("returned=%v skipped=%q fatal=%q; want a refusal", returned, tb.skipped, tb.fatal)
			}
			if !strings.Contains(tb.fatal, "refusing to run the tests under test") {
				t.Errorf("fatal message %q does not name the tests", tb.fatal)
			}
		})
	}
}

func TestLoad_RejectsInvalidPort(t *testing.T) {
	for _, port := range []string{"abc", "-1", "0", "65536"} {
		t.Run(port, func(t *testing.T) {
			setEnv(t, map[string]string{
				"GRAPH_TEST_MYSQL_HOST":     "127.0.0.1",
				"GRAPH_TEST_MYSQL_DATABASE": "graph_ops_ci",
				"GRAPH_TEST_MYSQL_PORT":     port,
			})
			_, tb, returned := runLoad(t)
			if returned || !strings.Contains(tb.fatal, "invalid GRAPH_TEST_MYSQL_PORT") {
				t.Fatalf("returned=%v fatal=%q; want an invalid-port failure", returned, tb.fatal)
			}
		})
	}
}

func TestLoad_ReturnsSettings(t *testing.T) {
	setEnv(t, map[string]string{
		"GRAPH_TEST_MYSQL_HOST":     "db.example",
		"GRAPH_TEST_MYSQL_PORT":     "3307",
		"GRAPH_TEST_MYSQL_DATABASE": "graph_ops_ci",
		"GRAPH_TEST_MYSQL_USER":     "u",
		"GRAPH_TEST_MYSQL_PASSWORD": "p",
		"GRAPH_TEST_MYSQL_TLS":      "disabled",
		"GRAPH_TEST_MYSQL_TLS_CA":   "/ca.pem",
	})
	got, tb, returned := runLoad(t)
	if !returned || tb.skipped != "" || tb.fatal != "" {
		t.Fatalf("returned=%v skipped=%q fatal=%q; want settings", returned, tb.skipped, tb.fatal)
	}
	want := Settings{Host: "db.example", Port: 3307, Database: "graph_ops_ci", User: "u", Password: "p", TLSMode: "disabled", TLSCAFile: "/ca.pem"}
	if got != want {
		t.Errorf("Load = %+v, want %+v", got, want)
	}
}

func TestLoad_DefaultPort(t *testing.T) {
	setEnv(t, map[string]string{
		"GRAPH_TEST_MYSQL_HOST":     "127.0.0.1",
		"GRAPH_TEST_MYSQL_DATABASE": "graph_ops_test_1_2",
	})
	got, _, returned := runLoad(t)
	if !returned || got.Port != 3306 || got.TLSMode != "" {
		t.Errorf("returned=%v Load = %+v; want port 3306 and TLS mode left unset", returned, got)
	}
}
