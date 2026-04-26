package handlerutil

import (
	"testing"
	"time"
)

func TestNowUnixMonotonic(t *testing.T) {
	t.Parallel()
	a := NowUnix()
	b := NowUnix()
	if b < a {
		t.Errorf("NowUnix should not go backwards: a=%d b=%d", a, b)
	}
	now := time.Now().UTC().Unix()
	if a > now+5 || a < now-5 {
		t.Errorf("NowUnix far from time.Now: a=%d wallclock=%d", a, now)
	}
}

func TestUnixToTimeUTC(t *testing.T) {
	t.Parallel()
	got := UnixToTime(1700000000)
	if got.Location() != time.UTC {
		t.Errorf("expected UTC, got %v", got.Location())
	}
	if got.Unix() != 1700000000 {
		t.Errorf("round-trip failed: got %d", got.Unix())
	}
}

func TestUnixToTimeZero(t *testing.T) {
	t.Parallel()
	got := UnixToTime(0)
	// Unix(0,0) is the epoch, NOT the Go zero value — verify both
	// claims so callers know what they get.
	if got.IsZero() {
		t.Error("Unix(0,0) is the epoch, not Go zero value")
	}
	if got.Unix() != 0 {
		t.Errorf("expected 0, got %d", got.Unix())
	}
}

func TestTimeToUnixZero(t *testing.T) {
	t.Parallel()
	if got := TimeToUnix(time.Time{}); got != 0 {
		t.Errorf("zero value should return 0, got %d", got)
	}
}

func TestTimeToUnixNonUTC(t *testing.T) {
	t.Parallel()
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("tz data unavailable")
	}
	tt := time.Date(2024, 6, 1, 12, 0, 0, 0, loc)
	got := TimeToUnix(tt)
	want := tt.UTC().Unix()
	if got != want {
		t.Errorf("non-UTC normalisation: got %d want %d", got, want)
	}
}

func TestTimeToUnixRoundTrip(t *testing.T) {
	t.Parallel()
	stamp := int64(1735689600) // 2025-01-01 00:00:00 UTC
	if got := TimeToUnix(UnixToTime(stamp)); got != stamp {
		t.Errorf("round-trip: got %d want %d", got, stamp)
	}
}
