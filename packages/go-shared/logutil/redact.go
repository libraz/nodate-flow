// Package logutil provides structured-logging helpers shared across all
// nodate-flow Go services. The centrepiece is RedactHandler, an slog.Handler
// wrapper that scrubs secret-looking values before they reach the underlying
// writer.
package logutil

import (
	"context"
	"fmt"
	"log/slog"
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
	"sk_live_",
	"mcp_",
	"pat_",
	"rfr_",
	"ghp_",
	"gho_",
	"github_pat_",
	"glpat-",
	"xoxb-",
	"xoxp-",
	"AKIA",
	"AIza",
	"SG.",
	"rk_live_",
}

// redactedSensitiveJSONKeys lists the JSON object field names whose values
// are always redacted, regardless of content. The OAuth/OIDC keys
// (client_secret, refresh_token, code, id_token, authorization_code) are
// included so token-exchange form bodies cannot leak through HTTP
// middleware traces or upstream error responses.
var redactedSensitiveJSONKeys = map[string]struct{}{
	"api_key":           {},
	"apikey":            {},
	"authorization":     {},
	"authorizationcode": {},
	"clientsecret":      {},
	"code":              {},
	"idtoken":           {},
	"password":          {},
	"refreshtoken":      {},
	"secret":            {},
	"token":             {},
}

func normalizedSensitiveKey(key string) string {
	key = strings.ToLower(key)
	return strings.ReplaceAll(key, "_", "")
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
//
// A prefix only counts where a word starts (see startsWord). Short
// prefixes otherwise match inside ordinary English — "sk-" sits inside
// task-filter, risk-adjusted and disk-usage, "pat_" inside compat_mode,
// "SG." inside MSG. — and the marker is an assertion that a credential
// was found at that spot. One planted in prose costs twice: the words it
// swallows are gone from the stored string, and an operator who reads it
// investigates a leak that never happened.
//
// The rule belongs here rather than in SecretPrefixes: dropping or
// lengthening a colliding prefix narrows what the scanner catches, while
// the boundary rule keeps every prefix and only rejects the positions
// where a secret cannot begin.
func Redact(s string) string {
	if s == "" {
		return s
	}
	prefixesMu.RLock()
	local := prefixes
	prefixesMu.RUnlock()

	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		best := ""
		if startsWord(s, i) {
			for _, p := range local {
				if len(p) > len(best) && strings.HasPrefix(s[i:], p) {
					best = p
				}
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

// startsWord reports whether offset i in s may begin a secret token: a
// credential never continues an alphanumeric run, so a prefix found there
// is part of a longer word rather than a key.
//
// Only ASCII letters and digits block a match. Every separator a
// credential actually arrives after stays open — space, ':', '=', '"',
// '(', '[', ',', newline, '-' and '_' — so header, JSON, env-dump and
// bracketed forms all still redact. A non-ASCII byte is treated as open
// too, so a key butted against CJK prose is caught.
//
// This is a different question from isTokenByte, which decides where a
// matched token ends; '=' for instance ends nothing but may precede a key.
func startsWord(s string, i int) bool {
	if i == 0 {
		return true
	}
	prev := s[i-1]
	switch {
	case prev >= '0' && prev <= '9':
		return false
	case prev >= 'A' && prev <= 'Z':
		return false
	case prev >= 'a' && prev <= 'z':
		return false
	default:
		return true
	}
}

// isTokenByte reports whether b is part of a contiguous secret token.
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
		q := strings.IndexByte(s[i:], '"')
		if q < 0 {
			b.WriteString(s[i:])
			break
		}
		b.WriteString(s[i : i+q])
		start := i + q + 1
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

		j := i
		for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
			j++
		}
		if j >= len(s) || s[j] != ':' {
			continue
		}
		_, sensitive := redactedSensitiveJSONKeys[normalizedSensitiveKey(key)]
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

// sensitiveAttrKeys are attribute keys whose values are always replaced
// with [REDACTED] regardless of content. Mirrors redactedSensitiveJSONKeys
// so an OAuth client_secret / refresh_token / authorization code passed
// as a structured slog attr is scrubbed identically to one embedded in a
// JSON-encoded log line.
var sensitiveAttrKeys = map[string]struct{}{
	"api_key":           {},
	"apikey":            {},
	"authorization":     {},
	"authorizationcode": {},
	"clientsecret":      {},
	"code":              {},
	"idtoken":           {},
	"password":          {},
	"refreshtoken":      {},
	"secret":            {},
	"token":             {},
}

// RedactHandler wraps another slog.Handler and scrubs secret-looking
// values from record attrs before forwarding.
type RedactHandler struct {
	inner slog.Handler
}

// NewRedactHandler returns a RedactHandler wrapping inner.
func NewRedactHandler(inner slog.Handler) *RedactHandler {
	return &RedactHandler{inner: inner}
}

// Enabled reports whether the inner handler handles records at lvl.
func (h *RedactHandler) Enabled(ctx context.Context, lvl slog.Level) bool {
	return h.inner.Enabled(ctx, lvl)
}

// Handle redacts attrs in-place on a copied record, then forwards.
func (h *RedactHandler) Handle(ctx context.Context, r slog.Record) error {
	newRec := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		newRec.AddAttrs(redactAttr(a))
		return true
	})
	return h.inner.Handle(ctx, newRec)
}

// WithAttrs returns a new handler whose inner has the supplied attrs
// attached (also redacted).
func (h *RedactHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	red := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		red[i] = redactAttr(a)
	}
	return &RedactHandler{inner: h.inner.WithAttrs(red)}
}

// WithGroup returns a new handler with the supplied group name pushed on
// the inner handler.
func (h *RedactHandler) WithGroup(name string) slog.Handler {
	return &RedactHandler{inner: h.inner.WithGroup(name)}
}

// redactAttr returns a copy of a with its value scrubbed if necessary.
func redactAttr(a slog.Attr) slog.Attr {
	if _, hit := sensitiveAttrKeys[normalizedSensitiveKey(a.Key)]; hit {
		return slog.String(a.Key, "[REDACTED]")
	}
	v := a.Value
	// Internal row ids are suppressed at the sink rather than only at the
	// call site. The forbidigo rule that guards the call site can match on
	// the callee name alone, so it cannot see slog.Any("workspace_id",
	// ws.ID) nor the loose logger.Warn(msg, "workspace_id", id) form —
	// both of which reach here as a plain integer value. slog normalises
	// every integer width to Int64/Uint64 on the way in, so those two
	// kinds are the whole numeric surface to check.
	if k := v.Kind(); (k == slog.KindInt64 || k == slog.KindUint64) && IsInternalIDKey(a.Key) {
		return slog.String(a.Key, InternalIDPlaceholder)
	}
	switch v.Kind() {
	case slog.KindString:
		return slog.String(a.Key, Redact(v.String()))
	case slog.KindGroup:
		attrs := v.Group()
		out := make([]any, 0, len(attrs))
		for _, inner := range attrs {
			red := redactAttr(inner)
			out = append(out, red)
		}
		return slog.Group(a.Key, out...)
	case slog.KindLogValuer:
		resolved := v.Resolve()
		return redactAttr(slog.Attr{Key: a.Key, Value: resolved})
	default:
		// Non-string values (typically slog.Any) still carry secrets in
		// their textual form. The dominant case is slog.Any("err", err):
		// error messages routinely embed upstream OAuth/OIDC response
		// bodies. Re-wrap error and fmt.Stringer values as a redacted
		// string; other Kinds (int, bool, time, duration, ...) cannot
		// hold prefixed secrets and pass through unchanged.
		switch inner := v.Any().(type) {
		case error:
			return slog.String(a.Key, Redact(inner.Error()))
		case fmt.Stringer:
			return slog.String(a.Key, Redact(inner.String()))
		default:
			return a
		}
	}
}
