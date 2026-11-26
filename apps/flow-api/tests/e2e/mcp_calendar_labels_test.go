package e2e

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// mcpEventID reads the event id a write tool answers with.
func mcpEventID(t *testing.T, payload string) string {
	t.Helper()
	var out struct {
		EventID string `json:"eventId"`
	}
	require.NoError(t, json.Unmarshal([]byte(payload), &out), "payload=%s", payload)
	require.NotEmpty(t, out.EventID, "payload=%s", payload)
	return out.EventID
}

func TestMCPCalendarEventLabels(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	t.Run("list", func(t *testing.T) {
		t.Parallel()
		f := newLabelFixture(t)
		o := f.owner
		a := createCalendarLabel(t, o, f.calID, "A", labelTestColor)
		weeklyTitle := "MCP weekly " + randomHex(3)
		singleTitle := "MCP single " + randomHex(3)
		postLabelEvent(t, o, f.calID, labelEventBody(weeklyTitle, labelSeriesStart,
			map[string]any{"recurrenceRule": weeklyRule(), "labelId": a.ID}))
		postLabelEvent(t, o, f.calID, labelEventBody(singleTitle, labelSeriesStart, nil))

		payload := mcpTool(t, o, "list_calendar_events", map[string]any{
			"startAt": labelSeriesStart.Add(-time.Hour).Unix(),
			"endAt":   labelOccurrence(3).Unix(),
		})
		var out struct {
			Events []map[string]any `json:"events"`
		}
		require.NoError(t, json.Unmarshal([]byte(payload), &out), "payload=%s", payload)
		weekly, single := 0, 0
		for _, e := range out.Events {
			switch e["title"] {
			case weeklyTitle:
				weekly++
				requireColorView(t, e, colorView{Color: labelTestColor, OwnerColor: labelOwnerColor, LabelID: a.ID, LabelName: "A"}, "recurring occurrence")
			case singleTitle:
				single++
				requireColorView(t, e, colorView{Color: labelOwnerColor, OwnerColor: labelOwnerColor}, "single event")
			}
		}
		require.Equal(t, 3, weekly, "every occurrence of the series carries the label")
		require.Equal(t, 1, single)
	})

	t.Run("create_and_update", func(t *testing.T) {
		t.Parallel()
		f := newLabelFixture(t)
		o := f.owner
		a := createCalendarLabel(t, o, f.calID, "A", "#2ECC87")
		b := createCalendarLabel(t, o, f.calID, "B", "#F5A623")

		created := mcpTool(t, o, "create_calendar_event", map[string]any{
			"calendarId": f.calID,
			"title":      "MCP labelled " + randomHex(3),
			"startAt":    labelSeriesStart.Unix(),
			"endAt":      labelSeriesStart.Add(time.Hour).Unix(),
			"labelId":    a.ID,
		})
		evtID := mcpEventID(t, created)
		requireColorView(t, getLabelEvent(t, o, f.calID, evtID),
			colorView{Color: "#2ECC87", OwnerColor: labelOwnerColor, LabelID: a.ID, LabelName: "A"}, "after create")

		mcpTool(t, o, "update_calendar_event", map[string]any{"eventId": evtID, "labelId": b.ID})
		requireColorView(t, getLabelEvent(t, o, f.calID, evtID),
			colorView{Color: "#F5A623", OwnerColor: labelOwnerColor, LabelID: b.ID, LabelName: "B"}, "after set")

		mcpTool(t, o, "update_calendar_event", map[string]any{"eventId": evtID, "labelId": ""})
		requireColorView(t, getLabelEvent(t, o, f.calID, evtID),
			colorView{Color: labelOwnerColor, OwnerColor: labelOwnerColor}, "an empty string clears the label")
		require.Empty(t, linkedLabelPublicID(t, evtID))
	})

	t.Run("refusals", func(t *testing.T) {
		t.Parallel()
		f := newLabelFixture(t)
		o := f.owner
		a := createCalendarLabel(t, o, f.calID, "A", labelTestColor)
		otherCal := createLabelCalendar(t, o, "Other "+randomHex(3), labelOwnerColor)
		foreign := createCalendarLabel(t, o, otherCal, "Foreign", "#FDC02D")
		evtID := eventIDOf(t, postLabelEvent(t, o, f.calID, labelEventBody("MCP refusal "+randomHex(3), labelSeriesStart, nil)))

		require.Equal(t, "CALENDAR.EVENT.LABEL_UNKNOWN",
			mcpToolErrorCode(t, o, "update_calendar_event", map[string]any{"eventId": evtID, "labelId": foreign.ID}),
			"another calendar's label is refused as REST refuses it")
		require.Equal(t, "MCP.TOOL.ARGUMENTS_INVALID",
			mcpToolErrorCode(t, o, "update_calendar_event", map[string]any{"eventId": evtID, "labelId": "not-a-uuid"}),
			"a malformed id is refused by the schema before the tool runs")
		require.Equal(t, "CALENDAR.EVENT.LABEL_UNKNOWN",
			mcpToolErrorCode(t, o, "update_calendar_event", map[string]any{"eventId": evtID, "labelId": uuid.Must(uuid.NewV7()).String()}),
			"a well-formed id that names no label reaches the tool and is refused there")
		require.Equal(t, "CALENDAR.EVENT.LABEL_UNKNOWN",
			mcpToolErrorCode(t, o, "create_calendar_event", map[string]any{
				"calendarId": f.calID, "title": "MCP bad label " + randomHex(3),
				"startAt": labelSeriesStart.Unix(), "endAt": labelSeriesStart.Add(time.Hour).Unix(),
				"labelId": foreign.ID,
			}))
		require.Empty(t, linkedLabelPublicID(t, evtID))

		manager := f.addMember(t, "manager")
		privateID := eventIDOf(t, postLabelEvent(t, o, f.calID, labelEventBody("MCP private "+randomHex(3), labelSeriesStart,
			map[string]any{"visibility": "private"})))
		require.Equal(t, "CALENDAR.EVENT.DETAILS_HIDDEN",
			mcpToolErrorCode(t, manager, "update_calendar_event", map[string]any{"eventId": privateID, "labelId": a.ID}))
		require.Equal(t, "CALENDAR.EVENT.DETAILS_HIDDEN",
			mcpToolErrorCode(t, manager, "update_calendar_event", map[string]any{"eventId": privateID, "labelId": ""}))
		require.Empty(t, linkedLabelPublicID(t, privateID))
	})

	t.Run("series_scopes", func(t *testing.T) {
		t.Parallel()
		f := newLabelFixture(t)
		o := f.owner
		a := createCalendarLabel(t, o, f.calID, "A", "#2ECC87")
		b := createCalendarLabel(t, o, f.calID, "B", "#F5A623")
		title := "MCP series " + randomHex(3)
		seriesID := eventIDOf(t, postLabelEvent(t, o, f.calID, labelEventBody(title, labelSeriesStart,
			map[string]any{"recurrenceRule": weeklyRule(), "labelId": a.ID})))

		overrideID := mcpEventID(t, mcpTool(t, o, "update_calendar_event", map[string]any{
			"eventId": seriesID, "scope": "occurrence",
			"occurrenceStart": labelOccurrence(1).Unix(), "labelId": b.ID,
		}))
		require.NotEqual(t, seriesID, overrideID)
		requireColorView(t, getLabelEvent(t, o, f.calID, overrideID),
			colorView{Color: "#F5A623", OwnerColor: labelOwnerColor, LabelID: b.ID, LabelName: "B"}, "override")
		requireColorView(t, getLabelEvent(t, o, f.calID, seriesID),
			colorView{Color: "#2ECC87", OwnerColor: labelOwnerColor, LabelID: a.ID, LabelName: "A"}, "series untouched")

		splitID := mcpEventID(t, mcpTool(t, o, "update_calendar_event", map[string]any{
			"eventId": seriesID, "scope": "thisAndFollowing",
			"occurrenceStart": labelOccurrence(2).Unix(), "title": title + " split",
		}))
		require.NotEqual(t, seriesID, splitID)
		requireColorView(t, getLabelEvent(t, o, f.calID, splitID),
			colorView{Color: "#2ECC87", OwnerColor: labelOwnerColor, LabelID: a.ID, LabelName: "A"}, "the new series inherits the label")
	})
}
