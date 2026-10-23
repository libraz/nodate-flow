package dbtype_test

import (
	"testing"
	"time"

	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
)

func TestDatetimeRangeIsTheColumns(t *testing.T) {
	t.Parallel()
	if got := time.Unix(dbtype.MaxDatetimeUnix, 0).UTC(); !got.Equal(time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)) {
		t.Errorf("MaxDatetimeUnix = %s", got)
	}
	if got := time.Unix(dbtype.MinDatetimeUnix, 0).UTC(); !got.Equal(time.Date(1000, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("MinDatetimeUnix = %s", got)
	}
	for _, c := range []struct {
		s    int64
		want bool
	}{
		{dbtype.MaxDatetimeUnix, true},
		{dbtype.MaxDatetimeUnix + 1, false},
		{dbtype.MinDatetimeUnix, true},
		{dbtype.MinDatetimeUnix - 1, false},
		{0, true},
	} {
		if got := dbtype.DatetimeRepresentable(c.s); got != c.want {
			t.Errorf("DatetimeRepresentable(%d) = %v, want %v", c.s, got, c.want)
		}
	}
}
