// Command grant-admin grants instance administrator rights to an existing
// user identified by email. It is the first-administrator path for
// OIDC-only instances, where /admin/setup cannot be reached through a
// password sign-in: the user signs in once, then an operator runs
//
//	grant-admin -email user@example.com
//
// against the same NF_DB_DSN the API uses. The command is idempotent.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/config"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/types"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtz"
)

// mysqlDuplicateEntry is the MySQL error number for a unique-key violation.
const mysqlDuplicateEntry = 1062

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("grant-admin", flag.ContinueOnError)
	fs.SetOutput(stderr)
	emailFlag := fs.String("email", "", "email address of an existing user to grant instance admin")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*emailFlag) == "" {
		fmt.Fprintln(stderr, "grant-admin: -email is required")
		return 2
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "grant-admin: config: %v\n", err)
		return 1
	}
	db, err := sql.Open("mysql", dbtz.NormalizeDSN(cfg.DbDsn))
	if err != nil {
		fmt.Fprintf(stderr, "grant-admin: open database: %v\n", err)
		return 1
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	q := generated.New(db)
	granted, err := grantAdmin(ctx, q, audit.New(q), *emailFlag)
	if err != nil {
		fmt.Fprintf(stderr, "grant-admin: %v\n", err)
		return 1
	}
	email := normalizeEmail(*emailFlag)
	if granted {
		fmt.Fprintf(stdout, "granted instance admin to %s\n", email)
	} else {
		fmt.Fprintf(stdout, "%s is already an instance admin\n", email)
	}
	return 0
}

func normalizeEmail(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

// grantAdmin grants instance admin to the enabled user with the given
// email. It reports whether a grant was written; false means the user was
// already an active admin.
func grantAdmin(ctx context.Context, q *generated.Queries, rec audit.Sink, rawEmail string) (bool, error) {
	email := normalizeEmail(rawEmail)
	user, err := q.FindUserByEmail(ctx, email)
	if errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("no enabled user with email %q: the user must sign in once before being granted admin", email)
	}
	if err != nil {
		return false, fmt.Errorf("look up user: %w", err)
	}

	active, err := hasActiveGrant(ctx, q, user.ID)
	if err != nil {
		return false, err
	}
	if active {
		return false, nil
	}

	_, err = q.AdminGrantInstanceAdmin(ctx, generated.AdminGrantInstanceAdminParams{
		PublicID: types.New(),
		UserID:   user.ID,
	})
	if err != nil {
		var me *mysql.MySQLError
		if errors.As(err, &me) && me.Number == mysqlDuplicateEntry {
			// Either a concurrent grant won (now active) or the user holds a
			// revoked grant row, which the one-row-per-user key forbids
			// re-inserting.
			if active, aerr := hasActiveGrant(ctx, q, user.ID); aerr == nil && active {
				return false, nil
			}
			return false, fmt.Errorf("user %q has a revoked admin grant that cannot be re-granted by this command", email)
		}
		return false, fmt.Errorf("grant instance admin: %w", err)
	}

	rec.Record(ctx, audit.Entry{
		Action:       "admin.instance_admin.grant",
		ResourceType: "user",
		ResourceID:   user.PublicID.String(),
	})
	return true, nil
}

func hasActiveGrant(ctx context.Context, q *generated.Queries, userID uint32) (bool, error) {
	_, err := q.AdminFindInstanceAdminByUserId(ctx, userID)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, fmt.Errorf("check existing grant: %w", err)
}
