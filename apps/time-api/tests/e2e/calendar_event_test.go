package e2e

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/time-api/tests/helpers"
)

// createCalendar is a test helper that creates a personal calendar and returns its public ID.
// Post-R5.14 the only user-facing calendar kind is "personal"; "system"
// calendars are provisioned automatically per workspace country.
func createCalendar(t *testing.T, tt *helpers.TestTenant) string {
	t.Helper()
	var cal struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("calendars"), tt.AccessToken, map[string]any{
		"kind": "personal", "name": "Events Cal " + t.Name(), "color": "#4285F4",
	}, &cal)
	require.NotEmpty(t, cal.ID)
	return cal.ID
}

func TestCreateEvent(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	start := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 1, 11, 0, 0, 0, time.UTC)

	body := map[string]any{
		"kind":     "event",
		"title":    "Sprint Planning",
		"startAt":  start.Format(time.RFC3339),
		"endAt":    end.Format(time.RFC3339),
		"timezone": "Asia/Tokyo",
	}
	var resp struct {
		ID       string `json:"id"`
		Kind     string `json:"kind"`
		Title    string `json:"title"`
		ShowAs   string `json:"showAs"`
		Timezone string `json:"timezone"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("calendars", calID, "events"), tt.AccessToken, body, &resp)

	require.NotEmpty(t, resp.ID)
	assert.Equal(t, "event", resp.Kind)
	assert.Equal(t, "Sprint Planning", resp.Title)
	assert.Equal(t, "busy", resp.ShowAs)
	assert.Equal(t, "Asia/Tokyo", resp.Timezone)
}

func TestCreateEventWithKinds(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	tests := []struct {
		kind       string
		wantShowAs string
	}{
		{"event", "busy"},
		{"block", "busy"},
		{"free", "free"},
	}

	for _, tc := range tests {
		t.Run(tc.kind, func(t *testing.T) {
			body := map[string]any{
				"kind":     tc.kind,
				"title":    "Test " + tc.kind,
				"startAt":  time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC).Format(time.RFC3339),
				"endAt":    time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC).Format(time.RFC3339),
				"timezone": "UTC",
			}
			if tc.kind == "free" {
				body["showAs"] = "free"
			}

			var resp struct {
				Kind   string `json:"kind"`
				ShowAs string `json:"showAs"`
			}
			helpers.DoJSON(t, http.MethodPost, tt.WsPath("calendars", calID, "events"), tt.AccessToken, body, &resp)
			assert.Equal(t, tc.kind, resp.Kind)
			assert.Equal(t, tc.wantShowAs, resp.ShowAs)
		})
	}
}

func TestListEventsByRange(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	// Create events on May 1, May 15, and June 1.
	dates := []time.Time{
		time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC),
	}
	for i, d := range dates {
		helpers.DoJSON(t, http.MethodPost, tt.WsPath("calendars", calID, "events"), tt.AccessToken, map[string]any{
			"kind":     "event",
			"title":    time.Month(d.Month()).String() + " event",
			"startAt":  d.Format(time.RFC3339),
			"endAt":    d.Add(1 * time.Hour).Format(time.RFC3339),
			"timezone": "UTC",
		}, nil)
		_ = i
	}

	// Query May only (should get 2).
	rangeStart := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	rangeEnd := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	var resp struct {
		Events []struct {
			Title string `json:"title"`
		} `json:"events"`
	}
	url := tt.WsPath("calendars", calID, "events") +
		"?start=" + rangeStart.Format(time.RFC3339) +
		"&end=" + rangeEnd.Format(time.RFC3339)
	helpers.DoJSON(t, http.MethodGet, url, tt.AccessToken, nil, &resp)

	assert.Equal(t, 2, len(resp.Events), "should return exactly 2 events in May")
}

func TestPatchEvent(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	var created struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("calendars", calID, "events"), tt.AccessToken, map[string]any{
		"kind":     "event",
		"title":    "Original Title",
		"startAt":  time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC).Format(time.RFC3339),
		"endAt":    time.Date(2026, 7, 1, 11, 0, 0, 0, time.UTC).Format(time.RFC3339),
		"timezone": "UTC",
	}, &created)

	var patched struct {
		Title string `json:"title"`
	}
	helpers.DoJSON(t, http.MethodPatch, tt.WsPath("calendars", calID, "events", created.ID), tt.AccessToken, map[string]any{
		"title": "Updated Title",
	}, &patched)

	assert.Equal(t, "Updated Title", patched.Title)
}

func TestDeleteEvent(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	var created struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("calendars", calID, "events"), tt.AccessToken, map[string]any{
		"kind":     "event",
		"title":    "Delete Me",
		"startAt":  time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC).Format(time.RFC3339),
		"endAt":    time.Date(2026, 8, 1, 11, 0, 0, 0, time.UTC).Format(time.RFC3339),
		"timezone": "UTC",
	}, &created)

	var deleted struct {
		Deleted bool `json:"deleted"`
	}
	helpers.DoJSON(t, http.MethodDelete, tt.WsPath("calendars", calID, "events", created.ID), tt.AccessToken, nil, &deleted)
	assert.True(t, deleted.Deleted)

	// GET should return 404.
	status, _ := helpers.DoJSONStatus(t, http.MethodGet, tt.WsPath("calendars", calID, "events", created.ID), tt.AccessToken, nil)
	assert.Equal(t, http.StatusNotFound, status)
}

func TestEventPermission_EditorCannotEditOthersEvent(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	calID := createCalendar(t, owner)

	calInternalID := helpers.ResolveCalendarInternalID(t, testDB, calID)
	editor := helpers.CreateExtraMember(t, testSrv, owner.WorkspaceID, owner.WorkspacePublicID, calInternalID, "")

	// Owner creates an event.
	var evt struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost, owner.WsPath("calendars", calID, "events"), owner.AccessToken, map[string]any{
		"kind":     "event",
		"title":    "Owner Event",
		"startAt":  time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC).Format(time.RFC3339),
		"endAt":    time.Date(2026, 9, 1, 11, 0, 0, 0, time.UTC).Format(time.RFC3339),
		"timezone": "UTC",
	}, &evt)

	// Editor tries to PATCH it.
	status, _ := helpers.DoJSONStatus(t, http.MethodPatch, editor.WsPath("calendars", calID, "events", evt.ID), editor.AccessToken, map[string]any{
		"title": "Hacked",
	})
	assert.Equal(t, http.StatusForbidden, status, "editor should not be able to edit owner's event")
}

func TestEventPermission_ManagerCanEditOthersEvent(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	calID := createCalendar(t, owner)

	calInternalID := helpers.ResolveCalendarInternalID(t, testDB, calID)
	manager := helpers.CreateExtraMember(t, testSrv, owner.WorkspaceID, owner.WorkspacePublicID, calInternalID, "")

	// Owner creates an event.
	var evt struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost, owner.WsPath("calendars", calID, "events"), owner.AccessToken, map[string]any{
		"kind":     "event",
		"title":    "Owner Event",
		"startAt":  time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC).Format(time.RFC3339),
		"endAt":    time.Date(2026, 10, 1, 11, 0, 0, 0, time.UTC).Format(time.RFC3339),
		"timezone": "UTC",
	}, &evt)

	// Manager patches it.
	var patched struct {
		Title string `json:"title"`
	}
	helpers.DoJSON(t, http.MethodPatch, manager.WsPath("calendars", calID, "events", evt.ID), manager.AccessToken, map[string]any{
		"title": "Manager Updated",
	}, &patched)
	assert.Equal(t, "Manager Updated", patched.Title)
}

func TestManagerDelegation_CreateEventOnBehalf(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	calID := createCalendar(t, owner)

	calInternalID := helpers.ResolveCalendarInternalID(t, testDB, calID)
	manager := helpers.CreateExtraMember(t, testSrv, owner.WorkspaceID, owner.WorkspacePublicID, calInternalID, "")

	// Manager creates an event with ownerUserId set to the calendar owner.
	var resp struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost, manager.WsPath("calendars", calID, "events"), manager.AccessToken, map[string]any{
		"kind":        "event",
		"title":       "Delegated Event",
		"startAt":     time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC).Format(time.RFC3339),
		"endAt":       time.Date(2026, 11, 1, 11, 0, 0, 0, time.UTC).Format(time.RFC3339),
		"timezone":    "UTC",
		"ownerUserId": owner.UserPublicID.String(),
	}, &resp)

	require.NotEmpty(t, resp.ID, "manager should be able to create event on behalf of another member")
}

func TestEditorCannotSetOtherOwner(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	calID := createCalendar(t, owner)

	calInternalID := helpers.ResolveCalendarInternalID(t, testDB, calID)
	editor := helpers.CreateExtraMember(t, testSrv, owner.WorkspaceID, owner.WorkspacePublicID, calInternalID, "")

	// Editor tries to create an event with ownerUserId set to the calendar owner.
	status, _ := helpers.DoJSONStatus(t, http.MethodPost, editor.WsPath("calendars", calID, "events"), editor.AccessToken, map[string]any{
		"kind":        "event",
		"title":       "Sneaky Event",
		"startAt":     time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC).Format(time.RFC3339),
		"endAt":       time.Date(2026, 12, 1, 11, 0, 0, 0, time.UTC).Format(time.RFC3339),
		"timezone":    "UTC",
		"ownerUserId": owner.UserPublicID.String(),
	})
	assert.Equal(t, http.StatusForbidden, status, "editor should not be able to set another user as event owner")
}
