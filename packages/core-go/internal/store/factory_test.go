package store

import (
	"path/filepath"
	"testing"
)

// TestOpen_SQLiteDefaultAndExplicit covers store.Open's factory contract
// (completion criterion: existing SQLite operation is unaffected/backward
// compatible): both "" and "sqlite" must produce a working, initialized
// SQLiteRepository.
func TestOpen_SQLiteDefaultAndExplicit(t *testing.T) {
	for _, backend := range []string{"", "sqlite"} {
		t.Run("backend="+backend, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "test.db")
			repo, err := Open(Config{Backend: backend, SQLitePath: dbPath})
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			if _, ok := repo.(*SQLiteRepository); !ok {
				t.Fatalf("expected a *SQLiteRepository, got %T", repo)
			}
			// Init must have already run: a basic operation should work
			// without the caller calling Init again.
			if _, err := repo.CreateProject("P", "PPPPP"); err != nil {
				t.Fatalf("CreateProject on the opened repo: %v", err)
			}
		})
	}
}

// TestOpen_UnsupportedBackendFailsLoudly covers the completion criterion
// that an unrecognized dbBackend value must be a hard error, never a
// silent fallback to sqlite.
func TestOpen_UnsupportedBackendFailsLoudly(t *testing.T) {
	_, err := Open(Config{Backend: "postgres"})
	if err == nil {
		t.Fatal("expected an error for an unsupported backend")
	}
}

// TestMySQLDriverConfig_WiresConfiguredFields is a basic sanity check that
// mysqlDriverConfig wires store.Config's connection fields into the driver
// Config it builds. It uses the default TLS mode (verify-full, no CA file),
// so it also covers the plain wiring case without a certificate pool.
// mysql_tls_test.go covers the TLS-specific fields in depth.
func TestMySQLDriverConfig_WiresConfiguredFields(t *testing.T) {
	dc, err := mysqlDriverConfig(Config{
		MySQLHost: "db.example.com", MySQLPort: 3307, MySQLDatabase: "graph_ops",
		MySQLUser: "app", MySQLPassword: "s3cret",
	})
	if err != nil {
		t.Fatalf("mysqlDriverConfig: %v", err)
	}
	if dc.User != "app" || dc.Passwd != "s3cret" {
		t.Errorf("User/Passwd = %q/%q, want app/s3cret", dc.User, dc.Passwd)
	}
	if dc.Addr != "db.example.com:3307" {
		t.Errorf("Addr = %q, want db.example.com:3307", dc.Addr)
	}
	if dc.DBName != "graph_ops" {
		t.Errorf("DBName = %q, want graph_ops", dc.DBName)
	}
	// Not dc.Params["charset"]: go-sql-driver/mysql only special-cases
	// "charset" while parsing a DSN string (ParseDSN); Config.Params is
	// applied verbatim as `SET <key> = <value>` on every connection, and
	// "charset" is not a real MySQL system variable, so setting it there
	// would fail every real connection with "Unknown system variable
	// 'charset'" (Error 1193). The charset/collation must be wired via
	// Collation instead, which the driver encodes directly into the initial
	// handshake response packet.
	if dc.Collation != "utf8mb4_general_ci" {
		t.Errorf("Collation = %q, want %q", dc.Collation, "utf8mb4_general_ci")
	}
	if dc.Params["charset"] != "" {
		t.Errorf(`Params["charset"] = %q, want unset (Config.Params is not DSN-parsed; `+
			`setting "charset" there would fail with "Unknown system variable" on a real server)`,
			dc.Params["charset"])
	}
}
