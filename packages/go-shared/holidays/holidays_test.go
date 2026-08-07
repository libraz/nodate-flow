package holidays_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/packages/go-shared/holidays"
	"github.com/libraz/nodate-flow/packages/go-shared/region"
)

// TestDatasetCoversTheCurrentYear is the expiry alarm. The embedded data
// spans a fixed year range, and once "now" walks past it every day looks
// like a working day again — holiday-aware snapping degrades to weekends
// only, silently and for everyone. Regenerate with `make gen-holidays`
// (widening the range in scripts/gen-holidays.ts) when this fails.
func TestDatasetCoversTheCurrentYear(t *testing.T) {
	from, to := holidays.CoveredYears()
	year := time.Now().UTC().Year()
	assert.LessOrEqual(t, from, year-1,
		"the dataset must still cover last year for tasks that were scheduled then")
	assert.GreaterOrEqual(t, to, year+1,
		"the dataset must cover next year so a due date a few months out is still holiday-aware")
}

// TestEverySupportedCountryHasData pins the invariant that makes the
// country picker honest: a country the product offers but ships no data
// for would accept the setting and then quietly ignore holidays.
func TestEverySupportedCountryHasData(t *testing.T) {
	for code := range region.SupportedCountries() {
		assert.Truef(t, holidays.Supported(code),
			"region offers %s but no holiday data is embedded for it", code)
	}
}

// TestKnownHolidaysAndWorkingDays spot-checks the data against dates that
// are fixed by statute, so a regeneration that quietly produced an empty
// or shifted set is caught.
func TestKnownHolidaysAndWorkingDays(t *testing.T) {
	day := func(s string) time.Time {
		parsed, err := time.Parse("2006-01-02", s)
		require.NoError(t, err)
		return parsed
	}

	assert.True(t, holidays.IsHoliday("JP", day("2026-01-01")), "New Year's Day is a Japanese holiday")
	assert.True(t, holidays.IsHoliday("JP", day("2026-05-03")), "Constitution Memorial Day is a Japanese holiday")
	assert.False(t, holidays.IsHoliday("JP", day("2026-06-10")), "an ordinary June weekday is not a Japanese holiday")

	assert.True(t, holidays.IsHoliday("US", day("2026-07-04")), "Independence Day is a US holiday")
	assert.False(t, holidays.IsHoliday("US", day("2026-05-03")),
		"a Japanese holiday must not leak into the US set")

	assert.False(t, holidays.IsHoliday("ZZ", day("2026-01-01")), "an unknown country has no holidays")
	assert.False(t, holidays.IsHoliday("", day("2026-01-01")), "an unset country has no holidays")
}

// TestLookupIsCaseInsensitive keeps a lowercase column value from
// silently disabling holidays.
func TestLookupIsCaseInsensitive(t *testing.T) {
	assert.Equal(t, holidays.Set("JP"), holidays.Set("jp"))
	assert.True(t, holidays.Supported("jp"))
}
