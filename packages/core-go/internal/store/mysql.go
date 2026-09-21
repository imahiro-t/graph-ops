package store

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/project"
)

// mysqlSchemaStatements is schemaDDL's MySQL equivalent (see that constant's
// doc comment for the overall shape). Differences from the SQLite version,
// all required by MySQL's dialect (see DFLT-00020's execution plan for the
// rationale behind each):
//
//   - TEXT columns can't carry a PRIMARY KEY/UNIQUE/FOREIGN KEY constraint in
//     MySQL's default configuration, so every id-shaped column is
//     VARCHAR(191) (the largest length that still fits utf8mb4's index-key
//     size limit).
//   - INTEGER -> INT; the "INTEGER NOT NULL DEFAULT 1" boolean idiom becomes
//     TINYINT(1) (read/written with the same boolToInt/`!= 0` pattern
//     already used for SQLite, since both are just database/sql ints).
//   - Body/free-text columns that can hold arbitrarily large content
//     (artifacts.content/metadata in particular, which carries base64 image
//     bytes or full HTML reports) are LONGTEXT rather than TEXT, which in
//     MySQL is capped at 64KiB.
//   - `... COLLATE NOCASE` (SQLite) becomes a `utf8mb4_general_ci` collation
//     on the column, which is already case-insensitive.
//   - Every table below explicitly sets COLLATE=utf8mb4_general_ci (not just
//     projects, which needs it for idx_projects_prefix_nocase) so all tables
//     share one collation regardless of the connected server's own default
//     collation for utf8mb4 (utf8mb4_general_ci on older MySQL/MariaDB,
//     utf8mb4_0900_ai_ci on a default MySQL 8.0 server). Foreign keys require
//     the referencing and referenced columns to use the same collation --
//     leaving even one table to inherit the server default instead of
//     stating it explicitly reintroduces a collation mismatch and makes the
//     FK's CREATE TABLE fail with error 3780 on MySQL 8.0 servers, exactly as
//     happened when only `projects` carried an explicit COLLATE.
//   - Foreign keys need an explicit ENGINE=InnoDB (MyISAM, MySQL's other
//     historical default, doesn't enforce them at all).
//
// The CREATE TABLE statements are kept as separate slice entries (rather
// than one `;`-joined string executed in a single Exec call) because the
// go-sql-driver/mysql config built by mysqlDriverConfig does not set
// multiStatements=true -- a single Exec carrying multiple statements is
// rejected by a real MySQL server as a syntax error after the first
// statement (the driver defaults multiStatements to false; enabling it
// would also change how "?" placeholders bind across statements, which
// isn't worth the trade-off here). Executing one statement per Exec call
// (see Init below) avoids that entirely and stays consistent with
// ensureColumn's existing one-ALTER-at-a-time pattern.
var mysqlSchemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS projects (
	id VARCHAR(191) PRIMARY KEY,
	name TEXT NOT NULL,
	prefix VARCHAR(20) NOT NULL,
	ticket_seq INT NOT NULL DEFAULT 0,
	created_at VARCHAR(64) NOT NULL,
	updated_at VARCHAR(64) NOT NULL,
	UNIQUE KEY idx_projects_prefix_nocase (prefix)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;`,

	`CREATE TABLE IF NOT EXISTS app_state (
	id INT PRIMARY KEY,
	current_project_id VARCHAR(191)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;`,

	`CREATE TABLE IF NOT EXISTS tickets (
	id VARCHAR(191) PRIMARY KEY,
	project_id VARCHAR(191),
	title TEXT NOT NULL,
	description LONGTEXT NOT NULL,
	status VARCHAR(64) NOT NULL,
	auto_executable TINYINT(1) NOT NULL DEFAULT 1,
	blocked TINYINT(1) NOT NULL DEFAULT 0,
	node_seq INT NOT NULL DEFAULT 0,
	refined_at VARCHAR(64),
	closed_reason LONGTEXT,
	assignee_name VARCHAR(191),
	graph_expanded_at VARCHAR(64),
	priority VARCHAR(16),
	created_at VARCHAR(64) NOT NULL,
	updated_at VARCHAR(64) NOT NULL,
	KEY idx_tickets_project (project_id),
	CONSTRAINT fk_tickets_project FOREIGN KEY (project_id) REFERENCES projects(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;`,

	`CREATE TABLE IF NOT EXISTS nodes (
	id VARCHAR(191) PRIMARY KEY,
	ticket_id VARCHAR(191) NOT NULL,
	name TEXT NOT NULL,
	type VARCHAR(64) NOT NULL,
	status VARCHAR(64) NOT NULL,
	iteration_count INT NOT NULL DEFAULT 0,
	max_iterations INT NOT NULL DEFAULT 3,
	assignee VARCHAR(191),
	is_manual TINYINT(1) NOT NULL DEFAULT 0,
	gate_id VARCHAR(191),
	criteria LONGTEXT,
	config_id VARCHAR(191),
	created_at VARCHAR(64) NOT NULL,
	updated_at VARCHAR(64) NOT NULL,
	KEY idx_nodes_ticket (ticket_id),
	CONSTRAINT fk_nodes_ticket FOREIGN KEY (ticket_id) REFERENCES tickets(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;`,

	`CREATE TABLE IF NOT EXISTS edges (
	id VARCHAR(191) PRIMARY KEY,
	ticket_id VARCHAR(191) NOT NULL,
	from_node_id VARCHAR(191) NOT NULL,
	to_node_id VARCHAR(191) NOT NULL,
	` + "`condition`" + ` VARCHAR(64),
	created_at VARCHAR(64) NOT NULL,
	KEY idx_edges_ticket (ticket_id),
	CONSTRAINT fk_edges_ticket FOREIGN KEY (ticket_id) REFERENCES tickets(id) ON DELETE CASCADE,
	CONSTRAINT fk_edges_from FOREIGN KEY (from_node_id) REFERENCES nodes(id) ON DELETE CASCADE,
	CONSTRAINT fk_edges_to FOREIGN KEY (to_node_id) REFERENCES nodes(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;`,

	`CREATE TABLE IF NOT EXISTS artifacts (
	id VARCHAR(191) PRIMARY KEY,
	ticket_id VARCHAR(191) NOT NULL,
	node_id VARCHAR(191) NOT NULL,
	name TEXT NOT NULL,
	type VARCHAR(64) NOT NULL,
	content LONGTEXT,
	file_path TEXT,
	metadata LONGTEXT,
	created_at VARCHAR(64) NOT NULL,
	KEY idx_artifacts_ticket (ticket_id),
	KEY idx_artifacts_node (node_id),
	CONSTRAINT fk_artifacts_ticket FOREIGN KEY (ticket_id) REFERENCES tickets(id) ON DELETE CASCADE,
	CONSTRAINT fk_artifacts_node FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;`,

	// Labels (DFLT-00084), see schemaDDL's labels/ticket_labels. name is
	// VARCHAR(100) (labels are capped at 50 characters) so the
	// (project_id, name) UNIQUE key fits InnoDB's 3072-byte index limit:
	// (191 + 100) * 4 = 1164 bytes.
	//
	// name alone uses the binary utf8mb4_bin collation, unlike every other
	// column (the table default stays utf8mb4_general_ci so the FK columns
	// keep matching projects/tickets -- see the collation note above).
	// Under utf8mb4_general_ci the UNIQUE key would treat names differing
	// only in accents ("cafe" / "café") or only in a supplementary-plane
	// character such as an emoji ("🐛 バグ" / "🚀 バグ" -- all such
	// characters weigh the same there) as duplicates, rejecting labels
	// SQLite accepts. The duplicate-name rule itself (case-insensitive,
	// strings.EqualFold) is enforced by the store's ensureLabelNameFree
	// under the project row lock, identically on both backends; this key
	// only stops byte-identical names. The labels table is new in
	// DFLT-00084 and never existed with the old collation outside that
	// ticket's own unreleased branch, so Init carries no ALTER for it.
	`CREATE TABLE IF NOT EXISTS labels (
	id VARCHAR(191) PRIMARY KEY,
	project_id VARCHAR(191) NOT NULL,
	name VARCHAR(100) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
	color VARCHAR(32) NOT NULL,
	created_at VARCHAR(64) NOT NULL,
	updated_at VARCHAR(64) NOT NULL,
	UNIQUE KEY idx_labels_project_name (project_id, name),
	CONSTRAINT fk_labels_project FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;`,

	`CREATE TABLE IF NOT EXISTS ticket_labels (
	ticket_id VARCHAR(191) NOT NULL,
	label_id VARCHAR(191) NOT NULL,
	created_at VARCHAR(64) NOT NULL,
	PRIMARY KEY (ticket_id, label_id),
	KEY idx_ticket_labels_label (label_id),
	CONSTRAINT fk_ticket_labels_ticket FOREIGN KEY (ticket_id) REFERENCES tickets(id) ON DELETE CASCADE,
	CONSTRAINT fk_ticket_labels_label FOREIGN KEY (label_id) REFERENCES labels(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;`,
}

// mysqlErDupEntry is MySQL's ER_DUP_ENTRY: a UNIQUE/PRIMARY KEY violation.
const mysqlErDupEntry = 1062

// isMySQLDuplicateKeyError reports whether err is (or wraps) a MySQL error
// 1062. Only that number counts: every other MySQL error is left as is, so
// it is never misreported as LABEL_NAME_TAKEN.
func isMySQLDuplicateKeyError(err error) bool {
	var myErr *mysqldriver.MySQLError
	return errors.As(err, &myErr) && myErr.Number == mysqlErDupEntry
}

// mysqlDialect is the shared label/ticket-update code's view of MySQL:
// explicit row locks (the pool has many connections) and error 1062.
var mysqlDialect = sqlDialect{
	forUpdate:         " FOR UPDATE",
	isUniqueViolation: isMySQLDuplicateKeyError,
}

// MySQLRepository implements GraphRepository on top of database/sql with
// the go-sql-driver/mysql driver. Unlike SQLiteRepository, it does not pin
// the pool to a single connection: MySQL is a real client/server database
// that serializes writes on its own, so normal connection pooling is safe.
// The read-increment-write sequences that mint ticket/node IDs
// (CreateTicket/CreateNode) instead use "SELECT ... FOR UPDATE" row locks
// inside a *sql.Tx to stay race-free under concurrent connections -- a
// guarantee SQLite got "for free" from SetMaxOpenConns(1) that MySQL has to
// earn explicitly.
type MySQLRepository struct {
	db *sql.DB
}

// MySQL TLS mode values accepted by NormalizeMySQLTLSMode/
// ValidateMySQLTLSSettings/mysqlDriverConfig (see this ticket's execution
// plan, D-1, for the rationale behind exactly these three and no others --
// in particular, why neither go-sql-driver/mysql's "preferred" nor
// "skip-verify" is offered).
const (
	// MySQLTLSVerifyFull is the default: the server's certificate chain and
	// hostname are both verified (Go's normal TLS client behavior). CA
	// verification uses cfg.MySQLTLSCAFile if set, or the OS trust store
	// otherwise.
	MySQLTLSVerifyFull = "verify-full"
	// MySQLTLSVerifyCA requires cfg.MySQLTLSCAFile and verifies the
	// server's certificate chain against it, but not the hostname --
	// needed for servers such as MySQL 8's auto-generated certificate,
	// whose CN/SAN do not name the host it is served from (see this
	// ticket's execution plan, F-5).
	MySQLTLSVerifyCA = "verify-ca"
	// MySQLTLSDisabled connects in plaintext. It must be chosen
	// explicitly -- see NormalizeMySQLTLSMode's doc comment for why an
	// unset value never means this.
	MySQLTLSDisabled = "disabled"
)

// NormalizeMySQLTLSMode maps mode as read from the home config file/
// GRAPH_MYSQL_TLS/the settings API to one of the three MySQLTLS* constants:
// "" (unset) becomes MySQLTLSVerifyFull -- this ticket's whole point is
// that an existing or new MySQL configuration gets a verified TLS
// connection by default, not a silent plaintext one -- and any of the three
// values themselves round-trips unchanged. Anything else, including a
// driver-level value this package deliberately does not support (in
// particular "preferred" and "skip-verify" -- see this ticket's execution
// plan, F-2/D-1, for why: "preferred" is exactly the fallback-to-plaintext
// downgrade this ticket exists to close, and "skip-verify" verifies nothing
// about the server it is talking to, so it protects against none of the
// impersonation this ticket's threat model cares about) or a casing/
// whitespace variant of a valid value, is an error rather than being read
// as one of the three -- there is no silent read-back to a safe default.
func NormalizeMySQLTLSMode(mode string) (string, error) {
	switch mode {
	case "":
		return MySQLTLSVerifyFull, nil
	case MySQLTLSVerifyFull, MySQLTLSVerifyCA, MySQLTLSDisabled:
		return mode, nil
	default:
		return "", fmt.Errorf(
			"unsupported mysql TLS mode %q (must be %q, %q, %q, or left unset for the default of %q)",
			mode, MySQLTLSVerifyFull, MySQLTLSVerifyCA, MySQLTLSDisabled, MySQLTLSVerifyFull)
	}
}

// ValidateMySQLTLSSettings normalizes mode (see NormalizeMySQLTLSMode) and
// checks it against caFile: MySQLTLSVerifyCA has nothing to verify the
// server's certificate chain against without a CA file (accepting it
// unset would silently fall back to the OS trust store, which lets any
// publicly-trusted certificate through and defeats the pinning
// verify-ca exists for -- see this ticket's execution plan, D-1), and a
// non-empty caFile must be an absolute path: this package reads it lazily,
// once per connection attempt (mysqlDriverConfig), by which time a
// relative path would be resolved against whatever the current process's
// cwd happens to be at that moment (the CLI's invocation directory, or the
// server's own working directory) rather than against the file the
// operator actually meant. It does not check that caFile exists or is
// readable -- that can only be answered by whatever machine actually opens
// the connection, which the caller of this function (a PUT/test-connection
// request handler, in particular) is not guaranteed to be running on.
func ValidateMySQLTLSSettings(mode, caFile string) error {
	normalized, err := NormalizeMySQLTLSMode(mode)
	if err != nil {
		return err
	}
	if normalized == MySQLTLSVerifyCA && caFile == "" {
		return fmt.Errorf("mysql TLS CA file is required when the TLS mode is %q", MySQLTLSVerifyCA)
	}
	if caFile != "" && !filepath.IsAbs(caFile) {
		return fmt.Errorf("mysql TLS CA file path must be absolute, got %q", caFile)
	}
	return nil
}

// mysqlTLSCertPool reads caFile (a PEM file) and returns the certificate
// pool it names, for use as either mysqlDriverConfig's RootCAs
// (MySQLTLSVerifyFull) or verify-ca's own manual chain verification. Errors
// deliberately never include caFile's content, only its path -- this value
// ends up in a *sql.DB open error, which (unlike a password) is not
// inherently secret, but a CA file's content is still not this package's
// to echo back.
func mysqlTLSCertPool(caFile string) (*x509.CertPool, error) {
	raw, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("reading mysql TLS CA file %s: %w", caFile, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(raw) {
		return nil, fmt.Errorf("no certificates found in mysql TLS CA file %s", caFile)
	}
	return pool, nil
}

// mysqlVerifyCAOnly builds the tls.Config.VerifyConnection callback
// MySQLTLSVerifyCA uses in place of Go's normal hostname-checking
// verification: it still verifies the presented certificate chains up to
// one of caPool's certificates, but never compares any name in the
// certificate against the host being dialed (see MySQLTLSVerifyCA's doc
// comment for why -- MySQL 8's auto-generated server certificate has no
// SAN, so ordinary verification always fails against it). This callback
// only runs when InsecureSkipVerify is true (see mysqlDriverConfig), which
// disables Go's own verification but not TLS itself -- the handshake, and
// therefore the identity of whoever holds caPool's private key, is still
// required to succeed before any data (including the password) crosses
// the wire.
func mysqlVerifyCAOnly(caPool *x509.CertPool) func(tls.ConnectionState) error {
	return func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 {
			return errors.New("mysql server presented no certificate")
		}
		opts := x509.VerifyOptions{Roots: caPool, Intermediates: x509.NewCertPool()}
		for _, intermediate := range cs.PeerCertificates[1:] {
			opts.Intermediates.AddCert(intermediate)
		}
		_, err := cs.PeerCertificates[0].Verify(opts)
		return err
	}
}

// mysqlDriverConfig builds the go-sql-driver/mysql *mysqldriver.Config
// openMySQLDB hands to mysqldriver.NewConnector -- the only place in this
// package (indeed, per this ticket's execution plan D-5, the only place
// that should exist anywhere) that turns a store.Config into driver
// settings. Using the driver's own Config/Connector, rather than building a
// DSN string by hand or via FormatDSN, gets user/password escaping right
// for values containing "@", ":" or "/", and -- the reason MySQLDSN was
// removed entirely -- is the only way to hand the driver a *tls.Config
// carrying a CA pool or a custom VerifyConnection callback: FormatDSN only
// ever writes out a TLSConfig *name*, silently dropping a *tls.Config set
// directly on the driver Config, which would have made a CA-based setup
// connect in plaintext without any error (see this ticket's execution
// plan, F-4).
//
// AllowFallbackToPlaintext is always explicitly set to false -- never left
// at the driver's own zero value -- so a future driver upgrade changing
// that default cannot silently reintroduce the exact downgrade this
// function exists to prevent; TestMySQLDriverConfig_NeverAllowsFallback
// pins this for all three TLS modes.
func mysqlDriverConfig(cfg Config) (*mysqldriver.Config, error) {
	mode, err := NormalizeMySQLTLSMode(cfg.MySQLTLSMode)
	if err != nil {
		return nil, err
	}
	if err := ValidateMySQLTLSSettings(mode, cfg.MySQLTLSCAFile); err != nil {
		return nil, err
	}

	dc := mysqldriver.NewConfig()
	dc.User = cfg.MySQLUser
	dc.Passwd = cfg.MySQLPassword
	dc.Net = "tcp"
	dc.Addr = fmt.Sprintf("%s:%d", cfg.MySQLHost, cfg.MySQLPort)
	dc.DBName = cfg.MySQLDatabase
	dc.ParseTime = false
	// NOT dc.Params["charset"] = "utf8mb4": go-sql-driver/mysql only gives
	// "charset" special handling inside ParseDSN's own string parsing (see
	// dsn.go's parseDSNParams); Config.Params is applied verbatim as
	// `SET <key> = <value>` for every connection (connection.go's
	// handleParams), and "charset" is not a real MySQL system variable, so
	// this would fail every real connection with
	// "Error 1193 (HY000): Unknown system variable 'charset'" regardless of
	// TLS mode. Since this package builds the driver Config directly rather
	// than going through a DSN string, the charset must be set via the
	// Collation field instead, which the driver encodes into the initial
	// handshake response packet (packets.go) rather than via a follow-up
	// SET statement.
	dc.Collation = "utf8mb4_general_ci"
	// Never a driver-recognized TLSConfig name (in particular never
	// "preferred", the one value that sets AllowFallbackToPlaintext on its
	// own) -- dc.TLS below is what actually configures TLS.
	dc.TLSConfig = ""
	dc.AllowFallbackToPlaintext = false

	switch mode {
	case MySQLTLSDisabled:
		dc.TLS = nil
	case MySQLTLSVerifyFull:
		tlsCfg := &tls.Config{ServerName: cfg.MySQLHost, MinVersion: tls.VersionTLS12}
		if cfg.MySQLTLSCAFile != "" {
			pool, err := mysqlTLSCertPool(cfg.MySQLTLSCAFile)
			if err != nil {
				return nil, err
			}
			// Setting RootCAs makes this CA the *only* one trusted for
			// this connection -- the OS trust store is not consulted in
			// addition to it. That is deliberate pinning, not an
			// oversight: see this ticket's execution plan, D-1/L-3.
			tlsCfg.RootCAs = pool
		}
		dc.TLS = tlsCfg
	case MySQLTLSVerifyCA:
		// ValidateMySQLTLSSettings already required a non-empty CA file
		// for this mode.
		pool, err := mysqlTLSCertPool(cfg.MySQLTLSCAFile)
		if err != nil {
			return nil, err
		}
		dc.TLS = &tls.Config{
			MinVersion: tls.VersionTLS12,
			// InsecureSkipVerify disables Go's built-in verification
			// (chain AND hostname) so VerifyConnection below is what runs
			// instead -- it is not "skip verification entirely". See
			// mysqlVerifyCAOnly's doc comment.
			InsecureSkipVerify: true, //nolint:gosec // verified by VerifyConnection below
			VerifyConnection:   mysqlVerifyCAOnly(pool),
		}
	}
	return dc, nil
}

// openMySQLDB builds a *sql.DB backed by go-sql-driver/mysql via
// mysqldriver.NewConnector (never sql.Open with a DSN string -- see
// mysqlDriverConfig's doc comment for why), configures its connection
// pool, and returns it without executing anything against the server yet
// (dialing happens lazily, on first use -- exactly like sql.Open).
func openMySQLDB(cfg Config) (*sql.DB, error) {
	dc, err := mysqlDriverConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("building mysql connection settings: %w", err)
	}
	connector, err := mysqldriver.NewConnector(dc)
	if err != nil {
		return nil, fmt.Errorf("building mysql connector: %w", err)
	}
	return sql.OpenDB(connector), nil
}

func NewMySQLRepository(cfg Config) (*MySQLRepository, error) {
	db, err := openMySQLDB(cfg)
	if err != nil {
		return nil, fmt.Errorf("opening mysql db: %w", err)
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(10)
	// serve runs as a long-lived process, and idle connections can be
	// silently dropped by the MySQL server's own wait_timeout or by
	// intermediate network equipment (NAT/firewalls) well before that.
	// Recycling connections periodically avoids handing out a
	// connection that looks alive to the pool but errors (or hangs)
	// against a peer that has already closed it. Keep this comfortably
	// shorter than typical wait_timeout defaults (MySQL's own default is
	// 8h) so recycling -- not the server -- is what triggers reconnects.
	db.SetConnMaxLifetime(3 * time.Minute)
	return &MySQLRepository{db: db}, nil
}

// PingMySQL opens a short-lived connection to cfg's target and pings it,
// without keeping the connection around -- this is the "接続テスト"
// (connection test) button's backing call, exercised before any settings
// are saved, so it must work against arbitrary in-progress form values, not
// just an already-configured repository.
func PingMySQL(ctx context.Context, cfg Config) error {
	db, err := openMySQLDB(cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	return db.PingContext(ctx)
}

// Init applies mysqlSchemaStatements (idempotent: CREATE TABLE IF NOT
// EXISTS, one statement per Exec call -- see mysqlSchemaStatements' doc
// comment for why it isn't one Exec call for the whole DDL).
func (r *MySQLRepository) Init() error {
	for _, stmt := range mysqlSchemaStatements {
		if _, err := r.db.Exec(stmt); err != nil {
			return fmt.Errorf("applying mysql schema: %w", err)
		}
	}
	// DFLT-00080 migration: drop the legacy projects.work_dir column from a
	// DB created before that ticket (see SQLiteRepository's
	// dropLegacyProjectsWorkDir for why its values are not carried over).
	// Checking first keeps Init idempotent; a DROP that loses a race with
	// another member's (or process's) concurrent Init is not an error (see
	// dropLegacyProjectsWorkDirColumn).
	if err := dropLegacyProjectsWorkDirColumn("mysql", func() (bool, error) {
		return r.mysqlColumnExists("projects", "work_dir")
	}, func() error {
		_, err := r.db.Exec(`ALTER TABLE projects DROP COLUMN work_dir`)
		return err
	}); err != nil {
		return err
	}
	// DFLT-00083 migration: tickets whose priority is NULL become MEDIUM.
	return backfillNullTicketPriority(r.db)
}

// mysqlColumnExists checks INFORMATION_SCHEMA.COLUMNS for the current
// database (DATABASE()). Used by Init's legacy-column migration and by tests
// to assert whether a schema has a given column.
func (r *MySQLRepository) mysqlColumnExists(table, column string) (bool, error) {
	var count int
	row := r.db.QueryRow(
		`SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME = ?`,
		table, column,
	)
	if err := row.Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

// --- Tickets ---

func (r *MySQLRepository) CreateTicket(projectID string, t domain.Ticket) (domain.Ticket, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return domain.Ticket{}, err
	}
	defer tx.Rollback() //nolint:errcheck

	var prefix string
	var seq int
	// FOR UPDATE locks the projects row for the duration of this
	// transaction, so a concurrent CreateTicket for the same project can't
	// read the same ticket_seq and mint a duplicate ID -- the guarantee
	// SQLiteRepository gets from SetMaxOpenConns(1) instead (see that
	// type's doc comment).
	row := tx.QueryRow(`SELECT prefix, ticket_seq FROM projects WHERE id = ? FOR UPDATE`, projectID)
	if err := row.Scan(&prefix, &seq); err != nil {
		if err == sql.ErrNoRows {
			return domain.Ticket{}, domain.NewAPIError(domain.ErrCodeProjectNotFound, "project %s not found", projectID)
		}
		return domain.Ticket{}, fmt.Errorf("loading project %s: %w", projectID, err)
	}
	seq++
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.Exec(`UPDATE projects SET ticket_seq = ?, updated_at = ? WHERE id = ?`, seq, now, projectID); err != nil {
		return domain.Ticket{}, fmt.Errorf("incrementing project ticket_seq: %w", err)
	}
	id := fmt.Sprintf("%s-%05d", prefix, seq)

	_, err = tx.Exec(
		`INSERT INTO tickets (id, project_id, title, description, status, auto_executable, blocked, node_seq, assignee_name, priority, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?)`,
		id, projectID, t.Title, t.Description, t.Status,
		boolToInt(t.AutoExecutable), boolToInt(t.Blocked), nullableString(t.Assignee), ticketPriorityOrDefault(t.Priority), now, now,
	)
	if err != nil {
		return domain.Ticket{}, fmt.Errorf("inserting ticket: %w", err)
	}
	if len(t.Labels) > 0 {
		if err := replaceTicketLabels(tx, id, projectID, labelIDsOf(t.Labels)); err != nil {
			return domain.Ticket{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return domain.Ticket{}, err
	}
	got, err := r.GetTicket(id)
	if err != nil {
		return domain.Ticket{}, err
	}
	return *got, nil
}

func (r *MySQLRepository) GetTicket(id string) (*domain.Ticket, error) {
	return getTicketWithLabels(r.db, sqlDialect{}, id)
}

func (r *MySQLRepository) GetTicketDetail(id string) (*domain.TicketDetail, error) {
	t, err := r.GetTicket(id)
	if err != nil || t == nil {
		return nil, err
	}
	nodes, err := r.ListNodesByTicket(t.ID)
	if err != nil {
		return nil, err
	}
	edges, err := r.ListEdgesByTicket(t.ID)
	if err != nil {
		return nil, err
	}
	artifacts, err := r.ListArtifactsByTicket(t.ID)
	if err != nil {
		return nil, err
	}
	return &domain.TicketDetail{Ticket: *t, Nodes: nodes, Edges: edges, Artifacts: artifacts}, nil
}

// ListTickets: see SQLiteRepository.ListTickets.
func (r *MySQLRepository) ListTickets() ([]domain.Ticket, error) {
	return listTicketsWithLabels(r.db,
		`SELECT `+ticketSelectCols+` FROM tickets ORDER BY created_at DESC`, nil,
		``, nil)
}

// ListTicketsByProject: see SQLiteRepository.ListTicketsByProject.
func (r *MySQLRepository) ListTicketsByProject(projectID string) ([]domain.Ticket, error) {
	return listTicketsWithLabels(r.db,
		`SELECT `+ticketSelectCols+` FROM tickets WHERE project_id = ? ORDER BY created_at DESC`, []any{projectID},
		`JOIN tickets t ON t.id = tl.ticket_id WHERE t.project_id = ?`, []any{projectID})
}

// UpdateTicket: see updateTicket (labels.go).
func (r *MySQLRepository) UpdateTicket(id string, patch TicketPatch) (domain.Ticket, error) {
	return updateTicket(r.db, mysqlDialect, id, patch)
}

func (r *MySQLRepository) DeleteTicket(id string) error {
	cur, err := r.GetTicket(id)
	if err != nil {
		return err
	}
	if cur == nil {
		return nil
	}
	_, err = r.db.Exec(`DELETE FROM tickets WHERE id = ?`, cur.ID)
	return err
}

// --- Nodes ---

func (r *MySQLRepository) CreateNode(n domain.GraphNode) (domain.GraphNode, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return domain.GraphNode{}, err
	}
	defer tx.Rollback() //nolint:errcheck

	var seq int
	row := tx.QueryRow(`SELECT node_seq FROM tickets WHERE id = ? FOR UPDATE`, n.TicketID)
	if err := row.Scan(&seq); err != nil {
		if err == sql.ErrNoRows {
			return domain.GraphNode{}, fmt.Errorf("ticket %s not found", n.TicketID)
		}
		return domain.GraphNode{}, fmt.Errorf("loading ticket %s: %w", n.TicketID, err)
	}
	seq++
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.Exec(`UPDATE tickets SET node_seq = ?, updated_at = ? WHERE id = ?`, seq, now, n.TicketID); err != nil {
		return domain.GraphNode{}, fmt.Errorf("incrementing ticket node_seq: %w", err)
	}
	id := fmt.Sprintf("%s-%02d", n.TicketID, seq)

	maxIter := n.MaxIterations
	if maxIter == 0 {
		maxIter = 3
	}
	_, err = tx.Exec(
		`INSERT INTO nodes (id, ticket_id, name, type, status, iteration_count, max_iterations, assignee, is_manual, gate_id, criteria, config_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, n.TicketID, n.Name, n.Type, n.Status, n.IterationCount, maxIter,
		nullableString(n.Assignee), boolToInt(n.IsManual), nullableString(n.GateID), nullableString(n.Criteria), nullableString(n.ConfigID),
		now, now,
	)
	if err != nil {
		return domain.GraphNode{}, fmt.Errorf("inserting node: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return domain.GraphNode{}, err
	}
	got, err := r.GetNode(id)
	if err != nil {
		return domain.GraphNode{}, err
	}
	return *got, nil
}

func (r *MySQLRepository) GetNode(id string) (*domain.GraphNode, error) {
	row := r.db.QueryRow(`SELECT `+nodeSelectCols+` FROM nodes WHERE id = ?`, id)
	n, err := scanNode(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting node %s: %w", id, err)
	}
	return n, nil
}

func (r *MySQLRepository) ListNodesByTicket(ticketID string) ([]domain.GraphNode, error) {
	rows, err := r.db.Query(`SELECT `+nodeSelectCols+` FROM nodes WHERE ticket_id = ? ORDER BY created_at ASC`, ticketID)
	if err != nil {
		return nil, fmt.Errorf("listing nodes for ticket %s: %w", ticketID, err)
	}
	defer rows.Close()
	out := []domain.GraphNode{}
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	return out, rows.Err()
}

func (r *MySQLRepository) UpdateNode(id string, patch NodePatch) (domain.GraphNode, error) {
	return updateNodeColumns(r.db, r.GetNode, id, patch)
}

// ClaimNode implements GraphRepository.ClaimNode; see that interface's doc
// comment for the contract.
func (r *MySQLRepository) ClaimNode(id string, newStatus domain.NodeStatus, excluded []domain.NodeStatus) (*domain.GraphNode, error) {
	return claimNodeCAS(r.db, r.GetNode, id, newStatus, excluded)
}

func (r *MySQLRepository) DeleteNode(id string) error {
	cur, err := r.GetNode(id)
	if err != nil {
		return err
	}
	if cur == nil {
		return nil
	}
	_, err = r.db.Exec(`DELETE FROM nodes WHERE id = ?`, cur.ID)
	return err
}

// --- Edges ---

func (r *MySQLRepository) CreateEdge(e domain.GraphEdge) (domain.GraphEdge, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	condition := e.Condition
	if condition == "" {
		condition = domain.EdgeAlways
	}
	_, err := r.db.Exec(
		"INSERT INTO edges (id, ticket_id, from_node_id, to_node_id, `condition`, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		e.ID, e.TicketID, e.FromNodeID, e.ToNodeID, condition, now,
	)
	if err != nil {
		return domain.GraphEdge{}, fmt.Errorf("inserting edge: %w", err)
	}
	e.Condition = condition
	e.CreatedAt = now
	return e, nil
}

func (r *MySQLRepository) ListEdgesByTicket(ticketID string) ([]domain.GraphEdge, error) {
	rows, err := r.db.Query(
		"SELECT "+mysqlEdgeSelectCols+" FROM edges WHERE ticket_id = ? ORDER BY created_at ASC",
		ticketID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing edges for ticket %s: %w", ticketID, err)
	}
	defer rows.Close()
	out := []domain.GraphEdge{}
	for rows.Next() {
		e, err := scanEdge(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListTicketGraphs implements TicketGraphLister; see that interface's doc
// comment for the contract.
func (r *MySQLRepository) ListTicketGraphs(ticketIDs []string) (map[string][]domain.GraphNode, map[string][]domain.GraphEdge, error) {
	return listTicketGraphs(r.db, mysqlEdgeSelectCols, ticketIDs)
}

func (r *MySQLRepository) ClearEdgesByTicket(ticketID string) error {
	_, err := r.db.Exec(`DELETE FROM edges WHERE ticket_id = ?`, ticketID)
	return err
}

// --- Artifacts ---

func (r *MySQLRepository) CreateArtifact(a domain.Artifact) (domain.Artifact, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := r.db.Exec(
		`INSERT INTO artifacts (id, ticket_id, node_id, name, type, content, file_path, metadata, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.TicketID, a.NodeID, a.Name, a.Type,
		nullableString(a.Content), nullableString(a.FilePath), nullableString(a.Metadata), now,
	)
	if err != nil {
		return domain.Artifact{}, fmt.Errorf("inserting artifact: %w", err)
	}
	a.CreatedAt = now
	a.HasContent = a.Content != nil && *a.Content != ""
	return a, nil
}

func (r *MySQLRepository) GetArtifact(id string) (*domain.Artifact, error) {
	row := r.db.QueryRow(`SELECT `+artifactSelectCols+` FROM artifacts WHERE id = ?`, id)
	a, err := scanArtifact(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting artifact %s: %w", id, err)
	}
	return a, nil
}

func (r *MySQLRepository) ListArtifactsByTicket(ticketID string) ([]domain.Artifact, error) {
	rows, err := r.db.Query(`SELECT `+artifactSummaryCols+` FROM artifacts WHERE ticket_id = ? ORDER BY created_at ASC`, ticketID)
	if err != nil {
		return nil, fmt.Errorf("listing artifacts for ticket %s: %w", ticketID, err)
	}
	defer rows.Close()
	out := []domain.Artifact{}
	for rows.Next() {
		a, err := scanArtifactSummary(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

func (r *MySQLRepository) ListArtifactsByNode(nodeID string) ([]domain.Artifact, error) {
	rows, err := r.db.Query(`SELECT `+artifactSelectCols+` FROM artifacts WHERE node_id = ? ORDER BY created_at ASC`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("listing artifacts for node %s: %w", nodeID, err)
	}
	defer rows.Close()
	out := []domain.Artifact{}
	for rows.Next() {
		a, err := scanArtifact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// --- Projects ---

// CreateProject mirrors SQLiteRepository.CreateProject's atomicity
// guarantee (resolving the prefix inside the same transaction that inserts
// the row), using FOR UPDATE to lock the existing prefixes for the
// duration -- see CreateTicket's doc comment for why MySQL needs this
// explicit locking where SQLite didn't.
func (r *MySQLRepository) CreateProject(name, prefix string) (domain.Project, error) {
	if name == "" {
		return domain.Project{}, domain.NewAPIError(domain.ErrCodeValidation, "project name is required")
	}

	tx, err := r.db.Begin()
	if err != nil {
		return domain.Project{}, err
	}
	defer tx.Rollback() //nolint:errcheck

	rows, err := tx.Query(`SELECT prefix FROM projects FOR UPDATE`)
	if err != nil {
		return domain.Project{}, fmt.Errorf("listing existing prefixes: %w", err)
	}
	var existing []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return domain.Project{}, err
		}
		existing = append(existing, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return domain.Project{}, err
	}
	rows.Close()

	resolvedPrefix, err := project.ResolvePrefix(name, prefix, existing)
	if err != nil {
		return domain.Project{}, err
	}

	id := "proj-" + shortUUID()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.Exec(
		`INSERT INTO projects (id, name, prefix, ticket_seq, created_at, updated_at) VALUES (?, ?, ?, 0, ?, ?)`,
		id, name, resolvedPrefix, now, now,
	); err != nil {
		return domain.Project{}, fmt.Errorf("inserting project: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return domain.Project{}, err
	}

	got, err := r.GetProject(id)
	if err != nil {
		return domain.Project{}, err
	}
	return *got, nil
}

func (r *MySQLRepository) GetProject(id string) (*domain.Project, error) {
	row := r.db.QueryRow(`SELECT `+projectSelectCols+` FROM projects WHERE id = ?`, id)
	p, err := scanProject(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting project %s: %w", id, err)
	}
	return p, nil
}

func (r *MySQLRepository) ListProjects() ([]domain.Project, error) {
	rows, err := r.db.Query(`SELECT ` + projectSelectCols + ` FROM projects ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("listing projects: %w", err)
	}
	defer rows.Close()
	out := []domain.Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func (r *MySQLRepository) UpdateProject(id string, patch ProjectPatch) (domain.Project, error) {
	return updateProjectColumns(r.db, r.GetProject, id, patch)
}

// DeleteProject mirrors SQLiteRepository.DeleteProject (see its doc
// comment): explicit ticket deletion (tickets.project_id has no ON DELETE
// CASCADE) and clearing app_state.current_project_id, both inside one
// transaction.
func (r *MySQLRepository) DeleteProject(id string) error {
	cur, err := r.GetProject(id)
	if err != nil {
		return err
	}
	if cur == nil {
		return nil
	}

	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.Exec(`UPDATE app_state SET current_project_id = NULL WHERE current_project_id = ?`, id); err != nil {
		return fmt.Errorf("clearing current project pointer for %s: %w", id, err)
	}
	if _, err := tx.Exec(`DELETE FROM tickets WHERE project_id = ?`, id); err != nil {
		return fmt.Errorf("deleting tickets under project %s: %w", id, err)
	}
	if err := deleteProjectLabels(tx, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM projects WHERE id = ?`, id); err != nil {
		return fmt.Errorf("deleting project %s: %w", id, err)
	}

	return tx.Commit()
}

// --- Labels (DFLT-00084; shared implementation in labels.go) ---

func (r *MySQLRepository) CreateLabel(projectID, name, color string) (domain.Label, error) {
	return createLabel(r.db, mysqlDialect, projectID, name, color)
}

func (r *MySQLRepository) GetLabel(id string) (*domain.Label, error) {
	return getLabel(r.db, sqlDialect{}, id)
}

func (r *MySQLRepository) ListLabelsByProject(projectID string) ([]domain.LabelUsage, error) {
	return listLabelsByProject(r.db, projectID)
}

func (r *MySQLRepository) UpdateLabel(id string, patch LabelPatch) (domain.Label, error) {
	return updateLabel(r.db, mysqlDialect, id, patch)
}

func (r *MySQLRepository) DeleteLabel(id string) (int, error) {
	return deleteLabel(r.db, mysqlDialect, id)
}

func (r *MySQLRepository) GetCurrentProjectID() (string, error) {
	var id sql.NullString
	row := r.db.QueryRow(`SELECT current_project_id FROM app_state WHERE id = 1`)
	err := row.Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("getting current project: %w", err)
	}
	if !id.Valid {
		return "", nil
	}
	return id.String, nil
}

func (r *MySQLRepository) SetCurrentProjectID(projectID string) error {
	_, err := r.db.Exec(
		`INSERT INTO app_state (id, current_project_id) VALUES (1, ?)
		 ON DUPLICATE KEY UPDATE current_project_id = VALUES(current_project_id)`,
		projectID,
	)
	if err != nil {
		return fmt.Errorf("setting current project: %w", err)
	}
	return nil
}
