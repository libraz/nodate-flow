// Package testhelpers provides service-agnostic integration-test
// primitives: a shared MySQL 9 testcontainer with the full repo
// schema loaded, a repo-root finder, and a per-file SQL executor.
//
// Apps (flow-api, auth-api) and shared kits (itemkit) wrap this
// package with service-specific tenant / server bootstrap as needed.
// Keeping the container + schema application here guarantees itemkit
// tests run against the same schema as the owning service integration
// tests.
//
// Requires Docker. Callers should guard tests behind
// testing.Short() / NF_TEST_INTEGRATION so `go test -short` stays
// fast on machines without Docker.
package testhelpers

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	// Registers the "mysql" driver with database/sql for the handles this
	// package hands back; nothing here references the package by name.
	_ "github.com/go-sql-driver/mysql"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/mysql"
)

// MySQLImage is the testcontainers image tag used across all
// services. Kept as a package variable so a single CI override can
// re-pin every service's integration tests in one place.
var MySQLImage = "mysql:9.6"

// MySQLConfig controls MySQL container startup. Zero value is valid:
// the defaults match the flow-api helper's historical behavior.
type MySQLConfig struct {
	// Database is the schema name created inside the container. When
	// empty, "nodate_flow_test" is used.
	Database string
	// Username / Password for the container's root user. Zero values
	// default to "nodate" / "nodate".
	Username string
	Password string
	// ExternalDSN, if non-empty, bypasses the container and connects
	// to an already-running MySQL (used by CI sidecar configs via
	// NF_TEST_DB_DSN). Schema is still applied on first connect.
	ExternalDSN string
}

// MySQLInstance is a running MySQL container plus an opened *sql.DB
// handle and the DSN used to connect. Container is nil when the
// instance is an ExternalDSN override.
type MySQLInstance struct {
	Container *mysql.MySQLContainer
	DB        *sql.DB
	DSN       string
}

// Close tears down the DB handle and terminates the container (when
// one was started by this package). Callers that use StartShared do
// not need to call Close — the process exit reaps it.
func (m *MySQLInstance) Close(ctx context.Context) error {
	if m == nil {
		return nil
	}
	if m.DB != nil {
		_ = m.DB.Close()
	}
	if m.Container != nil {
		return m.Container.Terminate(ctx)
	}
	return nil
}

// SharedMySQL caches one MySQLInstance per configured database name
// so multiple test packages in the same process share a container.
type SharedMySQL struct {
	once sync.Once
	inst *MySQLInstance
	err  error
	cfg  MySQLConfig
}

// NewSharedMySQL returns a lazy singleton. Start is goroutine-safe
// and is invoked on first call; subsequent calls return the same
// handle (or error).
func NewSharedMySQL(cfg MySQLConfig) *SharedMySQL {
	return &SharedMySQL{cfg: cfg}
}

// Start ensures the container is running, schema is applied, and the
// DB handle is open. Safe to call from TestMain or from each test.
func (s *SharedMySQL) Start(ctx context.Context) (*MySQLInstance, error) {
	s.once.Do(func() {
		s.inst, s.err = startMySQL(ctx, s.cfg)
	})
	return s.inst, s.err
}

// StartIsolated returns a brand new MySQL container (not cached).
// Use this only when a test must mutate DB state in a way that would
// break parallel tests. Callers are responsible for Close().
func StartIsolated(ctx context.Context, cfg MySQLConfig) (*MySQLInstance, error) {
	return startMySQL(ctx, cfg)
}

func startMySQL(ctx context.Context, cfg MySQLConfig) (*MySQLInstance, error) {
	database := cfg.Database
	if database == "" {
		database = "nodate_flow_test"
	}
	username := cfg.Username
	if username == "" {
		username = "nodate"
	}
	password := cfg.Password
	if password == "" {
		password = "nodate"
	}

	if cfg.ExternalDSN != "" {
		return openExternal(ctx, cfg.ExternalDSN)
	}
	if dsn := os.Getenv("NF_TEST_DB_DSN"); dsn != "" {
		return openExternal(ctx, dsn)
	}

	startCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	container, err := mysql.Run(
		startCtx,
		MySQLImage,
		mysql.WithDatabase(database),
		mysql.WithUsername(username),
		mysql.WithPassword(password),
		// Required before the schema's triggers will CREATE while the
		// binary log is on.
		testcontainers.WithCmdArgs("--log-bin-trust-function-creators=1"),
	)
	if err != nil {
		return nil, fmt.Errorf("start mysql container: %w", err)
	}

	dsn, err := container.ConnectionString(ctx, "parseTime=true", "multiStatements=true")
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, fmt.Errorf("connection string: %w", err)
	}

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, fmt.Errorf("open mysql: %w", err)
	}
	db.SetMaxOpenConns(20)

	if err := waitForPing(ctx, db, 60*time.Second); err != nil {
		_ = db.Close()
		_ = container.Terminate(ctx)
		return nil, err
	}

	if err := ApplyRepoSchema(ctx, db); err != nil {
		_ = db.Close()
		_ = container.Terminate(ctx)
		return nil, fmt.Errorf("apply schema: %w", err)
	}

	return &MySQLInstance{Container: container, DB: db, DSN: dsn}, nil
}

func openExternal(ctx context.Context, dsn string) (*MySQLInstance, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("open external mysql: %w", err)
	}
	db.SetMaxOpenConns(20)
	if err := waitForPing(ctx, db, 30*time.Second); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := ApplyRepoSchema(ctx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &MySQLInstance{DB: db, DSN: dsn}, nil
}

func waitForPing(ctx context.Context, db *sql.DB, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := db.PingContext(pingCtx)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("mysql ping never succeeded: %w", lastErr)
}

// ApplyRepoSchema loads the layered schema — tables, then cross-layer
// constraints, then views, then triggers — alphabetically within each
// directory and with FK checks disabled. Equivalent to sql/build-schema.sh
// but in-process.
//
// Cross-layer foreign keys live in sql/flow/constraints and must be applied
// after every CREATE TABLE of both layers, because they reference tables from
// each.
//
// Triggers are part of the schema, not an optional extra: the
// calendar_events projection guard rejects writes that would desync a task
// from its event, so a helper that skipped it would let those tests pass
// against a database the product never runs on.
func ApplyRepoSchema(ctx context.Context, db *sql.DB) error {
	root, err := RepoRoot()
	if err != nil {
		return err
	}
	coreTablesDir := filepath.Join(root, "sql", "core", "tables")
	flowTablesDir := filepath.Join(root, "sql", "flow", "tables")
	constraintsDir := filepath.Join(root, "sql", "flow", "constraints")
	viewsDir := filepath.Join(root, "sql", "flow", "views")
	coreTriggersDir := filepath.Join(root, "sql", "core", "triggers")
	flowTriggersDir := filepath.Join(root, "sql", "flow", "triggers")

	if _, err := db.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 0"); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, "SET UNIQUE_CHECKS = 0"); err != nil {
		return err
	}
	if err := ExecSQLDirs(ctx, db, coreTablesDir, flowTablesDir); err != nil {
		return fmt.Errorf("tables: %w", err)
	}
	if err := ExecSQLDir(ctx, db, constraintsDir); err != nil {
		return fmt.Errorf("constraints: %w", err)
	}
	if err := ExecSQLDir(ctx, db, viewsDir); err != nil {
		return fmt.Errorf("views: %w", err)
	}
	if err := ExecSQLDirs(ctx, db, coreTriggersDir, flowTriggersDir); err != nil {
		return fmt.Errorf("triggers: %w", err)
	}
	if _, err := db.ExecContext(ctx, "SET UNIQUE_CHECKS = 1"); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 1"); err != nil {
		return err
	}
	return nil
}

// ExecSQLDirs executes every *.sql file across the given directories,
// directory by directory and alphabetically within each, matching the
// emission order of sql/build-schema.sh.
//
// Creation order carries no semantics here: the caller runs this with FK
// checks disabled, and no delete path may depend on the order InnoDB walks
// a cascade chain. See sql/build-schema.sh for the two ON DELETE RESTRICT
// edges that make that rule worth stating.
func ExecSQLDirs(ctx context.Context, db *sql.DB, dirs ...string) error {
	for _, dir := range dirs {
		if err := ExecSQLDir(ctx, db, dir); err != nil {
			return err
		}
	}
	return nil
}

// ExecSQLDir executes every *.sql file in dir in alphabetical order,
// splitting each file into statements first so trigger bodies — which
// carry their own DELIMITER directive and internal semicolons — load the
// same way the mysql client would run them.
func ExecSQLDir(ctx context.Context, db *sql.DB, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, stmt := range splitSQLStatements(string(raw)) {
			if strings.TrimSpace(stmt) == "" {
				continue
			}
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("exec %s: %w", e.Name(), err)
			}
		}
	}
	return nil
}

// splitSQLStatements cuts a .sql file into individually executable
// statements, honouring the DELIMITER directive. DELIMITER is a mysql
// client feature rather than SQL, so a trigger body — whose IF / SIGNAL
// statements end in semicolons of their own — has to be reassembled here
// before it can be sent over the wire as one statement.
func splitSQLStatements(raw string) []string {
	delimiter := ";"
	var out []string
	var current strings.Builder
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToUpper(trimmed), "DELIMITER ") {
			if stmt := strings.TrimSpace(current.String()); stmt != "" {
				out = append(out, stmt)
				current.Reset()
			}
			delimiter = strings.TrimSpace(strings.TrimPrefix(trimmed, "DELIMITER "))
			continue
		}
		current.WriteString(line)
		current.WriteByte('\n')
		if strings.HasSuffix(strings.TrimSpace(current.String()), delimiter) {
			stmt := strings.TrimSpace(current.String())
			stmt = strings.TrimSuffix(stmt, delimiter)
			out = append(out, strings.TrimSpace(stmt))
			current.Reset()
		}
	}
	if stmt := strings.TrimSpace(current.String()); stmt != "" {
		out = append(out, stmt)
	}
	return out
}

// RepoRoot walks up from this file's location until it finds the
// repository root (the directory containing both `apps` and `sql`).
func RepoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot resolve caller")
	}
	dir := filepath.Dir(file)
	for i := 0; i < 12; i++ {
		if _, err := os.Stat(filepath.Join(dir, "sql", "core", "tables")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "apps")); err == nil {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("repo root not found from %s", file)
}
