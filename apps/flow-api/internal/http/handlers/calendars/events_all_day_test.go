package calendars

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
)

func problemCode(t *testing.T, err error) string {
	t.Helper()
	var problem *handlerutil.ProblemDetails
	if !errors.As(err, &problem) {
		t.Fatalf("want a problem, got %v", err)
	}
	return problem.Type
}

func strp(s string) *string { return &s }

func TestResolveAllDayDates(t *testing.T) {
	t.Parallel()

	day := func(s string) int64 {
		d, err := time.Parse("2006-01-02", s)
		if err != nil {
			t.Fatal(err)
		}
		return d.Unix()
	}

	t.Run("a one-day event ends the day after it starts", func(t *testing.T) {
		start, end, err := resolveAllDayDates("event", strp("2027-08-05"), strp("2027-08-06"), sql.NullTime{}, sql.NullTime{})
		if err != nil {
			t.Fatal(err)
		}
		if *start != day("2027-08-05") || *end != day("2027-08-06") {
			t.Errorf("got %d..%d", *start, *end)
		}
	})

	t.Run("a milestone is a point", func(t *testing.T) {
		start, end, err := resolveAllDayDates("milestone", strp("2027-08-05"), strp("2027-08-05"), sql.NullTime{}, sql.NullTime{})
		if err != nil {
			t.Fatal(err)
		}
		if *start != *end {
			t.Errorf("a milestone must end where it starts, got %d..%d", *start, *end)
		}
	})

	t.Run("an end that is not after the start is refused", func(t *testing.T) {
		for name, end := range map[string]string{"empty": "2027-08-05", "reversed": "2027-08-03"} {
			_, _, err := resolveAllDayDates("event", strp("2027-08-05"), strp(end), sql.NullTime{}, sql.NullTime{})
			if got := problemCode(t, err); got != "CALENDAR.EVENT.DATE_RANGE_INVALID" {
				t.Errorf("%s: got %s", name, got)
			}
		}
	})

	t.Run("a point is refused for any kind but a milestone", func(t *testing.T) {
		_, _, err := resolveAllDayDates("milestone", strp("2027-08-05"), strp("2027-08-06"), sql.NullTime{}, sql.NullTime{})
		if got := problemCode(t, err); got != "CALENDAR.EVENT.DATE_RANGE_INVALID" {
			t.Errorf("got %s", got)
		}
	})

	t.Run("a date that does not exist or lies outside the range is refused", func(t *testing.T) {
		for _, bad := range []string{"2027-02-30", "0999-12-31"} {
			_, _, err := resolveAllDayDates("event", strp(bad), strp("2027-08-06"), sql.NullTime{}, sql.NullTime{})
			if got := problemCode(t, err); got != "VALIDATION.BODY.FIELD_INVALID" {
				t.Errorf("%s: got %s", bad, got)
			}
		}
	})

	t.Run("a missing end comes from the stored row", func(t *testing.T) {
		stored := sql.NullTime{Time: time.Date(2027, 8, 6, 0, 0, 0, 0, time.UTC), Valid: true}
		start, end, err := resolveAllDayDates("event", strp("2027-08-04"), nil, sql.NullTime{}, stored)
		if err != nil {
			t.Fatal(err)
		}
		if *start != day("2027-08-04") || *end != day("2027-08-06") {
			t.Errorf("got %d..%d", *start, *end)
		}
	})

	t.Run("a missing end with nothing stored is refused", func(t *testing.T) {
		_, _, err := resolveAllDayDates("event", strp("2027-08-04"), nil, sql.NullTime{}, sql.NullTime{})
		if got := problemCode(t, err); got != "VALIDATION.BODY.FIELD_INVALID" {
			t.Errorf("got %s", got)
		}
	})
}

func TestRequireNotificationOffset(t *testing.T) {
	t.Parallel()

	in := func(v int32) *int32 { return &v }
	for _, ok := range []*int32{nil, in(0), in(10080)} {
		if err := requireNotificationOffset(ok); err != nil {
			t.Errorf("%v must pass: %v", ok, err)
		}
	}
	for _, bad := range []int32{-1, 10081} {
		err := requireNotificationOffset(in(bad))
		if got := problemCode(t, err); got != "CALENDAR.EVENT.NOTIFICATION_OFFSET_OUT_OF_RANGE" {
			t.Errorf("%d: got %s", bad, got)
		}
	}
}
