// Suspending a user must not delete what that user contributed.
//
// A listing query that reaches the author with
// `INNER JOIN users ... AND u.enabled = TRUE` drops every row a
// suspended account authored out of everyone else's view the moment the
// account is suspended: attachments leave the task, widgets leave the
// shared dashboard, lenses leave the lens list, comments and memos leave
// the calendar. The blobs stay in object storage and ref_count never
// moves, so nothing can be reclaimed either — the rows are invisible,
// not gone.
//
// Suspension is reversible bookkeeping (see delete_after_suspend_test.go
// for the contract). The rows below therefore stay, and only the byline
// is withheld: the API renders an empty author id and name, and the web
// client substitutes its own placeholder.
//
// Every assertion here scopes itself to rows this test created, because
// the suite runs in parallel against a shared database.
package e2e

import (
	"encoding/json"
	"fmt"
	"image/color"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// suspendedRangeStart / suspendedRangeEnd bound the calendar reads. The
// window is far enough out that no other suite's fixtures land in it.
const (
	suspendedRangeStart = "2028-03-01"
	suspendedRangeEnd   = "2028-04-01"
)

// addCalendarEditor grants the member write access to the host's
// calendar so they can author events, comments and memos on it.
func addCalendarEditor(t *testing.T, host *helpers.TestTenant, calID, email string) {
	t.Helper()
	var added struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		fmt.Sprintf("%s/workspaces/%s/calendars/%s/members",
			testServerURL, host.WorkspacePublicID, calID),
		host.AccessToken, map[string]any{"email": email, "role": "editor"}, &added)
	require.NotEmpty(t, added.ID, "add calendar member must return a subscription id")
}

// createEventAt creates a one-hour event inside the suspended-author
// window as the supplied actor.
func createEventAt(t *testing.T, actor *helpers.TestTenant, wsID, calID, title string) string {
	t.Helper()
	start := time.Date(2028, 3, 10, 10, 0, 0, 0, time.UTC)
	var resp struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		fmt.Sprintf("%s/workspaces/%s/calendars/%s/events", testServerURL, wsID, calID),
		actor.AccessToken, map[string]any{
			"kind":     "event",
			"title":    title,
			"startAt":  start.Unix(),
			"endAt":    start.Add(time.Hour).Unix(),
			"timezone": "UTC",
		}, &resp)
	require.NotEmpty(t, resp.ID, "create event must return a public id")
	return resp.ID
}

// TestSuspendedAuthorKeepsContributionsVisible is the regression for the
// creator/uploader/author joins. One member authors a row on every
// affected surface, the member is suspended through the admin API, and
// the workspace owner must still see each row.
func TestSuspendedAuthorKeepsContributionsVisible(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	owner := newTenant(t)
	member := inviteAndJoinWorkspace(t, owner)
	// Writing on the owner's task needs a project role; a bare workspace
	// membership is read-only there.
	doJSON(t, http.MethodPost,
		testServerURL+"/projects/"+owner.ProjectPublicID+"/members",
		owner.AccessToken, map[string]any{
			"userId": member.UserPublicID,
			"role":   "editor",
		}, nil)
	admin := newTenant(t)
	grantInstanceAdmin(t, testDB, admin.UserPublicID)

	wsBase := testServerURL + "/workspaces/" + owner.WorkspacePublicID

	// A task everyone in the workspace can read, so the member can
	// contribute to it and the owner can read the result back.
	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken, map[string]any{
		"projectId":  owner.ProjectPublicID,
		"title":      "Task with contributions from a departing member",
		"visibility": "public",
	}, &task)
	require.NotEmpty(t, task.ID, "task create must return a public id")

	// The member's contribution on the task surface: one comment and one
	// uploaded file.
	var taskComment struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+task.ID+"/comments",
		member.AccessToken, map[string]any{"body": "Handing this over before I go"}, &taskComment)
	require.NotEmpty(t, taskComment.ID, "task comment create must return a public id")

	taskPayload := makePNG(t, 6, 6, color.RGBA{R: 40, G: 80, B: 120, A: 255})
	taskUpload := presignAttachment(t, member.AccessToken, task.ID, "handover.png", "image/png", taskPayload)
	require.NotEmpty(t, taskUpload.AttachmentID, "task presign must reserve an attachment")
	uploadViaPresignedURL(t, taskUpload.UploadURL, "image/png", taskPayload, taskUpload.RequiredHeaders)
	confirmAttachment(t, member.AccessToken, task.ID, taskUpload.AttachmentID)

	// The member's contribution on the workspace surfaces.
	var widget struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, wsBase+"/dashboard/widgets", member.AccessToken, map[string]any{
		"widgetType": "task_summary",
		"title":      "Widget from a departing member",
		"positionX":  0, "positionY": 0, "width": 4, "height": 3,
	}, &widget)
	require.NotEmpty(t, widget.ID, "widget create must return a public id")

	var lens struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, wsBase+"/lenses", member.AccessToken, map[string]any{
		"name":      "Lens from a departing member",
		"filter":    json.RawMessage(`{}`),
		"sort":      json.RawMessage(`[]`),
		"isDefault": false,
	}, &lens)
	require.NotEmpty(t, lens.ID, "lens create must return a public id")

	// The member's contribution on the calendar surfaces. The owner's
	// calendar is shared with the member as an editor so the member can
	// author on it.
	calID := createCalendarMut(t, owner, "Shared calendar for the suspension case")
	addCalendarEditor(t, owner, calID, member.Email)
	evtID := createEventAt(t, owner, owner.WorkspacePublicID, calID, "Recurring sync")
	// An event the member owns, to prove the owner join no longer decides
	// whether the event exists for everyone else.
	memberEvtID := createEventAt(t, member, owner.WorkspacePublicID, calID, "Event owned by a departing member")

	evtBase := fmt.Sprintf("%s/calendars/%s/events/%s", wsBase, calID, evtID)

	var calComment struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, evtBase+"/comments", member.AccessToken,
		map[string]any{"body": "Notes from the last sync"}, &calComment)
	require.NotEmpty(t, calComment.ID, "calendar comment create must return a public id")

	var memo struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, fmt.Sprintf("%s/calendars/%s/memos", wsBase, calID),
		member.AccessToken, map[string]any{
			"title":      "Memo from a departing member",
			"sortWeight": 10,
		}, &memo)
	require.NotEmpty(t, memo.ID, "memo create must return a public id")

	calPayload := makePNG(t, 7, 7, color.RGBA{R: 200, G: 30, B: 60, A: 255})
	memberPersona := tenantPersona{tok: member.AccessToken, ws: owner.WorkspacePublicID}
	calUpload := presignCalendarAttachment(t, memberPersona, calID, evtID, "agenda.png", "image/png", calPayload)
	require.NotEmpty(t, calUpload.AttachmentID, "calendar presign must reserve an attachment")
	uploadViaPresignedURL(t, calUpload.UploadURL, "image/png", calPayload, calUpload.RequiredHeaders)
	confirmCalendarAttachment(t, memberPersona, calID, evtID, calUpload.AttachmentID)

	// Every row must be visible to the owner before the suspension, or a
	// green run below would prove nothing.
	require.True(t, ownerSeesTaskAttachment(t, owner, task.ID, taskUpload.AttachmentID),
		"precondition: the owner must see the member's attachment before the suspension")
	require.True(t, ownerSeesTaskComment(t, owner, task.ID, taskComment.ID),
		"precondition: the owner must see the member's comment before the suspension")
	require.True(t, ownerSeesWidget(t, owner, widget.ID),
		"precondition: the owner must see the member's widget before the suspension")
	require.True(t, ownerSeesLens(t, owner, lens.ID),
		"precondition: the owner must see the member's lens before the suspension")

	suspendUserViaAdmin(t, admin.AccessToken, member.UserPublicID)

	t.Run("task attachment survives", func(t *testing.T) {
		t.Parallel()
		var list struct {
			Attachments []struct {
				ID                  string `json:"id"`
				UploaderID          string `json:"uploaderId"`
				UploaderDisplayName string `json:"uploaderDisplayName"`
			} `json:"attachments"`
			Total int64 `json:"total"`
		}
		doJSON(t, http.MethodGet, testServerURL+"/tasks/"+task.ID+"/attachments",
			owner.AccessToken, nil, &list)

		var found bool
		for _, a := range list.Attachments {
			if a.ID != taskUpload.AttachmentID {
				continue
			}
			found = true
			assert.Empty(t, a.UploaderDisplayName,
				"a suspended uploader's name must be withheld, not rendered")
			assert.Empty(t, a.UploaderID,
				"a suspended uploader's id must be withheld rather than emitted as a zero UUID")
		}
		require.Truef(t, found,
			"suspending the uploader must not remove the attachment from the task; got %d row(s)",
			len(list.Attachments))
		assert.GreaterOrEqual(t, list.Total, int64(1),
			"total must still count the attachment the list returns")
	})

	t.Run("task comment survives", func(t *testing.T) {
		t.Parallel()
		var list struct {
			Comments []struct {
				ID                string `json:"id"`
				AuthorID          string `json:"authorId"`
				AuthorDisplayName string `json:"authorDisplayName"`
				Body              string `json:"body"`
			} `json:"comments"`
		}
		doJSON(t, http.MethodGet, testServerURL+"/tasks/"+task.ID+"/comments",
			owner.AccessToken, nil, &list)

		var found bool
		for _, c := range list.Comments {
			if c.ID != taskComment.ID {
				continue
			}
			found = true
			assert.Equal(t, "Handing this over before I go", c.Body,
				"the comment body must survive the author's suspension intact")
			assert.Empty(t, c.AuthorDisplayName, "a suspended author's name must be withheld")
			assert.Empty(t, c.AuthorID, "a suspended author's id must be withheld")
		}
		require.Truef(t, found,
			"suspending the author must not remove the comment from the task; got %d row(s)",
			len(list.Comments))
	})

	t.Run("dashboard widget survives", func(t *testing.T) {
		t.Parallel()
		require.True(t, ownerSeesWidget(t, owner, widget.ID),
			"suspending the creator must not remove the widget from the shared dashboard")

		var single struct {
			ID                 string `json:"id"`
			CreatorID          string `json:"creatorId"`
			CreatorDisplayName string `json:"creatorDisplayName"`
		}
		status, body := doJSONStatus(t, http.MethodGet,
			wsBase+"/dashboard/widgets/"+widget.ID, owner.AccessToken, nil)
		require.Equalf(t, http.StatusOK, status,
			"the single-widget read must still resolve after the creator is suspended; body=%s", string(body))
		require.NoError(t, json.Unmarshal(body, &single), "decode widget body=%s", string(body))
		assert.Empty(t, single.CreatorDisplayName, "a suspended creator's name must be withheld")
		assert.Empty(t, single.CreatorID, "a suspended creator's id must be withheld")
	})

	t.Run("lens survives", func(t *testing.T) {
		t.Parallel()
		require.True(t, ownerSeesLens(t, owner, lens.ID),
			"suspending the creator must not remove the lens from the workspace list")

		var single struct {
			ID                 string `json:"id"`
			CreatorID          string `json:"creatorId"`
			CreatorDisplayName string `json:"creatorDisplayName"`
		}
		status, body := doJSONStatus(t, http.MethodGet,
			wsBase+"/lenses/"+lens.ID, owner.AccessToken, nil)
		require.Equalf(t, http.StatusOK, status,
			"the single-lens read must still resolve after the creator is suspended; body=%s", string(body))
		require.NoError(t, json.Unmarshal(body, &single), "decode lens body=%s", string(body))
		assert.Empty(t, single.CreatorDisplayName, "a suspended creator's name must be withheld")
		assert.Empty(t, single.CreatorID, "a suspended creator's id must be withheld")
	})

	t.Run("calendar comment survives", func(t *testing.T) {
		t.Parallel()
		var list struct {
			Comments []struct {
				ID          string `json:"id"`
				UserID      string `json:"userId"`
				DisplayName string `json:"displayName"`
				Body        string `json:"body"`
			} `json:"comments"`
		}
		doJSON(t, http.MethodGet, evtBase+"/comments", owner.AccessToken, nil, &list)

		var found bool
		for _, c := range list.Comments {
			if c.ID != calComment.ID {
				continue
			}
			found = true
			assert.Equal(t, "Notes from the last sync", c.Body,
				"the comment body must survive the author's suspension intact")
			assert.Empty(t, c.DisplayName, "a suspended author's name must be withheld")
			assert.Empty(t, c.UserID, "a suspended author's id must be withheld")
		}
		require.Truef(t, found,
			"suspending the author must not remove the comment from the event; got %d row(s)",
			len(list.Comments))
	})

	t.Run("calendar memo survives", func(t *testing.T) {
		t.Parallel()
		var list struct {
			Memos []struct {
				ID              string `json:"id"`
				UserPublicID    string `json:"userPublicId"`
				UserDisplayName string `json:"userDisplayName"`
				Title           string `json:"title"`
			} `json:"memos"`
		}
		doJSON(t, http.MethodGet, fmt.Sprintf("%s/calendars/%s/memos", wsBase, calID),
			owner.AccessToken, nil, &list)

		var found bool
		for _, m := range list.Memos {
			if m.ID != memo.ID {
				continue
			}
			found = true
			assert.Equal(t, "Memo from a departing member", m.Title,
				"the memo must survive its creator's suspension intact")
			assert.Empty(t, m.UserDisplayName, "a suspended creator's name must be withheld")
			assert.Empty(t, m.UserPublicID, "a suspended creator's id must be withheld")
		}
		require.Truef(t, found,
			"suspending the creator must not remove the memo from the calendar; got %d row(s)",
			len(list.Memos))
	})

	t.Run("calendar event attachment survives", func(t *testing.T) {
		t.Parallel()
		var list struct {
			Attachments []struct {
				ID           string `json:"id"`
				UploaderID   string `json:"uploaderId"`
				UploaderName string `json:"uploaderName"`
			} `json:"attachments"`
		}
		doJSON(t, http.MethodGet, evtBase+"/attachments", owner.AccessToken, nil, &list)

		var found bool
		for _, a := range list.Attachments {
			if a.ID != calUpload.AttachmentID {
				continue
			}
			found = true
			assert.Empty(t, a.UploaderName, "a suspended uploader's name must be withheld")
			assert.Empty(t, a.UploaderID, "a suspended uploader's id must be withheld")
		}
		require.Truef(t, found,
			"suspending the uploader must not remove the file from the event; got %d row(s)",
			len(list.Attachments))
	})

	t.Run("event owned by the suspended member survives in the unified feed", func(t *testing.T) {
		t.Parallel()
		var feed struct {
			Events []struct {
				ID          string `json:"id"`
				OwnerUserID string `json:"ownerUserId"`
			} `json:"events"`
		}
		doJSON(t, http.MethodGet,
			fmt.Sprintf("%s/me/calendar-events?start=%s&end=%s",
				testServerURL, suspendedRangeStart, suspendedRangeEnd),
			owner.AccessToken, nil, &feed)

		var found bool
		for _, e := range feed.Events {
			if e.ID != memberEvtID {
				continue
			}
			found = true
			assert.Empty(t, e.OwnerUserID, "a suspended owner's id must be withheld")
		}
		require.Truef(t, found,
			"suspending an event's owner must not drop the event from another member's feed; got %d row(s)",
			len(feed.Events))
	})
}

// ownerSeesTaskAttachment reports whether the attachment is listed on the
// task for the given actor.
func ownerSeesTaskAttachment(t *testing.T, actor *helpers.TestTenant, taskID, attachmentID string) bool {
	t.Helper()
	var list struct {
		Attachments []struct {
			ID string `json:"id"`
		} `json:"attachments"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+taskID+"/attachments",
		actor.AccessToken, nil, &list)
	for _, a := range list.Attachments {
		if a.ID == attachmentID {
			return true
		}
	}
	return false
}

// ownerSeesTaskComment reports whether the comment is listed on the task
// for the given actor.
func ownerSeesTaskComment(t *testing.T, actor *helpers.TestTenant, taskID, commentID string) bool {
	t.Helper()
	var list struct {
		Comments []struct {
			ID string `json:"id"`
		} `json:"comments"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+taskID+"/comments",
		actor.AccessToken, nil, &list)
	for _, c := range list.Comments {
		if c.ID == commentID {
			return true
		}
	}
	return false
}

// ownerSeesWidget reports whether the widget is listed on the actor's
// workspace dashboard.
func ownerSeesWidget(t *testing.T, actor *helpers.TestTenant, widgetID string) bool {
	t.Helper()
	var list struct {
		Widgets []struct {
			ID string `json:"id"`
		} `json:"widgets"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+actor.WorkspacePublicID+"/dashboard/widgets",
		actor.AccessToken, nil, &list)
	for _, w := range list.Widgets {
		if w.ID == widgetID {
			return true
		}
	}
	return false
}

// ownerSeesLens reports whether the lens is listed in the actor's
// workspace.
func ownerSeesLens(t *testing.T, actor *helpers.TestTenant, lensID string) bool {
	t.Helper()
	var list struct {
		Lenses []struct {
			ID string `json:"id"`
		} `json:"lenses"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+actor.WorkspacePublicID+"/lenses",
		actor.AccessToken, nil, &list)
	for _, l := range list.Lenses {
		if l.ID == lensID {
			return true
		}
	}
	return false
}
