package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// mcpLabel decodes the label a create or update tool answers with.
func mcpLabel(t *testing.T, payload string) calendarLabelJSON {
	t.Helper()
	var out calendarLabelJSON
	require.NoError(t, json.Unmarshal([]byte(payload), &out), "payload=%s", payload)
	require.NotEmpty(t, out.ID, "payload=%s", payload)
	return out
}

// mcpLabels decodes the list tool's answer.
func mcpLabels(t *testing.T, payload string) []calendarLabelJSON {
	t.Helper()
	var out struct {
		Labels []calendarLabelJSON `json:"labels"`
	}
	require.NoError(t, json.Unmarshal([]byte(payload), &out), "payload=%s", payload)
	require.NotNil(t, out.Labels, "an empty list is [], not null: payload=%s", payload)
	return out.Labels
}

func TestMCPCalendarLabelTools(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	t.Run("manage", func(t *testing.T) {
		t.Parallel()
		f := newLabelFixture(t)
		o := f.owner

		require.Empty(t, mcpLabels(t, mcpTool(t, o, "list_calendar_labels", map[string]any{"calendarId": f.calID})))

		live := mcpLabel(t, mcpTool(t, o, "create_calendar_label", map[string]any{
			"calendarId": f.calID, "name": "  Live  ", "color": "#2ecc87"}))
		require.Equal(t, "Live", live.Name, "surrounding whitespace is trimmed")
		require.Equal(t, "#2ECC87", live.Color, "the colour is stored upper-case")
		require.Positive(t, live.CreatedAt)
		require.GreaterOrEqual(t, live.UpdatedAt, live.CreatedAt)
		require.EqualValues(t, 1, countCalendarEvents(t, o.WorkspacePublicID, f.calID, "calendar.label.created"))
		require.EqualValues(t, 1, countSubjectEvents(t, "calendar.label.created", live.ID))

		// A label made over REST is listed after it, in creation order, and the
		// two transports read the same rows.
		beta := createCalendarLabel(t, o, f.calID, "Beta", "#B38BDC")
		listed := mcpLabels(t, mcpTool(t, o, "list_calendar_labels", map[string]any{"calendarId": f.calID}))
		require.Equal(t, listCalendarLabels(t, o, f.calID), listed)
		require.Equal(t, []string{live.ID, beta.ID}, []string{listed[0].ID, listed[1].ID})

		renamed := mcpLabel(t, mcpTool(t, o, "update_calendar_label", map[string]any{
			"calendarId": f.calID, "labelId": live.ID, "name": "Live!"}))
		require.Equal(t, live.ID, renamed.ID)
		require.Equal(t, "Live!", renamed.Name)
		require.Equal(t, "#2ECC87", renamed.Color)
		require.EqualValues(t, 1, countCalendarEvents(t, o.WorkspacePublicID, f.calID, "calendar.label.updated"))

		recoloured := mcpLabel(t, mcpTool(t, o, "update_calendar_label", map[string]any{
			"calendarId": f.calID, "labelId": live.ID, "color": "#f35f8c"}))
		require.Equal(t, "Live!", recoloured.Name)
		require.Equal(t, "#F35F8C", recoloured.Color)
		require.EqualValues(t, 2, countCalendarEvents(t, o.WorkspacePublicID, f.calID, "calendar.label.updated"))

		// A call that changes nothing answers the label and records nothing.
		same := mcpLabel(t, mcpTool(t, o, "update_calendar_label", map[string]any{
			"calendarId": f.calID, "labelId": live.ID, "name": "Live!", "color": "#F35F8C"}))
		require.Equal(t, recoloured, same)
		require.EqualValues(t, 2, countCalendarEvents(t, o.WorkspacePublicID, f.calID, "calendar.label.updated"))

		out := mcpTool(t, o, "delete_calendar_label", map[string]any{"calendarId": f.calID, "labelId": live.ID})
		require.JSONEq(t, `{"success":true}`, out)
		require.EqualValues(t, 1, countCalendarEvents(t, o.WorkspacePublicID, f.calID, "calendar.label.deleted"))
		require.EqualValues(t, 1, countSubjectEvents(t, "calendar.label.deleted", live.ID))
		after := listCalendarLabels(t, o, f.calID)
		require.Len(t, after, 1)
		require.Equal(t, beta.ID, after[0].ID)
	})

	t.Run("name_taken", func(t *testing.T) {
		t.Parallel()
		f := newLabelFixture(t)
		o := f.owner
		createCalendarLabel(t, o, f.calID, "Alpha", labelTestColor)
		beta := createCalendarLabel(t, o, f.calID, "Beta", labelTestColor)

		require.Equal(t, "CALENDAR.LABEL.NAME_TAKEN", mcpToolErrorCode(t, o, "create_calendar_label",
			map[string]any{"calendarId": f.calID, "name": "Alpha", "color": labelTestColor}))
		require.Equal(t, "CALENDAR.LABEL.NAME_TAKEN", mcpToolErrorCode(t, o, "create_calendar_label",
			map[string]any{"calendarId": f.calID, "name": " Alpha ", "color": labelTestColor}),
			"the name is compared after trimming")
		require.Equal(t, "CALENDAR.LABEL.NAME_TAKEN", mcpToolErrorCode(t, o, "update_calendar_label",
			map[string]any{"calendarId": f.calID, "labelId": beta.ID, "name": "Alpha"}))

		require.Len(t, listCalendarLabels(t, o, f.calID), 2, "a refused write leaves the labels as they were")
		require.Equal(t, "Beta", listCalendarLabels(t, o, f.calID)[1].Name)
		require.EqualValues(t, 0, countCalendarEvents(t, o.WorkspacePublicID, f.calID, "calendar.label.updated"))
	})

	t.Run("limit", func(t *testing.T) {
		t.Parallel()
		f := newLabelFixture(t)
		o := f.owner
		var first calendarLabelJSON
		for n := 0; n < 50; n++ {
			l := createCalendarLabel(t, o, f.calID, fmt.Sprintf("Fill %02d", n), labelTestColor)
			if n == 0 {
				first = l
			}
		}

		require.Equal(t, "CALENDAR.LABEL.LIMIT_REACHED", mcpToolErrorCode(t, o, "create_calendar_label",
			map[string]any{"calendarId": f.calID, "name": "One too many", "color": labelTestColor}))
		require.Len(t, mcpLabels(t, mcpTool(t, o, "list_calendar_labels", map[string]any{"calendarId": f.calID})), 50)

		// Deleting one frees a slot, so the refusal above was the cap.
		mcpTool(t, o, "delete_calendar_label", map[string]any{"calendarId": f.calID, "labelId": first.ID})
		mcpTool(t, o, "create_calendar_label", map[string]any{"calendarId": f.calID, "name": "Fits now", "color": labelTestColor})
		require.Len(t, listCalendarLabels(t, o, f.calID), 50)
	})

	t.Run("role_floor", func(t *testing.T) {
		t.Parallel()
		f := newLabelFixture(t)
		o := f.owner
		existing := createCalendarLabel(t, o, f.calID, "Existing", labelTestColor)

		for _, role := range []string{"editor", "viewer"} {
			member := f.addMember(t, role)
			// Reading is open to every member of the calendar.
			got := mcpLabels(t, mcpTool(t, member, "list_calendar_labels", map[string]any{"calendarId": f.calID}))
			require.Len(t, got, 1, "%s lists", role)

			require.Equal(t, "CALENDAR.CALENDAR.MANAGER_ROLE_REQUIRED", mcpToolErrorCode(t, member, "create_calendar_label",
				map[string]any{"calendarId": f.calID, "name": "By " + role, "color": labelTestColor}), "%s create", role)
			require.Equal(t, "CALENDAR.CALENDAR.MANAGER_ROLE_REQUIRED", mcpToolErrorCode(t, member, "update_calendar_label",
				map[string]any{"calendarId": f.calID, "labelId": existing.ID, "name": "Renamed by " + role}), "%s update", role)
			require.Equal(t, "CALENDAR.CALENDAR.MANAGER_ROLE_REQUIRED", mcpToolErrorCode(t, member, "delete_calendar_label",
				map[string]any{"calendarId": f.calID, "labelId": existing.ID}), "%s delete", role)
		}
		after := listCalendarLabels(t, o, f.calID)
		require.Len(t, after, 1, "refused writes change nothing")
		require.Equal(t, "Existing", after[0].Name)

		// A manager passes the same floor REST applies.
		manager := f.addMember(t, "manager")
		made := mcpLabel(t, mcpTool(t, manager, "create_calendar_label",
			map[string]any{"calendarId": f.calID, "name": "By manager", "color": labelTestColor}))
		require.Equal(t, "By manager", made.Name)

		// A workspace member with no row on the calendar is not told more than
		// REST tells them.
		outsider := inviteAndJoinWorkspace(t, o)
		require.Equal(t, "CALENDAR.CALENDAR.ACCESS_DENIED", mcpToolErrorCode(t, outsider, "list_calendar_labels",
			map[string]any{"calendarId": f.calID}))
		require.Equal(t, "CALENDAR.CALENDAR.ACCESS_DENIED", mcpToolErrorCode(t, outsider, "create_calendar_label",
			map[string]any{"calendarId": f.calID, "name": "By outsider", "color": labelTestColor}))
	})

	t.Run("not_found", func(t *testing.T) {
		t.Parallel()
		f := newLabelFixture(t)
		o := f.owner
		otherCal := createLabelCalendar(t, o, "Other "+randomHex(3), labelOwnerColor)
		foreign := createCalendarLabel(t, o, otherCal, "Foreign", labelTestColor)
		createCalendarLabel(t, o, f.calID, "Mine", labelTestColor)

		for name, id := range map[string]string{"unknown": newTestUUID(t), "foreign": foreign.ID} {
			require.Equal(t, "CALENDAR.LABEL.NOT_FOUND", mcpToolErrorCode(t, o, "delete_calendar_label",
				map[string]any{"calendarId": f.calID, "labelId": id}), "delete of a %s label", name)
			require.Equal(t, "CALENDAR.LABEL.NOT_FOUND", mcpToolErrorCode(t, o, "update_calendar_label",
				map[string]any{"calendarId": f.calID, "labelId": id, "name": "X"}), "update of a %s label", name)
		}
		require.Equal(t, "CALENDAR.CALENDAR.NOT_FOUND", mcpToolErrorCode(t, o, "list_calendar_labels",
			map[string]any{"calendarId": newTestUUID(t)}))
		require.Len(t, listCalendarLabels(t, o, otherCal), 1, "another calendar's label was not deleted")
		require.EqualValues(t, 0, countCalendarEvents(t, o.WorkspacePublicID, f.calID, "calendar.label.deleted"))
	})

	t.Run("system_calendar", func(t *testing.T) {
		t.Parallel()
		o := newTenant(t)
		sysCalID := insertSystemCalendarWithOwner(t, o)

		// Reading is allowed, and so is nothing else, at the strongest role.
		require.Empty(t, mcpLabels(t, mcpTool(t, o, "list_calendar_labels", map[string]any{"calendarId": sysCalID})))

		mcpCode := mcpToolErrorCode(t, o, "create_calendar_label",
			map[string]any{"calendarId": sysCalID, "name": "On system", "color": labelTestColor})
		status, restCode := restErrorCode(t, http.MethodPost, labelCalURL(o, sysCalID)+"/labels", o.AccessToken,
			map[string]any{"name": "On system", "color": labelTestColor})
		require.Equal(t, http.StatusForbidden, status)
		require.Equal(t, "CALENDAR.CALENDAR.ACCESS_DENIED", restCode)
		require.Equal(t, restCode, mcpCode, "both transports refuse a label on a system calendar alike")
		require.Empty(t, listCalendarLabels(t, o, sysCalID))
	})

	t.Run("arguments", func(t *testing.T) {
		t.Parallel()
		f := newLabelFixture(t)
		o := f.owner
		label := createCalendarLabel(t, o, f.calID, "Alpha", labelTestColor)

		for name, args := range map[string]map[string]any{
			"colour not #RRGGBB": {"calendarId": f.calID, "name": "X", "color": "red"},
			"name too long":      {"calendarId": f.calID, "name": strings.Repeat("a", 51), "color": labelTestColor},
			"malformed calendar": {"calendarId": "not-a-uuid", "name": "X", "color": labelTestColor},
			"missing colour":     {"calendarId": f.calID, "name": "X"},
		} {
			require.Equal(t, "MCP.TOOL.ARGUMENTS_INVALID", mcpToolErrorCode(t, o, "create_calendar_label", args), name)
		}
		require.Equal(t, "MCP.TOOL.ARGUMENTS_INVALID", mcpToolErrorCode(t, o, "update_calendar_label",
			map[string]any{"calendarId": f.calID, "labelId": "not-a-uuid", "name": "X"}))

		// A name that is only whitespace passes the schema and is refused by
		// the rule REST applies.
		require.Equal(t, "VALIDATION.BODY.FIELD_INVALID", mcpToolErrorCode(t, o, "create_calendar_label",
			map[string]any{"calendarId": f.calID, "name": "   ", "color": labelTestColor}))
		require.Equal(t, "VALIDATION.BODY.FIELD_INVALID", mcpToolErrorCode(t, o, "update_calendar_label",
			map[string]any{"calendarId": f.calID, "labelId": label.ID, "name": "   "}))
		require.Len(t, listCalendarLabels(t, o, f.calID), 1)
	})
}
