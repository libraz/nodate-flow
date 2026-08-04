// Calendar mutation e2e suite: covers the calendar mutation endpoints
// that the testing audit flagged as under-exercised. Every test in this
// file drives the full HTTP router via testServerURL and uses the
// REST-based newTenant helper so the test path matches a real
// production caller (auth-api register + workspace owner).
//
// Endpoints under coverage:
//
//   - POST   /workspaces/{wsId}/calendars                                   create
//   - POST   /workspaces/{wsId}/calendars/{calId}/events                    create event
//   - POST   /workspaces/{wsId}/calendars/{calId}/events/{evtId}/attendees  add attendee
//   - POST   .../attendees/{attendeeId}/invite                              mint magic-link invite
//   - POST   /public/invites/accept                                         redeem invite (RSVP)
//   - POST   /workspaces/{wsId}/calendars/{calId}/members                   add calendar member
//   - DELETE /workspaces/{wsId}/calendars/{calId}/members/{userId}          remove member
//   - POST   /workspaces/{wsId}/calendars/{calId}/events/{evtId}/checklist  create checklist item
//   - PATCH  .../checklist/{itemId}                                         toggle / update item
//   - DELETE .../checklist/{itemId}                                         soft-delete item
//
// Endpoints flagged in the audit but NOT covered here because they do
// not exist in flow-api as of this commit:
//
//   - GET /calendar-invites/{token}/info — the only public invite path
//     is POST /public/invites/accept; there is no separate info preview.
//   - POST /calendars/{id}/invites — invites are minted per
//     (event, attendee) at .../attendees/{attendeeId}/invite, not at
//     calendar scope.
//   - PATCH /calendars/{id}/events/{evtId}/checklist/reorder — no
//     dedicated reorder endpoint; ordering is updated by setting
//     sortWeight via the per-item PATCH.
package e2e

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/tests/helpers"
)

// calMutHelper bundles the per-test owner tenant plus the calendar and
// event public IDs the mutation tests share.
type calMutHelper struct {
	owner *helpers.TestTenant
	calID string
	evtID string
}

// createCalendarMut creates a personal calendar in the owner's
// workspace and returns its public ID. Mirrors the calendar package
// helper but lives in the e2e package because that helper is unexported.
func createCalendarMut(t *testing.T, owner *helpers.TestTenant, name string) string {
	t.Helper()
	var resp struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/calendars",
		owner.AccessToken, map[string]any{
			"kind":  "personal",
			"name":  name,
			"color": "#4285F4",
		}, &resp)
	require.NotEmpty(t, resp.ID, "create calendar must return a public id")
	return resp.ID
}

// createEventMut creates a one-hour event on the supplied calendar and
// returns the event's public ID. Start/End are intentionally fixed in
// the future so independent tests do not collide on overlap windows.
func createEventMut(t *testing.T, owner *helpers.TestTenant, calID, title string) string {
	t.Helper()
	var resp struct {
		ID string `json:"id"`
	}
	start := time.Date(2027, 6, 1, 10, 0, 0, 0, time.UTC)
	end := start.Add(1 * time.Hour)
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/calendars/"+calID+"/events",
		owner.AccessToken, map[string]any{
			"kind":     "event",
			"title":    title,
			"startAt":  start.Unix(),
			"endAt":    end.Unix(),
			"timezone": "UTC",
		}, &resp)
	require.NotEmpty(t, resp.ID, "create event must return a public id")
	return resp.ID
}

// inviteAndJoinWorkspace creates a fresh tenant in their own workspace
// and then joins them into the host's workspace via the public invite
// flow. The returned tenant carries the host's WorkspacePublicID
// erased and replaced with the host's so callers can drive
// workspace-scoped routes as the joined member.
//
// The returned *helpers.TestTenant retains its own AccessToken (the
// joined user's session) and UserPublicID (the joined user); only the
// WorkspacePublicID/Slug are rewritten to point at the host workspace.
func inviteAndJoinWorkspace(t *testing.T, host *helpers.TestTenant) *helpers.TestTenant {
	t.Helper()

	// Mint a single-use invite as the host owner.
	var created struct {
		Token  string `json:"token"`
		Invite struct {
			ID string `json:"id"`
		} `json:"invite"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+host.WorkspacePublicID+"/invites",
		host.AccessToken, map[string]any{
			"role":      "member",
			"expiresIn": 86400,
			"maxUses":   1,
		}, &created)
	require.NotEmpty(t, created.Token, "host must mint a workspace invite token")

	// Bootstrap a fresh tenant. They land in their own workspace which
	// auto-cleans on test exit; we override the references below to point
	// at the host workspace so the caller sees a single joined member.
	guest := newTenant(t)

	// Accept the invite to become a member of the host's workspace.
	var accepted struct {
		WorkspaceID string `json:"workspaceId"`
		Role        string `json:"role"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/invites/"+created.Token+"/accept",
		guest.AccessToken, nil, &accepted)
	require.Equal(t, host.WorkspacePublicID, accepted.WorkspaceID,
		"guest must land in the host workspace after accepting")
	require.Equal(t, "member", accepted.Role)

	// Pin the joined user to the host workspace for the caller's
	// convenience. UserPublicID/Email/AccessToken stay tied to the guest
	// session, which is what caller actions need.
	guest.WorkspacePublicID = host.WorkspacePublicID
	guest.WorkspaceSlug = host.WorkspaceSlug
	return guest
}

// addAttendeeMut adds the named user as an attendee on the event and
// returns the attendee's public ID. AddAttendees rejects empty input
// lists so a single user is the minimum.
func addAttendeeMut(t *testing.T, owner *helpers.TestTenant, calID, evtID, userPublicID string) string {
	t.Helper()
	var resp struct {
		Attendees []struct {
			ID     string `json:"id"`
			UserID string `json:"userId"`
		} `json:"attendees"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/calendars/"+calID+"/events/"+evtID+"/attendees",
		owner.AccessToken, map[string]any{"userIds": []string{userPublicID}}, &resp)
	require.Len(t, resp.Attendees, 1, "expected exactly one attendee row in response")
	require.Equal(t, userPublicID, resp.Attendees[0].UserID)
	require.NotEmpty(t, resp.Attendees[0].ID)
	return resp.Attendees[0].ID
}

// TestCalendarInviteCreateAndAccept is the happy-path coverage of the
// per-attendee magic-link flow: host mints an invite, attendee redeems
// it via the unauthenticated /public/invites/accept endpoint, and the
// attendee's RSVP row is updated accordingly. The audit asked us to
// also verify a separate /calendar-invites/{token}/info preview
// endpoint, but that route is not part of the current product surface
// (see file header) so it is intentionally omitted.
func TestCalendarInviteCreateAndAccept(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	host := newTenant(t)
	member := inviteAndJoinWorkspace(t, host)
	calID := createCalendarMut(t, host, "Invite Mut Cal")
	evtID := createEventMut(t, host, calID, "Invite Mut Event")
	attendeeID := addAttendeeMut(t, host, calID, evtID, member.UserPublicID)

	// Mint the invite. The plaintext token comes back exactly once.
	var created struct {
		ID        string `json:"id"`
		Token     string `json:"token"`
		ExpiresAt int64  `json:"expiresAt"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+host.WorkspacePublicID+
			"/calendars/"+calID+"/events/"+evtID+"/attendees/"+attendeeID+"/invite",
		host.AccessToken, map[string]any{}, &created)
	require.NotEmpty(t, created.ID, "create invite must return a public id")
	require.NotEmpty(t, created.Token, "create invite must return a plaintext token exactly once")
	require.Greater(t, created.ExpiresAt, time.Now().Unix(), "expiresAt must be in the future")

	// The list endpoint must surface the invite without leaking the
	// plaintext token. That confirms the create response is the only
	// way to obtain the raw token.
	var listed struct {
		Invites []struct {
			ID    string `json:"id"`
			Token string `json:"token,omitempty"`
		} `json:"invites"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+host.WorkspacePublicID+
			"/calendars/"+calID+"/events/"+evtID+"/invites",
		host.AccessToken, nil, &listed)
	require.Len(t, listed.Invites, 1, "exactly one active invite must exist for the (event, attendee) pair")
	assert.Equal(t, created.ID, listed.Invites[0].ID)
	assert.Empty(t, listed.Invites[0].Token, "list response must never re-expose the plaintext token")

	// Redeem the invite via the unauthenticated public endpoint. No
	// bearer token is set; the opaque token is the entire capability.
	status, body := doJSONStatus(t, http.MethodPost,
		testServerURL+"/public/invites/accept", "",
		map[string]any{"token": created.Token, "rsvp": "accepted"})
	require.Equal(t, http.StatusOK, status, "accept must succeed; body=%s", string(body))

	// The attendee's RSVP must now be 'accepted' in the DB. We read
	// the row directly because the public accept response does not
	// echo every attendee field.
	var rsvp string
	require.NoError(t, testDB.QueryRow(
		`SELECT a.rsvp
		 FROM calendar_event_attendees a
		 JOIN calendar_events e ON e.id = a.event_id
		 JOIN users u ON u.id = a.user_id
		 WHERE e.public_id = UUID_TO_BIN(?, 0)
		   AND u.public_id = UUID_TO_BIN(?, 0)
		 LIMIT 1`,
		evtID, member.UserPublicID).Scan(&rsvp))
	assert.Equal(t, "accepted", rsvp, "redeeming the invite must stamp the attendee RSVP")

	// Reading the invite row confirms the accepted_at column was
	// stamped, which is the dedupe gate the magic-link path relies on.
	var acceptedAtValid bool
	require.NoError(t, testDB.QueryRow(
		`SELECT accepted_at IS NOT NULL FROM calendar_event_invites
		 WHERE public_id = UUID_TO_BIN(?, 0)`,
		created.ID).Scan(&acceptedAtValid))
	assert.True(t, acceptedAtValid, "accepted_at must be stamped on redeem")
}

// TestCalendarInviteAcceptIsTokenScoped mirrors the audit's tenant
// isolation request: a magic-link invite is a capability bound to a
// single (event, attendee) pair. Anyone holding the token may redeem
// it — the design choice is intentional because the recipient may not
// have an account in the calendar's host workspace yet — but the
// effect is bounded to that attendee row. We assert both halves: an
// outsider holding the token can redeem it (returns 200), and the
// resulting RSVP write only touches the named attendee.
func TestCalendarInviteAcceptIsTokenScoped(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	host := newTenant(t)
	member := inviteAndJoinWorkspace(t, host)
	outsider := newTenant(t) // entirely separate workspace

	calID := createCalendarMut(t, host, "Token Scope Cal")
	evtID := createEventMut(t, host, calID, "Token Scope Event")
	attendeeID := addAttendeeMut(t, host, calID, evtID, member.UserPublicID)

	var created struct {
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+host.WorkspacePublicID+
			"/calendars/"+calID+"/events/"+evtID+"/attendees/"+attendeeID+"/invite",
		host.AccessToken, map[string]any{}, &created)
	require.NotEmpty(t, created.Token)

	// The outsider's bearer is irrelevant: /public/invites/accept is
	// unauthenticated. We send their bearer anyway to prove that
	// holding it does not somehow cause the redeem to write to the
	// outsider's account state.
	status, body := doJSONStatus(t, http.MethodPost,
		testServerURL+"/public/invites/accept", outsider.AccessToken,
		map[string]any{"token": created.Token, "rsvp": "tentative"})
	require.Equal(t, http.StatusOK, status, "token-bearer redeem must succeed; body=%s", string(body))

	// The attendee row that received the RSVP must be the original
	// invitee (member), not the outsider. Verify both: member RSVP
	// flipped, outsider has no attendee row on this event.
	var memberRsvp string
	require.NoError(t, testDB.QueryRow(
		`SELECT a.rsvp
		 FROM calendar_event_attendees a
		 JOIN calendar_events e ON e.id = a.event_id
		 JOIN users u ON u.id = a.user_id
		 WHERE e.public_id = UUID_TO_BIN(?, 0)
		   AND u.public_id = UUID_TO_BIN(?, 0)
		 LIMIT 1`,
		evtID, member.UserPublicID).Scan(&memberRsvp))
	assert.Equal(t, "tentative", memberRsvp, "redeem must stamp the named attendee row only")

	var outsiderRows int
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*)
		 FROM calendar_event_attendees a
		 JOIN calendar_events e ON e.id = a.event_id
		 JOIN users u ON u.id = a.user_id
		 WHERE e.public_id = UUID_TO_BIN(?, 0)
		   AND u.public_id = UUID_TO_BIN(?, 0)`,
		evtID, outsider.UserPublicID).Scan(&outsiderRows))
	assert.Equal(t, 0, outsiderRows,
		"redeem with an outsider's session must not create an attendee row for them")
}

// TestCalendarMemberAddAndRemove drives the calendar membership
// surface end-to-end:
//
//  1. Owner POSTs /calendars/{id}/members with another workspace
//     member's email and observes a new membership row.
//  2. Owner repeats the call → 409 with CALENDAR.MEMBER.ALREADY_SUBSCRIBED.
//  3. Owner DELETEs /calendars/{id}/members/{userId} → subscription
//     row flips enabled = FALSE.
//  4. Listing members no longer surfaces the removed user.
//
// The audit's "/members POST {user_id: ...}" wording is corrected to
// match the actual handler contract, which is keyed by email.
func TestCalendarMemberAddAndRemove(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	host := newTenant(t)
	member := inviteAndJoinWorkspace(t, host)
	calID := createCalendarMut(t, host, "Member Mut Cal")

	// 1. Add the workspace member to the calendar.
	var added struct {
		ID     string `json:"id"`
		UserID string `json:"userId"`
		Role   string `json:"role"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+host.WorkspacePublicID+"/calendars/"+calID+"/members",
		host.AccessToken, map[string]any{
			"email": member.Email,
			"role":  "editor",
		}, &added)
	require.NotEmpty(t, added.ID, "add-member must return a subscription public id")
	assert.Equal(t, member.UserPublicID, added.UserID)
	assert.Equal(t, "editor", added.Role)

	// The member must now show up in the calendar's member list.
	var listed struct {
		Members []struct {
			UserID string `json:"userId"`
		} `json:"members"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+host.WorkspacePublicID+"/calendars/"+calID+"/members",
		host.AccessToken, nil, &listed)
	memberPresent := false
	for _, m := range listed.Members {
		if m.UserID == member.UserPublicID {
			memberPresent = true
			break
		}
	}
	assert.True(t, memberPresent, "added member must surface in the member list")

	// 2. Repeating the add must collapse to ALREADY_SUBSCRIBED.
	status, body := doJSONStatus(t, http.MethodPost,
		testServerURL+"/workspaces/"+host.WorkspacePublicID+"/calendars/"+calID+"/members",
		host.AccessToken, map[string]any{
			"email": member.Email,
			"role":  "editor",
		})
	require.Equal(t, http.StatusConflict, status,
		"second add must collapse to 409; body=%s", string(body))
	assert.Contains(t, string(body), "CALENDAR.MEMBER.ALREADY_SUBSCRIBED",
		"conflict body must carry the canonical error code")

	// 3. Remove the member. The handler soft-deletes the
	// calendar_members row.
	var removed struct {
		Removed bool `json:"removed"`
	}
	doJSON(t, http.MethodDelete,
		testServerURL+"/workspaces/"+host.WorkspacePublicID+
			"/calendars/"+calID+"/members/"+member.UserPublicID,
		host.AccessToken, nil, &removed)
	assert.True(t, removed.Removed, "remove-member must confirm success")

	// Verify the grant row survives with enabled = FALSE, scoped to this
	// calendar and user. The row has to stay: a later re-add updates it in
	// place, and deleting it would lose who granted access and when. The
	// list endpoint hides disabled rows, so a list check alone would pass
	// even if the revoke had done nothing.
	var enabled bool
	require.NoError(t, testDB.QueryRow(
		`SELECT cm.enabled
		 FROM calendar_members cm
		 JOIN calendars c ON c.id = cm.calendar_id
		 JOIN users u ON u.id = cm.user_id
		 WHERE c.public_id = UUID_TO_BIN(?, 0)
		   AND u.public_id = UUID_TO_BIN(?, 0)
		 LIMIT 1`,
		calID, member.UserPublicID).Scan(&enabled))
	assert.False(t, enabled, "remove-member must flip calendar_members.enabled to FALSE")

	// 4. The member must no longer appear in the active list.
	var listedAfter struct {
		Members []struct {
			UserID string `json:"userId"`
		} `json:"members"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+host.WorkspacePublicID+"/calendars/"+calID+"/members",
		host.AccessToken, nil, &listedAfter)
	for _, m := range listedAfter.Members {
		assert.NotEqual(t, member.UserPublicID, m.UserID,
			"removed member must not surface in the active member list")
	}
}

// TestCalendarChecklistCRUD covers the full per-event checklist mutation
// surface: create three items, toggle one's done flag, soft-delete a
// second, and verify the GET returns exactly the surviving rows. Sort
// weight is exercised on create so the list ordering is deterministic.
func TestCalendarChecklistCRUD(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	calID := createCalendarMut(t, owner, "Checklist Cal")
	evtID := createEventMut(t, owner, calID, "Checklist Event")

	type checklistItem struct {
		ID         string `json:"id"`
		Title      string `json:"title"`
		Done       bool   `json:"done"`
		SortWeight int32  `json:"sortWeight"`
	}

	createItem := func(title string, weight int32) checklistItem {
		t.Helper()
		var resp checklistItem
		doJSON(t, http.MethodPost,
			testServerURL+"/workspaces/"+owner.WorkspacePublicID+
				"/calendars/"+calID+"/events/"+evtID+"/checklist",
			owner.AccessToken, map[string]any{
				"title":      title,
				"sortWeight": weight,
			}, &resp)
		require.NotEmpty(t, resp.ID, "create checklist item must return a public id")
		require.Equal(t, title, resp.Title)
		require.False(t, resp.Done, "freshly created items must not be done")
		return resp
	}

	first := createItem("Send agenda", 10)
	second := createItem("Book room", 20)
	third := createItem("Print handouts", 30)

	// Toggle the first item's done flag via PATCH.
	var updated struct {
		Updated bool `json:"updated"`
	}
	doJSON(t, http.MethodPatch,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+
			"/calendars/"+calID+"/events/"+evtID+"/checklist/"+first.ID,
		owner.AccessToken, map[string]any{"done": true}, &updated)
	assert.True(t, updated.Updated, "toggle done must confirm the write")

	// Soft-delete the second item.
	var deleted struct {
		Deleted bool `json:"deleted"`
	}
	doJSON(t, http.MethodDelete,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+
			"/calendars/"+calID+"/events/"+evtID+"/checklist/"+second.ID,
		owner.AccessToken, nil, &deleted)
	assert.True(t, deleted.Deleted, "delete checklist item must confirm the write")

	// GET the list. The deleted item must be absent; the toggled item
	// must come back with done = true; the third item is unchanged.
	var listed struct {
		Items []checklistItem `json:"items"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+
			"/calendars/"+calID+"/events/"+evtID+"/checklist",
		owner.AccessToken, nil, &listed)

	require.Len(t, listed.Items, 2,
		"after one delete the list must surface exactly the surviving items")

	gotByID := map[string]checklistItem{}
	for _, item := range listed.Items {
		gotByID[item.ID] = item
	}

	require.Contains(t, gotByID, first.ID, "toggled item must still be in the list")
	assert.True(t, gotByID[first.ID].Done, "toggled item's done flag must be persisted")

	require.Contains(t, gotByID, third.ID, "untouched item must still be in the list")
	assert.False(t, gotByID[third.ID].Done, "untouched item must remain not-done")

	assert.NotContains(t, gotByID, second.ID, "deleted item must drop out of the list")

	// Cross-check via DB: the second item's enabled column must be FALSE
	// even though the GET hides it.
	var secondEnabled bool
	require.NoError(t, testDB.QueryRow(
		`SELECT enabled FROM calendar_event_checklist_items
		 WHERE public_id = UUID_TO_BIN(?, 0)`,
		second.ID).Scan(&secondEnabled))
	assert.False(t, secondEnabled, "delete must flip enabled to FALSE on the underlying row")
}

// TestCalendarMutationsCrossTenantBlocked is the consolidated ACL
// regression: an outsider in workspace B receives access-denied or
// not-found on every calendar mutation route in workspace A, with no
// state change on the host's calendar. The outsider holds a valid
// session (they are a real workspace owner in their own workspace) so
// the failure must come from the calendar/workspace ACL gate, not from
// missing auth.
func TestCalendarMutationsCrossTenantBlocked(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	host := newTenant(t)
	outsider := newTenant(t) // separate workspace
	calID := createCalendarMut(t, host, "ACL Mut Cal")
	evtID := createEventMut(t, host, calID, "ACL Mut Event")

	// Seed one checklist item so the per-item DELETE can target a
	// real public id and surface the ACL gate rather than a 404 at
	// the resolver.
	var item struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+host.WorkspacePublicID+
			"/calendars/"+calID+"/events/"+evtID+"/checklist",
		host.AccessToken, map[string]any{"title": "Owned by host", "sortWeight": 10}, &item)
	require.NotEmpty(t, item.ID)

	wsBase := testServerURL + "/workspaces/" + host.WorkspacePublicID + "/calendars/" + calID

	cases := []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{
			name:   "create_event",
			method: http.MethodPost,
			path:   wsBase + "/events",
			body: map[string]any{
				"kind":     "event",
				"title":    "Outsider Event",
				"startAt":  time.Date(2027, 6, 2, 10, 0, 0, 0, time.UTC).Unix(),
				"endAt":    time.Date(2027, 6, 2, 11, 0, 0, 0, time.UTC).Unix(),
				"timezone": "UTC",
			},
		},
		{
			name:   "add_member",
			method: http.MethodPost,
			path:   wsBase + "/members",
			body:   map[string]any{"email": outsider.Email, "role": "editor"},
		},
		{
			name:   "remove_member",
			method: http.MethodDelete,
			path:   wsBase + "/members/" + outsider.UserPublicID,
			body:   nil,
		},
		{
			name:   "create_checklist_item",
			method: http.MethodPost,
			path:   wsBase + "/events/" + evtID + "/checklist",
			body:   map[string]any{"title": "Outsider item", "sortWeight": 5},
		},
		{
			name:   "update_checklist_item",
			method: http.MethodPatch,
			path:   wsBase + "/events/" + evtID + "/checklist/" + item.ID,
			body:   map[string]any{"done": true},
		},
		{
			name:   "delete_checklist_item",
			method: http.MethodDelete,
			path:   wsBase + "/events/" + evtID + "/checklist/" + item.ID,
			body:   nil,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			status, body := doJSONStatus(t, tc.method, tc.path, outsider.AccessToken, tc.body)
			// The handler graph collapses cross-tenant access to either
			// 403 (workspace member missing) or 404 (calendar not
			// visible). Anything else is a leak.
			if status != http.StatusForbidden && status != http.StatusNotFound {
				t.Fatalf("%s %s expected 403 or 404, got %d body=%s",
					tc.method, tc.path, status, string(body))
			}
			// The body must reference a calendar/workspace ACL code,
			// not an internal detail. We only require the prefix
			// "CALENDAR." so the test does not break when an error
			// gets renamed.
			assert.True(t,
				strings.Contains(string(body), "CALENDAR."),
				"error body must surface a CALENDAR.* error code; body=%s", string(body))
		})
	}

	// Final state assertion: the host's checklist still has exactly
	// the seed row enabled, proving none of the cross-tenant probes
	// landed a write. We read by event id so the count is unaffected
	// by other parallel tests.
	var seedCount int
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*)
		 FROM calendar_event_checklist_items i
		 JOIN calendar_events e ON e.id = i.event_id
		 WHERE e.public_id = UUID_TO_BIN(?, 0)
		   AND i.enabled = TRUE`,
		evtID).Scan(&seedCount))
	assert.Equal(t, 1, seedCount, "no cross-tenant write must have landed on host's checklist")
}
