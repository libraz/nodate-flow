package calendars

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFoldLine_ASCIIShort(t *testing.T) {
	// ASCII text that fits in one line should not be folded.
	line := "SUMMARY:Short title"
	result := foldLine(line)
	assert.Equal(t, "SUMMARY:Short title\r\n", result)
}

func TestFoldLine_ASCIILong(t *testing.T) {
	// ASCII text longer than 75 octets should be folded.
	line := strings.Repeat("A", 200)
	result := foldLine(line)

	lines := strings.Split(result, "\r\n")
	// Last element after split is empty string after trailing \r\n
	require.True(t, len(lines) >= 2, "should produce multiple lines")

	for i, l := range lines {
		if l == "" {
			continue
		}
		assert.LessOrEqual(t, len(l), 75, "line %d exceeds 75 octets: %d", i, len(l))
		if i > 0 {
			assert.True(t, strings.HasPrefix(l, " "), "continuation line %d must start with space", i)
		}
	}

	// Reassemble and verify content is preserved
	reassembled := strings.ReplaceAll(result, "\r\n ", "")
	reassembled = strings.TrimSuffix(reassembled, "\r\n")
	assert.Equal(t, line, reassembled)
}

func TestFoldLine_UTF8MultiByte(t *testing.T) {
	// Japanese text that needs folding. Each kanji/kana is 3 bytes in UTF-8.
	// 25 kanji = 75 bytes exactly for the first line, then more follows.
	line := "SUMMARY:" + strings.Repeat("あ", 30) // 8 + 90 = 98 bytes
	result := foldLine(line)

	require.True(t, utf8.ValidString(result), "folded output must be valid UTF-8")

	lines := strings.Split(result, "\r\n")
	for i, l := range lines {
		if l == "" {
			continue
		}
		assert.LessOrEqual(t, len(l), 75, "line %d exceeds 75 octets: %d", i, len(l))
		if i > 0 {
			assert.True(t, strings.HasPrefix(l, " "), "continuation line %d must start with space", i)
		}
	}

	// Reassemble and verify content is preserved
	reassembled := strings.ReplaceAll(result, "\r\n ", "")
	reassembled = strings.TrimSuffix(reassembled, "\r\n")
	assert.Equal(t, line, reassembled)
}

func TestFoldLine_UTF8NoCutMidRune(t *testing.T) {
	// Create a line where a naive byte-slice at 75 would cut a multi-byte char.
	// "SUMMARY:" is 8 bytes. Fill with 'a' to 73 bytes (65 a's), then a 3-byte char.
	// Total = 8 + 65 + 3 = 76 bytes. Naive cut at 75 would split the 3-byte char.
	line := "SUMMARY:" + strings.Repeat("a", 65) + "あ" + "tail"
	result := foldLine(line)

	require.True(t, utf8.ValidString(result), "must not produce invalid UTF-8")

	lines := strings.Split(result, "\r\n")
	for i, l := range lines {
		if l == "" {
			continue
		}
		assert.LessOrEqual(t, len(l), 75, "line %d exceeds 75 octets", i)
	}

	// Verify round-trip
	reassembled := strings.ReplaceAll(result, "\r\n ", "")
	reassembled = strings.TrimSuffix(reassembled, "\r\n")
	assert.Equal(t, line, reassembled)
}

func TestFoldLine_ContinuationStartsWithSpace(t *testing.T) {
	line := strings.Repeat("X", 200)
	result := foldLine(line)
	lines := strings.Split(result, "\r\n")
	for i, l := range lines {
		if l == "" || i == 0 {
			continue
		}
		assert.True(t, strings.HasPrefix(l, " "), "continuation line %d must start with space", i)
	}
}
