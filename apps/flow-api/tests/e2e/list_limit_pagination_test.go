package e2e

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/tests/helpers"
)

// TestListReactionsForTaskRespectsLimit verifies that ListReactionsForTask
// (previously an unbounded `:many` query) honours LIMIT/OFFSET and reports
// the pre-page total via COUNT(*) OVER(). Without the LIMIT, a task with a
// large reaction fan-out would return every row regardless of the caller's
// requested page size.
func TestListReactionsForTaskRespectsLimit(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	const totalReactions = 12
	const pageLimit = 5

	tt := newTenant(t)
	t.Cleanup(func() { helpers.PurgeWorkspace(t, testDB, tt.WorkspacePublicID) })

	// Create one task via the public API to react to.
	var created struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     "reaction target",
		"priority":  0,
	}, &created)
	require.NotEmpty(t, created.ID)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	wsInternalID := lookupWorkspaceInternalID(ctx, t, testDB, tt.WorkspacePublicID)
	userInternalID := lookupUserInternalID(ctx, t, testDB, tt.UserPublicID)
	taskInternalID := lookupTaskInternalID(ctx, t, testDB, created.ID)

	// Seed reactions directly. The unique key is (user_id, task_id, emoji,
	// enabled), so distinct emoji per row lets a single user own all
	// totalReactions rows without minting extra members.
	emojis := []string{
		"\U0001F600", "\U0001F601", "\U0001F602", "\U0001F603",
		"\U0001F604", "\U0001F605", "\U0001F606", "\U0001F607",
		"\U0001F608", "\U0001F609", "\U0001F60A", "\U0001F60B",
	}
	require.GreaterOrEqual(t, len(emojis), totalReactions)
	for i := 0; i < totalReactions; i++ {
		_, err := testDB.ExecContext(ctx,
			`INSERT INTO reactions (public_id, workspace_id, user_id, task_id, comment_id, emoji)
			 VALUES (?, ?, ?, ?, NULL, ?)`,
			types.New(), wsInternalID, userInternalID, taskInternalID, emojis[i])
		require.NoError(t, err, "seed reaction %d", i)
	}

	queries := generated.New(testDB)
	taskID := sql.NullInt32{Int32: int32(taskInternalID), Valid: true} //#nosec G115 -- test seed task id fits int32

	// First page: at most pageLimit rows, total reflects the full count.
	page1, err := queries.ListReactionsForTask(ctx, generated.ListReactionsForTaskParams{
		TaskID: taskID,
		Limit:  pageLimit,
		Offset: 0,
	})
	require.NoError(t, err)
	require.Lenf(t, page1, pageLimit, "first page must be clamped to the LIMIT")
	require.NotEmpty(t, page1)
	require.EqualValues(t, totalReactions, totalAsInt64ForTest(page1[0].Total),
		"COUNT(*) OVER() must report the pre-page total")

	// Page through with OFFSET and confirm we see every row exactly once.
	seen := make(map[string]struct{}, totalReactions)
	for offset := int32(0); ; offset += pageLimit {
		rows, perr := queries.ListReactionsForTask(ctx, generated.ListReactionsForTaskParams{
			TaskID: taskID,
			Limit:  pageLimit,
			Offset: offset,
		})
		require.NoError(t, perr)
		if len(rows) == 0 {
			break
		}
		require.LessOrEqual(t, len(rows), int(pageLimit), "page must never exceed LIMIT")
		for _, r := range rows {
			id := r.PublicID.String()
			_, dup := seen[id]
			require.Falsef(t, dup, "duplicate reaction %s at offset %d", id, offset)
			seen[id] = struct{}{}
		}
		if offset > int32(totalReactions)+pageLimit {
			t.Fatalf("pagination did not terminate (offset=%d)", offset)
		}
	}
	require.Lenf(t, seen, totalReactions,
		"expected %d unique reactions across all pages, got %d", totalReactions, len(seen))
}

// lookupTaskInternalID resolves a task's internal id from its public UUID.
// Test-only: production never crosses this boundary.
func lookupTaskInternalID(ctx context.Context, t *testing.T, db *sql.DB, publicID string) uint32 {
	t.Helper()
	pid, err := types.Parse(publicID)
	require.NoError(t, err)
	var id uint32
	err = db.QueryRowContext(ctx, `SELECT id FROM tasks WHERE public_id = ?`, pid).Scan(&id)
	require.NoError(t, err)
	return id
}

// totalAsInt64ForTest normalises the interface{} `total` column emitted by
// COUNT(*) OVER() into an int64 for assertions. MySQL returns it as int64
// or []byte depending on driver settings; handle both.
func totalAsInt64ForTest(v interface{}) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int32:
		return int64(x)
	case []byte:
		var n int64
		_, _ = fmt.Sscanf(string(x), "%d", &n)
		return n
	default:
		return 0
	}
}
