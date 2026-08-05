// Soft-delete cycle e2e suite.
//
// Every test here runs the same operation more than once. That is the
// point rather than thoroughness: the shape being guarded against —
// `UNIQUE (tuple..., enabled)`, which allows one live row and exactly
// one tombstone — passes a single add/remove cycle and fails on the
// second remove, when the row being disabled collides with the first
// tombstone. A one-cycle test would have gone green against the schema
// that shipped the bug, in every one of the dozen tables that had it.
//
// The second failure mode is the same key seen from the other side: a
// tuple the product documents as repeatable (a task's time blocks, an
// invite re-issued after a revoke) refused its second row outright.
package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// decodeInto unmarshals a raw response body captured by doJSONStatus.
// The negative-path helper returns bytes rather than decoding, and these
// tests need both the status and the payload on every call.
func decodeInto(body []byte, out any) error {
	return json.Unmarshal(body, out)
}

// createTaskForCycle makes a task in the tenant's default project.
func createTaskForCycle(t *testing.T, tt *helpers.TestTenant, title string) string {
	t.Helper()
	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     title,
	}, &task)
	require.NotEmpty(t, task.ID)
	return task.ID
}

// TestReactionAddRemoveCycles is the canonical H-11 case: react, undo,
// react again, undo again. The second undo is where the old key failed
// with a duplicate entry, after which that reaction could never be
// removed.
func TestReactionAddRemoveCycles(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskForCycle(t, tt, "Reaction cycles")

	for cycle := 0; cycle < 3; cycle++ {
		var created struct {
			ID string `json:"id"`
		}
		status, body := doJSONStatus(t, http.MethodPost,
			testServerURL+"/tasks/"+taskID+"/reactions",
			tt.AccessToken, map[string]any{"emoji": "👍"})
		require.Equal(t, http.StatusOK, status,
			"cycle %d: adding the reaction must succeed; body=%s", cycle, string(body))
		require.NoError(t, decodeInto(body, &created))
		require.NotEmpty(t, created.ID)

		status, body = doJSONStatus(t, http.MethodDelete,
			testServerURL+"/tasks/"+taskID+"/reactions/"+created.ID,
			tt.AccessToken, nil)
		require.Equal(t, http.StatusOK, status,
			"cycle %d: removing the reaction must succeed; body=%s", cycle, string(body))
	}

	// After the last cycle nothing is live, and the tombstones have
	// accumulated rather than collided.
	var listed struct {
		Total int64 `json:"total"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+taskID+"/reactions",
		tt.AccessToken, nil, &listed)
	assert.Zero(t, listed.Total, "no reaction may survive the final undo")

	var tombstones int
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*) FROM reactions r
		   JOIN tasks t ON t.id = r.task_id
		  WHERE t.public_id = UUID_TO_BIN(?, 0) AND r.enabled = FALSE`,
		taskID).Scan(&tombstones))
	assert.Equal(t, 3, tombstones,
		"each undo must leave its own tombstone instead of colliding with the previous one")
}

// TestTaskLabelAttachDetachCycles covers the same key shape on the
// task-label junction.
func TestTaskLabelAttachDetachCycles(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskForCycle(t, tt, "Label cycles")

	var label struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/labels",
		tt.AccessToken, map[string]any{"name": "cycle-label", "color": "#4285F4"}, &label)
	require.NotEmpty(t, label.ID)

	for cycle := 0; cycle < 3; cycle++ {
		status, body := doJSONStatus(t, http.MethodPost,
			testServerURL+"/tasks/"+taskID+"/labels",
			tt.AccessToken, map[string]any{"labelId": label.ID})
		require.Equal(t, http.StatusOK, status,
			"cycle %d: attaching the label must succeed; body=%s", cycle, string(body))

		status, body = doJSONStatus(t, http.MethodDelete,
			testServerURL+"/tasks/"+taskID+"/labels/"+label.ID,
			tt.AccessToken, nil)
		require.Equal(t, http.StatusOK, status,
			"cycle %d: detaching the label must succeed; body=%s", cycle, string(body))
	}

	var listed struct {
		Labels []struct {
			ID string `json:"id"`
		} `json:"labels"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+taskID+"/labels",
		tt.AccessToken, nil, &listed)
	assert.Empty(t, listed.Labels, "no label may survive the final detach")
}

// TestFavoriteAddRemoveCycles covers the per-user favourites junction,
// keyed on (user, target_type, target_public_id).
func TestFavoriteAddRemoveCycles(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskForCycle(t, tt, "Favorite cycles")

	for cycle := 0; cycle < 3; cycle++ {
		var created struct {
			ID string `json:"id"`
		}
		status, body := doJSONStatus(t, http.MethodPost,
			testServerURL+"/me/favorites", tt.AccessToken, map[string]any{
				"workspaceId": tt.WorkspacePublicID,
				"targetType":  "task",
				"targetId":    taskID,
			})
		require.Equal(t, http.StatusOK, status,
			"cycle %d: starring must succeed; body=%s", cycle, string(body))
		require.NoError(t, decodeInto(body, &created))
		require.NotEmpty(t, created.ID)

		status, body = doJSONStatus(t, http.MethodDelete,
			testServerURL+"/me/favorites/"+created.ID+"?workspaceId="+tt.WorkspacePublicID,
			tt.AccessToken, nil)
		require.Equal(t, http.StatusOK, status,
			"cycle %d: unstarring must succeed; body=%s", cycle, string(body))
	}
}

// TestPublicShareEventAttachDetachCycles is the H-11 case with the worst
// consequence, because the row it strands is published to an
// unauthenticated URL. The old key made the second detach fail with a
// 500 while the event stayed on the page, and the re-attach that
// followed returned 200 while reporting the event as skipped — so the
// editor was told the event was not published at the moment it was.
func TestPublicShareEventAttachDetachCycles(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendarMut(t, tt, "Share Cycle Cal")
	evtID := createEventMut(t, tt, calID, "Recurring publication")

	var share struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/public-shares",
		tt.AccessToken, map[string]any{"title": "Cycle share"}, &share)
	require.NotEmpty(t, share.Token)

	shareEvents := testServerURL + "/workspaces/" + tt.WorkspacePublicID +
		"/public-shares/" + share.ID + "/events"

	for cycle := 0; cycle < 3; cycle++ {
		var attach struct {
			Attached int `json:"attached"`
			Skipped  int `json:"skipped"`
		}
		status, body := doJSONStatus(t, http.MethodPost, shareEvents,
			tt.AccessToken, map[string]any{"eventIds": []string{evtID}})
		require.Equal(t, http.StatusOK, status,
			"cycle %d: attach must succeed; body=%s", cycle, string(body))
		require.NoError(t, decodeInto(body, &attach))
		assert.Equal(t, 1, attach.Attached,
			"cycle %d: the event must be reported as published", cycle)
		assert.Zero(t, attach.Skipped, "cycle %d: nothing may be skipped", cycle)

		// The report has to agree with the page. Reporting attached
		// while the event is absent, or skipped while it is present,
		// are both ways of lying about a public URL.
		status, rendered := doJSONStatus(t, http.MethodGet,
			testServerURL+"/share/cal/"+share.Token, "", nil)
		require.Equal(t, http.StatusOK, status, "cycle %d: render; body=%s", cycle, string(rendered))
		assert.Contains(t, string(rendered), "Recurring publication",
			"cycle %d: an attached event must appear on the public page", cycle)

		status, body = doJSONStatus(t, http.MethodDelete,
			shareEvents+"/"+evtID, tt.AccessToken, nil)
		require.Equal(t, http.StatusOK, status,
			"cycle %d: detach must succeed; body=%s", cycle, string(body))

		status, rendered = doJSONStatus(t, http.MethodGet,
			testServerURL+"/share/cal/"+share.Token, "", nil)
		require.Equal(t, http.StatusOK, status, "cycle %d: render; body=%s", cycle, string(rendered))
		assert.NotContains(t, string(rendered), "Recurring publication",
			"cycle %d: a detached event must leave the public page", cycle)
	}
}

// TestEventInviteRevokeAndReinvite covers the inverse shape. Invites are
// keyed on (event, attendee) with no liveness column, which is the right
// key for a single standing grant — but the create path looked only at
// live rows, so a re-invite went to the insert, collided with the
// revoked row and failed permanently. The fix revives the row, and the
// revived invite must carry a new token: restoring the old one would
// make the revocation reversible by whoever still held the link.
func TestEventInviteRevokeAndReinvite(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	host := newTenant(t)
	member := inviteAndJoinWorkspace(t, host)
	calID := createCalendarMut(t, host, "Invite Cycle Cal")
	evtID := createEventMut(t, host, calID, "Invite Cycle Event")
	attendeeID := addAttendeeMut(t, host, calID, evtID, member.UserPublicID)

	inviteBase := testServerURL + "/workspaces/" + host.WorkspacePublicID +
		"/calendars/" + calID + "/events/" + evtID
	mintURL := inviteBase + "/attendees/" + attendeeID + "/invite"

	seen := map[string]bool{}
	for cycle := 0; cycle < 3; cycle++ {
		var minted struct {
			ID    string `json:"id"`
			Token string `json:"token"`
		}
		status, body := doJSONStatus(t, http.MethodPost, mintURL, host.AccessToken, map[string]any{})
		require.Equal(t, http.StatusOK, status,
			"cycle %d: inviting the attendee must succeed; body=%s", cycle, string(body))
		require.NoError(t, decodeInto(body, &minted))
		require.NotEmpty(t, minted.Token, "cycle %d: a fresh token must be issued", cycle)
		assert.False(t, seen[minted.Token],
			"cycle %d: a revived invite must not reissue a token already handed out", cycle)
		seen[minted.Token] = true

		status, body = doJSONStatus(t, http.MethodDelete,
			inviteBase+"/invites/"+minted.ID, host.AccessToken, nil)
		require.Equal(t, http.StatusOK, status,
			"cycle %d: revoking must succeed; body=%s", cycle, string(body))

		// The revoked token must stop working before the next cycle
		// re-issues one, or "revoked" would only mean "hidden".
		status, _ = doJSONStatus(t, http.MethodPost,
			testServerURL+"/public/invites/accept", "",
			map[string]any{"token": minted.Token, "rsvp": "accepted"})
		assert.GreaterOrEqual(t, status, 400,
			"cycle %d: a revoked token must not be redeemable", cycle)
	}

	// One grant, one row — the invite is not a series, so the cycles
	// revived the same row rather than piling up beside it.
	var rows int
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*) FROM calendar_event_invites i
		   JOIN calendar_events e ON e.id = i.event_id
		  WHERE e.public_id = UUID_TO_BIN(?, 0)`,
		evtID).Scan(&rows))
	assert.Equal(t, 1, rows,
		"re-inviting must revive the single (event, attendee) row rather than add another")
}

// TestArchivedTaskDetailIsReachable is H-31. The archive room lists
// archived tasks and links to them, so the detail view they link to has
// to resolve; it was filtered out of v_task_detail, which every
// task-detail route reads, so every one of those links 404'd.
func TestArchivedTaskDetailIsReachable(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskForCycle(t, tt, "Archived but readable")

	status, body := doJSONStatus(t, http.MethodPost,
		testServerURL+"/tasks/"+taskID+"/archive", tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status, "archive must succeed; body=%s", string(body))

	var detail struct {
		ID         string `json:"id"`
		Title      string `json:"title"`
		ArchivedAt *int64 `json:"archivedAt"`
	}
	status, body = doJSONStatus(t, http.MethodGet,
		testServerURL+"/tasks/"+taskID, tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status,
		"an archived task's detail must still resolve; body=%s", string(body))
	require.NoError(t, decodeInto(body, &detail))
	assert.Equal(t, taskID, detail.ID)
	assert.Equal(t, "Archived but readable", detail.Title)
	assert.NotNil(t, detail.ArchivedAt, "the detail must report that the task is archived")

	// The archive room lists it, which is what makes the 404 above a
	// contradiction rather than a policy.
	var archived struct {
		Total int64 `json:"total"`
		Tasks []struct {
			ID string `json:"id"`
		} `json:"tasks"`
	}
	doJSON(t, http.MethodGet,
		fmt.Sprintf("%s/workspaces/%s/tasks/archived?limit=50", testServerURL, tt.WorkspacePublicID),
		tt.AccessToken, nil, &archived)
	found := false
	for _, task := range archived.Tasks {
		if task.ID == taskID {
			found = true
			break
		}
	}
	assert.True(t, found, "the archived task must appear in the archive listing")

	// Soft-deletion is a different question and still hides the row.
	_, err := testDB.Exec(
		`UPDATE tasks SET enabled = FALSE WHERE public_id = UUID_TO_BIN(?, 0)`, taskID)
	require.NoError(t, err)

	status, _ = doJSONStatus(t, http.MethodGet,
		testServerURL+"/tasks/"+taskID, tt.AccessToken, nil)
	assert.Equal(t, http.StatusNotFound, status,
		"a soft-deleted task must still be unreachable")
}

// TestUnarchiveRoundTrip guards the pair: archiving is reversible and
// the detail view reads the same either way.
func TestUnarchiveRoundTrip(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskForCycle(t, tt, "Round trip")

	for cycle := 0; cycle < 2; cycle++ {
		status, body := doJSONStatus(t, http.MethodPost,
			testServerURL+"/tasks/"+taskID+"/archive", tt.AccessToken, nil)
		require.Equal(t, http.StatusOK, status, "cycle %d: archive; body=%s", cycle, string(body))

		var archivedDetail struct {
			ArchivedAt *int64 `json:"archivedAt"`
		}
		status, body = doJSONStatus(t, http.MethodGet,
			testServerURL+"/tasks/"+taskID, tt.AccessToken, nil)
		require.Equal(t, http.StatusOK, status, "cycle %d: archived detail; body=%s", cycle, string(body))
		require.NoError(t, decodeInto(body, &archivedDetail))
		require.NotNil(t, archivedDetail.ArchivedAt, "cycle %d: archivedAt must be set", cycle)

		status, body = doJSONStatus(t, http.MethodPost,
			testServerURL+"/tasks/"+taskID+"/unarchive", tt.AccessToken, nil)
		require.Equal(t, http.StatusOK, status, "cycle %d: unarchive; body=%s", cycle, string(body))

		var activeDetail struct {
			ArchivedAt *int64 `json:"archivedAt"`
		}
		status, body = doJSONStatus(t, http.MethodGet,
			testServerURL+"/tasks/"+taskID, tt.AccessToken, nil)
		require.Equal(t, http.StatusOK, status, "cycle %d: active detail; body=%s", cycle, string(body))
		require.NoError(t, decodeInto(body, &activeDetail))
		assert.Nil(t, activeDetail.ArchivedAt, "cycle %d: archivedAt must clear", cycle)
	}
}

// TestTaskScheduleUnscheduleRescheduleOverHTTP is C-6 seen from the
// product surface: projecting a task onto a calendar, removing the
// projection, and projecting it again. The second projection used to
// collide with the tombstone the first one left behind, which made the
// task permanently unschedulable.
func TestTaskScheduleUnscheduleRescheduleOverHTTP(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendarMut(t, tt, "Projection Cal")
	taskID := createTaskForCycle(t, tt, "Reschedulable task")

	fromTask := testServerURL + "/workspaces/" + tt.WorkspacePublicID +
		"/calendars/" + calID + "/events/from-task"

	for cycle := 0; cycle < 3; cycle++ {
		var projected struct {
			ID string `json:"id"`
		}
		status, body := doJSONStatus(t, http.MethodPost, fromTask, tt.AccessToken, map[string]any{
			"taskId":   taskID,
			"timezone": "UTC",
		})
		require.Equal(t, http.StatusOK, status,
			"cycle %d: projecting the task must succeed; body=%s", cycle, string(body))
		require.NoError(t, decodeInto(body, &projected))
		require.NotEmpty(t, projected.ID)

		status, body = doJSONStatus(t, http.MethodDelete,
			testServerURL+"/workspaces/"+tt.WorkspacePublicID+
				"/calendars/"+calID+"/events/"+projected.ID,
			tt.AccessToken, nil)
		require.Equal(t, http.StatusOK, status,
			"cycle %d: unscheduling must succeed; body=%s", cycle, string(body))
	}

	var live, tombstones int
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*) FROM calendar_events ce
		   JOIN tasks t ON t.id = ce.task_id
		  WHERE t.public_id = UUID_TO_BIN(?, 0) AND ce.enabled = TRUE`,
		taskID).Scan(&live))
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*) FROM calendar_events ce
		   JOIN tasks t ON t.id = ce.task_id
		  WHERE t.public_id = UUID_TO_BIN(?, 0) AND ce.enabled = FALSE`,
		taskID).Scan(&tombstones))
	assert.Zero(t, live, "nothing may remain projected after the final unschedule")
	assert.Equal(t, 3, tombstones,
		"each unschedule must leave its own tombstone instead of colliding with the previous one")
}
