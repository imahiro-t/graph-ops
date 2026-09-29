package store

// CloseForTest closes a SQL repository's connection pool. The repositories
// have no Close of their own (a graph-engine process keeps one for its whole
// life), but a test that opens many of them -- one per simulated process --
// must give their connections back, or repeated runs (-count) exhaust the
// MySQL server's max_connections.
func CloseForTest(repo any) error {
	switch r := repo.(type) {
	case *MySQLRepository:
		return r.db.Close()
	case *SQLiteRepository:
		return r.db.Close()
	}
	return nil
}

// SetClientSchemaForTest makes a SQL repository act as a client that knows
// schema version current and records minClient as the minimum (see
// schema_version.go), so that tests can play an older or a newer
// graph-engine against the same database. Production code has no way to do
// this.
func SetClientSchemaForTest(repo any, current, minClient int) {
	c := &clientSchema{Current: current, MinClient: minClient}
	switch r := repo.(type) {
	case *MySQLRepository:
		r.clientSchema = c
	case *SQLiteRepository:
		r.clientSchema = c
	}
}
