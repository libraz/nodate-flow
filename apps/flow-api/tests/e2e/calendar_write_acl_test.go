// Calendar write-authorization e2e suite. Two invariants that the
// schema states but the handlers used to leave unenforced:
//
//   - calendar_members.role is ordered (owner > manager > editor >
//     viewer) and "editor writes events, viewer reads". Every handler
//     that changes a calendar's contents must therefore refuse a
//     viewer, and every one of them must refuse a system calendar,
//     whose rows come from a provider feed rather than from people.
//
//   - Publishing an event on a public share is a per-event decision, so
//     it takes write access on the calendar that event lives in.
//     Workspace membership is not a substitute: a workspace holds
//     calendars whose audiences do not coincide, and the output of the
//     attach endpoint is a URL anyone on the internet can open.
//
// Every test drives the full HTTP router via testServerURL and uses the
// REST-based newTenant helper so the path exercised matches a real
// production caller.
package e2e

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// addCalendarMemberWithRole grants the named user the given role on a
// calendar and asserts the response echoes it back.
func addCalendarMemberWithRole(t *testing.T, host *helpers.TestTenant, calID, email, role string) {
	t.Helper()
	var added struct {
		UserID string `json:"userId"`
		Role   string `json:"role"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+host.WorkspacePublicID+"/calendars/"+calID+"/members",
		host.AccessToken, map[string]any{"email": email, "role": role}, &added)
	require.Equal(t, role, added.Role, "add-member must grant the requested role")
}

// TestCalendarViewerCannotWriteContents is the H-9 regression: a
// read-only member of a calendar must not be able to create anything
// its whole audience will see. The check is per surface rather than
// per handler file, because the hole was that one shared helper existed
// and nothing called it — a single-endpoint test would have passed
// while every sibling stayed open.
//
// The final leg promotes the same user to editor and repeats the event
// create, so a regression that simply denies everyone cannot pass.
func TestCalendarViewerCannotWriteContents(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	host := newTenant(t)
	viewer := inviteAndJoinWorkspace(t, host)
	calID := createCalendarMut(t, host, "Viewer ACL Cal")
	evtID := createEventMut(t, host, calID, "Viewer ACL Event")
	addCalendarMemberWithRole(t, host, calID, viewer.Email, "viewer")

	base := testServerURL + "/workspaces/" + host.WorkspacePublicID + "/calendars/" + calID
	evtBase := base + "/events/" + evtID

	start := time.Date(2027, 7, 1, 10, 0, 0, 0, time.UTC)
	writes := []struct {
		name   string
		method string
		url    string
		body   map[string]any
	}{
		{"create event", http.MethodPost, base + "/events", map[string]any{
			"kind":     "event",
			"title":    "Viewer authored event",
			"startAt":  start.Unix(),
			"endAt":    start.Add(time.Hour).Unix(),
			"timezone": "UTC",
		}},
		{"patch event", http.MethodPatch, evtBase, map[string]any{
			"title": "Viewer renamed this",
		}},
		{"delete event", http.MethodDelete, evtBase, nil},
		{"create comment", http.MethodPost, evtBase + "/comments", map[string]any{
			"body": "Viewer authored comment",
		}},
		{"create checklist item", http.MethodPost, evtBase + "/checklist", map[string]any{
			"title": "Viewer authored checklist item",
		}},
		{"presign attachment", http.MethodPost, evtBase + "/attachments/presign", map[string]any{
			"filename":    "viewer.txt",
			"contentType": "text/plain",
			"byteSize":    12,
			"sha256":      strings.Repeat("a", 64),
		}},
		{"create memo", http.MethodPost, base + "/memos", map[string]any{
			"title": "Viewer authored memo",
		}},
	}

	for _, w := range writes {
		t.Run(w.name, func(t *testing.T) {
			status, body := doJSONStatus(t, w.method, w.url, viewer.AccessToken, w.body)
			require.Equal(t, http.StatusForbidden, status,
				"%s must be refused for a calendar viewer; body=%s", w.name, string(body))
		})
	}

	// Reading is what a viewer is for; the floor must not have moved.
	var listed struct {
		Events []struct {
			ID string `json:"id"`
		} `json:"events"`
	}
	doJSON(t, http.MethodGet, base+"/events?start=2027-01-01&end=2028-01-01",
		viewer.AccessToken, nil, &listed)
	assert.NotEmpty(t, listed.Events, "a viewer must still be able to read the calendar")

	// Nothing the viewer attempted may have landed. Comments are the
	// clearest witness: the refusal happens before the insert, so a
	// count of zero proves the handler did not write and then reject.
	var comments int
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*)
		   FROM calendar_event_comments c
		   JOIN calendar_events e ON e.id = c.event_id
		  WHERE e.public_id = UUID_TO_BIN(?, 0)`,
		evtID).Scan(&comments))
	assert.Zero(t, comments, "a refused comment must not reach the table")

	// Promoting to editor must open exactly the write that was refused.
	doJSON(t, http.MethodPatch,
		base+"/members/"+viewer.UserPublicID,
		host.AccessToken, map[string]any{"role": "editor"}, &struct {
			Updated bool `json:"updated"`
		}{})

	var created struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, base+"/events", viewer.AccessToken, map[string]any{
		"kind":     "event",
		"title":    "Editor authored event",
		"startAt":  start.Unix(),
		"endAt":    start.Add(time.Hour).Unix(),
		"timezone": "UTC",
	}, &created)
	assert.NotEmpty(t, created.ID, "an editor must be able to create an event")
}

// TestSystemCalendarRejectsContentWrites is the second half of H-9: a
// system calendar's rows are populated from a provider feed, so a user
// row written there has no source to be reconciled against and survives
// no refresh. The refusal must not depend on role, so the test promotes
// the subscriber to owner first — the strongest role the enum has —
// and still expects a refusal.
func TestSystemCalendarRejectsContentWrites(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)

	// Subscribing to a country's holiday feed creates the workspace's
	// system calendar on first call and grants the caller viewer.
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/calendars/subscribe-system",
		owner.AccessToken, map[string]any{"country": "JP"}, &struct {
			Ok bool `json:"ok"`
		}{})

	var listed struct {
		Calendars []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
		} `json:"calendars"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/calendars",
		owner.AccessToken, nil, &listed)

	sysCalID := ""
	for _, c := range listed.Calendars {
		if c.Kind == "system" {
			sysCalID = c.ID
			break
		}
	}
	require.NotEmpty(t, sysCalID, "subscribing to a holiday feed must surface a system calendar")

	// Give the caller the highest role the enum has, so the refusal that
	// follows can only come from the calendar's kind. There is no API
	// for this by design — the subscribe path grants viewer and the
	// role-change endpoint needs manager, which the subscriber is not.
	_, err := testDB.Exec(
		`UPDATE calendar_members cm
		   JOIN calendars c ON c.id = cm.calendar_id
		   JOIN users u ON u.id = cm.user_id
		    SET cm.role = 'owner'
		  WHERE c.public_id = UUID_TO_BIN(?, 0)
		    AND u.public_id = UUID_TO_BIN(?, 0)`,
		sysCalID, owner.UserPublicID)
	require.NoError(t, err)

	base := testServerURL + "/workspaces/" + owner.WorkspacePublicID + "/calendars/" + sysCalID
	start := time.Date(2027, 8, 1, 10, 0, 0, 0, time.UTC)

	status, body := doJSONStatus(t, http.MethodPost, base+"/events", owner.AccessToken, map[string]any{
		"kind":     "event",
		"title":    "Hand-written holiday",
		"startAt":  start.Unix(),
		"endAt":    start.Add(time.Hour).Unix(),
		"timezone": "UTC",
	})
	require.Equal(t, http.StatusForbidden, status,
		"creating an event on a system calendar must be refused at any role; body=%s", string(body))

	status, body = doJSONStatus(t, http.MethodPost, base+"/memos", owner.AccessToken, map[string]any{
		"title": "Hand-written memo",
	})
	require.Equal(t, http.StatusForbidden, status,
		"creating a memo on a system calendar must be refused at any role; body=%s", string(body))

	var events int
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*)
		   FROM calendar_events e
		   JOIN calendars c ON c.id = e.calendar_id
		  WHERE c.public_id = UUID_TO_BIN(?, 0)`,
		sysCalID).Scan(&events))
	assert.Zero(t, events, "a refused system-calendar write must not reach the table")
}

// TestPublicShareAttachRequiresCalendarWriteAccess is the H-5
// regression. A workspace member who holds no grant at all on a
// colleague's calendar must not be able to republish that calendar's
// events on a share page, whose URL needs no authentication to open.
//
// The attach endpoint reports per-event outcomes rather than failing
// the batch — the same contract confidential events have always had —
// so the assertion is that the event is counted as skipped, no link row
// exists, and the public render does not carry the title. The last leg
// grants editor on the calendar and repeats the attach, so a
// regression that refuses every attach cannot pass.
func TestPublicShareAttachRequiresCalendarWriteAccess(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	host := newTenant(t)
	outsider := inviteAndJoinWorkspace(t, host)

	// The host's calendar. The outsider is a workspace member but is
	// deliberately never added to it.
	calID := createCalendarMut(t, host, "Private Schedule")
	evtID := createEventMut(t, host, calID, "Board seat negotiation")

	// The outsider can still open a share page of their own — creating
	// one publishes nothing.
	var share struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+outsider.WorkspacePublicID+"/public-shares",
		outsider.AccessToken, map[string]any{"title": "Team calendar"}, &share)
	require.NotEmpty(t, share.ID)
	require.NotEmpty(t, share.Token, "create must return the plaintext token exactly once")

	shareBase := testServerURL + "/workspaces/" + outsider.WorkspacePublicID +
		"/public-shares/" + share.ID

	var attach struct {
		Attached int `json:"attached"`
		Skipped  int `json:"skipped"`
	}
	doJSON(t, http.MethodPost, shareBase+"/events", outsider.AccessToken,
		map[string]any{"eventIds": []string{evtID}}, &attach)
	assert.Zero(t, attach.Attached,
		"an event on a calendar the actor cannot write must not be published")
	assert.Equal(t, 1, attach.Skipped, "the refused event must be reported as skipped")

	var links int
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*)
		   FROM calendar_public_share_events l
		   JOIN calendar_events e ON e.id = l.event_id
		  WHERE e.public_id = UUID_TO_BIN(?, 0)
		    AND l.enabled = TRUE`,
		evtID).Scan(&links))
	assert.Zero(t, links, "no share link row may exist for the refused event")

	// The unauthenticated page is the surface that matters: it must not
	// carry the event under any field.
	status, body := doJSONStatus(t, http.MethodGet,
		testServerURL+"/share/cal/"+share.Token, "", nil)
	require.Equal(t, http.StatusOK, status, "the share page itself must render; body=%s", string(body))
	assert.NotContains(t, string(body), "Board seat negotiation",
		"the public page must not carry an event the publisher had no access to")

	// Granting editor on the calendar is what changes the answer.
	addCalendarMemberWithRole(t, host, calID, outsider.Email, "editor")

	var attachAfter struct {
		Attached int `json:"attached"`
		Skipped  int `json:"skipped"`
	}
	doJSON(t, http.MethodPost, shareBase+"/events", outsider.AccessToken,
		map[string]any{"eventIds": []string{evtID}}, &attachAfter)
	assert.Equal(t, 1, attachAfter.Attached,
		"a calendar editor must be able to publish that calendar's events")
	assert.Zero(t, attachAfter.Skipped)

	status, body = doJSONStatus(t, http.MethodGet,
		testServerURL+"/share/cal/"+share.Token, "", nil)
	require.Equal(t, http.StatusOK, status, "share render must still succeed; body=%s", string(body))
	assert.Contains(t, string(body), "Board seat negotiation",
		"the published event must reach the public page once access is granted")
}

// TestPublicShareAttachSkipsViewerOnlyCalendar narrows the H-5 fix to
// the role boundary rather than the membership boundary: read access to
// a calendar is not permission to republish it to the world. A viewer
// sees the event in the app, so an implementation that checks only for
// the existence of a calendar_members row would pass the previous test
// and fail this one.
func TestPublicShareAttachSkipsViewerOnlyCalendar(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	host := newTenant(t)
	viewer := inviteAndJoinWorkspace(t, host)

	calID := createCalendarMut(t, host, "Shared Schedule")
	evtID := createEventMut(t, host, calID, "Quarterly close review")
	addCalendarMemberWithRole(t, host, calID, viewer.Email, "viewer")

	var share struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+viewer.WorkspacePublicID+"/public-shares",
		viewer.AccessToken, map[string]any{"title": "Viewer share"}, &share)
	require.NotEmpty(t, share.ID)

	var attach struct {
		Attached int `json:"attached"`
		Skipped  int `json:"skipped"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+viewer.WorkspacePublicID+"/public-shares/"+share.ID+"/events",
		viewer.AccessToken, map[string]any{"eventIds": []string{evtID}}, &attach)
	assert.Zero(t, attach.Attached,
		"a calendar viewer must not be able to publish that calendar's events")
	assert.Equal(t, 1, attach.Skipped)

	status, body := doJSONStatus(t, http.MethodGet,
		testServerURL+"/share/cal/"+share.Token, "", nil)
	require.Equal(t, http.StatusOK, status, "share render must succeed; body=%s", string(body))
	assert.NotContains(t, string(body), "Quarterly close review",
		"a viewer's attach attempt must leave the public page empty")
}
