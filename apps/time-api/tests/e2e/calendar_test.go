package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/time-api/tests/helpers"
)

func TestCreateCalendar(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	body := map[string]any{
		"kind":  "shared",
		"name":  "Team Calendar",
		"color": "#4285F4",
	}
	var resp struct {
		ID    string `json:"id"`
		Kind  string `json:"kind"`
		Name  string `json:"name"`
		Color string `json:"color"`
		Role  string `json:"role"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("calendars"), tt.AccessToken, body, &resp)

	require.NotEmpty(t, resp.ID)
	assert.Equal(t, "shared", resp.Kind)
	assert.Equal(t, "Team Calendar", resp.Name)
	assert.Equal(t, "#4285F4", resp.Color)
	assert.Equal(t, "owner", resp.Role)
}

func TestListCalendars(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	helpers.DoJSON(t, http.MethodPost, tt.WsPath("calendars"), tt.AccessToken, map[string]any{
		"kind": "shared", "name": "Cal A", "color": "#FF0000",
	}, nil)
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("calendars"), tt.AccessToken, map[string]any{
		"kind": "shared", "name": "Cal B", "color": "#00FF00",
	}, nil)

	var resp struct {
		Calendars []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"calendars"`
	}
	helpers.DoJSON(t, http.MethodGet, tt.WsPath("calendars"), tt.AccessToken, nil, &resp)

	require.GreaterOrEqual(t, len(resp.Calendars), 2, "should list at least 2 calendars")

	names := make([]string, len(resp.Calendars))
	for i, c := range resp.Calendars {
		names[i] = c.Name
	}
	assert.Contains(t, names, "Cal A")
	assert.Contains(t, names, "Cal B")
}

func TestGetCalendar(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	var created struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("calendars"), tt.AccessToken, map[string]any{
		"kind": "shared", "name": "Get Test", "color": "#123456",
	}, &created)

	var got struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	helpers.DoJSON(t, http.MethodGet, tt.WsPath("calendars", created.ID), tt.AccessToken, nil, &got)

	assert.Equal(t, created.ID, got.ID)
	assert.Equal(t, "Get Test", got.Name)
}

func TestPatchCalendar(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	var created struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("calendars"), tt.AccessToken, map[string]any{
		"kind": "shared", "name": "Before", "color": "#000000",
	}, &created)

	var patched struct {
		Name string `json:"name"`
	}
	helpers.DoJSON(t, http.MethodPatch, tt.WsPath("calendars", created.ID), tt.AccessToken, map[string]any{
		"name": "After",
	}, &patched)

	assert.Equal(t, "After", patched.Name)
}

func TestDeleteCalendar(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	var created struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("calendars"), tt.AccessToken, map[string]any{
		"kind": "shared", "name": "Doomed", "color": "#FF0000",
	}, &created)

	var deleted struct {
		Deleted bool `json:"deleted"`
	}
	helpers.DoJSON(t, http.MethodDelete, tt.WsPath("calendars", created.ID), tt.AccessToken, nil, &deleted)
	assert.True(t, deleted.Deleted)

	// After deletion, the calendar should not appear in the list.
	var list struct {
		Calendars []struct {
			ID string `json:"id"`
		} `json:"calendars"`
	}
	helpers.DoJSON(t, http.MethodGet, tt.WsPath("calendars"), tt.AccessToken, nil, &list)
	for _, c := range list.Calendars {
		assert.NotEqual(t, created.ID, c.ID, "deleted calendar should not appear in list")
	}
}
