package safety

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestScanFields_SnippetStaysValidUTF8 sweeps the context around a match
// so the fixed 20-byte window opens and closes at every position within
// a multi-byte character.
//
// Task titles and descriptions are free-form workspace content. A window
// taken on raw byte offsets slices the character at its edge, and the
// reviewer deciding whether a lens is safe to publish sees U+FFFD
// instead of the context that would tell them.
func TestScanFields_SnippetStaysValidUTF8(t *testing.T) {
	t.Parallel()

	const secret = "alice@example.com"
	for pad := 0; pad < 6; pad++ {
		prose := strings.Repeat("設計方針を確認", 4)
		text := prose + strings.Repeat("x", pad) + secret + strings.Repeat("x", pad) + prose

		findings := scanFields("task-1", text, "")
		if len(findings) == 0 {
			t.Fatalf("pad=%d: no finding for %q", pad, text)
		}
		got := findings[0].Snippet
		if !utf8.ValidString(got) {
			t.Fatalf("pad=%d: snippet %q is not valid UTF-8", pad, got)
		}
		if strings.ContainsRune(got, utf8.RuneError) {
			t.Fatalf("pad=%d: snippet %q carries U+FFFD", pad, got)
		}
		if strings.Contains(got, secret) {
			t.Fatalf("pad=%d: snippet %q leaks the match unredacted", pad, got)
		}
		if !strings.Contains(got, "**") {
			t.Fatalf("pad=%d: snippet %q dropped the redacted match", pad, got)
		}
	}
}

// TestRedactSnippet_KeepsSurroundingContext holds that snapping the
// window to rune boundaries still returns context on both sides. A
// version that emptied the window would satisfy the UTF-8 property and
// tell the reviewer nothing.
func TestRedactSnippet_KeepsSurroundingContext(t *testing.T) {
	t.Parallel()

	text := "連絡先は alice@example.com です、確認してください"
	loc := heuristics[0].re.FindStringIndex(text)
	if loc == nil {
		t.Fatal("the email heuristic did not match the fixture")
	}

	got := redactSnippet(text, loc)
	if !strings.Contains(got, "連絡先は") {
		t.Fatalf("snippet %q lost the text before the match", got)
	}
	if !strings.Contains(got, "です") {
		t.Fatalf("snippet %q lost the text after the match", got)
	}
}
