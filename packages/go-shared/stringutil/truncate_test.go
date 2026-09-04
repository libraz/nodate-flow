package stringutil

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Three bytes per rune, so a byte-indexed cut lands mid-rune at two out of
// every three limits.
const rune3 = "設計方針を確認して次の手順に進みます"

// TestTruncateBytesCutsOnRuneBoundaries sweeps every limit across a
// multi-byte string and holds the four properties a caller relies on: the
// result is valid UTF-8, carries no replacement character, fits the limit,
// and is a prefix of the input.
//
// The limits that matter are the ones a byte cut mangles, and a sweep finds
// them without anyone working out which they are.
func TestTruncateBytesCutsOnRuneBoundaries(t *testing.T) {
	t.Parallel()

	for n := 0; n <= len(rune3)+2; n++ {
		got := TruncateBytes(rune3, n)
		if !utf8.ValidString(got) {
			t.Fatalf("TruncateBytes(%q, %d) = %q, which is not valid UTF-8", rune3, n, got)
		}
		if strings.ContainsRune(got, utf8.RuneError) {
			t.Fatalf("TruncateBytes(%q, %d) = %q, which carries U+FFFD", rune3, n, got)
		}
		if len(got) > n {
			t.Fatalf("TruncateBytes(%q, %d) = %q (%d bytes), over the limit", rune3, n, got, len(got))
		}
		if !strings.HasPrefix(rune3, got) {
			t.Fatalf("TruncateBytes(%q, %d) = %q, which is not a prefix of the input", rune3, n, got)
		}
	}
}

// TestTruncateBytesCutsAsLateAsItCan pins the cut at the last rune boundary
// at or below the limit. A helper that answered "" for every over-long
// input would satisfy the properties above and store nothing.
func TestTruncateBytesCutsAsLateAsItCan(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		in   string
		max  int
		want string
	}{
		{name: "limit on the boundary", in: rune3, max: 6, want: "設計"},
		{name: "one byte past the boundary", in: rune3, max: 7, want: "設計"},
		{name: "one byte short of the next", in: rune3, max: 8, want: "設計"},
		{name: "next boundary", in: rune3, max: 9, want: "設計方"},
		{name: "shorter than one rune", in: rune3, max: 2, want: ""},
		{name: "ascii cuts exactly", in: "abcdef", max: 4, want: "abcd"},
		{name: "mixed width", in: "ab設計", max: 5, want: "ab設"},
		{name: "zero limit", in: rune3, max: 0, want: ""},
		{name: "negative limit", in: rune3, max: -1, want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := TruncateBytes(tc.in, tc.max); got != tc.want {
				t.Fatalf("TruncateBytes(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
			}
		})
	}
}

// TestTruncateBytesLeavesShortStringsAlone holds the no-op path: a value
// inside the limit is returned as it arrived, including the empty string.
func TestTruncateBytesLeavesShortStringsAlone(t *testing.T) {
	t.Parallel()

	for _, s := range []string{"", "ok", "設計", strings.Repeat("a", 500), rune3} {
		if got := TruncateBytes(s, 500); got != s {
			t.Fatalf("TruncateBytes(%q, 500) = %q, want it unchanged", s, got)
		}
	}
}

// TestTruncateRunesCountsCharacters holds the character-limit variant,
// including that the byte length of the result is free to exceed the limit.
func TestTruncateRunesCountsCharacters(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		in   string
		max  int
		want string
	}{
		{name: "cuts at the rune count", in: rune3, max: 3, want: "設計方"},
		{name: "exactly the rune count", in: "設計方", max: 3, want: "設計方"},
		{name: "under the rune count", in: "設計", max: 3, want: "設計"},
		{name: "ascii", in: "abcdef", max: 3, want: "abc"},
		{name: "mixed width", in: "ab設計", max: 3, want: "ab設"},
		{name: "empty input", in: "", max: 3, want: ""},
		{name: "zero limit", in: rune3, max: 0, want: ""},
		{name: "negative limit", in: rune3, max: -1, want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := TruncateRunes(tc.in, tc.max)
			if got != tc.want {
				t.Fatalf("TruncateRunes(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
			}
			if utf8.RuneCountInString(got) > tc.max && tc.max > 0 {
				t.Fatalf("TruncateRunes(%q, %d) = %q, over the rune limit", tc.in, tc.max, got)
			}
		})
	}
}
