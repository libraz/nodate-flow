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
	"sort"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
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

// ApplyRepoSchema loads the layered schema (sql/core/tables, sql/flow/tables,
// sql/flow/constraints, sql/flow/views) alphabetically with FK checks
// disabled. Equivalent to sql/build-schema.sh but in-process.
//
// Cross-layer foreign keys live in sql/flow/constraints and must be applied
// after every CREATE TABLE of both layers, because they reference tables from
// each.
func ApplyRepoSchema(ctx context.Context, db *sql.DB) error {
	root, err := RepoRoot()
	if err != nil {
		return err
	}
	coreTablesDir := filepath.Join(root, "sql", "core", "tables")
	flowTablesDir := filepath.Join(root, "sql", "flow", "tables")
	constraintsDir := filepath.Join(root, "sql", "flow", "constraints")
	viewsDir := filepath.Join(root, "sql", "flow", "views")

	if _, err := db.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 0"); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, "SET UNIQUE_CHECKS = 0"); err != nil {
		return err
	}
	if err := ExecSQLDirsMerged(ctx, db, coreTablesDir, flowTablesDir); err != nil {
		return fmt.Errorf("tables: %w", err)
	}
	if err := ExecSQLDir(ctx, db, constraintsDir); err != nil {
		return fmt.Errorf("constraints: %w", err)
	}
	if err := ExecSQLDir(ctx, db, viewsDir); err != nil {
		return fmt.Errorf("views: %w", err)
	}
	if _, err := db.ExecContext(ctx, "SET UNIQUE_CHECKS = 1"); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 1"); err != nil {
		return err
	}
	return nil
}

// ExecSQLDir executes every *.sql file in dir in alphabetical order.
// The driver must be opened with multiStatements=true for files that
// contain multiple statements.
// ExecSQLDirsMerged executes every .sql file across the given directories
// ordered by FILENAME rather than by directory.
//
// The merge is load-bearing, not cosmetic. InnoDB evaluates a DELETE's
// cascade chain in table creation order, and workspace teardown relies on
// `attachments` rows going away before the `storage_objects` rows they
// reference via fk_attachments_storage_object (ON DELETE RESTRICT). Loading
// directory-by-directory would create storage_objects (core) before
// attachments (flow) and turn workspace deletion into a 1451 error. Sorting
// by filename reproduces the single-directory order this schema was built
// under, and matches sql/build-schema.sh.
func ExecSQLDirsMerged(ctx context.Context, db *sql.DB, dirs ...string) error {
	type entry struct{ name, path string }
	var all []entry
	for _, dir := range dirs {
		items, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, e := range items {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
				continue
			}
			all = append(all, entry{e.Name(), filepath.Join(dir, e.Name())})
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].name < all[j].name })

	for _, e := range all {
		raw, err := os.ReadFile(e.path)
		if err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx, string(raw)); err != nil {
			return fmt.Errorf("exec %s: %w", e.name, err)
		}
	}
	return nil
}

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
		if _, err := db.ExecContext(ctx, string(raw)); err != nil {
			return fmt.Errorf("exec %s: %w", e.Name(), err)
		}
	}
	return nil
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
