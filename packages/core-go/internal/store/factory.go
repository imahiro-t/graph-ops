package store

import "fmt"

// Config selects and configures a GraphRepository backend. Backend is
// "" or "sqlite" (default, for backward compatibility) or "mysql"; any
// other value is rejected by Open rather than silently falling back to
// sqlite (see DFLT-00020's completion criteria: an unsupported dbBackend
// must fail loudly at startup, not silently degrade).
//
// MySQLPassword is expected to already be resolved to its real value (not
// a "${ENV_VAR_NAME}" reference) -- see runtimeconfig.ResolveSecret, which
// cmd/graph-engine's loadRuntimeConfig applies before constructing this
// Config, so this package doesn't need to know about that syntax at all.
type Config struct {
	Backend    string
	SQLitePath string

	MySQLHost     string
	MySQLPort     int
	MySQLDatabase string
	MySQLUser     string
	MySQLPassword string

	// MySQLTLSMode selects how the connection to MySQLHost is secured: one
	// of MySQLTLSVerifyFull (the default when this is ""), MySQLTLSVerifyCA
	// or MySQLTLSDisabled -- see those constants and
	// NormalizeMySQLTLSMode/ValidateMySQLTLSSettings for the full contract.
	// There is deliberately no "try TLS, fall back to plaintext" mode: see
	// this ticket's (DFLT-00037) execution plan for why that downgrade is
	// never offered.
	MySQLTLSMode string
	// MySQLTLSCAFile is an absolute path to a PEM file naming the CA(s)
	// trusted for the server's certificate. Required when MySQLTLSMode is
	// MySQLTLSVerifyCA; optional for MySQLTLSVerifyFull (unset falls back
	// to the OS trust store); ignored for MySQLTLSDisabled.
	MySQLTLSCAFile string
}

// Open constructs the GraphRepository selected by cfg.Backend and
// initializes it (schema creation -- see GraphRepository.Init), consolidating
// what used to be two independent call sites (cmd/graph-engine's run() and
// cmdServe(), both calling NewSQLiteRepository+Init directly) into the one
// place that knows how to turn runtime settings into a ready-to-use
// repository.
func Open(cfg Config) (GraphRepository, error) {
	var repo GraphRepository
	var err error

	switch cfg.Backend {
	case "", "sqlite":
		repo, err = NewSQLiteRepository(cfg.SQLitePath)
	case "mysql":
		repo, err = NewMySQLRepository(cfg)
	default:
		return nil, fmt.Errorf(`unsupported db backend %q (must be "sqlite" or "mysql")`, cfg.Backend)
	}
	if err != nil {
		return nil, err
	}

	if err := repo.Init(); err != nil {
		return nil, err
	}
	return repo, nil
}
