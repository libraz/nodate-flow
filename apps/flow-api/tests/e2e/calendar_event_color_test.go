package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// colorCase is one row of the pairwise table over the resolution inputs.
type colorCase struct {
	label, owner, details, endpoint, shape string
}

func (c colorCase) name() string {
	return strings.Join([]string{c.label, c.owner, c.details, c.endpoint, c.shape}, "/")
}

var colorMatrix = []colorCase{
	{"labelDeleted", "memberWithColour", "hidden", "calendarList", "seriesOccurrence"},
	{"labelDeleted", "removedFromCalendar", "visible", "getEvent", "override"},
	{"none", "memberWithColour", "visible", "getEvent", "single"},
	{"none", "removedFromCalendar", "hidden", "crossCalendar", "seriesOccurrence"},
	{"none", "memberWithColour", "hidden", "meEvents", "override"},
	{"labelled", "removedFromCalendar", "visible", "meEvents", "seriesOccurrence"},
	{"labelled", "memberWithColour", "hidden", "crossCalendar", "override"},
	{"labelled", "memberWithColour", "visible", "getEvent", "seriesOccurrence"},
	{"labelDeleted", "memberWithColour", "visible", "crossCalendar", "single"},
	{"labelDeleted", "memberWithColour", "visible", "meEvents", "single"},
	{"none", "memberWithColour", "visible", "calendarList", "seriesOccurrence"},
	{"labelDeleted", "memberWithColour", "hidden", "crossCalendar", "single"},
	{"none", "memberWithColour", "hidden", "getEvent", "seriesOccurrence"},
	{"labelled", "removedFromCalendar", "hidden", "calendarList", "single"},
	{"labelDeleted", "removedFromCalendar", "hidden", "calendarList", "override"},
}

// colorFixture is a calendar owned by O (colour #F35F8C) and V, a second
// owner who reads. O authors every event, so V is never a party to one.
type colorFixture struct {
	o, v  *helpers.TestTenant
	calID string
	label calendarLabelJSON
}

func newColorFixture(t *testing.T) *colorFixture {
	t.Helper()
	o := newTenant(t)
	v := inviteAndJoinWorkspace(t, o)
	calID := createLabelCalendar(t, o, "Colour "+randomHex(3), labelOwnerColor)
	addCalendarMemberWithRole(t, o, calID, v.Email, "manager")
	setCalendarMemberRole(t, o, calID, v.UserPublicID, "owner")
	f := &colorFixture{o: o, v: v, calID: calID}
	f.label = createCalendarLabel(t, o, calID, "L", labelTestColor)
	return f
}

// readAs fetches the event through one endpoint as V and returns its row.
func (f *colorFixture) readAs(t *testing.T, endpoint, evtID string) map[string]any {
	t.Helper()
	ws := f.v.WorkspacePublicID
	switch endpoint {
	case "getEvent":
		return getLabelEvent(t, f.v, f.calID, evtID)
	case "calendarList":
		return f.findRow(t, labelCalURL(f.v, f.calID)+"/events?"+labelRangeQuery, evtID)
	case "crossCalendar":
		return f.findRow(t, fmt.Sprintf("%s/workspaces/%s/calendar-events?%s", testServerURL, ws, labelRangeQuery), evtID)
	case "meEvents":
		return f.findRow(t, testServerURL+"/me/calendar-events?"+labelRangeQuery, evtID)
	}
	t.Fatalf("unknown endpoint %q", endpoint)
	return nil
}

func (f *colorFixture) findRow(t *testing.T, url, evtID string) map[string]any {
	t.Helper()
	var out struct {
		Events []map[string]any `json:"events"`
	}
	doJSON(t, http.MethodGet, url, f.v.AccessToken, nil, &out)
	for _, row := range out.Events {
		if row["id"] == evtID {
			return row
		}
	}
	t.Fatalf("event %s is not in %s: %d rows", evtID, url, len(out.Events))
	return nil
}

func TestCalendarEventColorMatrix(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	for _, c := range colorMatrix {
		t.Run(c.name(), func(t *testing.T) {
			t.Parallel()
			f := newColorFixture(t)
			title := "Colour " + randomHex(4)
			visibility := "default"
			if c.details == "hidden" {
				visibility = "private"
			}
			linkAtCreate := map[string]any{}
			if c.label != "none" && c.shape != "override" {
				linkAtCreate["labelId"] = f.label.ID
			}
			extra := map[string]any{"visibility": visibility}
			for k, v := range linkAtCreate {
				extra[k] = v
			}
			if c.shape != "single" {
				extra["recurrenceRule"] = weeklyRule()
			}
			created := postLabelEvent(t, f.o, f.calID, labelEventBody(title, labelSeriesStart, extra))
			evtID := eventIDOf(t, created)
			if c.shape == "override" {
				body := map[string]any{"scope": "occurrence", "occurrenceStart": labelOccurrence(1).Unix()}
				if c.label != "none" {
					body["labelId"] = f.label.ID
				} else {
					body["title"] = title + " moved"
				}
				evtID = eventIDOf(t, patchLabelEvent(t, f.o, f.calID, evtID, body))
			}
			if c.label == "labelDeleted" {
				status, raw := doJSONStatus(t, http.MethodDelete,
					labelCalURL(f.o, f.calID)+"/labels/"+f.label.ID, f.o.AccessToken, nil)
				require.Equal(t, http.StatusNoContent, status, "body=%s", raw)
			}
			if c.owner == "removedFromCalendar" {
				doJSON(t, http.MethodDelete, labelCalURL(f.v, f.calID)+"/members/"+f.o.UserPublicID,
					f.v.AccessToken, nil, nil)
			}

			want := colorView{Color: labelOwnerColor, OwnerColor: labelOwnerColor, Hidden: c.details == "hidden"}
			if c.owner == "removedFromCalendar" {
				want.Color, want.OwnerColor = labelFallbackColor, labelFallbackColor
			}
			if c.label == "labelled" && c.details == "visible" {
				want.Color, want.LabelID, want.LabelName = labelTestColor, f.label.ID, f.label.Name
			}
			requireColorView(t, f.readAs(t, c.endpoint, evtID), want, c.name())
		})
	}

	t.Run("foreignCalendarLink", func(t *testing.T) {
		t.Parallel()
		f := newColorFixture(t)
		otherCal := createLabelCalendar(t, f.o, "Other "+randomHex(3), labelOwnerColor)
		foreign := createCalendarLabel(t, f.o, otherCal, "Foreign", "#FDC02D")
		evtID := eventIDOf(t, postLabelEvent(t, f.o, f.calID, labelEventBody("Foreign link "+randomHex(3), labelSeriesStart, nil)))
		_, err := testDB.Exec(
			`INSERT INTO calendar_event_labels (event_id, label_id)
			 SELECT e.id, l.id FROM calendar_events e, calendar_labels l
			  WHERE e.public_id = UUID_TO_BIN(?, 0) AND l.public_id = UUID_TO_BIN(?, 0)`, evtID, foreign.ID)
		require.NoError(t, err)
		for _, endpoint := range []string{"getEvent", "calendarList", "crossCalendar", "meEvents"} {
			requireColorView(t, f.readAs(t, endpoint, evtID),
				colorView{Color: labelOwnerColor, OwnerColor: labelOwnerColor}, "foreign link via "+endpoint)
		}
	})

	t.Run("ownerColourInvalid", func(t *testing.T) {
		t.Parallel()
		f := newColorFixture(t)
		evtID := eventIDOf(t, postLabelEvent(t, f.o, f.calID, labelEventBody("Bad owner colour "+randomHex(3), labelSeriesStart, nil)))
		_, err := testDB.Exec(
			`UPDATE calendar_members cm
			   JOIN users u ON u.id = cm.user_id
			   JOIN calendars c ON c.id = cm.calendar_id
			    SET cm.member_color = 'red'
			  WHERE c.public_id = UUID_TO_BIN(?, 0) AND u.public_id = UUID_TO_BIN(?, 0)`,
			f.calID, f.o.UserPublicID)
		require.NoError(t, err)
		for _, endpoint := range []string{"getEvent", "calendarList", "crossCalendar", "meEvents"} {
			requireColorView(t, f.readAs(t, endpoint, evtID),
				colorView{Color: labelFallbackColor, OwnerColor: labelFallbackColor}, "invalid owner colour via "+endpoint)
		}
	})

	t.Run("lowercaseLabelColour", func(t *testing.T) {
		t.Parallel()
		f := newColorFixture(t)
		evtID := eventIDOf(t, postLabelEvent(t, f.o, f.calID, labelEventBody("Lower label colour "+randomHex(3), labelSeriesStart,
			map[string]any{"labelId": f.label.ID})))
		_, err := testDB.Exec(`UPDATE calendar_labels SET color = '#2ecc87' WHERE public_id = UUID_TO_BIN(?, 0)`, f.label.ID)
		require.NoError(t, err)
		for _, endpoint := range []string{"getEvent", "calendarList", "crossCalendar", "meEvents"} {
			requireColorView(t, f.readAs(t, endpoint, evtID),
				colorView{Color: "#2ecc87", OwnerColor: labelOwnerColor, LabelID: f.label.ID, LabelName: f.label.Name},
				"lower-case label colour via "+endpoint)
		}
	})

	t.Run("responses", func(t *testing.T) {
		t.Parallel()
		f := newColorFixture(t)
		o := f.o
		url := labelCalURL(o, f.calID) + "/events"
		title := "Responses " + randomHex(4)
		body := labelEventBody(title, labelSeriesStart, map[string]any{"labelId": f.label.ID})
		key := http.Header{"Idempotency-Key": []string{"colour-" + randomHex(8)}}

		status, raw, err := postCalendarEventWithHeaders(url, o.AccessToken, body, key)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, "body=%s", raw)
		var created map[string]any
		require.NoError(t, json.Unmarshal(raw, &created))
		evtID := eventIDOf(t, created)
		requireColorView(t, created, colorView{Color: labelTestColor, OwnerColor: labelOwnerColor, LabelID: f.label.ID, LabelName: "L"}, "create")

		patched := patchLabelEvent(t, o, f.calID, evtID, map[string]any{"title": title + " edited"})
		requireColorView(t, patched, colorView{Color: labelTestColor, OwnerColor: labelOwnerColor, LabelID: f.label.ID, LabelName: "L"}, "patch")

		// A replay re-reads the label, so a rename after the create shows.
		doJSON(t, http.MethodPatch, labelCalURL(o, f.calID)+"/labels/"+f.label.ID, o.AccessToken, map[string]any{"name": "L renamed"}, nil)
		status, raw, err = postCalendarEventWithHeaders(url, o.AccessToken, body, key)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, "body=%s", raw)
		var replay map[string]any
		require.NoError(t, json.Unmarshal(raw, &replay))
		require.Equal(t, evtID, replay["id"])
		requireColorView(t, replay, colorView{Color: labelTestColor, OwnerColor: labelOwnerColor, LabelID: f.label.ID, LabelName: "L renamed"}, "replay after rename")

		// ...and so does deleting the label.
		status, raw = doJSONStatus(t, http.MethodDelete, labelCalURL(o, f.calID)+"/labels/"+f.label.ID, o.AccessToken, nil)
		require.Equal(t, http.StatusNoContent, status, "body=%s", raw)
		status, raw, err = postCalendarEventWithHeaders(url, o.AccessToken, body, key)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, "body=%s", raw)
		replay = nil
		require.NoError(t, json.Unmarshal(raw, &replay))
		requireColorView(t, replay, colorView{Color: labelOwnerColor, OwnerColor: labelOwnerColor}, "replay after label delete")

		taskID := createTaskWithDue(t, o, "Colour task "+randomHex(3), "2027-09-09")
		var fromTask map[string]any
		doJSON(t, http.MethodPost, url+"/from-task", o.AccessToken, map[string]any{"taskId": taskID, "timezone": "UTC"}, &fromTask)
		for _, field := range []string{"color", "ownerColor"} {
			require.Containsf(t, fromTask, field, "from-task response omits %q", field)
			require.Regexp(t, hexColorPattern, fromTask[field], "from-task %s", field)
		}
		require.Contains(t, fromTask, "labelId")
		require.Contains(t, fromTask, "detailsHidden")

		// canEdit: the owner may, a viewer may not.
		viewer := inviteAndJoinWorkspace(t, o)
		addCalendarMemberWithRole(t, o, f.calID, viewer.Email, "viewer")
		require.Equal(t, true, getLabelEvent(t, o, f.calID, evtID)["canEdit"], "owner canEdit")
		require.Equal(t, false, getLabelEvent(t, viewer, f.calID, evtID)["canEdit"], "viewer canEdit")
	})
}
