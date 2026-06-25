package calendar

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/tests/helpers"
)

// memoResponse mirrors the JSON shape returned by the memo endpoints,
// limited to the fields the body round-trip assertions care about.
type memoResponse struct {
	ID    string  `json:"id"`
	Title string  `json:"title"`
	Body  *string `json:"body,omitempty"`
	Done  bool    `json:"done"`
}

type memoListResponse struct {
	Memos []memoResponse `json:"memos"`
}

// findMemo returns the memo with the given ID from a list response.
func findMemo(t *testing.T, list memoListResponse, id string) memoResponse {
	t.Helper()
	for _, m := range list.Memos {
		if m.ID == id {
			return m
		}
	}
	require.Failf(t, "memo not found", "memo %s missing from list", id)
	return memoResponse{}
}

// TestMemoBodyRoundTrips verifies that the user-authored body survives
// create and list, distinct from the admin-only notes column.
func TestMemoBodyRoundTrips(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	wantBody := "line one\nline two\nline three"
	var created memoResponse
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("calendars", calID, "memos"), tt.AccessToken, map[string]any{
		"title": "Shopping list",
		"body":  wantBody,
	}, &created)

	require.NotEmpty(t, created.ID)
	require.NotNil(t, created.Body)
	assert.Equal(t, wantBody, *created.Body)

	var list memoListResponse
	helpers.DoJSON(t, http.MethodGet, tt.WsPath("calendars", calID, "memos"), tt.AccessToken, nil, &list)

	got := findMemo(t, list, created.ID)
	require.NotNil(t, got.Body, "body should round-trip through list")
	assert.Equal(t, wantBody, *got.Body)
}

// TestMemoCreateWithoutBody confirms an omitted body surfaces as a
// null/absent field rather than an empty string.
func TestMemoCreateWithoutBody(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	var created memoResponse
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("calendars", calID, "memos"), tt.AccessToken, map[string]any{
		"title": "No body memo",
	}, &created)

	require.NotEmpty(t, created.ID)
	assert.Nil(t, created.Body)

	var list memoListResponse
	helpers.DoJSON(t, http.MethodGet, tt.WsPath("calendars", calID, "memos"), tt.AccessToken, nil, &list)
	got := findMemo(t, list, created.ID)
	assert.Nil(t, got.Body)
}

// TestMemoUpdatePreservesBodyOnOmit verifies the COALESCE behavior: a
// partial update that omits body must leave the stored value intact,
// while still applying the fields that were sent.
func TestMemoUpdatePreservesBodyOnOmit(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	wantBody := "keep me across the title rename"
	var created memoResponse
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("calendars", calID, "memos"), tt.AccessToken, map[string]any{
		"title": "Original title",
		"body":  wantBody,
	}, &created)
	require.NotEmpty(t, created.ID)

	// Partial update touching only the title; body intentionally omitted.
	var updated struct {
		Updated bool `json:"updated"`
	}
	helpers.DoJSON(t, http.MethodPatch, tt.WsPath("calendars", calID, "memos", created.ID), tt.AccessToken, map[string]any{
		"title": "Renamed title",
	}, &updated)
	assert.True(t, updated.Updated)

	var list memoListResponse
	helpers.DoJSON(t, http.MethodGet, tt.WsPath("calendars", calID, "memos"), tt.AccessToken, nil, &list)
	got := findMemo(t, list, created.ID)
	assert.Equal(t, "Renamed title", got.Title)
	require.NotNil(t, got.Body, "omitting body on update must preserve the stored value")
	assert.Equal(t, wantBody, *got.Body)
}

// TestMemoUpdateSetsBody verifies an explicit body on update replaces
// the stored value.
func TestMemoUpdateSetsBody(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	var created memoResponse
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("calendars", calID, "memos"), tt.AccessToken, map[string]any{
		"title": "Memo",
		"body":  "before",
	}, &created)
	require.NotEmpty(t, created.ID)

	var updated struct {
		Updated bool `json:"updated"`
	}
	helpers.DoJSON(t, http.MethodPatch, tt.WsPath("calendars", calID, "memos", created.ID), tt.AccessToken, map[string]any{
		"body": "after",
	}, &updated)
	assert.True(t, updated.Updated)

	var list memoListResponse
	helpers.DoJSON(t, http.MethodGet, tt.WsPath("calendars", calID, "memos"), tt.AccessToken, nil, &list)
	got := findMemo(t, list, created.ID)
	require.NotNil(t, got.Body)
	assert.Equal(t, "after", *got.Body)
}
