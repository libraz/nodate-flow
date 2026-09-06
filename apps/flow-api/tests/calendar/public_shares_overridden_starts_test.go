package calendar

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// Tests in this file pin what the unauthenticated /share/cal/{token}
// render tells an anonymous reader about occurrences an override row
// stands in for.
//
// The share page runs the same client-side expander the app does, so a
// master served without overriddenStarts keeps emitting the occurrence its
// override replaced. Where the two range endpoints scope that field to the
// overrides a viewer may see, a share has no viewer: it is scoped to the
// share's own contents instead, which is what the assertions below hold.
//
// The handler under test is calendars.RenderPublicShare.

// shareRenderEvent is the slice of the public render these tests read.
type shareRenderEvent struct {
	ID               string         `json:"id"`
	Title            string         `json:"title"`
	StartAt          *int64         `json:"startAt"`
	RecurrenceRule   map[string]any `json:"recurrenceRule"`
	OverriddenStarts []string       `json:"overriddenStarts"`
}

// createShare mints a share page and returns its public ID and the
// plaintext token, which the API returns exactly once at create time.
func createShare(t *testing.T, tt *helpers.CalendarTestTenant, title string) (string, string) {
	t.Helper()
	var share struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("public-shares"), tt.AccessToken, map[string]any{
		"title": title,
	}, &share)
	require.NotEmpty(t, share.ID)
	require.NotEmpty(t, share.Token)
	return share.ID, share.Token
}

// attachShareEvents publishes events on a share and returns how many were
// attached and how many the attach path refused.
func attachShareEvents(t *testing.T, tt *helpers.CalendarTestTenant, shareID string, eventIDs ...string) (attached, skipped int) {
	t.Helper()
	var res struct {
		Attached int `json:"attached"`
		Skipped  int `json:"skipped"`
	}
	helpers.DoJSON(t, http.MethodPost, tt.WsPath("public-shares", shareID, "events"), tt.AccessToken, map[string]any{
		"eventIds": eventIDs,
	}, &res)
	return res.Attached, res.Skipped
}

// renderShare reads the share page the way an outsider does: no token, no
// session, just the URL.
func renderShare(t *testing.T, tt *helpers.CalendarTestTenant, token string) []shareRenderEvent {
	t.Helper()
	var rendered struct {
		Events []shareRenderEvent `json:"events"`
	}
	helpers.DoJSON(t, http.MethodGet, tt.BaseURL+"/share/cal/"+token, "", nil, &rendered)
	return rendered.Events
}

func findShareEvent(t *testing.T, events []shareRenderEvent, id string) shareRenderEvent {
	t.Helper()
	for _, e := range events {
		if e.ID == id {
			return e
		}
	}
	t.Fatalf("event %s not on the share page (%d events)", id, len(events))
	return shareRenderEvent{}
}

// TestRenderPublicShare_MasterCarriesOverriddenStartOfAttachedOverride is
// the regression guard for the duplicate on the one surface people outside
// the workspace read. With both halves of the series published, the master
// must name the occurrence the override draws, or the same meeting appears
// twice on a page nobody in the workspace is looking at.
func TestRenderPublicShare_MasterCarriesOverriddenStartOfAttachedOverride(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	first := time.Date(2027, 3, 1, 10, 0, 0, 0, time.UTC)
	masterID := weeklySeries(t, tt, calID, first)
	third := first.AddDate(0, 0, 14)
	overrideID := overrideOneOccurrence(t, tt, tt.AccessToken, calID, masterID, "Stand-up (moved)", third)

	shareID, token := createShare(t, tt, "Series And Its Override")
	attached, skipped := attachShareEvents(t, tt, shareID, masterID, overrideID)
	require.Equal(t, 2, attached, "master and override must both publish")
	require.Equal(t, 0, skipped)

	events := renderShare(t, tt, token)

	master := findShareEvent(t, events, masterID)
	require.NotNil(t, master.RecurrenceRule, "the master must still carry its rule")
	assert.Equal(t,
		[]string{third.UTC().Format(time.RFC3339)},
		master.OverriddenStarts,
		"the master must name the occurrence the override published beside it stands in for, "+
			"as an RFC 3339 instant — the same shape the two range endpoints use, so the one "+
			"client-side parser reads all three")

	// An override emits no occurrences of its own, so a start subtracted
	// from it would be subtracted from nothing.
	override := findShareEvent(t, events, overrideID)
	assert.Nil(t, override.RecurrenceRule, "an override owns no rule of its own")
	assert.Empty(t, override.OverriddenStarts, "only a recurring master carries overriddenStarts")
}

// TestRenderPublicShare_UnattachedOverrideSubtractsNothing pins the
// scoping decision this field is made under.
//
// A share has no authenticated viewer, so there is no one whose visibility
// could narrow the read. It is narrowed to the share's own contents
// instead: an occurrence is subtracted only when the override standing in
// for it is itself published here. Subtracting an unpublished override's
// start would take an occurrence off the page with nothing to replace it,
// and the hole would be readable — an anonymous reader would learn that a
// series they can see was edited somewhere they cannot. The page shows the
// original occurrence, which is what the share was given.
func TestRenderPublicShare_UnattachedOverrideSubtractsNothing(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	first := time.Date(2027, 4, 5, 10, 0, 0, 0, time.UTC)
	masterID := weeklySeries(t, tt, calID, first)
	third := first.AddDate(0, 0, 14)
	overrideID := overrideOneOccurrence(t, tt, tt.AccessToken, calID, masterID, "Stand-up (moved)", third)

	shareID, token := createShare(t, tt, "Series Only")
	attached, _ := attachShareEvents(t, tt, shareID, masterID)
	require.Equal(t, 1, attached, "only the master is published")

	events := renderShare(t, tt, token)

	for _, e := range events {
		assert.NotEqual(t, overrideID, e.ID, "an override nobody published must not render")
	}
	master := findShareEvent(t, events, masterID)
	assert.Empty(t, master.OverriddenStarts,
		"an override the share does not publish must leave no trace on the page, not even "+
			"a missing occurrence")
}

// TestRenderPublicShare_DetachedOverrideStopsSubtracting holds the same
// rule through the action an editor takes to withdraw an occurrence. Once
// the override is detached it stops rendering, so it must stop subtracting
// in the same read — otherwise taking one event off the page silently
// takes two.
func TestRenderPublicShare_DetachedOverrideStopsSubtracting(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	first := time.Date(2027, 5, 3, 10, 0, 0, 0, time.UTC)
	masterID := weeklySeries(t, tt, calID, first)
	third := first.AddDate(0, 0, 14)
	overrideID := overrideOneOccurrence(t, tt, tt.AccessToken, calID, masterID, "Stand-up (moved)", third)

	shareID, token := createShare(t, tt, "Series Then Withdrawn")
	attached, _ := attachShareEvents(t, tt, shareID, masterID, overrideID)
	require.Equal(t, 2, attached)

	before := findShareEvent(t, renderShare(t, tt, token), masterID)
	require.Equal(t, []string{third.UTC().Format(time.RFC3339)}, before.OverriddenStarts,
		"precondition: the published override suppresses the occurrence it replaces")

	var detach struct {
		Removed bool `json:"removed"`
	}
	helpers.DoJSON(t, http.MethodDelete, tt.WsPath("public-shares", shareID, "events", overrideID), tt.AccessToken, nil, &detach)
	require.True(t, detach.Removed)

	after := findShareEvent(t, renderShare(t, tt, token), masterID)
	assert.Empty(t, after.OverriddenStarts,
		"a withdrawn override draws nothing, so it must subtract nothing")
}

// TestRenderPublicShare_ConfidentialOverrideSubtractsNothing keeps the
// field behind the render's own visibility gate. A confidential event is
// refused at attach and excluded by the render query, so it never reaches
// the page; naming the start it replaced would say it exists anyway.
func TestRenderPublicShare_ConfidentialOverrideSubtractsNothing(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	first := time.Date(2027, 6, 7, 10, 0, 0, 0, time.UTC)
	masterID := weeklySeries(t, tt, calID, first)
	third := first.AddDate(0, 0, 14)

	var patched eventView
	helpers.DoJSON(t, http.MethodPatch, tt.WsPath("calendars", calID, "events", masterID), tt.AccessToken, map[string]any{
		"scope":           "occurrence",
		"occurrenceStart": third.Unix(),
		"title":           "Stand-up (confidential)",
		"visibility":      "confidential",
	}, &patched)
	require.NotEqual(t, masterID, patched.ID)

	shareID, token := createShare(t, tt, "Series With Confidential Override")
	attached, skipped := attachShareEvents(t, tt, shareID, masterID, patched.ID)
	require.Equal(t, 1, attached, "only the master may publish")
	require.Equal(t, 1, skipped, "a confidential override is refused at attach")

	events := renderShare(t, tt, token)

	for _, e := range events {
		assert.NotEqual(t, patched.ID, e.ID, "a confidential override must never reach the page")
	}
	master := findShareEvent(t, events, masterID)
	assert.Empty(t, master.OverriddenStarts,
		"an occurrence whose replacement is confidential keeps showing at its original time; "+
			"suppressing it would report the confidential row's existence")
}

// TestRenderPublicShare_UnoverriddenSeriesCarriesNoStarts keeps the field
// off a series nothing stands in for, so its presence stays readable as a
// statement rather than as noise.
func TestRenderPublicShare_UnoverriddenSeriesCarriesNoStarts(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	first := time.Date(2027, 7, 5, 10, 0, 0, 0, time.UTC)
	masterID := weeklySeries(t, tt, calID, first)

	shareID, token := createShare(t, tt, "Untouched Series")
	attached, _ := attachShareEvents(t, tt, shareID, masterID)
	require.Equal(t, 1, attached)

	master := findShareEvent(t, renderShare(t, tt, token), masterID)
	require.NotNil(t, master.RecurrenceRule)
	assert.Empty(t, master.OverriddenStarts, "a series with no override names no replaced start")
}
