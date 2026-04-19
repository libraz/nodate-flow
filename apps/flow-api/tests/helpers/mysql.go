// Package helpers provides shared test helpers for apps/flow-api integration
// tests: a real MySQL container, a real Huma+chi server bound to that
// database, and tenant lifecycle helpers that exercise the public API
// rather than reaching into the database directly.
//
// Helpers in this package require Docker. Tests that import them must
// guard their use behind testing.Short() / NF_TEST_INTEGRATION so that
// `go test -short` stays fast on machines without Docker.
package helpers

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/mysql"
)

// MySQLInstance is a running MySQL 9.6 container plus an opened *sql.DB
// handle and the DSN used to connect.
type MySQLInstance struct {
	Container *mysql.MySQLContainer
	DB        *sql.DB
	DSN       string
}

const (
	// MySQL 9 Community is used in tests to match the production target
	// (see docs/requirements.md §3.5). Embeddings are stored via the
	// schema defined in ADR 0003.
	mysqlImage    = "mysql:9.6"
	mysqlDatabase = "nodate_flow_test"
	mysqlUser     = "nodate"
	mysqlPassword = "nodate"
)

var (
	sharedOnce sync.Once
	sharedInst *MySQLInstance
	sharedErr  error
)

// StartShared returns a process-wide MySQL instance with the schema
// applied exactly once. Subsequent callers receive the same handle.
//
// Tests that use StartShared MUST create their own tenant via
// CreateTestTenant so they remain isolated from other tests in the
// package.
func StartShared(t *testing.T) *MySQLInstance {
	t.Helper()
	sharedOnce.Do(func() {
		sharedInst, sharedErr = startMySQL(context.Background())
	})
	require.NoError(t, sharedErr, "shared MySQL container failed to start")
	require.NotNil(t, sharedInst)
	return sharedInst
}

// EnsureShared is the same as StartShared but without a *testing.T
// dependency, so it can be called from TestMain. The container is
// leaked to the process and reaped by testcontainers-ryuk on exit.
func EnsureShared() (*MySQLInstance, error) {
	sharedOnce.Do(func() {
		sharedInst, sharedErr = startMySQL(context.Background())
	})
	return sharedInst, sharedErr
}

// StartIsolated returns a brand new MySQL container, terminated when
// the test ends. Use this only when a test must mutate global DB state
// (e.g. schema reload) in a way that would break parallel tests.
func StartIsolated(t *testing.T) *MySQLInstance {
	t.Helper()
	inst, err := startMySQL(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = inst.DB.Close()
		_ = inst.Container.Terminate(context.Background())
	})
	return inst
}

// startMySQL boots a MySQL 9.6 testcontainer, opens a *sql.DB handle,
// and applies the full schema (sql/tables + sql/views).
func startMySQL(ctx context.Context) (*MySQLInstance, error) {
	startCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	container, err := mysql.Run(
		startCtx,
		mysqlImage,
		mysql.WithDatabase(mysqlDatabase),
		mysql.WithUsername(mysqlUser),
		mysql.WithPassword(mysqlPassword),
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

	if err := applySchema(ctx, db); err != nil {
		_ = db.Close()
		_ = container.Terminate(ctx)
		return nil, fmt.Errorf("apply schema: %w", err)
	}

	return &MySQLInstance{Container: container, DB: db, DSN: dsn}, nil
}

func waitForPing(ctx context.Context, db *sql.DB, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		ctxPing, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := db.PingContext(ctxPing)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("mysql ping never succeeded: %w", lastErr)
}

// applySchema loads every file in sql/tables/*.sql and sql/views/*.sql
// (alphabetical, with FK checks disabled) into the database. This
// mirrors the behaviour of sql/build-schema.sh but stays in-process so
// no shell is required.
func applySchema(ctx context.Context, db *sql.DB) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	tablesDir := filepath.Join(root, "sql", "tables")
	viewsDir := filepath.Join(root, "sql", "views")

	if _, err := db.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 0"); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, "SET UNIQUE_CHECKS = 0"); err != nil {
		return err
	}

	if err := execSQLDir(ctx, db, tablesDir); err != nil {
		return fmt.Errorf("tables: %w", err)
	}
	if err := execSQLDir(ctx, db, viewsDir); err != nil {
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

func execSQLDir(ctx context.Context, db *sql.DB, dir string) error {
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
		// multiStatements=true lets the driver execute all statements
		// in the file in a single Exec call.
		if _, err := db.ExecContext(ctx, string(raw)); err != nil {
			return fmt.Errorf("exec %s: %w", e.Name(), err)
		}
	}
	return nil
}

// repoRoot walks up from this file's location until it finds the
// repository root (the directory containing both `apps` and `sql`).
func repoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot resolve caller")
	}
	dir := filepath.Dir(file)
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "sql", "tables")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "apps", "flow-api")); err == nil {
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
