package region

import (
	"strings"
	"testing"
	"time"
)

func TestValidateWorkingDays(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		in      string
		wantErr bool
	}{
		{"", false},
		{"MTWTF__", false},
		{"_______", false},
		{"MTWTFSS", false},
		{"ABCDEFG", false},
		{"MTWTF", true},
		{"MTWTF___", true},
		// Seven characters, seventeen bytes. The columns are CHAR(7)
		// latin1, so a multi-byte label has nowhere to go; the check
		// exists so the caller is told that rather than being told the
		// string is the wrong length.
		{"月火水木金__", true},
		{"MTWTF_\n", true},
	} {
		err := ValidateWorkingDays(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("ValidateWorkingDays(%q): wantErr=%v, got=%v", c.in, c.wantErr, err)
		}
	}
}

// TestValidateWorkingDaysExplainsNonASCII pins the message, because the
// whole point of the separate check is which failure the caller is told
// about: reporting a kanji label as a length problem sends them looking
// for a missing character.
func TestValidateWorkingDaysExplainsNonASCII(t *testing.T) {
	t.Parallel()
	err := ValidateWorkingDays("月火水木金__")
	if err == nil {
		t.Fatal("ValidateWorkingDays: want an error for a multi-byte label")
	}
	if !strings.Contains(err.Error(), "ASCII") {
		t.Errorf("ValidateWorkingDays: want the error to name the ASCII restriction, got %q", err)
	}
}

func TestEffectiveWorkingDays(t *testing.T) {
	t.Parallel()
	if got := EffectiveWorkingDays("", ""); got != WorkingDaysDefault {
		t.Errorf("both empty: want %q, got %q", WorkingDaysDefault, got)
	}
	if got := EffectiveWorkingDays("", "MTWTFSS"); got != "MTWTFSS" {
		t.Errorf("ws only: want %q, got %q", "MTWTFSS", got)
	}
	if got := EffectiveWorkingDays("_______", "MTWTFSS"); got != "_______" {
		t.Errorf("user override: want all-off, got %q", got)
	}
}

func TestIsWorkingDay(t *testing.T) {
	t.Parallel()
	// 2026-04-20 is a Monday; 2026-04-25 is a Saturday.
	monday := time.Date(2026, 4, 20, 9, 0, 0, 0, time.UTC)
	saturday := time.Date(2026, 4, 25, 9, 0, 0, 0, time.UTC)

	if !IsWorkingDay("MTWTF__", monday, time.UTC, nil, false) {
		t.Error("Mon should be working under MTWTF__")
	}
	if IsWorkingDay("MTWTF__", saturday, time.UTC, nil, false) {
		t.Error("Sat should be off under MTWTF__")
	}
	if !IsWorkingDay("MTWTFSS", saturday, time.UTC, nil, false) {
		t.Error("Sat should be working under MTWTFSS")
	}

	holidays := map[string]struct{}{"2026-04-20": {}}
	if IsWorkingDay("MTWTF__", monday, time.UTC, holidays, true) {
		t.Error("Mon should be off when it's a treated holiday")
	}
	if !IsWorkingDay("MTWTF__", monday, time.UTC, holidays, false) {
		t.Error("treatHolidays=false should ignore the holiday set")
	}
}

func TestNextWorkingDay(t *testing.T) {
	t.Parallel()
	// Start on Saturday; expect Monday (with MTWTF__).
	saturday := time.Date(2026, 4, 25, 14, 30, 0, 0, time.UTC)
	next := NextWorkingDay("MTWTF__", saturday, time.UTC, nil, false)
	want := time.Date(2026, 4, 27, 14, 30, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("NextWorkingDay: want %v, got %v", want, next)
	}
	// All-off string must not loop forever.
	stuck := NextWorkingDay("_______", saturday, time.UTC, nil, false)
	if !stuck.Equal(saturday) {
		t.Errorf("NextWorkingDay with all-off: expected original %v, got %v", saturday, stuck)
	}
}

func TestEffectiveWorkingHours(t *testing.T) {
	t.Parallel()
	start, end := EffectiveWorkingHours(nil, nil, nil, nil)
	if start.Format("15:04") != "09:00" || end.Format("15:04") != "18:00" {
		t.Errorf("all-nil: want 09:00/18:00, got %s/%s",
			start.Format("15:04"), end.Format("15:04"))
	}
	us := mustParseClock("10:00")
	ue := mustParseClock("19:00")
	start, end = EffectiveWorkingHours(&us, &ue, nil, nil)
	if start.Format("15:04") != "10:00" || end.Format("15:04") != "19:00" {
		t.Errorf("user override: want 10:00/19:00, got %s/%s",
			start.Format("15:04"), end.Format("15:04"))
	}
}

func TestParseSnapMode(t *testing.T) {
	t.Parallel()
	cases := map[string]SnapMode{
		"off":  SnapOff,
		"warn": SnapWarn,
		"auto": SnapAuto,
		"":     SnapWarn,
		"AUTO": SnapAuto,
	}
	for in, want := range cases {
		if got := ParseSnapMode(in); got != want {
			t.Errorf("ParseSnapMode(%q): want %q, got %q", in, want, got)
		}
	}
}
