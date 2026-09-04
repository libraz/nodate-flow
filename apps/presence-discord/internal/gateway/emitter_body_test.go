package gateway

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestTruncatedBodyStaysValidUTF8 sweeps an over-long error envelope so
// a multi-byte character lands on the 512-byte clip at every offset.
//
// A flow-api error envelope quotes the values it rejected, and those are
// workspace content rather than ASCII, so a byte-indexed clip ends the
// log line with U+FFFD.
func TestTruncatedBodyStaysValidUTF8(t *testing.T) {
	t.Parallel()

	for pad := 0; pad < 6; pad++ {
		body := strings.Repeat("x", pad) + strings.Repeat("設計方針を確認", 200)
		got := truncatedBody(strings.NewReader(body))

		if !strings.HasSuffix(got, "...(truncated)") {
			t.Fatalf("pad=%d: %q was not marked truncated", pad, got)
		}
		clipped := strings.TrimSuffix(got, "...(truncated)")
		if !utf8.ValidString(clipped) {
			t.Fatalf("pad=%d: clipped body %q is not valid UTF-8", pad, clipped)
		}
		if strings.ContainsRune(clipped, utf8.RuneError) {
			t.Fatalf("pad=%d: clipped body %q carries U+FFFD", pad, clipped)
		}
		if len(clipped) > 512 {
			t.Fatalf("pad=%d: clipped body is %d bytes, over the 512 cap", pad, len(clipped))
		}
		if len(clipped) < 508 {
			t.Fatalf("pad=%d: clipped body is only %d bytes, far short of the 512 cap", pad, len(clipped))
		}
	}
}

// TestTruncatedBodyPassesShortBodiesThrough holds the no-op path: a body
// inside the cap is returned whole and unmarked.
func TestTruncatedBodyPassesShortBodiesThrough(t *testing.T) {
	t.Parallel()

	body := `{"code":"WS_TASK_NOT_FOUND","detail":"設計方針"}`
	if got := truncatedBody(strings.NewReader(body)); got != body {
		t.Fatalf("truncatedBody = %q, want %q", got, body)
	}
}
