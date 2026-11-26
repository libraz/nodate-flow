package calendars

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

func TestEventMappersProjectCreatorAvatarStorageKey(t *testing.T) {
	t.Parallel()

	creatorID := types.New()
	key := "avatars/" + creatorID.String() + "/0190abcd-1111-7000-8000-000000000000.jpg"
	want := "/avatars/" + creatorID.String() + "?v=0190abcd"

	full := eventFromFullRow(calendar.FindCalendarEventByPublicIdRow{CreatorPublicID: creatorID, CreatorAvatarUrl: nullStringOf(key)}, eventViewer{})
	require.NotNil(t, full.CreatorAvatarURL)
	require.Equal(t, want, *full.CreatorAvatarURL)

	rng := eventFromRangeRow(calendar.ListCalendarEventsByRangeRow{CreatorPublicID: creatorID, CreatorAvatarUrl: nullStringOf(key)}, 0)
	require.NotNil(t, rng.CreatorAvatarURL)
	require.Equal(t, want, *rng.CreatorAvatarURL)

	ext := eventFromFullRow(calendar.FindCalendarEventByPublicIdRow{CreatorPublicID: creatorID, CreatorAvatarUrl: nullStringOf("https://cdn.example/a.png")}, eventViewer{})
	require.Equal(t, "https://cdn.example/a.png", *ext.CreatorAvatarURL)
}

func TestMemberAndAttendeeRowsProjectAvatarStorageKey(t *testing.T) {
	t.Parallel()

	userID := types.New()
	key := "avatars/" + userID.String() + "/0190abcd-1111-7000-8000-000000000000.jpg"
	want := "/avatars/" + userID.String() + "?v=0190abcd"

	m := memberFromRow(calendar.ListCalendarMembersRow{UserPublicID: userID, AvatarUrl: nullStringOf(key)})
	require.NotNil(t, m.AvatarURL)
	require.Equal(t, want, *m.AvatarURL)

	a := attendeeFromRow(calendar.ListCalendarEventAttendeesRow{UserPublicID: userID, AvatarUrl: nullStringOf(key)})
	require.NotNil(t, a.AvatarURL)
	require.Equal(t, want, *a.AvatarURL)

	ext := attendeeFromRow(calendar.ListCalendarEventAttendeesRow{UserPublicID: userID, AvatarUrl: nullStringOf("https://cdn.example/a.png")})
	require.Equal(t, "https://cdn.example/a.png", *ext.AvatarURL)
}
