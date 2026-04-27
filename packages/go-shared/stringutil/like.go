// Package stringutil provides string manipulation helpers shared across the
// backend services. The package is intentionally small; add new helpers only
// when the same logic would otherwise be duplicated in two or more callers.
package stringutil

import "strings"

// EscapeLike escapes the MySQL LIKE metacharacters in a user-supplied search
// term so they are matched literally inside a LIKE pattern.
//
// Backslash is doubled first so that the subsequently inserted `\%` and `\_`
// escape sequences are not themselves re-escaped. The result is intended to
// be wrapped between '%' wildcards by the caller, e.g.
//
//	pattern := "%" + stringutil.EscapeLike(userInput) + "%"
//
// This helper does not perform case-folding; callers that need a
// case-insensitive match should lowercase the input first (or rely on a
// case-insensitive collation).
func EscapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}
