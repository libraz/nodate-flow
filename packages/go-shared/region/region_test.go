package region_test

import (
	"testing"

	"github.com/libraz/nodate-flow/packages/go-shared/region"
	"github.com/stretchr/testify/assert"
)

func TestValidateTimezone(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"UTC", true},
		{"Asia/Tokyo", true},
		{"America/New_York", true},
		{"Europe/Berlin", true},
		{"", false},
		{"Not/A_Timezone", false},
	}
	for _, c := range cases {
		err := region.ValidateTimezone(c.in)
		if c.want {
			assert.NoError(t, err, "tz=%q", c.in)
		} else {
			assert.Error(t, err, "tz=%q", c.in)
		}
	}
}

func TestValidateCountry(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", true}, // empty means unset, not invalid
		{"JP", true},
		{"US", true},
		{"GB", true},
		{"jp", false},
		{"JPN", false},
		{"XX", false},
		{"1", false},
	}
	for _, c := range cases {
		err := region.ValidateCountry(c.in)
		if c.want {
			assert.NoError(t, err, "cc=%q", c.in)
		} else {
			assert.Error(t, err, "cc=%q", c.in)
		}
	}
}

func TestEffectiveTimezone(t *testing.T) {
	assert.Equal(t, "Asia/Tokyo", region.EffectiveTimezone("Asia/Tokyo", "UTC"))
	assert.Equal(t, "UTC", region.EffectiveTimezone("", "", ""))
	assert.Equal(t, "Europe/Berlin", region.EffectiveTimezone("", "Europe/Berlin", "Asia/Tokyo"))
	assert.Equal(t, "UTC", region.EffectiveTimezone())
}

func TestEffectiveCountry(t *testing.T) {
	assert.Equal(t, "JP", region.EffectiveCountry("JP", ""))
	assert.Equal(t, "", region.EffectiveCountry("", "", ""))
	assert.Equal(t, "DE", region.EffectiveCountry("", "DE", "JP"))
}

func TestSupportedCountries(t *testing.T) {
	got := region.SupportedCountries()
	assert.Contains(t, got, "JP")
	assert.Contains(t, got, "US")
	assert.Equal(t, "Japan", got["JP"])
}
