package agentruntime

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestTruncateCutsOnRuneBoundaries proves the agent_memo summary is cut
// where a rune ends.
//
// The one caller writes tasks.agent_memo.last_thought, so a mid-rune cut is
// not a display glitch that a reload clears: the trailing bytes are dropped
// on the way into the column and the replacement character is what the
// inbox shows from then on. Every non-ASCII summary long enough to be
// truncated hit this.
func TestTruncateCutsOnRuneBoundaries(t *testing.T) {
	t.Parallel()

	// Three bytes per rune, so a byte-indexed cut lands mid-rune for two
	// out of every three limits.
	const rune3 = "設計方針を確認して次の手順に進みます"

	for n := 0; n <= len(rune3)+2; n++ {
		got := truncate(rune3, n)
		if !utf8.ValidString(got) {
			t.Fatalf("truncate(%q, %d) = %q, which is not valid UTF-8", rune3, n, got)
		}
		if strings.ContainsRune(got, utf8.RuneError) {
			t.Fatalf("truncate(%q, %d) = %q, which carries U+FFFD", rune3, n, got)
		}
		if len(got) > n {
			t.Fatalf("truncate(%q, %d) = %q (%d bytes), over the limit", rune3, n, got, len(got))
		}
		if !strings.HasPrefix(rune3, got) {
			t.Fatalf("truncate(%q, %d) = %q, which is not a prefix of the input", rune3, n, got)
		}
	}
}

func TestTruncateLeavesShortStringsAlone(t *testing.T) {
	t.Parallel()

	for _, s := range []string{"", "ok", "設計", strings.Repeat("a", 500)} {
		if got := truncate(s, 500); got != s {
			t.Fatalf("truncate(%q, 500) = %q, want it unchanged", s, got)
		}
	}
}
