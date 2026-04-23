package e2e

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/time-api/tests/helpers"
)

func TestCreatePublicShare_ReturnsTokenOnce(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	var created struct {
		ID         string `json:"id"`
		Title      string `json:"title"`
		Token      string `json:"token"`
		Timezone   string `json:"timezone"`
		EventCount int64  `json:"eventCount"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("public-shares"), tt.AccessToken, map[string]any{
		"title": "Release Schedule",
	}, &created)

	require.NotEmpty(t, created.ID)
	require.NotEmpty(t, created.Token, "create response must include plaintext token exactly once")
	assert.Equal(t, "Release Schedule", created.Title)
	assert.Equal(t, "UTC", created.Timezone)
	assert.Equal(t, int64(0), created.EventCount)

	var got struct {
		Share struct {
			ID    string `json:"id"`
			Token string `json:"token"`
		} `json:"share"`
	}
	helpers.DoJSON(t, http.MethodGet, tt.WsPath("public-shares", created.ID), tt.AccessToken, nil, &got)
	assert.Equal(t, created.ID, got.Share.ID)
	assert.Empty(t, got.Share.Token, "GET response must never include the token")

	var list struct {
		Shares []struct {
			ID    string `json:"id"`
			Token string `json:"token"`
		} `json:"shares"`
	}
	helpers.DoJSON(t, http.MethodGet, tt.WsPath("public-shares"), tt.AccessToken, nil, &list)
	require.Len(t, list.Shares, 1)
	assert.Equal(t, created.ID, list.Shares[0].ID)
	assert.Empty(t, list.Shares[0].Token, "list response must never include the token")
}

func TestRenderPublicShare_HappyPath(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	var evt struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("calendars", calID, "events"), tt.AccessToken, map[string]any{
		"kind":       "event",
		"title":      "Public Kickoff",
		"startAt":    time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC).Format(time.RFC3339),
		"endAt":      time.Date(2026, 5, 1, 11, 0, 0, 0, time.UTC).Format(time.RFC3339),
		"timezone":   "UTC",
		"visibility": "default",
	}, &evt)

	var share struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("public-shares"), tt.AccessToken, map[string]any{
		"title": "Team Roadmap",
	}, &share)

	var attach struct {
		Attached int `json:"attached"`
		Skipped  int `json:"skipped"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("public-shares", share.ID, "events"), tt.AccessToken, map[string]any{
		"eventIds": []string{evt.ID},
	}, &attach)
	require.Equal(t, 1, attach.Attached)
	require.Equal(t, 0, attach.Skipped)

	var rendered struct {
		Page struct {
			Title     string `json:"title"`
			Timezone  string `json:"timezone"`
			CreatedAt int64  `json:"createdAt"`
		} `json:"page"`
		Events []map[string]any `json:"events"`
	}
	helpers.DoJSON(t, http.MethodGet, tt.BaseURL+"/share/cal/"+share.Token, "", nil, &rendered)

	assert.Equal(t, "Team Roadmap", rendered.Page.Title)
	assert.Equal(t, "UTC", rendered.Page.Timezone)
	require.Len(t, rendered.Events, 1)

	id, ok := rendered.Events[0]["id"].(string)
	require.True(t, ok, "render event must include string id")
	assert.Equal(t, evt.ID, id)

	_, hasAttendees := rendered.Events[0]["attendees"]
	assert.False(t, hasAttendees, "render must not expose attendees")
	_, hasOwner := rendered.Events[0]["owner"]
	assert.False(t, hasOwner, "render must not expose owner")
	_, hasOwnerID := rendered.Events[0]["ownerUserId"]
	assert.False(t, hasOwnerID, "render must not expose ownerUserId")
}

func TestRenderPublicShare_InvalidToken(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	status, body := helpers.DoJSONStatus(t, http.MethodGet, tt.BaseURL+"/share/cal/nonexistent-token-abc", "", nil)
	assert.Equal(t, http.StatusNotFound, status)

	// Verify the problem+json envelope shape: `type` carries the machine
	// code, `detail` carries the human message with NO code prefix.
	// Regression guard for the envelope bug where the code was concatenated
	// into `detail` and `type` was never set.
	var envelope struct {
		Type   string `json:"type"`
		Title  string `json:"title"`
		Detail string `json:"detail"`
		Status int    `json:"status"`
	}
	require.NoError(t, json.Unmarshal(body, &envelope), "decode envelope body=%s", string(body))
	assert.Equal(t, "SHARE.SHARE.TOKEN_INVALID", envelope.Type, "type must carry the machine code")
	assert.Equal(t, "Share token is invalid", envelope.Detail, "detail must be the human message only (no code prefix)")
	assert.NotContains(t, envelope.Detail, ":", "detail must not contain the legacy 'CODE: msg' prefix")
	assert.Equal(t, http.StatusNotFound, envelope.Status)
	assert.NotEmpty(t, envelope.Title)
}

func TestRenderPublicShare_Expired(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	var share struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("public-shares"), tt.AccessToken, map[string]any{
		"title": "Soon Expired",
	}, &share)

	past := time.Now().Add(-1 * time.Hour).Unix()
	helpers.DoJSON(t, http.MethodPatch, tt.WsPath("public-shares", share.ID), tt.AccessToken, map[string]any{
		"expiresAt": past,
	}, nil)

	status, _ := helpers.DoJSONStatus(t, http.MethodGet, tt.BaseURL+"/share/cal/"+share.Token, "", nil)
	assert.Equal(t, http.StatusGone, status)
}

func TestAttachEvents_RejectsConfidential(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	var publicEvt struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("calendars", calID, "events"), tt.AccessToken, map[string]any{
		"kind":       "event",
		"title":      "Public Meeting",
		"startAt":    time.Date(2026, 5, 2, 9, 0, 0, 0, time.UTC).Format(time.RFC3339),
		"endAt":      time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC).Format(time.RFC3339),
		"timezone":   "UTC",
		"visibility": "default",
	}, &publicEvt)

	var secretEvt struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("calendars", calID, "events"), tt.AccessToken, map[string]any{
		"kind":       "event",
		"title":      "Secret Meeting",
		"startAt":    time.Date(2026, 5, 2, 11, 0, 0, 0, time.UTC).Format(time.RFC3339),
		"endAt":      time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC).Format(time.RFC3339),
		"timezone":   "UTC",
		"visibility": "confidential",
	}, &secretEvt)

	var share struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("public-shares"), tt.AccessToken, map[string]any{
		"title": "Mixed Visibility",
	}, &share)

	var attach struct {
		Attached int `json:"attached"`
		Skipped  int `json:"skipped"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("public-shares", share.ID, "events"), tt.AccessToken, map[string]any{
		"eventIds": []string{publicEvt.ID, secretEvt.ID},
	}, &attach)
	assert.Equal(t, 1, attach.Attached)
	assert.Equal(t, 1, attach.Skipped)

	var rendered struct {
		Events []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"events"`
	}
	helpers.DoJSON(t, http.MethodGet, tt.BaseURL+"/share/cal/"+share.Token, "", nil, &rendered)
	require.Len(t, rendered.Events, 1)
	assert.Equal(t, publicEvt.ID, rendered.Events[0].ID)
	assert.Equal(t, "Public Meeting", rendered.Events[0].Title)
}

func TestRotateToken_InvalidatesOldToken(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	var share struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("public-shares"), tt.AccessToken, map[string]any{
		"title": "Rotate Me",
	}, &share)
	token1 := share.Token
	require.NotEmpty(t, token1)

	var rotated struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("public-shares", share.ID, "rotate"), tt.AccessToken, nil, &rotated)
	token2 := rotated.Token
	require.NotEmpty(t, token2)
	assert.NotEqual(t, token1, token2, "rotate must mint a new token")

	status1, _ := helpers.DoJSONStatus(t, http.MethodGet, tt.BaseURL+"/share/cal/"+token1, "", nil)
	assert.Equal(t, http.StatusNotFound, status1, "old token must be rejected after rotate")

	status2, _ := helpers.DoJSONStatus(t, http.MethodGet, tt.BaseURL+"/share/cal/"+token2, "", nil)
	assert.Equal(t, http.StatusOK, status2, "new token must render successfully")
}

func TestDetachEvent_RemovesFromRender(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	var evt struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("calendars", calID, "events"), tt.AccessToken, map[string]any{
		"kind":       "event",
		"title":      "Attach-Then-Detach",
		"startAt":    time.Date(2026, 5, 3, 9, 0, 0, 0, time.UTC).Format(time.RFC3339),
		"endAt":      time.Date(2026, 5, 3, 10, 0, 0, 0, time.UTC).Format(time.RFC3339),
		"timezone":   "UTC",
		"visibility": "default",
	}, &evt)

	var share struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("public-shares"), tt.AccessToken, map[string]any{
		"title": "Detach Test",
	}, &share)

	var attach struct {
		Attached int `json:"attached"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("public-shares", share.ID, "events"), tt.AccessToken, map[string]any{
		"eventIds": []string{evt.ID},
	}, &attach)
	require.Equal(t, 1, attach.Attached)

	var before struct {
		Events []struct {
			ID string `json:"id"`
		} `json:"events"`
	}
	helpers.DoJSON(t, http.MethodGet, tt.BaseURL+"/share/cal/"+share.Token, "", nil, &before)
	require.Len(t, before.Events, 1)
	assert.Equal(t, evt.ID, before.Events[0].ID)

	var detach struct {
		Removed bool `json:"removed"`
	}
	helpers.DoJSON(t, http.MethodDelete, tt.WsPath("public-shares", share.ID, "events", evt.ID), tt.AccessToken, nil, &detach)
	assert.True(t, detach.Removed)

	var after struct {
		Events []struct {
			ID string `json:"id"`
		} `json:"events"`
	}
	helpers.DoJSON(t, http.MethodGet, tt.BaseURL+"/share/cal/"+share.Token, "", nil, &after)
	assert.Len(t, after.Events, 0, "render events must be empty after detach")
}

func TestDeletePublicShare_AdminOnly(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	calID := createCalendar(t, owner)

	calInternalID := helpers.ResolveCalendarInternalID(t, testDB, calID)
	member := helpers.CreateExtraMember(t, testSrv, owner.WorkspaceID, owner.WorkspacePublicID, calInternalID, "")

	var share struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost, owner.WsPath("public-shares"), owner.AccessToken, map[string]any{
		"title": "Admin Only Delete",
	}, &share)

	forbiddenStatus, _ := helpers.DoJSONStatus(t, http.MethodDelete, member.WsPath("public-shares", share.ID), member.AccessToken, nil)
	assert.Equal(t, http.StatusForbidden, forbiddenStatus, "non-admin member must not delete a share")

	var deleted struct {
		Deleted bool `json:"deleted"`
	}
	helpers.DoJSON(t, http.MethodDelete, owner.WsPath("public-shares", share.ID), owner.AccessToken, nil, &deleted)
	assert.True(t, deleted.Deleted)

	status, _ := helpers.DoJSONStatus(t, http.MethodGet, owner.WsPath("public-shares", share.ID), owner.AccessToken, nil)
	assert.Equal(t, http.StatusNotFound, status, "deleted share must 404 on subsequent GET")
}

func TestPatchPublicShare_UpdatesTitle(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	var share struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("public-shares"), tt.AccessToken, map[string]any{
		"title": "Old Title",
	}, &share)

	var patched struct {
		Title string `json:"title"`
	}
	helpers.DoJSON(t, http.MethodPatch, tt.WsPath("public-shares", share.ID), tt.AccessToken, map[string]any{
		"title": "New Title",
	}, &patched)
	assert.Equal(t, "New Title", patched.Title)

	var got struct {
		Share struct {
			Title string `json:"title"`
		} `json:"share"`
	}
	helpers.DoJSON(t, http.MethodGet, tt.WsPath("public-shares", share.ID), tt.AccessToken, nil, &got)
	assert.Equal(t, "New Title", got.Share.Title)
}

func TestListPublicShares_ScopedToWorkspace(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tenantA := newTenant(t)
	tenantB := newTenant(t)

	var shareA struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost, tenantA.WsPath("public-shares"), tenantA.AccessToken, map[string]any{
		"title": "Tenant A Share",
	}, &shareA)

	var shareB struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost, tenantB.WsPath("public-shares"), tenantB.AccessToken, map[string]any{
		"title": "Tenant B Share",
	}, &shareB)

	var listA struct {
		Shares []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"shares"`
	}
	helpers.DoJSON(t, http.MethodGet, tenantA.WsPath("public-shares"), tenantA.AccessToken, nil, &listA)
	require.Len(t, listA.Shares, 1)
	assert.Equal(t, shareA.ID, listA.Shares[0].ID)
	assert.Equal(t, "Tenant A Share", listA.Shares[0].Title)

	var listB struct {
		Shares []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"shares"`
	}
	helpers.DoJSON(t, http.MethodGet, tenantB.WsPath("public-shares"), tenantB.AccessToken, nil, &listB)
	require.Len(t, listB.Shares, 1)
	assert.Equal(t, shareB.ID, listB.Shares[0].ID)
	assert.Equal(t, "Tenant B Share", listB.Shares[0].Title)

	assert.NotEqual(t, listA.Shares[0].ID, listB.Shares[0].ID, "workspaces must not leak shares to each other")
}
