package duedate

import (
	"testing"
	"time"

	"github.com/libraz/nodate-flow/packages/go-shared/region"
)

func TestDaysUntilCountsCalendarDaysInTheZone(t *testing.T) {
	t.Parallel()
	tokyo, err := region.Resolve("Asia/Tokyo")
	if err != nil {
		t.Fatal(err)
	}
	due := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		now  time.Time
		z    region.Zone
		want int
	}{
		{"due today, afternoon UTC", time.Date(2026, 10, 23, 15, 0, 0, 0, time.UTC), region.UTC(), 0},
		{"next midnight UTC", time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC), region.UTC(), -1},
		{"day before in UTC", time.Date(2026, 10, 22, 20, 0, 0, 0, time.UTC), region.UTC(), 1},
		{"same instant is already the due day in Tokyo", time.Date(2026, 10, 22, 20, 0, 0, 0, time.UTC), tokyo, 0},
	} {
		if got := DaysUntil(due, tc.now, tc.z); got != tc.want {
			t.Errorf("%s: DaysUntil = %d, want %d", tc.name, got, tc.want)
		}
		if got := Overdue(due, tc.now, tc.z); got != (tc.want < 0) {
			t.Errorf("%s: Overdue = %v, want %v", tc.name, got, tc.want < 0)
		}
	}
}
