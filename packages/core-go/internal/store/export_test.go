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
