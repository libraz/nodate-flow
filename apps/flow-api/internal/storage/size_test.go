package storage

import (
	"testing"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
)

func TestExceedsUploadLimit(t *testing.T) {
	t.Parallel()
	const max = handlerutil.MaxUploadSize

	cases := []struct {
		name   string
		actual uint64
		want   bool
	}{
		// A client declaring 1 byte but streaming a 200 MB body: the
		// actual stored size is what must be enforced, and it is over
		// the cap, so confirm rejects it.
		{"declared-1-byte-actually-200mb", 200 * 1024 * 1024, true},
		{"one-byte-over", max + 1, true},
		{"exactly-at-limit", max, false},
		{"well-under-limit", 5 * 1024 * 1024, false},
		{"empty", 0, false},
	}
	for _, tc := range cases {
		if got := ExceedsUploadLimit(tc.actual, max); got != tc.want {
			t.Errorf("%s: ExceedsUploadLimit(%d, %d) = %v, want %v", tc.name, tc.actual, max, got, tc.want)
		}
	}
}
