// Command importasana copies Asana projects into a nodate-flow workspace:
// tasks, subtasks, assignees, watchers, dates, completion, comments, tags,
// sections, dependencies and attachments. It is an operations helper, not
// part of the API; the mapping and report contract is sql/core/IMPORTERS.md.
//
// Usage:
//
//	ASANA_TOKEN=... NF_DB_DSN=... \
//	  importasana -mapping mapping.yaml -dry-run
//	... importasana -mapping - < mapping.yaml
//	... importasana -mapping mapping.yaml -sync -dry-run
//
// The Asana token comes from the environment so it never appears in the
// process list. The database and object storage come from the same NF_*
// settings as the API. NF_OAUTH_ALLOWED_DOMAINS must match the value auth-api
// runs with, or a restricted deployment is reported as open.
//
// -sync makes earlier imports follow Asana: changed tasks and comments are
// rewritten and ones Asana no longer has are soft-deleted. Run it with
// -dry-run first to read the plan.
//
// Exit codes: 0 success; 1 one or more writes failed; 2 nothing was written
// (bad input, credentials, validation, fetch, a refused sync, or another run
// holding the lock).
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/asanaimport"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/config"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/storage"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtz"
)

func main() {
	os.Exit(run())
}

func run() int {
	mappingPath := flag.String("mapping", "", `mapping file path, or "-" for stdin (required)`)
	dryRun := flag.Bool("dry-run", false, "fetch and validate, print the report, write nothing")
	syncMode := flag.Bool("sync", false, "rewrite and delete earlier imports to match Asana")
	flag.Parse()

	if *mappingPath == "" {
		return fail("-mapping is required")
	}
	var mapping io.Reader = os.Stdin
	if *mappingPath != "-" {
		f, err := os.Open(*mappingPath)
		if err != nil {
			return fail(err)
		}
		defer f.Close()
		mapping = f
	}

	cfg, err := config.Load()
	if err != nil {
		return fail("load config:", err)
	}
	db, err := sql.Open("mysql", dbtz.NormalizeDSN(cfg.DbDsn))
	if err != nil {
		return fail("open db:", err)
	}
	defer db.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		return fail("connect db:", err)
	}
	if err := dbtz.AssertUTCSession(pingCtx, db); err != nil {
		return fail("db session timezone:", err)
	}

	var store *storage.Client
	if cfg.S3Endpoint != "" {
		store, err = storage.NewClient(storage.Config{
			Endpoint:  cfg.S3Endpoint,
			AccessKey: cfg.S3AccessKey,
			SecretKey: cfg.S3SecretKey,
			Bucket:    cfg.S3Bucket,
			UseSSL:    cfg.S3UseSSL,
		})
		if err != nil {
			return fail("storage:", err)
		}
	}

	code, err := asanaimport.Run(ctx, asanaimport.Options{
		Token:          os.Getenv("ASANA_TOKEN"),
		Mapping:        mapping,
		DryRun:         *dryRun,
		Sync:           *syncMode,
		DB:             db,
		Storage:        store,
		AllowedDomains: splitList(os.Getenv("NF_OAUTH_ALLOWED_DOMAINS")),
		Out:            os.Stdout,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		if code == asanaimport.ExitOK {
			code = asanaimport.ExitNoWrites
		}
	}
	return code
}

// fail prints the one-line fatal error and returns the "nothing written" code.
func fail(msg ...any) int {
	fmt.Fprintln(os.Stderr, append([]any{"error:"}, msg...)...)
	return asanaimport.ExitNoWrites
}

// splitList splits a comma-separated list, trimming and dropping empties.
func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
