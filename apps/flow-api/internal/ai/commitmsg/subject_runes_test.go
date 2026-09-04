package commitmsg

import (
	"strings"
	"testing"
	"unicode/utf8"
)

var subjectChanges = []Change{{Path: "apps/flow-api/internal/ai/x.go", Status: StatusModified}}

// TestSubjectLowercasesTheFirstCharacter holds that the imperative
// lowercasing acts on the first character rather than the first byte.
//
// A summary starting with a multi-byte character has no first byte to
// lowercase: taking one splits the character, and the subject reaches
// the proposal with U+FFFD where the summary began.
func TestSubjectLowercasesTheFirstCharacter(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		summary string
		want    string
	}{
		{name: "ascii still lowercases", summary: "Add login form", want: "add login form"},
		{name: "already lowercase", summary: "add login form", want: "add login form"},
		{name: "accented capital", summary: "Ändere die Anmeldung", want: "ändere die Anmeldung"},
		{name: "caseless script is left alone", summary: "設計方針を更新", want: "設計方針を更新"},
		{name: "emoji is left alone", summary: "🚀 ship it", want: "🚀 ship it"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Propose(subjectChanges, tc.summary).Subject
			if got != tc.want {
				t.Fatalf("subject for %q = %q, want %q", tc.summary, got, tc.want)
			}
			if strings.ContainsRune(got, utf8.RuneError) {
				t.Fatalf("subject for %q = %q, which carries U+FFFD", tc.summary, got)
			}
		})
	}
}

// TestSubjectClipStaysValidUTF8 sweeps a summary across the subject
// width so a multi-byte character lands on the cut at every offset.
func TestSubjectClipStaysValidUTF8(t *testing.T) {
	t.Parallel()

	for pad := 0; pad < 6; pad++ {
		summary := strings.Repeat("x", pad) + strings.Repeat("設計方針を更新", 20)
		got := Propose(subjectChanges, summary).Subject

		if !utf8.ValidString(got) {
			t.Fatalf("pad=%d: subject %q is not valid UTF-8", pad, got)
		}
		if strings.ContainsRune(got, utf8.RuneError) {
			t.Fatalf("pad=%d: subject %q carries U+FFFD", pad, got)
		}
		if len(got) > subjectMaxLen {
			t.Fatalf("pad=%d: subject is %d bytes, over the %d cap", pad, len(got), subjectMaxLen)
		}
		if len(got) < subjectMaxLen-4 {
			t.Fatalf("pad=%d: subject clipped to %d bytes, far short of the %d cap", pad, len(got), subjectMaxLen)
		}
	}
}
