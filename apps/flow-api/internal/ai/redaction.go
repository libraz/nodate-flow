// Package ai contains the LLM provider abstraction's user-facing helpers
// (redaction, cost guard, task orchestration). The concrete provider
// implementations and the only allowed callers of internal/crypto live in
// the sub-package internal/ai/providers.
package ai

import (
	"strings"
	"sync"
	"unicode"
)

// SecretPrefixes is the fixed list of literal prefixes scanned for in
// strings passed to Redact. The project rule forbids regex, so this list is
// the single source of truth for prefix-based secret detection.
//
// Add new prefixes via RegisterPrefix at process startup. Order does not
// matter; the scanner walks every prefix per input.
var SecretPrefixes = []string{
	"sk-ant-",
	"sk-",
	"mcp_",
	"pat_",
	"rfr_",
	"ghp_",
	"gho_",
	"github_pat_",
	"xoxb-",
	"xoxp-",
	"AIza",
}

// redactedSensitiveJSONKeys lists the JSON object field names whose values
// are always redacted, regardless of content.
var redactedSensitiveJSONKeys = map[string]struct{}{
	"api_key":       {},
	"apikey":        {},
	"authorization": {},
	"token":         {},
	"password":      {},
	"secret":        {},
}

var (
	prefixesMu sync.RWMutex
	prefixes   = append([]string(nil), SecretPrefixes...)
)

// RegisterPrefix adds a literal secret prefix to the redaction scanner.
// Safe to call from init functions; it is a no-op if the prefix is already
// registered or shorter than 4 characters (too short to be specific).
func RegisterPrefix(p string) {
	if len(p) < 4 {
		return
	}
	prefixesMu.Lock()
	defer prefixesMu.Unlock()
	for _, existing := range prefixes {
		if existing == p {
			return
		}
	}
	prefixes = append(prefixes, p)
}

// Redact scans s for any registered secret prefix and replaces every match
// with "[REDACTED:<prefix>]". The scanner is greedy on the secret token: a
// match runs until the first non-token character (whitespace, quote, comma,
// brace, bracket, semicolon, or newline).
func Redact(s string) string {
	if s == "" {
		return s
	}
	prefixesMu.RLock()
	local := prefixes
	prefixesMu.RUnlock()

	// Single-pass scan choosing the longest matching prefix at each
	// position. This avoids nested replacement artifacts (e.g. "sk-ant-"
	// being re-matched as "sk-" after an earlier pass).
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		best := ""
		for _, p := range local {
			if len(p) > len(best) && strings.HasPrefix(s[i:], p) {
				best = p
			}
		}
		if best == "" {
			b.WriteByte(s[i])
			i++
			continue
		}
		j := i + len(best)
		for j < len(s) && isTokenByte(s[j]) {
			j++
		}
		b.WriteString("[REDACTED:")
		b.WriteString(best)
		b.WriteString("]")
		i = j
	}
	return b.String()
}

// isTokenByte reports whether b is part of a contiguous secret token.
// Tokens are typically base62 with - / _; we allow any printable ASCII that
// is not whitespace, a structural JSON byte, or a quote.
func isTokenByte(b byte) bool {
	if b > unicode.MaxASCII {
		return false
	}
	switch b {
	case ' ', '\t', '\n', '\r', '"', '\'', ',', '}', '{', '[', ']', ';', ')', '(':
		return false
	}
	return b > 0x20
}

// RedactJSONFields walks raw JSON-ish text and replaces the value of any
// object field whose key matches redactedSensitiveJSONKeys. The scan is a
// hand-rolled string walk (no regex, no full json.Decode) so it tolerates
// non-strict JSON such as logs that interleave structured fragments.
//
// The output is also passed through Redact so prefix matches in non-target
// fields are still scrubbed.
func RedactJSONFields(s string) string {
	if s == "" {
		return s
	}
	out := redactJSONOnce(s)
	return Redact(out)
}

func redactJSONOnce(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		// Find next "key":
		q := strings.IndexByte(s[i:], '"')
		if q < 0 {
			b.WriteString(s[i:])
			break
		}
		b.WriteString(s[i : i+q])
		start := i + q + 1
		// Read key until next unescaped quote.
		end := start
		for end < len(s) {
			if s[end] == '\\' && end+1 < len(s) {
				end += 2
				continue
			}
			if s[end] == '"' {
				break
			}
			end++
		}
		if end >= len(s) {
			b.WriteString(s[i+q:])
			break
		}
		key := s[start:end]
		b.WriteByte('"')
		b.WriteString(key)
		b.WriteByte('"')
		i = end + 1

		// Look for ':' then a string value.
		j := i
		for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
			j++
		}
		if j >= len(s) || s[j] != ':' {
			continue
		}
		_, sensitive := redactedSensitiveJSONKeys[strings.ToLower(key)]
		if !sensitive {
			continue
		}
		b.WriteString(s[i : j+1])
		k := j + 1
		for k < len(s) && (s[k] == ' ' || s[k] == '\t') {
			k++
		}
		if k >= len(s) || s[k] != '"' {
			i = j + 1
			continue
		}
		// Skip the original string value.
		vEnd := k + 1
		for vEnd < len(s) {
			if s[vEnd] == '\\' && vEnd+1 < len(s) {
				vEnd += 2
				continue
			}
			if s[vEnd] == '"' {
				break
			}
			vEnd++
		}
		if vEnd >= len(s) {
			b.WriteString(s[k:])
			break
		}
		b.WriteString(`"[REDACTED]"`)
		i = vEnd + 1
	}
	return b.String()
}
