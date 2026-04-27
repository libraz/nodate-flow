package handlerutil

import (
	"fmt"
	"testing"
)

func TestBindDefaults(t *testing.T) {
	t.Parallel()
	got := Bind(0, 0, DefaultListLimit, MaxListLimit)
	if got.Limit != DefaultListLimit {
		t.Errorf("limit=0 should default: got %d want %d", got.Limit, DefaultListLimit)
	}
	if got.Offset != 0 {
		t.Errorf("offset=0 should stay 0: got %d", got.Offset)
	}
}

func TestBindClampsOverMax(t *testing.T) {
	t.Parallel()
	got := Bind(MaxListLimit+100, 0, DefaultListLimit, MaxListLimit)
	if got.Limit != MaxListLimit {
		t.Errorf("limit > max should clamp: got %d want %d", got.Limit, MaxListLimit)
	}
}

func TestBindNegativeLimit(t *testing.T) {
	t.Parallel()
	got := Bind(-5, 0, DefaultListLimit, MaxListLimit)
	if got.Limit != DefaultListLimit {
		t.Errorf("negative limit should default: got %d want %d", got.Limit, DefaultListLimit)
	}
}

func TestBindNegativeOffset(t *testing.T) {
	t.Parallel()
	got := Bind(20, -7, DefaultListLimit, MaxListLimit)
	if got.Offset != 0 {
		t.Errorf("negative offset should clamp to 0: got %d", got.Offset)
	}
	if got.Limit != 20 {
		t.Errorf("limit should be preserved: got %d want 20", got.Limit)
	}
}

func TestBindCustomMax(t *testing.T) {
	t.Parallel()
	// Endpoint that needs a higher cap (e.g. cross-workspace dashboard).
	got := Bind(900, 0, 200, 1000)
	if got.Limit != 900 {
		t.Errorf("under custom max: got %d want 900", got.Limit)
	}
	got2 := Bind(2000, 0, 200, 1000)
	if got2.Limit != 1000 {
		t.Errorf("over custom max: got %d want 1000", got2.Limit)
	}
}

func TestBindZeroMaxIsUnbounded(t *testing.T) {
	t.Parallel()
	// A maxLimit of 0 disables the upper clamp so callers can opt
	// into "trust the tag" behaviour. The default still kicks in for
	// limit <= 0.
	got := Bind(10000, 0, 50, 0)
	if got.Limit != 10000 {
		t.Errorf("max=0 should not clamp: got %d want 10000", got.Limit)
	}
}

func TestBindWithinRange(t *testing.T) {
	t.Parallel()
	got := Bind(75, 25, DefaultListLimit, MaxListLimit)
	if got.Limit != 75 || got.Offset != 25 {
		t.Errorf("in-range: got (%d,%d) want (75,25)", got.Limit, got.Offset)
	}
}

// TestStandardLimitTagMatchesConstants guards against drift between the
// canonical pagination constants and the struct-tag string copied onto
// list endpoint Limit fields. If a future change updates one without
// the other, list endpoints would silently disagree with the helper.
func TestStandardLimitTagMatchesConstants(t *testing.T) {
	t.Parallel()
	want := fmt.Sprintf(`query:"limit" minimum:"1" maximum:"%d" default:"%d"`, MaxListLimit, DefaultListLimit)
	if StandardLimitTag != want {
		t.Errorf("StandardLimitTag drifted from constants:\n got  %q\n want %q", StandardLimitTag, want)
	}
	if StandardOffsetTag != `query:"offset" minimum:"0" default:"0"` {
		t.Errorf("StandardOffsetTag unexpected: %q", StandardOffsetTag)
	}
}
