package calendar

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// TestRenderPublicShare_PrivateEventIsTimeOnly asserts the visibility
// contract enforced on the unauthenticated /share/cal/{token} render:
//
//   - a `private`-visibility event stays VISIBLE as an opaque time block
//     (start/end/show_as/block_label) but its descriptive content
//     (title/memo/location/url) is stripped so nothing leaks publicly;
//   - a `default`-visibility event renders with full details;
//   - a `confidential`-visibility event is fully absent from the render.
func TestRenderPublicShare_PrivateEventIsTimeOnly(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	// Default-visibility event: full details must remain visible.
	var publicEvt struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("calendars", calID, "events"), tt.AccessToken, map[string]any{
		"kind":       "event",
		"title":      "Public Kickoff",
		"location":   "HQ Room 1",
		"memo":       "Bring the roadmap deck",
		"url":        "https://example.com/kickoff",
		"startAt":    time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC).Unix(),
		"endAt":      time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC).Unix(),
		"timezone":   "UTC",
		"visibility": "default",
	}, &publicEvt)

	// Private-visibility event: only the time block should survive.
	var privateEvt struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("calendars", calID, "events"), tt.AccessToken, map[string]any{
		"kind":       "event",
		"title":      "Dentist Appointment",
		"location":   "123 Secret St",
		"memo":       "Personal medical note",
		"url":        "https://example.com/private",
		"startAt":    time.Date(2026, 6, 1, 11, 0, 0, 0, time.UTC).Unix(),
		"endAt":      time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC).Unix(),
		"timezone":   "UTC",
		"visibility": "private",
	}, &privateEvt)

	// Confidential-visibility event: must never reach the render at all.
	var secretEvt struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("calendars", calID, "events"), tt.AccessToken, map[string]any{
		"kind":       "event",
		"title":      "Board Secret",
		"location":   "Boardroom",
		"memo":       "Confidential",
		"startAt":    time.Date(2026, 6, 1, 13, 0, 0, 0, time.UTC).Unix(),
		"endAt":      time.Date(2026, 6, 1, 14, 0, 0, 0, time.UTC).Unix(),
		"timezone":   "UTC",
		"visibility": "confidential",
	}, &secretEvt)

	var share struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("public-shares"), tt.AccessToken, map[string]any{
		"title": "Mixed Visibility Share",
	}, &share)
	require.NotEmpty(t, share.Token)

	// Attach all three. Confidential is rejected (skipped) at the attach
	// path; default and private are accepted.
	var attach struct {
		Attached int `json:"attached"`
		Skipped  int `json:"skipped"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("public-shares", share.ID, "events"), tt.AccessToken, map[string]any{
		"eventIds": []string{publicEvt.ID, privateEvt.ID, secretEvt.ID},
	}, &attach)
	require.Equal(t, 2, attach.Attached, "default + private must attach")
	require.Equal(t, 1, attach.Skipped, "confidential must be rejected at attach")

	var rendered struct {
		Events []map[string]any `json:"events"`
	}
	helpers.DoJSON(t, http.MethodGet, tt.BaseURL+"/share/cal/"+share.Token, "", nil, &rendered)

	// Confidential is entirely absent: only default + private remain.
	require.Len(t, rendered.Events, 2, "confidential event must never appear in the render")

	byID := map[string]map[string]any{}
	for _, e := range rendered.Events {
		id, _ := e["id"].(string)
		byID[id] = e
	}

	for id := range byID {
		assert.NotEqual(t, secretEvt.ID, id, "confidential event id must not appear")
	}

	// Default event: full details remain visible.
	pub, ok := byID[publicEvt.ID]
	require.True(t, ok, "default-visibility event must be present")
	assert.Equal(t, "Public Kickoff", pub["title"], "default event keeps its title")
	assert.Equal(t, "HQ Room 1", pub["location"], "default event keeps its location")
	assert.Equal(t, "Bring the roadmap deck", pub["memo"], "default event keeps its memo")
	assert.Equal(t, "https://example.com/kickoff", pub["url"], "default event keeps its url")

	// Private event: time block visible, descriptive content stripped.
	priv, ok := byID[privateEvt.ID]
	require.True(t, ok, "private-visibility event must remain visible as a time block")

	// Time block survives.
	assert.NotNil(t, priv["startAt"], "private event must keep its start time")
	assert.NotNil(t, priv["endAt"], "private event must keep its end time")
	assert.Contains(t, priv, "showAs", "private event must keep its show_as time-block status")

	// Descriptive content is stripped — must not leak on a public page.
	if title, present := priv["title"]; present {
		assert.Equal(t, "", title, "private event title must be blanked")
	}
	assert.NotContains(t, priv, "location", "private event location must be stripped")
	assert.NotContains(t, priv, "memo", "private event memo must be stripped")
	assert.NotContains(t, priv, "url", "private event url must be stripped")

	// Defence in depth: the raw secret strings must not appear anywhere
	// in the private event payload.
	for k, v := range priv {
		if s, isStr := v.(string); isStr {
			assert.NotEqual(t, "Dentist Appointment", s, "private title leaked via field %q", k)
			assert.NotEqual(t, "123 Secret St", s, "private location leaked via field %q", k)
			assert.NotEqual(t, "Personal medical note", s, "private memo leaked via field %q", k)
			assert.NotEqual(t, "https://example.com/private", s, "private url leaked via field %q", k)
		}
	}
}
