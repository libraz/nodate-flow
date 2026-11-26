package e2e

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

const (
	labelTestColor     = "#2ECC87"
	labelOwnerColor    = "#F35F8C"
	labelFallbackColor = "#47B2F7"
	labelRangeQuery    = "start=2027-09-01&end=2027-10-01"
)

var (
	labelSeriesStart = time.Date(2027, time.September, 6, 10, 0, 0, 0, time.UTC)
	hexColorPattern  = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
)

// labelOccurrence is the start of the n-th weekly occurrence of a series
// created at labelSeriesStart.
func labelOccurrence(n int) time.Time { return labelSeriesStart.AddDate(0, 0, 7*n) }

func labelCalURL(tt *helpers.TestTenant, calID string) string {
	return testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/calendars/" + calID
}

// createLabelCalendar creates a personal calendar whose colour (and so the
// owner's member colour) is the one given.
func createLabelCalendar(t *testing.T, tt *helpers.TestTenant, name, color string) string {
	t.Helper()
	var resp struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/calendars",
		tt.AccessToken, map[string]any{"kind": "personal", "name": name, "color": color}, &resp)
	require.NotEmpty(t, resp.ID)
	return resp.ID
}

type calendarLabelJSON struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Color     string `json:"color"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

func createCalendarLabel(t *testing.T, tt *helpers.TestTenant, calID, name, color string) calendarLabelJSON {
	t.Helper()
	status, raw := doJSONStatus(t, http.MethodPost, labelCalURL(tt, calID)+"/labels", tt.AccessToken,
		map[string]any{"name": name, "color": color})
	require.Equal(t, http.StatusCreated, status, "create label %q: body=%s", name, raw)
	var out calendarLabelJSON
	require.NoError(t, json.Unmarshal(raw, &out))
	require.NotEmpty(t, out.ID)
	return out
}

func listCalendarLabels(t *testing.T, tt *helpers.TestTenant, calID string) []calendarLabelJSON {
	t.Helper()
	var out struct {
		Labels []calendarLabelJSON `json:"labels"`
	}
	doJSON(t, http.MethodGet, labelCalURL(tt, calID)+"/labels", tt.AccessToken, nil, &out)
	return out.Labels
}

func labelEventBody(title string, start time.Time, extra map[string]any) map[string]any {
	body := map[string]any{
		"kind":     "event",
		"title":    title,
		"startAt":  start.Unix(),
		"endAt":    start.Add(time.Hour).Unix(),
		"timezone": "UTC",
	}
	for k, v := range extra {
		body[k] = v
	}
	return body
}

func weeklyRule() map[string]any { return map[string]any{"freq": "weekly"} }

func postLabelEvent(t *testing.T, tt *helpers.TestTenant, calID string, body map[string]any) map[string]any {
	t.Helper()
	var out map[string]any
	doJSON(t, http.MethodPost, labelCalURL(tt, calID)+"/events", tt.AccessToken, body, &out)
	return out
}

func getLabelEvent(t *testing.T, tt *helpers.TestTenant, calID, evtID string) map[string]any {
	t.Helper()
	var out map[string]any
	doJSON(t, http.MethodGet, labelCalURL(tt, calID)+"/events/"+evtID, tt.AccessToken, nil, &out)
	return out
}

func patchLabelEvent(t *testing.T, tt *helpers.TestTenant, calID, evtID string, body map[string]any) map[string]any {
	t.Helper()
	var out map[string]any
	doJSON(t, http.MethodPatch, labelCalURL(tt, calID)+"/events/"+evtID, tt.AccessToken, body, &out)
	return out
}

func patchLabelEventStatus(t *testing.T, tt *helpers.TestTenant, calID, evtID string, body map[string]any) (int, []byte) {
	t.Helper()
	return doJSONStatus(t, http.MethodPatch, labelCalURL(tt, calID)+"/events/"+evtID, tt.AccessToken, body)
}

func eventIDOf(t *testing.T, m map[string]any) string {
	t.Helper()
	id, ok := m["id"].(string)
	require.True(t, ok && id != "", "event payload carries no id: %v", m)
	return id
}

// colorView is the five colour fields every event response carries. An
// empty LabelID / LabelName means the response must say null.
type colorView struct {
	Color      string
	OwnerColor string
	LabelID    string
	LabelName  string
	Hidden     bool
}

// requireColorView checks that the colour fields are present (they are
// never omitted) and carry the wanted values.
func requireColorView(t *testing.T, m map[string]any, want colorView, what string) {
	t.Helper()
	for _, key := range []string{"color", "ownerColor", "labelId", "labelName", "detailsHidden"} {
		require.Containsf(t, m, key, "%s: response omits %q: %v", what, key, m)
	}
	require.Equalf(t, want.Color, m["color"], "%s: color", what)
	require.Equalf(t, want.OwnerColor, m["ownerColor"], "%s: ownerColor", what)
	require.Equalf(t, want.Hidden, m["detailsHidden"], "%s: detailsHidden", what)
	for key, wantValue := range map[string]string{"labelId": want.LabelID, "labelName": want.LabelName} {
		if wantValue == "" {
			require.Nilf(t, m[key], "%s: %s must be null", what, key)
		} else {
			require.Equalf(t, wantValue, m[key], "%s: %s", what, key)
		}
	}
}

func countCalendarEvents(t *testing.T, wsPublicID, calID, eventType string) int64 {
	t.Helper()
	wsID := helpers.ResolveWorkspaceInternalID(t, testDB, wsPublicID)
	calendarID := helpers.ResolveCalendarInternalID(t, testDB, calID)
	var n int64
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*) FROM events WHERE workspace_id = ? AND calendar_id = ? AND type = ?`,
		wsID, calendarID, eventType).Scan(&n))
	return n
}

func countSubjectEvents(t *testing.T, eventType, subjectPublicID string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*) FROM events WHERE type = ? AND subject_public_id = UUID_TO_BIN(?, 0)`,
		eventType, subjectPublicID).Scan(&n))
	return n
}

// linkedLabelPublicID reads the label row linked to an event row straight
// from the table, so a copy made onto an override is visible as a row.
func linkedLabelPublicID(t *testing.T, eventPublicID string) string {
	t.Helper()
	var id string
	err := testDB.QueryRow(
		`SELECT BIN_TO_UUID(l.public_id, 0)
		   FROM calendar_event_labels el
		   JOIN calendar_labels l ON l.id = el.label_id
		   JOIN calendar_events e ON e.id = el.event_id
		  WHERE e.public_id = UUID_TO_BIN(?, 0)`, eventPublicID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return ""
	}
	require.NoError(t, err)
	return id
}

func eventUpdatedAtMillis(t *testing.T, eventPublicID string) int64 {
	t.Helper()
	var ms int64
	require.NoError(t, testDB.QueryRow(
		`SELECT CAST(ROUND(UNIX_TIMESTAMP(updated_at) * 1000) AS SIGNED)
		   FROM calendar_events WHERE public_id = UUID_TO_BIN(?, 0)`, eventPublicID).Scan(&ms))
	return ms
}

// resetEventUpdatedAt pins updated_at to a past instant so a later write
// is observable without waiting for the clock to tick.
func resetEventUpdatedAt(t *testing.T, eventPublicID string) int64 {
	t.Helper()
	_, err := testDB.Exec(
		`UPDATE calendar_events SET updated_at = '2020-01-01 00:00:00.000' WHERE public_id = UUID_TO_BIN(?, 0)`,
		eventPublicID)
	require.NoError(t, err)
	return eventUpdatedAtMillis(t, eventPublicID)
}

func newTestUUID(t *testing.T) string {
	t.Helper()
	var id string
	require.NoError(t, testDB.QueryRow(`SELECT UUID()`).Scan(&id))
	return id
}

// labelFixture is one tenant that owns one calendar.
type labelFixture struct {
	owner *helpers.TestTenant
	calID string
}

func newLabelFixture(t *testing.T) *labelFixture {
	t.Helper()
	owner := newTenant(t)
	return &labelFixture{owner: owner, calID: createLabelCalendar(t, owner, "Labels "+randomHex(3), labelOwnerColor)}
}

// addMember joins a fresh user to the workspace and to the calendar at role.
func (f *labelFixture) addMember(t *testing.T, role string) *helpers.TestTenant {
	t.Helper()
	member := inviteAndJoinWorkspace(t, f.owner)
	addCalendarMemberWithRole(t, f.owner, f.calID, member.Email, role)
	return member
}

// insertSystemCalendarEvent writes an event onto a system calendar, which
// the API refuses to create.
func insertSystemCalendarEvent(t *testing.T, tt *helpers.TestTenant, sysCalID string) string {
	t.Helper()
	evtID := newTestUUID(t)
	wsID := helpers.ResolveWorkspaceInternalID(t, testDB, tt.WorkspacePublicID)
	calendarID := helpers.ResolveCalendarInternalID(t, testDB, sysCalID)
	userID := helpers.ResolveUserInternalID(t, testDB, tt.UserPublicID)
	_, err := testDB.Exec(
		`INSERT INTO calendar_events
		   (public_id, workspace_id, calendar_id, kind, title, start_at, end_at, timezone, owner_user_id, created_by_user_id)
		 VALUES (UUID_TO_BIN(?, 0), ?, ?, 'event', 'Holiday', '2027-09-10 00:00:00', '2027-09-10 01:00:00', 'UTC', ?, ?)`,
		evtID, wsID, calendarID, userID, userID)
	require.NoError(t, err)
	return evtID
}

func TestCalendarLabels(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	t.Run("manage", func(t *testing.T) {
		t.Parallel()
		f := newLabelFixture(t)
		owner := f.owner
		base := labelCalURL(owner, f.calID) + "/labels"

		status, raw := doJSONStatus(t, http.MethodPost, base, owner.AccessToken,
			map[string]any{"name": "Live", "color": "#2ecc87"})
		require.Equal(t, http.StatusCreated, status, "body=%s", raw)
		var live calendarLabelJSON
		require.NoError(t, json.Unmarshal(raw, &live))
		require.Equal(t, "Live", live.Name)
		require.Equal(t, "#2ECC87", live.Color, "the colour is stored upper-case")
		require.Positive(t, live.CreatedAt)
		require.GreaterOrEqual(t, live.UpdatedAt, live.CreatedAt)
		alpha := createCalendarLabel(t, owner, f.calID, "Alpha", "#F35F8C")
		beta := createCalendarLabel(t, owner, f.calID, "Beta", "#B38BDC")

		listed := listCalendarLabels(t, owner, f.calID)
		require.Len(t, listed, 3)
		require.Equal(t, []string{live.ID, alpha.ID, beta.ID},
			[]string{listed[0].ID, listed[1].ID, listed[2].ID}, "labels list in creation order, not by name")
		require.EqualValues(t, 3, countCalendarEvents(t, owner.WorkspacePublicID, f.calID, "calendar.label.created"))

		var renamed calendarLabelJSON
		doJSON(t, http.MethodPatch, base+"/"+live.ID, owner.AccessToken, map[string]any{"name": "Live!"}, &renamed)
		require.Equal(t, "Live!", renamed.Name)
		require.Equal(t, "#2ECC87", renamed.Color)
		require.EqualValues(t, 1, countCalendarEvents(t, owner.WorkspacePublicID, f.calID, "calendar.label.updated"))

		var same calendarLabelJSON
		doJSON(t, http.MethodPatch, base+"/"+live.ID, owner.AccessToken,
			map[string]any{"name": "Live!", "color": "#2ecc87"}, &same)
		require.Equal(t, renamed.ID, same.ID)
		require.Equal(t, "Live!", same.Name)
		require.EqualValues(t, 1, countCalendarEvents(t, owner.WorkspacePublicID, f.calID, "calendar.label.updated"),
			"an update that changes nothing records nothing")

		var recoloured calendarLabelJSON
		doJSON(t, http.MethodPatch, base+"/"+live.ID, owner.AccessToken, map[string]any{"color": "#abcdef"}, &recoloured)
		require.Equal(t, "#ABCDEF", recoloured.Color)
		require.EqualValues(t, 2, countCalendarEvents(t, owner.WorkspacePublicID, f.calID, "calendar.label.updated"))

		status, raw = doJSONStatus(t, http.MethodDelete, base+"/"+beta.ID, owner.AccessToken, nil)
		require.Equal(t, http.StatusNoContent, status, "body=%s", raw)
		require.Len(t, listCalendarLabels(t, owner, f.calID), 2)
		require.EqualValues(t, 1, countCalendarEvents(t, owner.WorkspacePublicID, f.calID, "calendar.label.deleted"))

		otherCal := createLabelCalendar(t, owner, "Other "+randomHex(3), labelOwnerColor)
		foreign := createCalendarLabel(t, owner, otherCal, "Foreign", "#FDC02D")
		for name, id := range map[string]string{
			"unknown":   newTestUUID(t),
			"deleted":   beta.ID,
			"malformed": "not-a-uuid",
			"foreign":   foreign.ID,
		} {
			status, raw = doJSONStatus(t, http.MethodPatch, base+"/"+id, owner.AccessToken, map[string]any{"name": "X"})
			requireDenied(t, status, raw, http.StatusNotFound, "CALENDAR.LABEL.NOT_FOUND", "update of a "+name+" label")
			status, raw = doJSONStatus(t, http.MethodDelete, base+"/"+id, owner.AccessToken, nil)
			requireDenied(t, status, raw, http.StatusNotFound, "CALENDAR.LABEL.NOT_FOUND", "delete of a "+name+" label")
		}
		require.Len(t, listCalendarLabels(t, owner, otherCal), 1, "the foreign label survives a delete aimed at another calendar")
	})

	t.Run("roles", func(t *testing.T) {
		t.Parallel()
		f := newLabelFixture(t)
		owner := f.owner
		existing := createCalendarLabel(t, owner, f.calID, "Existing", labelTestColor)

		for _, role := range []string{"editor", "viewer"} {
			member := f.addMember(t, role)
			mbase := labelCalURL(member, f.calID) + "/labels"
			status, raw := doJSONStatus(t, http.MethodPost, mbase, member.AccessToken,
				map[string]any{"name": "By " + role, "color": labelTestColor})
			requireDenied(t, status, raw, http.StatusForbidden, "CALENDAR.CALENDAR.MANAGER_ROLE_REQUIRED", role+" create")
			status, raw = doJSONStatus(t, http.MethodPatch, mbase+"/"+existing.ID, member.AccessToken,
				map[string]any{"name": "Renamed by " + role})
			requireDenied(t, status, raw, http.StatusForbidden, "CALENDAR.CALENDAR.MANAGER_ROLE_REQUIRED", role+" update")
			status, raw = doJSONStatus(t, http.MethodDelete, mbase+"/"+existing.ID, member.AccessToken, nil)
			requireDenied(t, status, raw, http.StatusForbidden, "CALENDAR.CALENDAR.MANAGER_ROLE_REQUIRED", role+" delete")
			if role == "viewer" {
				labels := listCalendarLabels(t, member, f.calID)
				require.Len(t, labels, 1)
				require.Equal(t, existing.ID, labels[0].ID)
			}
		}
		require.Equal(t, "Existing", listCalendarLabels(t, owner, f.calID)[0].Name, "refused writes changed nothing")

		outsider := inviteAndJoinWorkspace(t, owner)
		status, raw := doJSONStatus(t, http.MethodGet, labelCalURL(outsider, f.calID)+"/labels", outsider.AccessToken, nil)
		requireDenied(t, status, raw, http.StatusForbidden, "CALENDAR.CALENDAR.ACCESS_DENIED", "non-member list")

		sysCalID := insertSystemCalendarWithOwner(t, owner)
		status, raw = doJSONStatus(t, http.MethodPost, labelCalURL(owner, sysCalID)+"/labels", owner.AccessToken,
			map[string]any{"name": "On system", "color": labelTestColor})
		requireDenied(t, status, raw, http.StatusForbidden, "CALENDAR.CALENDAR.ACCESS_DENIED", "label on a system calendar")
	})

	t.Run("input", func(t *testing.T) {
		t.Parallel()
		f := newLabelFixture(t)
		owner := f.owner
		base := labelCalURL(owner, f.calID) + "/labels"
		post := func(name, color string) (int, []byte) {
			return doJSONStatus(t, http.MethodPost, base, owner.AccessToken, map[string]any{"name": name, "color": color})
		}

		trimmed := createCalendarLabel(t, owner, f.calID, "  ライブ　", labelTestColor)
		require.Equal(t, "ライブ", trimmed.Name, "ASCII and ideographic spaces are trimmed")
		status, raw := post("ライブ", labelTestColor)
		requireDenied(t, status, raw, http.StatusConflict, "CALENDAR.LABEL.NAME_TAKEN", "same name after trimming")

		createCalendarLabel(t, owner, f.calID, "ハ", labelTestColor)
		createCalendarLabel(t, owner, f.calID, "バ", labelTestColor)

		for name, value := range map[string]string{"blank": "   ", "ideographic blank": "　　", "control": "a\x07b"} {
			status, raw = post(value, labelTestColor)
			requireDenied(t, status, raw, http.StatusUnprocessableEntity, "VALIDATION.BODY.FIELD_INVALID", name+" name")
		}
		status, raw = post(strings.Repeat("a", 51), labelTestColor)
		require.Equal(t, http.StatusUnprocessableEntity, status, "a 51 character name is refused by the schema: %s", raw)
		status, raw = post(strings.Repeat("あ", 50), labelTestColor)
		require.Equal(t, http.StatusCreated, status, "50 runes fit: %s", raw)
		for _, color := range []string{"#FFF", "red", "#GGGGGG", "2ECC87", "#2ECC8777"} {
			status, raw = post("Bad colour "+color, color)
			require.Equalf(t, http.StatusUnprocessableEntity, status, "colour %q: %s", color, raw)
		}

		// Fill to the limit, then the next create is refused.
		for n := len(listCalendarLabels(t, owner, f.calID)); n < 50; n++ {
			createCalendarLabel(t, owner, f.calID, fmt.Sprintf("Fill %02d", n), labelTestColor)
		}
		status, raw = post("One too many", labelTestColor)
		requireDenied(t, status, raw, http.StatusConflict, "CALENDAR.LABEL.LIMIT_REACHED", "51st label")
		require.Len(t, listCalendarLabels(t, owner, f.calID), 50)
	})

	t.Run("limit_race", func(t *testing.T) {
		t.Parallel()
		f := newLabelFixture(t)
		owner := f.owner
		for n := 0; n < 49; n++ {
			createCalendarLabel(t, owner, f.calID, fmt.Sprintf("Seed %02d", n), labelTestColor)
		}
		base := labelCalURL(owner, f.calID) + "/labels"
		type result struct {
			status int
			body   []byte
			err    error
		}
		results := make([]result, 2)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := range results {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				results[i].status, results[i].body, results[i].err = sendJSONStatus(http.MethodPost, base, owner.AccessToken,
					map[string]any{"name": fmt.Sprintf("Racer %d", i), "color": labelTestColor})
			}()
		}
		close(start)
		wg.Wait()

		created, refused := 0, 0
		for i, r := range results {
			require.NoErrorf(t, r.err, "request %d", i)
			switch r.status {
			case http.StatusCreated:
				created++
			case http.StatusConflict:
				require.Equal(t, "CALENDAR.LABEL.LIMIT_REACHED", decodeErrorCode(t, r.body))
				refused++
			default:
				t.Fatalf("request %d: unexpected status %d body=%s", i, r.status, r.body)
			}
		}
		require.Equal(t, 1, created, "exactly one of two racing creates wins the last slot")
		require.Equal(t, 1, refused)
		require.Len(t, listCalendarLabels(t, owner, f.calID), 50)
	})

	t.Run("assign", func(t *testing.T) {
		t.Parallel()
		f := newLabelFixture(t)
		owner := f.owner
		a := createCalendarLabel(t, owner, f.calID, "A", "#2ECC87")
		b := createCalendarLabel(t, owner, f.calID, "B", "#F5A623")
		otherCal := createLabelCalendar(t, owner, "Other "+randomHex(3), labelOwnerColor)
		foreign := createCalendarLabel(t, owner, otherCal, "Foreign", "#FDC02D")

		created := postLabelEvent(t, owner, f.calID, labelEventBody("Assigned "+randomHex(3), labelSeriesStart,
			map[string]any{"labelId": a.ID}))
		evtID := eventIDOf(t, created)
		requireColorView(t, created, colorView{Color: "#2ECC87", OwnerColor: labelOwnerColor, LabelID: a.ID, LabelName: "A"}, "create")

		patched := patchLabelEvent(t, owner, f.calID, evtID, map[string]any{"labelId": b.ID})
		requireColorView(t, patched, colorView{Color: "#F5A623", OwnerColor: labelOwnerColor, LabelID: b.ID, LabelName: "B"}, "reassign")

		cleared := patchLabelEvent(t, owner, f.calID, evtID, map[string]any{"clear": []string{"labelId"}})
		requireColorView(t, cleared, colorView{Color: labelOwnerColor, OwnerColor: labelOwnerColor}, "clear")
		require.Empty(t, linkedLabelPublicID(t, evtID))

		patchLabelEvent(t, owner, f.calID, evtID, map[string]any{"labelId": a.ID})
		both := patchLabelEvent(t, owner, f.calID, evtID, map[string]any{"labelId": b.ID, "clear": []string{"labelId"}})
		requireColorView(t, both, colorView{Color: labelOwnerColor, OwnerColor: labelOwnerColor}, "value and clear together: clear wins")

		for name, id := range map[string]string{"foreign": foreign.ID, "malformed": "not-a-uuid", "unknown": newTestUUID(t)} {
			status, raw := patchLabelEventStatus(t, owner, f.calID, evtID, map[string]any{"labelId": id})
			requireDenied(t, status, raw, http.StatusUnprocessableEntity, "CALENDAR.EVENT.LABEL_UNKNOWN", name+" label on patch")
			title := "Refused " + name + " " + randomHex(3)
			status, raw = doJSONStatus(t, http.MethodPost, labelCalURL(owner, f.calID)+"/events", owner.AccessToken,
				labelEventBody(title, labelSeriesStart, map[string]any{"labelId": id}))
			requireDenied(t, status, raw, http.StatusUnprocessableEntity, "CALENDAR.EVENT.LABEL_UNKNOWN", name+" label on create")
			var n int
			require.NoError(t, testDB.QueryRow(`SELECT COUNT(*) FROM calendar_events WHERE title = ?`, title).Scan(&n))
			require.Zero(t, n, "a refused create leaves no event behind")
		}
		require.Empty(t, linkedLabelPublicID(t, evtID), "a refused patch leaves the event unlabelled")

		editor := f.addMember(t, "editor")
		own := postLabelEvent(t, editor, f.calID, labelEventBody("Editor event "+randomHex(3), labelSeriesStart,
			map[string]any{"labelId": a.ID}))
		requireColorView(t, own, colorView{Color: "#2ECC87", OwnerColor: memberColorOfUser(t, editor, f.calID, editor.UserPublicID),
			LabelID: a.ID, LabelName: "A"}, "an editor labels their own event without managing labels")
		reassigned := patchLabelEvent(t, editor, f.calID, eventIDOf(t, own), map[string]any{"labelId": b.ID})
		require.Equal(t, b.ID, reassigned["labelId"])
		status, raw := patchLabelEventStatus(t, editor, f.calID, evtID, map[string]any{"labelId": a.ID})
		requireDenied(t, status, raw, http.StatusForbidden, "CALENDAR.EVENT.EDIT_PERMISSION_REQUIRED", "editor labelling the owner's event")

		viewer := f.addMember(t, "viewer")
		status, raw = patchLabelEventStatus(t, viewer, f.calID, evtID, map[string]any{"labelId": a.ID})
		requireDenied(t, status, raw, http.StatusForbidden, "CALENDAR.CALENDAR.EDITOR_ROLE_REQUIRED", "viewer labelling an event")

		sysCalID := insertSystemCalendarWithOwner(t, owner)
		sysEvtID := insertSystemCalendarEvent(t, owner, sysCalID)
		status, raw = patchLabelEventStatus(t, owner, sysCalID, sysEvtID, map[string]any{"labelId": a.ID})
		requireDenied(t, status, raw, http.StatusForbidden, "CALENDAR.CALENDAR.ACCESS_DENIED", "labelling a system calendar event")
	})

	t.Run("hidden", func(t *testing.T) {
		t.Parallel()
		f := newLabelFixture(t)
		owner := f.owner
		a := createCalendarLabel(t, owner, f.calID, "A", labelTestColor)
		otherCal := createLabelCalendar(t, owner, "Other "+randomHex(3), labelOwnerColor)
		foreign := createCalendarLabel(t, owner, otherCal, "Foreign", "#FDC02D")
		manager := f.addMember(t, "manager")

		evt := postLabelEvent(t, owner, f.calID, labelEventBody("Private "+randomHex(3), labelSeriesStart,
			map[string]any{"visibility": "private"}))
		evtID := eventIDOf(t, evt)

		status, raw := patchLabelEventStatus(t, manager, f.calID, evtID, map[string]any{"labelId": a.ID})
		requireDenied(t, status, raw, http.StatusForbidden, "CALENDAR.EVENT.DETAILS_HIDDEN", "manager setting a label")
		status, raw = patchLabelEventStatus(t, manager, f.calID, evtID, map[string]any{"clear": []string{"labelId"}})
		requireDenied(t, status, raw, http.StatusForbidden, "CALENDAR.EVENT.DETAILS_HIDDEN", "manager clearing a label")
		status, raw = patchLabelEventStatus(t, manager, f.calID, evtID, map[string]any{"labelId": foreign.ID})
		requireDenied(t, status, raw, http.StatusForbidden, "CALENDAR.EVENT.DETAILS_HIDDEN", "the visibility check precedes label resolution")
		require.Empty(t, linkedLabelPublicID(t, evtID))

		for _, field := range []string{"location", "memo", "url"} {
			status, raw = patchLabelEventStatus(t, manager, f.calID, evtID, map[string]any{field: "x"})
			requireDenied(t, status, raw, http.StatusForbidden, "CALENDAR.EVENT.DETAILS_HIDDEN", "manager setting "+field)
			status, raw = patchLabelEventStatus(t, manager, f.calID, evtID, map[string]any{"clear": []string{field}})
			requireDenied(t, status, raw, http.StatusForbidden, "CALENDAR.EVENT.DETAILS_HIDDEN", "manager clearing "+field)
		}

		series := postLabelEvent(t, owner, f.calID, labelEventBody("Private weekly "+randomHex(3), labelSeriesStart,
			map[string]any{"visibility": "private", "recurrenceRule": weeklyRule()}))
		seriesID := eventIDOf(t, series)
		status, raw = patchLabelEventStatus(t, manager, f.calID, seriesID, map[string]any{
			"scope": "occurrence", "occurrenceStart": labelOccurrence(1).Unix(), "location": "x"})
		requireDenied(t, status, raw, http.StatusForbidden, "CALENDAR.EVENT.DETAILS_HIDDEN", "manager setting a location on an occurrence")

		// A body that leaves the details alone still goes through.
		status, raw = patchLabelEventStatus(t, manager, f.calID, evtID, map[string]any{"title": "Retitled " + randomHex(3), "kind": "block"})
		require.Equal(t, http.StatusOK, status, string(raw))

		// The party who may see the details can still label it.
		labelled := patchLabelEvent(t, owner, f.calID, evtID, map[string]any{"labelId": a.ID})
		require.Equal(t, a.ID, labelled["labelId"])
		withLocation := patchLabelEvent(t, owner, f.calID, evtID, map[string]any{"location": "Room 1"})
		require.Equal(t, "Room 1", withLocation["location"])
	})

	t.Run("series", func(t *testing.T) {
		t.Parallel()
		f := newLabelFixture(t)
		o := f.owner
		a := createCalendarLabel(t, o, f.calID, "A", "#2ECC87")
		b := createCalendarLabel(t, o, f.calID, "B", "#F5A623")
		c := createCalendarLabel(t, o, f.calID, "C", "#42A5F5")
		d := createCalendarLabel(t, o, f.calID, "D", "#FF7043")
		view := func(l calendarLabelJSON) colorView {
			return colorView{Color: l.Color, OwnerColor: labelOwnerColor, LabelID: l.ID, LabelName: l.Name}
		}

		title := "Weekly " + randomHex(3)
		series := postLabelEvent(t, o, f.calID, labelEventBody(title, labelSeriesStart,
			map[string]any{"recurrenceRule": weeklyRule(), "labelId": a.ID}))
		seriesID := eventIDOf(t, series)
		occurrence := labelOccurrence(1)

		// Editing an occurrence copies the series label onto the override.
		ov := patchLabelEvent(t, o, f.calID, seriesID, map[string]any{
			"scope": "occurrence", "occurrenceStart": occurrence.Unix(), "title": title + " moved"})
		overrideID := eventIDOf(t, ov)
		require.NotEqual(t, seriesID, overrideID)
		requireColorView(t, ov, view(a), "override copy")
		require.Equal(t, a.ID, linkedLabelPublicID(t, overrideID), "the override row carries its own link")

		ov = patchLabelEvent(t, o, f.calID, seriesID, map[string]any{
			"scope": "occurrence", "occurrenceStart": occurrence.Unix(), "labelId": b.ID})
		require.Equal(t, overrideID, eventIDOf(t, ov))
		requireColorView(t, ov, view(b), "override relabelled")
		requireColorView(t, getLabelEvent(t, o, f.calID, seriesID), view(a), "series untouched by an occurrence edit")

		// A series edit that sends no label leaves the override's label.
		patchLabelEvent(t, o, f.calID, seriesID, map[string]any{"scope": "series", "title": title + " renamed"})
		requireColorView(t, getLabelEvent(t, o, f.calID, overrideID), view(b), "override keeps B")
		requireColorView(t, getLabelEvent(t, o, f.calID, seriesID), view(a), "series keeps A")

		// A series edit that sends a label rewrites the overrides too.
		patchLabelEvent(t, o, f.calID, seriesID, map[string]any{"scope": "series", "labelId": c.ID})
		requireColorView(t, getLabelEvent(t, o, f.calID, seriesID), view(c), "series C")
		requireColorView(t, getLabelEvent(t, o, f.calID, overrideID), view(c), "override follows the series to C")

		// Revert the override, relabel the series, edit the occurrence again.
		status, raw := doJSONStatus(t, http.MethodDelete, labelCalURL(o, f.calID)+"/events/"+overrideID, o.AccessToken, nil)
		require.Equal(t, http.StatusOK, status, "body=%s", raw)
		patchLabelEvent(t, o, f.calID, seriesID, map[string]any{
			"scope": "series", "labelId": d.ID, "clear": []string{"recurrenceExceptions"}})
		revived := patchLabelEvent(t, o, f.calID, seriesID, map[string]any{
			"scope": "occurrence", "occurrenceStart": occurrence.Unix(), "title": title + " again"})
		require.Equal(t, overrideID, eventIDOf(t, revived), "the reverted override row is revived")
		requireColorView(t, revived, view(d), "a revived override takes the series' current label")
		require.Equal(t, d.ID, linkedLabelPublicID(t, overrideID))

		// thisAndFollowing: the new series inherits, a moved override keeps its own.
		title2 := "Split " + randomHex(3)
		s2 := postLabelEvent(t, o, f.calID, labelEventBody(title2, labelSeriesStart,
			map[string]any{"recurrenceRule": weeklyRule(), "labelId": a.ID}))
		s2ID := eventIDOf(t, s2)
		later := patchLabelEvent(t, o, f.calID, s2ID, map[string]any{
			"scope": "occurrence", "occurrenceStart": labelOccurrence(3).Unix(), "labelId": b.ID})
		laterID := eventIDOf(t, later)
		split := patchLabelEvent(t, o, f.calID, s2ID, map[string]any{
			"scope": "thisAndFollowing", "occurrenceStart": labelOccurrence(1).Unix(), "title": title2 + " split"})
		require.NotEqual(t, s2ID, eventIDOf(t, split))
		requireColorView(t, split, view(a), "the new series inherits the label")
		requireColorView(t, getLabelEvent(t, o, f.calID, laterID), view(b), "a carried override keeps its own label")

		s3 := postLabelEvent(t, o, f.calID, labelEventBody("Split clear "+randomHex(3), labelSeriesStart,
			map[string]any{"recurrenceRule": weeklyRule(), "labelId": a.ID}))
		s3ID := eventIDOf(t, s3)
		splitClear := patchLabelEvent(t, o, f.calID, s3ID, map[string]any{
			"scope": "thisAndFollowing", "occurrenceStart": labelOccurrence(1).Unix(), "clear": []string{"labelId"}})
		require.NotEqual(t, s3ID, eventIDOf(t, splitClear))
		requireColorView(t, splitClear, colorView{Color: labelOwnerColor, OwnerColor: labelOwnerColor}, "clear on a split")
		requireColorView(t, getLabelEvent(t, o, f.calID, s3ID), view(a), "the truncated head keeps its label")
	})

	t.Run("linked", func(t *testing.T) {
		t.Parallel()
		f := newLabelFixture(t)
		o := f.owner
		a := createCalendarLabel(t, o, f.calID, "A", labelTestColor)
		taskID := createTaskWithDue(t, o, "Linked "+randomHex(3), "2027-09-08")
		var projected struct {
			ID string `json:"id"`
		}
		doJSON(t, http.MethodPost, labelCalURL(o, f.calID)+"/events/from-task", o.AccessToken,
			map[string]any{"taskId": taskID, "timezone": "UTC"}, &projected)
		require.NotEmpty(t, projected.ID)

		before := countSubjectEvents(t, "calendar.event.updated", projected.ID)
		labelled := patchLabelEvent(t, o, f.calID, projected.ID, map[string]any{"labelId": a.ID})
		require.Equal(t, a.ID, labelled["labelId"])
		require.Equal(t, before+1, countSubjectEvents(t, "calendar.event.updated", projected.ID),
			"a label-only change on a task-linked event records exactly one update")
		patchLabelEvent(t, o, f.calID, projected.ID, map[string]any{"labelId": a.ID})
		require.Equal(t, before+1, countSubjectEvents(t, "calendar.event.updated", projected.ID),
			"re-sending the same label records nothing")
	})

	t.Run("delete", func(t *testing.T) {
		t.Parallel()
		f := newLabelFixture(t)
		o := f.owner
		a := createCalendarLabel(t, o, f.calID, "A", labelTestColor)
		evt := postLabelEvent(t, o, f.calID, labelEventBody("Doomed label "+randomHex(3), labelSeriesStart,
			map[string]any{"labelId": a.ID}))
		evtID := eventIDOf(t, evt)
		requireColorView(t, getLabelEvent(t, o, f.calID, evtID),
			colorView{Color: labelTestColor, OwnerColor: labelOwnerColor, LabelID: a.ID, LabelName: "A"}, "before delete")

		status, raw := doJSONStatus(t, http.MethodDelete, labelCalURL(o, f.calID)+"/labels/"+a.ID, o.AccessToken, nil)
		require.Equal(t, http.StatusNoContent, status, "body=%s", raw)
		requireColorView(t, getLabelEvent(t, o, f.calID, evtID),
			colorView{Color: labelOwnerColor, OwnerColor: labelOwnerColor}, "after delete")
		require.Empty(t, linkedLabelPublicID(t, evtID), "the link row goes with the label")
	})

	t.Run("shared_db", func(t *testing.T) {
		t.Parallel()
		f := newLabelFixture(t)
		o := f.owner
		wsID := helpers.ResolveWorkspaceInternalID(t, testDB, o.WorkspacePublicID)
		calendarID := helpers.ResolveCalendarInternalID(t, testDB, f.calID)

		// Rows written the way the sibling product writes them are read back.
		evt := postLabelEvent(t, o, f.calID, labelEventBody("Written by time "+randomHex(3), labelSeriesStart, nil))
		evtID := eventIDOf(t, evt)
		labelPublicID := newTestUUID(t)
		_, err := testDB.Exec(
			`INSERT INTO calendar_labels (public_id, workspace_id, calendar_id, name, color)
			 VALUES (UUID_TO_BIN(?, 0), ?, ?, 'FromTime', '#47B2F7')`, labelPublicID, wsID, calendarID)
		require.NoError(t, err)
		_, err = testDB.Exec(
			`INSERT INTO calendar_event_labels (event_id, label_id)
			 SELECT e.id, l.id FROM calendar_events e, calendar_labels l
			  WHERE e.public_id = UUID_TO_BIN(?, 0) AND l.public_id = UUID_TO_BIN(?, 0)`, evtID, labelPublicID)
		require.NoError(t, err)
		labels := listCalendarLabels(t, o, f.calID)
		require.Len(t, labels, 1)
		require.Equal(t, labelPublicID, labels[0].ID)
		require.Equal(t, "FromTime", labels[0].Name)
		require.Equal(t, "#47B2F7", labels[0].Color)
		requireColorView(t, getLabelEvent(t, o, f.calID, evtID),
			colorView{Color: "#47B2F7", OwnerColor: labelOwnerColor, LabelID: labelPublicID, LabelName: "FromTime"}, "time-written link")

		// Label writes through flow land in the shared log.
		made := createCalendarLabel(t, o, f.calID, "Mine", labelTestColor)
		doJSON(t, http.MethodPatch, labelCalURL(o, f.calID)+"/labels/"+made.ID, o.AccessToken, map[string]any{"name": "Mine 2"}, nil)
		doJSON(t, http.MethodDelete, labelCalURL(o, f.calID)+"/labels/"+made.ID, o.AccessToken, nil, nil)
		for kind, summary := range map[string]string{
			"calendar.label.created": "Mine",
			"calendar.label.updated": "Mine 2",
			"calendar.label.deleted": "Mine 2",
		} {
			var payload []byte
			require.NoErrorf(t, testDB.QueryRow(
				`SELECT payload_json FROM events WHERE workspace_id = ? AND type = ? AND subject_public_id = UUID_TO_BIN(?, 0)`,
				wsID, kind, made.ID).Scan(&payload), "%s must name the label as its subject", kind)
			var got map[string]any
			require.NoError(t, json.Unmarshal(payload, &got))
			require.Equal(t, f.calID, got["calendarId"], kind)
			require.Equal(t, made.ID, got["labelId"], kind)
			require.Equal(t, summary, got["summary"], kind)
		}

		// A label-only patch advances the row's own updated_at.
		a := createCalendarLabel(t, o, f.calID, "A", labelTestColor)
		baseline := resetEventUpdatedAt(t, evtID)
		patchLabelEvent(t, o, f.calID, evtID, map[string]any{"labelId": a.ID})
		require.Greater(t, eventUpdatedAtMillis(t, evtID), baseline, "a label-only patch bumps calendar_events.updated_at")

		// A series-scope write advances the overrides it propagates to.
		series := postLabelEvent(t, o, f.calID, labelEventBody("Propagating "+randomHex(3), labelSeriesStart,
			map[string]any{"recurrenceRule": weeklyRule(), "labelId": a.ID}))
		seriesID := eventIDOf(t, series)
		ov := patchLabelEvent(t, o, f.calID, seriesID, map[string]any{
			"scope": "occurrence", "occurrenceStart": labelOccurrence(1).Unix(), "title": "Moved"})
		overrideID := eventIDOf(t, ov)
		b := createCalendarLabel(t, o, f.calID, "B", "#F5A623")
		overrideBaseline := resetEventUpdatedAt(t, overrideID)
		patchLabelEvent(t, o, f.calID, seriesID, map[string]any{"scope": "series", "labelId": b.ID})
		require.Equal(t, b.ID, linkedLabelPublicID(t, overrideID))
		require.Greater(t, eventUpdatedAtMillis(t, overrideID), overrideBaseline,
			"the override row's updated_at advances when the series label reaches it")
	})
}
