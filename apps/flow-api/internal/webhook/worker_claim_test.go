package webhook

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	// Side effect: register the MySQL database/sql driver.
	_ "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
	tcmysql "github.com/testcontainers/testcontainers-go/modules/mysql"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
)

// claimTestTables are the only tables the delivery claim path touches,
// in FK dependency order. The full-schema helpers in tests/helpers are
// deliberately not imported here: they pull in the HTTP router tree,
// which would couple this package's test build to the whole application.
var claimTestTables = []string{
	"workspaces.sql",
	"users.sql",
	"webhook_subscriptions.sql",
	"webhook_deliveries.sql",
}

// TestClaimBatchAtomicClaim verifies the multi-replica safety contract
// of the delivery queue on a real MySQL:
//
//   - claimBatch flips the rows it returns to 'delivering' before any
//     HTTP work, so a subsequent scan (another replica's tick) does not
//     re-select an already-claimed row.
//   - Rows currently row-locked by a concurrent claimer mid-transaction
//     are skipped via FOR UPDATE SKIP LOCKED instead of blocking or
//     double-claiming, and become claimable again once that transaction
//     releases the lock.
//
// The concurrent claimer is simulated with a second transaction holding
// a FOR UPDATE lock on the row, which is exactly the state a real
// replica is in between its claim SELECT and COMMIT.
func TestClaimBatchAtomicClaim(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	if os.Getenv("NF_TEST_INTEGRATION") == "" {
		t.Skip("set NF_TEST_INTEGRATION=1 to run webhook claim tests")
	}

	ctx := context.Background()
	db := startClaimTestDB(t)
	queries := generated.New(db)
	w := NewWorker(db, queries)

	wsID, subID := seedClaimTenant(ctx, t, db)

	t.Run("claimed row is not re-selected", func(t *testing.T) {
		pubID := seedClaimDelivery(ctx, t, queries, wsID, subID)

		first, err := w.claimBatch(ctx)
		require.NoError(t, err)
		require.Len(t, first, 1)
		require.Equal(t, pubID.String(), first[0].PublicID.String())
		require.Equal(t, "delivering", deliveryStatusByPublicID(ctx, t, db, pubID),
			"claimBatch must flip claimed rows to 'delivering' before any POST")

		second, err := w.claimBatch(ctx)
		require.NoError(t, err)
		require.Empty(t, second,
			"a committed claim must drop the row out of the pending queue")
	})

	t.Run("row locked by a concurrent claimer is skipped", func(t *testing.T) {
		pubID := seedClaimDelivery(ctx, t, queries, wsID, subID)

		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback() }()
		var lockedID uint32
		require.NoError(t, tx.QueryRowContext(ctx,
			`SELECT id FROM webhook_deliveries WHERE public_id = ? FOR UPDATE`,
			pubID).Scan(&lockedID))

		claimed, err := w.claimBatch(ctx)
		require.NoError(t, err)
		require.Empty(t, claimed,
			"FOR UPDATE SKIP LOCKED must skip rows held by a concurrent claimer")
		require.Equal(t, "pending", deliveryStatusByPublicID(ctx, t, db, pubID),
			"a skipped row must stay pending, untouched by the losing claimer")

		require.NoError(t, tx.Rollback())

		claimed, err = w.claimBatch(ctx)
		require.NoError(t, err)
		require.Len(t, claimed, 1,
			"once the concurrent lock is released the row must be claimable again")
		require.Equal(t, pubID.String(), claimed[0].PublicID.String())
	})
}

// startClaimTestDB boots a throwaway MySQL container and applies only
// the claim-path tables. FK checks are disabled while creating them
// because users carries an FK to storage_objects, which this test never
// touches and does not create.
func startClaimTestDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	container, err := tcmysql.Run(ctx, "mysql:9.6",
		tcmysql.WithDatabase("webhook_claim_test"),
		tcmysql.WithUsername("nodate"),
		tcmysql.WithPassword("nodate"),
	)
	require.NoError(t, err, "start mysql container")
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	dsn, err := container.ConnectionString(ctx, "parseTime=true", "multiStatements=true")
	require.NoError(t, err)
	db, err := sql.Open("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.Eventually(t, func() bool { return db.PingContext(ctx) == nil },
		time.Minute, 500*time.Millisecond, "mysql never became reachable")

	// SET FOREIGN_KEY_CHECKS is session-scoped, so the DDL must run on
	// a single pinned connection rather than the pool.
	conn, err := db.Conn(ctx)
	require.NoError(t, err)
	defer conn.Close()
	_, err = conn.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS=0")
	require.NoError(t, err)
	root := repoRootDir(t)
	for _, name := range claimTestTables {
		ddl, err := os.ReadFile(filepath.Join(root, "sql", "tables", name))
		require.NoErrorf(t, err, "read DDL %s", name)
		_, err = conn.ExecContext(ctx, string(ddl))
		require.NoErrorf(t, err, "apply DDL %s", name)
	}
	_, err = conn.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS=1")
	require.NoError(t, err)

	return db
}

// repoRootDir resolves the repository root from this file's location so
// the test can load table DDL from sql/tables regardless of the
// working directory `go test` runs in.
func repoRootDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "..")
}

// seedClaimTenant inserts the minimal workspace / user / subscription
// rows required by the delivery FKs and returns their internal ids.
func seedClaimTenant(ctx context.Context, t *testing.T, db *sql.DB) (wsID, subID uint32) {
	t.Helper()
	res, err := db.ExecContext(ctx,
		`INSERT INTO workspaces (public_id, slug, name) VALUES (?, 'webhook-claim-test', 'Webhook claim test')`,
		types.New())
	require.NoError(t, err)
	wsID = lastInsertID32(t, res)

	res, err = db.ExecContext(ctx,
		`INSERT INTO users (public_id, email, display_name) VALUES (?, 'webhook-claim-test@example.com', 'Webhook claim tester')`,
		types.New())
	require.NoError(t, err)
	userID := lastInsertID32(t, res)

	res, err = db.ExecContext(ctx,
		`INSERT INTO webhook_subscriptions (public_id, workspace_id, creator_id, url, secret, event_types)
		 VALUES (?, ?, ?, 'https://example.com/hook', 'test-secret', '["*"]')`,
		types.New(), wsID, userID)
	require.NoError(t, err)
	subID = lastInsertID32(t, res)
	return wsID, subID
}

// seedClaimDelivery inserts a due pending delivery row and returns its
// public id. next_retry_at is backdated one second so DATETIME(3)
// truncation can never push the row past NOW().
func seedClaimDelivery(ctx context.Context, t *testing.T, q *generated.Queries, wsID, subID uint32) types.PublicID {
	t.Helper()
	pubID := types.New()
	affected, err := q.CreateWebhookDelivery(ctx, generated.CreateWebhookDeliveryParams{
		PublicID:       pubID,
		WorkspaceID:    wsID,
		SubscriptionID: subID,
		EventType:      "task.created",
		PayloadJson:    []byte(`{"eventType":"task.created"}`),
		NextRetryAt:    sql.NullTime{Time: time.Now().UTC().Add(-time.Second), Valid: true},
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, affected)
	return pubID
}

// deliveryStatusByPublicID reads a delivery row's current status.
func deliveryStatusByPublicID(ctx context.Context, t *testing.T, db *sql.DB, pubID types.PublicID) string {
	t.Helper()
	var status string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT status FROM webhook_deliveries WHERE public_id = ?`, pubID).Scan(&status))
	return status
}

// lastInsertID32 narrows sql.Result.LastInsertId to the uint32 internal
// id space used by the schema.
func lastInsertID32(t *testing.T, res sql.Result) uint32 {
	t.Helper()
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return uint32(id) //#nosec G115 -- test-seeded auto-increment ids fit uint32
}
