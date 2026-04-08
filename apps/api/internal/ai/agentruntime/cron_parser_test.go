package agentruntime

import (
	"testing"
	"time"
)

func TestParseCronValid(t *testing.T) {
	cases := []struct {
		expr string
		at   time.Time
		want bool
	}{
		{"* * * * *", time.Date(2026, 4, 8, 12, 34, 0, 0, time.UTC), true},
		{"0 * * * *", time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC), true},
		{"0 * * * *", time.Date(2026, 4, 8, 12, 1, 0, 0, time.UTC), false},
		{"30 9 * * *", time.Date(2026, 4, 8, 9, 30, 0, 0, time.UTC), true},
		{"30 9 * * *", time.Date(2026, 4, 8, 10, 30, 0, 0, time.UTC), false},
		{"0,30 * * * *", time.Date(2026, 4, 8, 12, 30, 0, 0, time.UTC), true},
		{"0 0 1 * *", time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC), true},
		{"0 0 * * 0", time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC), true}, // Sunday
	}
	for _, tc := range cases {
		c, err := ParseCron(tc.expr)
		if err != nil {
			t.Fatalf("ParseCron(%q): %v", tc.expr, err)
		}
		if got := c.Matches(tc.at); got != tc.want {
			t.Errorf("Matches(%q @ %v) = %v, want %v", tc.expr, tc.at, got, tc.want)
		}
	}
}

func TestParseCronInvalid(t *testing.T) {
	for _, expr := range []string{"", "* * * *", "60 * * * *", "* 24 * * *", "abc * * * *"} {
		if _, err := ParseCron(expr); err == nil {
			t.Errorf("ParseCron(%q) unexpectedly succeeded", expr)
		}
	}
}
