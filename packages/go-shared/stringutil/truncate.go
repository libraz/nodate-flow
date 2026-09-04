package stringutil

import "unicode/utf8"

// TruncateBytes returns s clipped to at most maxBytes bytes, cutting at a
// rune boundary so a valid UTF-8 input yields a valid UTF-8 result.
//
// Every caller that caps a string against a storage limit has to cut here.
// A cut on a raw byte index severs the last multi-byte rune of the value:
// the surviving fragment is not valid UTF-8, so a utf8mb4 column rejects it
// under STRICT_TRANS_TABLES and a reader that tolerates it renders U+FFFD.
// Both outcomes are silent at the call site — the caller asked for a
// shorter string and got one.
//
// The cut is never longer than maxBytes, so the result always fits a limit
// expressed in bytes. A limit expressed in characters (a MySQL VARCHAR(n)
// counts characters, not bytes) is satisfied as well, because a string of
// at most n bytes holds at most n characters.
//
// A non-positive maxBytes returns the empty string.
func TruncateBytes(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// TruncateRunes returns s clipped to at most maxRunes runes.
//
// Use this when the limit counts characters rather than storage — a title
// shown in a fixed-width slot, say. When the limit is a column width, use
// [TruncateBytes]: n runes can be up to 4n bytes.
//
// A non-positive maxRunes returns the empty string.
func TruncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	count := 0
	for i := range s {
		if count == maxRunes {
			return s[:i]
		}
		count++
	}
	return s
}
