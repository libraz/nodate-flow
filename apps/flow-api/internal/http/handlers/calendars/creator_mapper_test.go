package calendars

import (
	"database/sql"
	"testing"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
)

// nullStringOf builds a valid sql.NullString for test fixtures.
func nullStringOf(s string) sql.NullString {
	return sql.NullString{String: s, Valid: true}
}

// TestEventFromFullRow_PopulatesCreator asserts the Get/Patch mapper
// surfaces the creator's public_id, display name, and avatar URL drawn
// from the LEFT JOIN on users in FindCalendarEventByPublicId.
func TestEventFromFullRow_PopulatesCreator(t *testing.T) {
	creatorID := types.New()
	row := calendar.FindCalendarEventByPublicIdRow{
		PublicID:           types.New(),
		Kind:               calendar.CalendarEventsKindEvent,
		Visibility:         calendar.CalendarEventsVisibilityDefault,
		ShowAs:             calendar.CalendarEventsShowAsBusy,
		Title:              "Standup",
		Timezone:           "UTC",
		CreatedAt:          time.Unix(1700000000, 0),
		CreatorPublicID:    creatorID,
		CreatorDisplayName: nullStringOf("Ada Lovelace"),
		CreatorAvatarUrl:   nullStringOf("https://cdn.example/ada.png"),
	}

	resp := eventFromFullRow(row)
	if resp.CreatorID != creatorID.String() {
		t.Fatalf("CreatorID = %q, want %q", resp.CreatorID, creatorID.String())
	}
	if resp.CreatorDisplayName != "Ada Lovelace" {
		t.Fatalf("CreatorDisplayName = %q, want Ada Lovelace", resp.CreatorDisplayName)
	}
	if resp.CreatorAvatarURL == nil || *resp.CreatorAvatarURL != "https://cdn.example/ada.png" {
		t.Fatalf("CreatorAvatarURL = %v, want https://cdn.example/ada.png", resp.CreatorAvatarURL)
	}
}

// TestEventFromFullRow_NullAvatar asserts a creator without an avatar
// produces a nil pointer (omitted in JSON), not an empty string.
func TestEventFromFullRow_NullAvatar(t *testing.T) {
	row := calendar.FindCalendarEventByPublicIdRow{
		PublicID:           types.New(),
		Kind:               calendar.CalendarEventsKindEvent,
		Visibility:         calendar.CalendarEventsVisibilityDefault,
		ShowAs:             calendar.CalendarEventsShowAsBusy,
		Title:              "No-avatar event",
		Timezone:           "UTC",
		CreatedAt:          time.Unix(1700000000, 0),
		CreatorPublicID:    types.New(),
		CreatorDisplayName: nullStringOf("Grace Hopper"),
		CreatorAvatarUrl:   sql.NullString{},
	}

	resp := eventFromFullRow(row)
	if resp.CreatorAvatarURL != nil {
		t.Fatalf("CreatorAvatarURL = %v, want nil", *resp.CreatorAvatarURL)
	}
	if resp.CreatorDisplayName != "Grace Hopper" {
		t.Fatalf("CreatorDisplayName = %q, want Grace Hopper", resp.CreatorDisplayName)
	}
}

// TestEventFromRangeRow_MixedCreatorsNoPerRowLookup builds a list of
// events authored by distinct creators and asserts the range-row mapper
// resolves each creator from the row itself. Because the JOIN delivers
// the creator identity inline, the mapper takes no DB handle, so there
// is structurally no per-row query — proving the N+1 avoidance for the
// list endpoints.
func TestEventFromRangeRow_MixedCreatorsNoPerRowLookup(t *testing.T) {
	makeRow := func(title, name string, cid types.PublicID) calendar.ListCalendarEventsByRangeRow {
		return calendar.ListCalendarEventsByRangeRow{
			PublicID:           types.New(),
			Kind:               calendar.CalendarEventsKindEvent,
			Visibility:         calendar.CalendarEventsVisibilityDefault,
			ShowAs:             calendar.CalendarEventsShowAsBusy,
			Title:              title,
			Timezone:           "UTC",
			CreatedAt:          time.Unix(1700000000, 0),
			CreatorPublicID:    cid,
			CreatorDisplayName: nullStringOf(name),
		}
	}

	c1, c2, c3 := types.New(), types.New(), types.New()
	rows := []calendar.ListCalendarEventsByRangeRow{
		makeRow("A", "Alice", c1),
		makeRow("B", "Bob", c2),
		makeRow("C", "Carol", c3),
		makeRow("D", "Alice", c1), // same creator as the first row
	}

	wantNames := []string{"Alice", "Bob", "Carol", "Alice"}
	wantIDs := []string{c1.String(), c2.String(), c3.String(), c1.String()}

	for i, r := range rows {
		resp := eventFromRangeRow(r)
		if resp.CreatorDisplayName != wantNames[i] {
			t.Fatalf("row %d CreatorDisplayName = %q, want %q", i, resp.CreatorDisplayName, wantNames[i])
		}
		if resp.CreatorID != wantIDs[i] {
			t.Fatalf("row %d CreatorID = %q, want %q", i, resp.CreatorID, wantIDs[i])
		}
	}
}

// TestEventFromRecurringRow_PopulatesCreator covers the recurring-range
// mapper, which shares the creator columns with the non-recurring path.
func TestEventFromRecurringRow_PopulatesCreator(t *testing.T) {
	cid := types.New()
	row := calendar.ListRecurringCalendarEventsByRangeRow{
		PublicID:           types.New(),
		Kind:               calendar.CalendarEventsKindEvent,
		Visibility:         calendar.CalendarEventsVisibilityDefault,
		ShowAs:             calendar.CalendarEventsShowAsBusy,
		Title:              "Weekly sync",
		Timezone:           "UTC",
		CreatedAt:          time.Unix(1700000000, 0),
		CreatorPublicID:    cid,
		CreatorDisplayName: nullStringOf("Edsger"),
		CreatorAvatarUrl:   nullStringOf("https://cdn.example/e.png"),
	}

	resp := eventFromRecurringRow(row)
	if resp.CreatorID != cid.String() {
		t.Fatalf("CreatorID = %q, want %q", resp.CreatorID, cid.String())
	}
	if resp.CreatorDisplayName != "Edsger" {
		t.Fatalf("CreatorDisplayName = %q, want Edsger", resp.CreatorDisplayName)
	}
	if resp.CreatorAvatarURL == nil || *resp.CreatorAvatarURL != "https://cdn.example/e.png" {
		t.Fatalf("CreatorAvatarURL = %v, want https://cdn.example/e.png", resp.CreatorAvatarURL)
	}
}

// TestCreatorPublicIDString_ZeroSuppressed asserts a zero creator
// public_id (the LEFT JOIN miss for a hard-deleted creator) renders as
// the empty string rather than a zero UUID, so the field is omitted.
func TestCreatorPublicIDString_ZeroSuppressed(t *testing.T) {
	if got := creatorPublicIDString(types.PublicID{}); got != "" {
		t.Fatalf("creatorPublicIDString(zero) = %q, want empty", got)
	}
	id := types.New()
	if got := creatorPublicIDString(id); got != id.String() {
		t.Fatalf("creatorPublicIDString(id) = %q, want %q", got, id.String())
	}
}
