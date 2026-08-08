// Per-viewer calendar display preferences: the PATCH must leave a row
// behind that the next read observes.
//
// The preference write reports success unconditionally, so the only way
// to tell a stored preference from a discarded one is to read it back
// through the API. Both read paths are checked here — GET
// /workspaces/{wsId}/calendars/{calId} (findSubscription +
// calendarFromRow) and GET /workspaces/{wsId}/calendars (the list
// query's COALESCE over the same table) — because the sidebar renders
// from the list and the settings panel from the single-calendar read,
// and a preference only one of them sees is still broken from the
// user's side.
package e2e

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// calendarView is the subset of CalendarResponse these tests assert on.
type calendarView struct {
	ID                     string `json:"id"`
	MemberColor            string `json:"memberColor"`
	DisplayColor           string `json:"displayColor"`
	Visible                bool   `json:"visible"`
	SubscriptionSortWeight int32  `json:"subscriptionSortWeight"`
}

// addCalendarMemberByEmail grants a workspace member access to the
// calendar at the named role, which is what makes their per-viewer
// preferences reachable in the first place.
func addCalendarMemberByEmail(t *testing.T, owner *helpers.TestTenant, calID, email, role string) {
	t.Helper()
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/calendars/"+calID+"/members",
		owner.AccessToken, map[string]any{"email": email, "role": role}, nil)
}

// getCalendarAs reads a single calendar as the given actor.
func getCalendarAs(t *testing.T, actor *helpers.TestTenant, calID string) calendarView {
	t.Helper()
	var got calendarView
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+actor.WorkspacePublicID+"/calendars/"+calID,
		actor.AccessToken, nil, &got)
	require.Equal(t, calID, got.ID, "GET calendar returned a different calendar")
	return got
}

// listedCalendarAs finds the calendar in the actor's sidebar listing.
func listedCalendarAs(t *testing.T, actor *helpers.TestTenant, calID string) calendarView {
	t.Helper()
	var listed struct {
		Calendars []calendarView `json:"calendars"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+actor.WorkspacePublicID+"/calendars",
		actor.AccessToken, nil, &listed)
	for _, c := range listed.Calendars {
		if c.ID == calID {
			return c
		}
	}
	t.Fatalf("calendar %s absent from the actor's calendar list", calID)
	return calendarView{}
}

// patchSubscription sends the self-subscription PATCH and returns the
// raw status plus body so callers can assert on failures too.
func patchSubscription(t *testing.T, actor *helpers.TestTenant, calID string, body map[string]any) (int, []byte) {
	t.Helper()
	return doJSONStatus(t, http.MethodPatch,
		testServerURL+"/workspaces/"+actor.WorkspacePublicID+"/calendars/"+calID+"/subscription",
		actor.AccessToken, body)
}

// requireSubscriptionUpdated asserts the PATCH answered 200 with
// updated=true. Every case in this file expects success; the question
// under test is whether the success was backed by a stored row.
func requireSubscriptionUpdated(
	t *testing.T,
	actor *helpers.TestTenant,
	calID string,
	body map[string]any,
	label string,
) {
	t.Helper()
	status, raw := patchSubscription(t, actor, calID, body)
	require.Equalf(t, http.StatusOK, status, "%s: body=%s", label, string(raw))
	var out struct {
		Updated bool `json:"updated"`
	}
	require.NoErrorf(t, json.Unmarshal(raw, &out), "%s: decode body=%s", label, string(raw))
	assert.Truef(t, out.Updated, "%s: handler must report the preference as updated", label)
}

// TestCalendarSubscriptionPreferencesAreStored is the core regression:
// a viewer who has never expressed a preference sets a display colour,
// and the next read has to show it.
//
// Preferences live in calendar_subscriptions while access lives in
// calendar_members, so on this path there is no subscription row yet.
// A write that can only update an existing row therefore matches
// nothing, and because the handler answers updated=true regardless, the
// endpoint looks healthy while the colour reverts on the next render.
// The assertions below are on response bodies for that reason: the
// contract is what a caller can read back, not what a row happens to
// hold.
func TestCalendarSubscriptionPreferencesAreStored(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	const (
		chosenColor = "#123456"
		otherColor  = "#ABCDEF"
	)

	owner := newTenant(t)
	calID := createCalendarMut(t, owner, "Prefs Cal")
	member := inviteAndJoinWorkspace(t, owner)
	addCalendarMemberByEmail(t, owner, calID, member.Email, "editor")

	// Baseline: with no preference stored the calendar renders in the
	// colour the membership assigned. Both reads must agree, and neither
	// may already be the colour we are about to pick — otherwise the
	// assertion after the PATCH would pass without anything being written.
	baseline := getCalendarAs(t, member, calID)
	require.NotEmpty(t, baseline.MemberColor, "membership must carry a colour")
	require.Equal(t, baseline.MemberColor, baseline.DisplayColor,
		"with no stored preference the display colour falls back to the membership colour")
	require.NotEqual(t, chosenColor, baseline.DisplayColor,
		"test colour must differ from the membership default")
	require.True(t, baseline.Visible, "a calendar with no stored preference renders by default")
	assert.Equal(t, baseline.DisplayColor, listedCalendarAs(t, member, calID).DisplayColor,
		"list and single-calendar reads must agree before any preference exists")

	ownerBefore := getCalendarAs(t, owner, calID)

	// First write: nothing to update, so this is the insert path.
	requireSubscriptionUpdated(t, member, calID,
		map[string]any{"displayColor": chosenColor, "sortWeight": 42}, "first patch")

	afterFirst := getCalendarAs(t, member, calID)
	assert.Equal(t, chosenColor, afterFirst.DisplayColor,
		"the colour the member just set must survive to the next read")
	assert.Equal(t, int32(42), afterFirst.SubscriptionSortWeight,
		"the sort weight the member just set must survive to the next read")
	assert.True(t, afterFirst.Visible, "an unspecified visible must start from the column default")
	assert.Equal(t, baseline.MemberColor, afterFirst.MemberColor,
		"a private display colour must not overwrite the membership colour")

	fromList := listedCalendarAs(t, member, calID)
	assert.Equal(t, chosenColor, fromList.DisplayColor,
		"the sidebar listing must render the stored preference, not the membership colour")
	assert.Equal(t, int32(42), fromList.SubscriptionSortWeight,
		"the sidebar listing must render the stored sort weight")

	// The preference is the viewer's alone: the owner's own view of the
	// same calendar is untouched.
	ownerAfter := getCalendarAs(t, owner, calID)
	assert.Equal(t, ownerBefore.DisplayColor, ownerAfter.DisplayColor,
		"one member's display colour must not leak into another member's view")

	// Second write touching only visibility. The stored colour and sort
	// weight must survive fields the request does not mention.
	requireSubscriptionUpdated(t, member, calID,
		map[string]any{"visible": false}, "visibility-only patch")

	afterSecond := getCalendarAs(t, member, calID)
	assert.False(t, afterSecond.Visible, "visible=false must be stored")
	assert.Equal(t, chosenColor, afterSecond.DisplayColor,
		"a patch that omits displayColor must leave the stored colour alone")
	assert.Equal(t, int32(42), afterSecond.SubscriptionSortWeight,
		"a patch that omits sortWeight must leave the stored weight alone")

	// Repeating a patch byte-for-byte can write no row at all. That is a
	// successful no-op, not a missing subscription, and must not surface
	// as 404.
	status, raw := patchSubscription(t, member, calID, map[string]any{"visible": false})
	require.Equalf(t, http.StatusOK, status,
		"an identical repeat patch must stay successful; body=%s", string(raw))

	afterRepeat := getCalendarAs(t, member, calID)
	assert.Equal(t, afterSecond, afterRepeat,
		"an identical repeat patch must leave the stored preferences exactly as they were")

	// A third write flips the colour again, proving the duplicate-key
	// branch updates rather than keeping the first value forever.
	requireSubscriptionUpdated(t, member, calID,
		map[string]any{"displayColor": otherColor}, "recolour patch")

	afterThird := getCalendarAs(t, member, calID)
	assert.Equal(t, otherColor, afterThird.DisplayColor,
		"recolouring an existing preference must take effect")
	assert.False(t, afterThird.Visible,
		"a colour-only patch must leave the stored visibility alone")

	// Four patches must have produced exactly one preference row: a write
	// that inserted every time would render correctly on the read path
	// while quietly duplicating rows behind it.
	var rows int
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*)
		 FROM calendar_subscriptions cs
		 JOIN calendars c ON c.id = cs.calendar_id
		 JOIN users u ON u.id = cs.user_id
		 WHERE c.public_id = UUID_TO_BIN(?, 0)
		   AND u.public_id = UUID_TO_BIN(?, 0)`,
		calID, member.UserPublicID).Scan(&rows))
	assert.Equal(t, 1, rows,
		"repeated preference writes must converge on a single subscription row")
}

// TestCalendarSubscriptionPreferencesAreIndependentPerViewer pins the
// per-viewer half of the contract: two members of the same calendar each
// set their own colour, and neither reads the other's.
//
// A write keyed on the calendar alone — or one that fell back to the
// shared membership colour — would satisfy the single-viewer test above
// and still repaint the calendar for everybody.
func TestCalendarSubscriptionPreferencesAreIndependentPerViewer(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	const (
		firstColor  = "#111111"
		secondColor = "#222222"
	)

	owner := newTenant(t)
	calID := createCalendarMut(t, owner, "Per Viewer Cal")
	first := inviteAndJoinWorkspace(t, owner)
	second := inviteAndJoinWorkspace(t, owner)
	addCalendarMemberByEmail(t, owner, calID, first.Email, "editor")
	addCalendarMemberByEmail(t, owner, calID, second.Email, "editor")

	requireSubscriptionUpdated(t, first, calID,
		map[string]any{"displayColor": firstColor, "visible": false}, "first member patch")
	requireSubscriptionUpdated(t, second, calID,
		map[string]any{"displayColor": secondColor}, "second member patch")

	firstView := getCalendarAs(t, first, calID)
	assert.Equal(t, firstColor, firstView.DisplayColor,
		"each member must read back their own colour")
	assert.False(t, firstView.Visible, "each member must read back their own visibility")

	secondView := getCalendarAs(t, second, calID)
	assert.Equal(t, secondColor, secondView.DisplayColor,
		"a second member's write must not overwrite the first member's colour")
	assert.True(t, secondView.Visible,
		"hiding the layer for one member must not hide it for another")

	ownerView := getCalendarAs(t, owner, calID)
	assert.NotEqual(t, firstColor, ownerView.DisplayColor,
		"the owner must not inherit a member's private colour")
	assert.NotEqual(t, secondColor, ownerView.DisplayColor,
		"the owner must not inherit a member's private colour")
	assert.True(t, ownerView.Visible,
		"a member hiding the layer must not hide it for the owner")
}
