package e2e

import (
	"context"
	"database/sql"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/tests/helpers"
)

// TestKeysetPaginationListTasksForWorkspace exercises the keyset
// pagination contract on ListTasksForWorkspaceKeyset: paging through a
// fresh workspace populated with N tasks must yield every row exactly
// once, with no duplicates and no skips, and must terminate after
// ceil(N/limit) pages.
//
// The test uses a single tenant (parallel-safe via createTestTenant +
// PurgeWorkspace cleanup) and is gated by NF_TEST_INTEGRATION=1 like
// every other test in this package.
func TestKeysetPaginationListTasksForWorkspace(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	const totalRows = 30
	const pageLimit = 10
	const expectedPages = totalRows / pageLimit

	tt := newTenant(t)
	t.Cleanup(func() { helpers.PurgeWorkspace(t, testDB, tt.WorkspacePublicID) })

	// Seed the workspace with `totalRows` tasks via the public API. The
	// tasks are all created back-to-back by the same caller so their
	// created_at timestamps may share the same second; the
	// (created_at, public_id) tuple keyset is exactly the right tool to
	// disambiguate ties, which is what we are testing.
	for i := 0; i < totalRows; i++ {
		var created struct {
			ID string `json:"id"`
		}
		doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
			"projectId": tt.ProjectPublicID,
			"title":     "keyset task",
			"priority":  0,
		}, &created)
		require.NotEmpty(t, created.ID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	queries := generated.New(testDB)
	wsInternalID := lookupWorkspaceInternalID(ctx, t, testDB, tt.WorkspacePublicID)

	// Page through with keyset cursor until empty. Track every public_id
	// we have seen so we can detect duplicates and skips.
	seen := make(map[string]struct{}, totalRows)
	var cursorCreatedAt sql.NullTime
	var cursorPublicID types.PublicID

	pages := 0
	for {
		rows, err := queries.ListTasksForWorkspaceKeyset(ctx,
			generated.ListTasksForWorkspaceKeysetParams{
				WorkspaceID:     wsInternalID,
				StateFilter:     "", // empty string skips the filter
				CursorCreatedAt: cursorCreatedAt,
				CursorPublicID:  cursorPublicID,
				Limit:           pageLimit,
			})
		require.NoError(t, err, "page %d", pages+1)

		if len(rows) == 0 {
			break
		}
		pages++
		require.LessOrEqualf(t, len(rows), pageLimit,
			"page %d returned %d rows (limit was %d)", pages, len(rows), pageLimit)

		for _, r := range rows {
			id := r.PublicID.String()
			_, dup := seen[id]
			require.Falsef(t, dup, "duplicate public_id %s on page %d", id, pages)
			seen[id] = struct{}{}
		}

		// Advance the cursor to the LAST row of this page.
		last := rows[len(rows)-1]
		cursorCreatedAt = sql.NullTime{Time: last.CreatedAt, Valid: true}
		cursorPublicID = last.PublicID

		// Defensive cap so a regression that returns the same page
		// forever does not hang the test.
		if pages > expectedPages+2 {
			t.Fatalf("paged %d times, expected at most %d", pages, expectedPages)
		}
	}

	require.Equalf(t, expectedPages, pages,
		"expected exactly %d pages (totalRows=%d, limit=%d), got %d",
		expectedPages, totalRows, pageLimit, pages)
	require.Lenf(t, seen, totalRows,
		"expected %d unique public_ids across all pages, got %d", totalRows, len(seen))
}
