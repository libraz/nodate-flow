// How the agent surface turns a stored series into the meetings it
// actually produces.
//
// A model asking "what is on my calendar" gets whatever the server
// expands, with no rule to reconcile afterwards, so an occurrence the
// expansion gets wrong is an answer the model has no way to doubt.
package e2e

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// createRecurringEventMut creates a recurring event and returns its
// public id. The rule goes in as the JSON object the column stores.
func createRecurringEventMut(
	t *testing.T,
	owner *helpers.TestTenant,
	calID, title string,
	start time.Time,
	rule map[string]any,
) string {
	t.Helper()
	var resp struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/calendars/"+calID+"/events",
		owner.AccessToken, map[string]any{
			"kind":           "event",
			"title":          title,
			"startAt":        start.Unix(),
			"endAt":          start.Add(time.Hour).Unix(),
			"timezone":       "UTC",
			"recurrenceRule": rule,
		}, &resp)
	require.NotEmpty(t, resp.ID, "create recurring event must return a public id")
	return resp.ID
}

// mcpEventStartsByTitle lists the caller's calendar over [start, end) via
// MCP and returns the start instants reported for one title, in the order
// the tool answered.
func mcpEventStartsByTitle(
	t *testing.T,
	tt *helpers.TestTenant,
	title string,
	start, end time.Time,
) []int64 {
	t.Helper()
	payload := mcpTool(t, tt, "list_calendar_events", map[string]any{
		"startAt": start.Unix(),
		"endAt":   end.Unix(),
	})
	var out struct {
		Events []struct {
			Title   string `json:"title"`
			StartAt int64  `json:"startAt"`
		} `json:"events"`
	}
	require.NoError(t, json.Unmarshal([]byte(payload), &out),
		"list_calendar_events must answer a JSON object; payload=%s", payload)

	var starts []int64
	for _, e := range out.Events {
		if e.Title == title {
			starts = append(starts, e.StartAt)
		}
	}
	return starts
}

// TestMCPListEventsCountsAnOverriddenOccurrenceOnce pins what a moved
// occurrence looks like to an agent: the meeting appears once, at the
// time it was moved to.
//
// A series is one row plus a rule, and moving a single occurrence writes
// a second row carrying the start it replaces. That row has no rule of
// its own, so the non-recurring read returns it at its new time — the
// master must therefore stop producing the occurrence it stands in for.
// Left in, the model is told the same weekly meeting happens twice on
// that day, once at an hour nobody is meeting, and answers scheduling
// questions from it.
func TestMCPListEventsCountsAnOverriddenOccurrenceOnce(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	host := newTenant(t)
	calID := createCalendarMut(t, host, "Recurrence Calendar")

	title := "Weekly Sync " + randomHex(4)
	first := time.Date(2027, time.September, 6, 10, 0, 0, 0, time.UTC)
	evtID := createRecurringEventMut(t, host, calID, title, first, map[string]any{"freq": "weekly"})

	// The window holds three occurrences: 09-06, 09-13 and 09-20.
	windowStart := time.Date(2027, time.September, 6, 0, 0, 0, 0, time.UTC)
	windowEnd := time.Date(2027, time.September, 27, 0, 0, 0, 0, time.UTC)

	original := time.Date(2027, time.September, 13, 10, 0, 0, 0, time.UTC)
	moved := time.Date(2027, time.September, 13, 15, 0, 0, 0, time.UTC)
	doJSON(t, http.MethodPatch,
		testServerURL+"/workspaces/"+host.WorkspacePublicID+"/calendars/"+calID+"/events/"+evtID,
		host.AccessToken, map[string]any{
			"scope":           "occurrence",
			"occurrenceStart": original.Unix(),
			"startAt":         moved.Unix(),
			"endAt":           moved.Add(time.Hour).Unix(),
		}, nil)

	starts := mcpEventStartsByTitle(t, host, title, windowStart, windowEnd)
	require.ElementsMatch(t,
		[]int64{
			first.Unix(),
			moved.Unix(),
			time.Date(2027, time.September, 20, 10, 0, 0, 0, time.UTC).Unix(),
		},
		starts,
		"the series must report three meetings, the middle one only at the time it moved to")
}
