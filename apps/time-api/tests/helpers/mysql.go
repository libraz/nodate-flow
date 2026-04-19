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
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/testcontainers/testcontainers-go/modules/mysql"
)

// MySQLInstance is a running MySQL container plus an opened *sql.DB handle.
type MySQLInstance struct {
	Container *mysql.MySQLContainer
	DB        *sql.DB
	DSN       string
}

const (
	mysqlImage    = "mysql:9.6"
	mysqlDatabase = "nodate_time_test"
	mysqlUser     = "nodate"
	mysqlPassword = "nodate"
)

var (
	sharedOnce sync.Once
	sharedInst *MySQLInstance
	sharedErr  error
)

// EnsureShared returns a process-wide MySQL instance with the schema applied
// exactly once. Subsequent calls return the same handle. Safe for TestMain.
func EnsureShared() (*MySQLInstance, error) {
	sharedOnce.Do(func() {
		sharedInst, sharedErr = startMySQL(context.Background())
	})
	return sharedInst, sharedErr
}

func startMySQL(ctx context.Context) (*MySQLInstance, error) {
	// Allow using an external DB via env var (e.g. CI with a sidecar container).
	if dsn := os.Getenv("ND_TEST_DB_DSN"); dsn != "" {
		db, err := sql.Open("mysql", dsn)
		if err != nil {
			return nil, fmt.Errorf("open external mysql: %w", err)
		}
		db.SetMaxOpenConns(20)
		if err := waitForPing(ctx, db, 30*time.Second); err != nil {
			_ = db.Close()
			return nil, err
		}
		if err := applySchema(ctx, db); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("apply schema: %w", err)
		}
		return &MySQLInstance{DB: db, DSN: dsn}, nil
	}

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
		if _, err := db.ExecContext(ctx, string(raw)); err != nil {
			return fmt.Errorf("exec %s: %w", e.Name(), err)
		}
	}
	return nil
}

func repoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot resolve caller")
	}
	dir := filepath.Dir(file)
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "sql", "tables")); err == nil {
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
