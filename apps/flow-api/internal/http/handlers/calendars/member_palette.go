package calendars

import (
	"context"
	"strings"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
)

// memberPalette is the colour set a new calendar member is drawn from, in
// assignment order. It matches nodate-time's palette so a calendar shared
// between the two products hands out the same colours.
var memberPalette = []string{
	"#47B2F7", "#F35F8C", "#B38BDC", "#FDC02D", "#E73B3B",
	"#2ECC87", "#F5A623", "#8F8F8F", "#42A5F5", "#FF7043",
}

// leastUsedPaletteColor returns the palette colour held by the fewest of
// used, ties going to the earlier palette entry. Case is ignored when
// counting and a colour outside the palette counts toward nothing.
func leastUsedPaletteColor(used []string) string {
	counts := make(map[string]int, len(memberPalette))
	for _, c := range used {
		counts[strings.ToUpper(c)]++
	}
	best := memberPalette[0]
	for _, c := range memberPalette {
		if counts[c] < counts[best] {
			best = c
		}
	}
	return best
}

// nextMemberColor picks the colour for a member joining the calendar.
func nextMemberColor(ctx context.Context, cq *calendar.Queries, workspaceID, calendarID uint32) (string, error) {
	used, err := cq.ListCalendarMemberColors(ctx, calendar.ListCalendarMemberColorsParams{
		CalendarID:  calendarID,
		WorkspaceID: workspaceID,
	})
	if err != nil {
		return "", err
	}
	return leastUsedPaletteColor(used), nil
}
