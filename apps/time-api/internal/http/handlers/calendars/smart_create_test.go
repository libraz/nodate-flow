package calendars

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNextWeekdayFrom_NextWeekSundayFromMonday(t *testing.T) {
	// Monday 2026-04-20. "来週日曜" should be Sunday 2026-05-03 (13 days ahead),
	// not this Sunday 2026-04-26 (6 days ahead).
	monday := time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC)
	result := nextWeekdayFrom(monday, time.Sunday, true)
	expected := monday.AddDate(0, 0, 13) // 2026-05-03
	assert.Equal(t, expected, result)
}

func TestNextWeekdayFrom_NextWeekMondayFromMonday(t *testing.T) {
	// Monday 2026-04-20. "来週月曜" should be 7 days ahead = 2026-04-27.
	monday := time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC)
	result := nextWeekdayFrom(monday, time.Monday, true)
	expected := monday.AddDate(0, 0, 7)
	assert.Equal(t, expected, result)
}

func TestNextWeekdayFrom_NextWeekFridayFromMonday(t *testing.T) {
	// Monday 2026-04-20. "来週金曜" should be 11 days ahead = 2026-05-01.
	monday := time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC)
	result := nextWeekdayFrom(monday, time.Friday, true)
	expected := monday.AddDate(0, 0, 11)
	assert.Equal(t, expected, result)
}

func TestNextWeekdayFrom_ThisWeekFridayFromMonday(t *testing.T) {
	// Monday 2026-04-20. "金曜" (this week) should be 4 days ahead = 2026-04-24.
	monday := time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC)
	result := nextWeekdayFrom(monday, time.Friday, false)
	expected := monday.AddDate(0, 0, 4)
	assert.Equal(t, expected, result)
}

func TestNextWeekdayFrom_ThisWeekSameDay(t *testing.T) {
	// Monday 2026-04-20. "月曜" when today is Monday → next Monday (7 days).
	monday := time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC)
	result := nextWeekdayFrom(monday, time.Monday, false)
	expected := monday.AddDate(0, 0, 7)
	assert.Equal(t, expected, result)
}

func TestNextWeekdayFrom_NextWeekSaturdayFromFriday(t *testing.T) {
	// Friday 2026-04-24. "来週土曜" should be 8 days ahead = 2026-05-02.
	friday := time.Date(2026, 4, 24, 10, 0, 0, 0, time.UTC)
	result := nextWeekdayFrom(friday, time.Saturday, true)
	expected := friday.AddDate(0, 0, 8)
	assert.Equal(t, expected, result)
}

func TestParseEventFromText_TimezoneRespected(t *testing.T) {
	now := time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC)

	t.Run("Asia/Tokyo timezone", func(t *testing.T) {
		proposal, err := ParseEventFromText("明日14時ミーティング", now, "Asia/Tokyo")
		assert.NoError(t, err)
		loc, _ := time.LoadLocation("Asia/Tokyo")
		startTime := time.Unix(proposal.StartAt, 0).In(loc)
		assert.Equal(t, "Asia/Tokyo", startTime.Location().String())
		assert.Equal(t, 14, startTime.Hour())
	})

	t.Run("America/New_York timezone", func(t *testing.T) {
		proposal, err := ParseEventFromText("明日14時ミーティング", now, "America/New_York")
		assert.NoError(t, err)
		loc, _ := time.LoadLocation("America/New_York")
		startTime := time.Unix(proposal.StartAt, 0).In(loc)
		assert.Equal(t, "America/New_York", startTime.Location().String())
		assert.Equal(t, 14, startTime.Hour())
	})

	t.Run("UTC timezone", func(t *testing.T) {
		proposal, err := ParseEventFromText("明日14時ミーティング", now, "UTC")
		assert.NoError(t, err)
		startTime := time.Unix(proposal.StartAt, 0).In(time.UTC)
		assert.Equal(t, "UTC", startTime.Location().String())
		assert.Equal(t, 14, startTime.Hour())
	})

	t.Run("invalid timezone returns error", func(t *testing.T) {
		_, err := ParseEventFromText("明日14時ミーティング", now, "Invalid/Zone")
		assert.Error(t, err)
	})

	t.Run("default hour is 9 in given timezone", func(t *testing.T) {
		proposal, err := ParseEventFromText("明日ミーティング", now, "Europe/London")
		assert.NoError(t, err)
		loc, _ := time.LoadLocation("Europe/London")
		startTime := time.Unix(proposal.StartAt, 0).In(loc)
		assert.Equal(t, "Europe/London", startTime.Location().String())
		assert.Equal(t, 9, startTime.Hour())
	})
}
