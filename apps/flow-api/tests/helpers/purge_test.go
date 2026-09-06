package helpers

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

// TestPurgeWorkspaceLeavesNothingBehind is the guard on the per-test
// cleanup contract. It drives a workspace through a spread of the
// product surface, purges it, and then asks the database — not a list
// written out in this file — whether anything survived.
//
// The check is deliberately phrased against information_schema: any
// table carrying a workspace_id must hold zero rows for the purged
// workspace. A table added tomorrow is covered by this test today,
// which is the property a hand-maintained list cannot offer. Rows that
// outlive their workspace are not inert: they are visible to every
// later test in the same shared database, which is how a suite starts
// passing or failing depending on the order it ran in.
func TestPurgeWorkspaceLeavesNothingBehind(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	t.Parallel()

	db := StartShared(t).DB
	srv := StartTestServer(t, db)

	tenant := CreateTestTenant(t, srv.BaseURL)
	wsBase := srv.BaseURL + "/workspaces/" + tenant.WorkspacePublicID
	seedWorkspaceSurface(t, db, srv, tenant, wsBase)

	wsID, ok := WorkspaceInternalID(t, db, tenant.WorkspacePublicID)
	require.True(t, ok, "workspace should exist before the purge")

	// The fixture has to actually reach a broad set of tables, or an
	// empty residue afterwards would prove nothing.
	before := WorkspaceResidue(t, db, wsID)
	t.Logf("fixture populated %d of %d workspace-scoped tables: %v",
		len(before), len(WorkspaceScopedTables(t, db)), before)
	require.GreaterOrEqualf(t, len(before), 15,
		"fixture only populated %d workspace-scoped tables: %v", len(before), before)

	PurgeWorkspace(t, db, tenant.WorkspacePublicID)

	require.Empty(t, WorkspaceResidue(t, db, wsID),
		"rows survived the purge and are now visible to every other test")

	_, stillThere := WorkspaceInternalID(t, db, tenant.WorkspacePublicID)
	require.False(t, stillThere, "workspace row survived the purge")
}

// seedWorkspaceSurface exercises enough of the product to leave rows in
// a wide slice of the workspace-scoped tables — task, calendar, AI, and
// the workspace furniture (labels, sprints, lenses) that the tenant
// fixture alone does not touch.
func seedWorkspaceSurface(t *testing.T, db *sql.DB, srv *TestServer, tenant *TestTenant, wsBase string) {
	t.Helper()

	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, srv.BaseURL+"/tasks", tenant.AccessToken, map[string]any{
		"projectId": tenant.ProjectPublicID,
		"title":     "Purge fixture task",
	}, &task)
	require.NotEmpty(t, task.ID)
	taskBase := srv.BaseURL + "/tasks/" + task.ID

	doJSON(t, http.MethodPost, taskBase+"/comments", tenant.AccessToken,
		map[string]any{"body": "purge fixture comment"}, nil)

	doJSON(t, http.MethodPost, taskBase+"/reactions", tenant.AccessToken,
		map[string]any{"emoji": "👍"}, nil)

	var label struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, wsBase+"/labels", tenant.AccessToken,
		map[string]any{"name": "purge-" + randomHex(4), "color": "#3366ff"}, &label)
	require.NotEmpty(t, label.ID)
	doJSON(t, http.MethodPost, taskBase+"/labels", tenant.AccessToken,
		map[string]any{"labelId": label.ID}, nil)

	doJSON(t, http.MethodPost, wsBase+"/timeboxes", tenant.AccessToken, map[string]any{
		"name":     "Purge Fixture Sprint",
		"startsOn": "2026-05-01",
		"endsOn":   "2026-05-14",
	}, nil)

	// "sort" is a required field on the create body but a non-empty one is
	// refused (see validateLensSort / validateLensGroupBy in the lenses
	// handler, since no surface applies a lens ordering), so an empty
	// array is the only value that both satisfies the schema and is
	// accepted. This fixture only needs the row to exist for the residue
	// check, not an ordering.
	doJSON(t, http.MethodPost, wsBase+"/lenses", tenant.AccessToken, map[string]any{
		"name":      "Purge Fixture Lens",
		"filter":    json.RawMessage(`{"priority":{"gte":3}}`),
		"sort":      json.RawMessage(`[]`),
		"isDefault": false,
	}, nil)

	var calendar struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, wsBase+"/calendars", tenant.AccessToken, map[string]any{
		"kind":  "personal",
		"name":  "Purge Fixture Calendar",
		"color": "#4285F4",
	}, &calendar)
	require.NotEmpty(t, calendar.ID)

	var event struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, wsBase+"/calendars/"+calendar.ID+"/events", tenant.AccessToken, map[string]any{
		"kind":       "event",
		"title":      "Purge Fixture Event",
		"startAt":    time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC).Unix(),
		"endAt":      time.Date(2026, 7, 1, 11, 0, 0, 0, time.UTC).Unix(),
		"timezone":   "UTC",
		"visibility": "default",
	}, &event)
	require.NotEmpty(t, event.ID)

	SeedAgent(t, db, tenant.WorkspacePublicID, SeedAgentOptions{})
	seedRestrictedBlobs(t, db, tenant, task.ID, event.ID)
}

// seedRestrictedBlobs puts an ON DELETE RESTRICT edge inside the
// workspace's dependency graph, which is the only thing the sweep in
// PurgeWorkspace exists for.
//
// Everything else in a workspace hangs off workspaces(id) by CASCADE, so
// deleting the workspace row alone would clear it. attachments and
// calendar_event_attachments reference storage_objects with RESTRICT, and
// all three cascade from the workspace, so the cascade can reach
// storage_objects while a referencing row is still there. Without these
// rows the fixture never asks the sweep to do anything, and a sweep that
// deleted nothing at all would still leave a clean database behind.
//
// The rows go in through SQL rather than the upload API on purpose: what
// is under test is the delete order the foreign keys impose, not object
// storage, and requiring MinIO would make the guard skippable.
func seedRestrictedBlobs(t *testing.T, db *sql.DB, tenant *TestTenant, taskPublicID, eventPublicID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	wsID, ok := WorkspaceInternalID(t, db, tenant.WorkspacePublicID)
	require.True(t, ok, "workspace should exist while seeding")
	userID := internalIDByPublicID(ctx, t, db, "users", tenant.UserPublicID)
	taskID := internalIDByPublicID(ctx, t, db, "tasks", taskPublicID)
	eventID := internalIDByPublicID(ctx, t, db, "calendar_events", eventPublicID)

	newBlob := func(marker byte) uint32 {
		sha := make([]byte, 32)
		for i := range sha {
			sha[i] = marker
		}
		res, err := db.ExecContext(ctx, `
			INSERT INTO storage_objects (
				public_id, workspace_id, sha256, byte_size, content_type,
				storage_key, ref_count
			) VALUES (?, ?, ?, 11, 'text/plain', ?, 1)`,
			types.New(), wsID, sha,
			fmt.Sprintf("workspace/%s/%s", tenant.WorkspacePublicID, hex.EncodeToString(sha)))
		require.NoError(t, err, "insert storage_objects")
		id, err := res.LastInsertId()
		require.NoError(t, err)
		return uint32(id) //#nosec G115 -- LastInsertId in a test fixture
	}

	taskBlob := newBlob(0x11)
	_, err := db.ExecContext(ctx, `
		INSERT INTO attachments (
			public_id, workspace_id, task_id, uploader_id,
			storage_object_id, filename
		) VALUES (?, ?, ?, ?, ?, 'purge-fixture.txt')`,
		types.New(), wsID, taskID, userID, taskBlob)
	require.NoError(t, err, "insert attachments")

	eventBlob := newBlob(0x22)
	_, err = db.ExecContext(ctx, `
		INSERT INTO calendar_event_attachments (
			public_id, workspace_id, event_id, uploader_id,
			storage_object_id, filename
		) VALUES (?, ?, ?, ?, ?, 'purge-fixture-event.txt')`,
		types.New(), wsID, eventID, userID, eventBlob)
	require.NoError(t, err, "insert calendar_event_attachments")
}

// internalIDByPublicID resolves a row's internal id from its public id.
func internalIDByPublicID(ctx context.Context, t *testing.T, db *sql.DB, table, publicID string) uint32 {
	t.Helper()
	var id uint32
	err := db.QueryRowContext(ctx,
		"SELECT id FROM "+quoteIdent(t, table)+" WHERE public_id = UUID_TO_BIN(?, 0) LIMIT 1",
		publicID).Scan(&id)
	require.NoErrorf(t, err, "resolve %s id for %s", table, publicID)
	return id
}
