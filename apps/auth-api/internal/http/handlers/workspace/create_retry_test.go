package workspace

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/types"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
)

// impatientPool opens a second pool against the same server, pinned to
// a single connection whose innodb_lock_wait_timeout is 1 second. The
// handler under test runs on this pool so a row lock held elsewhere
// fails its transaction in about a second instead of the default 50,
// which is what makes the retry observable inside a test.
func impatientPool(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)
	_, err = db.Exec("SET SESSION innodb_lock_wait_timeout = 1")
	require.NoError(t, err)
	var got int
	require.NoError(t, db.QueryRow("SELECT @@innodb_lock_wait_timeout").Scan(&got))
	require.Equal(t, 1, got, "lock wait timeout must stick to the pooled connection")
	return db
}

// TestCreateWorkspaceSurvivesTransientLockContention is the regression
// for the first thing a new user ever does. Creating a workspace writes
// the workspace row, the owner membership, a personal calendar layer
// and a holiday subscription in one transaction, so it touches enough
// shared parents to lose a lock race under load. Losing that race is
// transient by definition — the standard MySQL answer is to run the
// transaction again — but the handler used to open its transaction by
// hand, so the first 1213/1205 came back as a permanent 500 on the
// onboarding step.
//
// The contention here is a lock held on the creator's users row, which
// the membership insert needs for its foreign-key check. It is released
// after the first attempt has timed out, so the retry has something to
// succeed at: a handler with no retry returns an error, a retrying one
// returns the workspace.
func TestCreateWorkspaceSurvivesTransientLockContention(t *testing.T) {
	inst := requireWorkspaceTxDB(t)
	ctx := context.Background()

	uid := raceNewUser(t, generated.New(inst.DB))

	db := impatientPool(t, inst.DSN)
	deps := Deps{DB: db, Queries: generated.New(db), Audit: audit.NoopSink{}}

	// Hold the creator's users row from an unrelated session. The
	// membership insert's FK check needs a shared lock on it and will
	// queue behind this exclusive one.
	lockTx, err := inst.DB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = lockTx.Rollback() }()
	var locked uint32
	require.NoError(t, lockTx.QueryRowContext(ctx,
		"SELECT id FROM users WHERE id = ? FOR UPDATE", uid).Scan(&locked))

	// Release once the first attempt has certainly timed out (1s) so the
	// retry runs against an uncontended row.
	released := make(chan struct{})
	go func() {
		time.Sleep(1500 * time.Millisecond)
		_ = lockTx.Rollback()
		close(released)
	}()

	slug := "retry-" + types.New().String()
	out, err := Create(deps)(authn.WithActor(ctx, uid), &CreateWorkspaceInput{
		Body: CreateWorkspaceInputBody{
			Slug:     slug,
			Name:     "Retry Under Contention",
			Timezone: "UTC",
			Country:  "US",
		},
	})
	<-released
	require.NoError(t, err,
		"a lock the contending session released must not surface as a permanent failure")
	require.NotEmpty(t, out.Body.ID)
	require.Equal(t, slug, out.Body.Slug)

	// The whole transaction landed, not just the workspace row: the
	// owner membership is what the retry had to redo.
	var members int
	require.NoError(t, inst.DB.QueryRow(`
		SELECT COUNT(*)
		FROM workspace_members m
		JOIN workspaces w ON w.id = m.workspace_id
		WHERE w.slug = ? AND m.user_id = ? AND m.role = 'owner'`,
		slug, uid,
	).Scan(&members))
	require.Equal(t, 1, members, "the retried attempt must leave exactly one owner membership")
}
