package e2e

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestAuditCalendarEventLifecycle exercises the calendar event CRUD path
// and verifies that each mutation produces the expected audit_logs entry.
// Calendar mutations were previously the only mutation domain that did
// not feed the audit recorder, so this guards the gap from regressing:
// create / update / delete must each append a workspace-scoped row that
// surfaces in v_workspace_activity alongside tasks, projects, etc.
func TestAuditCalendarEventLifecycle(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	calID := createCalendarMut(t, owner, "Audit Cal")
	evtID := createEventMut(t, owner, calID, "Audit Event")

	// Update the event title.
	var updated struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	newStart := time.Date(2027, 7, 1, 9, 0, 0, 0, time.UTC)
	doJSON(t, http.MethodPatch,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/calendars/"+calID+"/events/"+evtID,
		owner.AccessToken, map[string]any{
			"title":   "Audit Event Renamed",
			"startAt": newStart.Unix(),
			"endAt":   newStart.Add(time.Hour).Unix(),
		}, &updated)
	require.Equal(t, "Audit Event Renamed", updated.Title)

	// Delete (soft-delete) the event.
	status, body := doJSONStatus(t, http.MethodDelete,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/calendars/"+calID+"/events/"+evtID,
		owner.AccessToken, nil)
	require.Equal(t, http.StatusOK, status, "delete event must succeed; body=%s", string(body))

	logs := queryAuditLogs(t, testDB, owner.WorkspacePublicID)

	t.Run("calendar.event.create", func(t *testing.T) {
		t.Parallel()
		row := findAudit(t, logs, "calendar.event.create", "calendar.event")
		require.True(t, row.ActorUserID.Valid, "calendar.event.create must record an actor")
		require.True(t, row.ResourcePublicID.Valid, "calendar.event.create must record a resource id")
		require.Equal(t, evtID, row.ResourcePublicID.String,
			"resource public id must match the created event")
	})

	t.Run("calendar.event.update", func(t *testing.T) {
		t.Parallel()
		row := findAudit(t, logs, "calendar.event.update", "calendar.event")
		require.True(t, row.ActorUserID.Valid, "calendar.event.update must record an actor")
		require.True(t, row.ResourcePublicID.Valid, "calendar.event.update must record a resource id")
		require.Equal(t, evtID, row.ResourcePublicID.String,
			"resource public id must match the updated event")
	})

	t.Run("calendar.event.delete", func(t *testing.T) {
		t.Parallel()
		row := findAudit(t, logs, "calendar.event.delete", "calendar.event")
		require.True(t, row.ActorUserID.Valid, "calendar.event.delete must record an actor")
		require.True(t, row.ResourcePublicID.Valid, "calendar.event.delete must record a resource id")
		require.Equal(t, evtID, row.ResourcePublicID.String,
			"resource public id must match the deleted event")
	})
}

// TestAuditCalendarMemoLifecycle mirrors the event lifecycle test for the
// calendar memo surface: create / update / delete must each append a
// calendar.memo audit row scoped to the actor's workspace.
func TestAuditCalendarMemoLifecycle(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	calID := createCalendarMut(t, owner, "Audit Memo Cal")

	var created struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/calendars/"+calID+"/memos",
		owner.AccessToken, map[string]any{
			"title":      "Audit Memo",
			"sortWeight": 10,
		}, &created)
	require.NotEmpty(t, created.ID, "create memo must return a public id")

	var updated struct {
		Updated bool `json:"updated"`
	}
	doJSON(t, http.MethodPatch,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/calendars/"+calID+"/memos/"+created.ID,
		owner.AccessToken, map[string]any{"done": true}, &updated)
	require.True(t, updated.Updated, "update memo must confirm the write")

	status, body := doJSONStatus(t, http.MethodDelete,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/calendars/"+calID+"/memos/"+created.ID,
		owner.AccessToken, nil)
	require.Equal(t, http.StatusOK, status, "delete memo must succeed; body=%s", string(body))

	logs := queryAuditLogs(t, testDB, owner.WorkspacePublicID)

	t.Run("calendar.memo.create", func(t *testing.T) {
		t.Parallel()
		row := findAudit(t, logs, "calendar.memo.create", "calendar.memo")
		require.True(t, row.ActorUserID.Valid, "calendar.memo.create must record an actor")
		require.Equal(t, created.ID, row.ResourcePublicID.String,
			"resource public id must match the created memo")
	})

	t.Run("calendar.memo.update", func(t *testing.T) {
		t.Parallel()
		row := findAudit(t, logs, "calendar.memo.update", "calendar.memo")
		require.Equal(t, created.ID, row.ResourcePublicID.String,
			"resource public id must match the updated memo")
	})

	t.Run("calendar.memo.delete", func(t *testing.T) {
		t.Parallel()
		row := findAudit(t, logs, "calendar.memo.delete", "calendar.memo")
		require.Equal(t, created.ID, row.ResourcePublicID.String,
			"resource public id must match the deleted memo")
	})
}
