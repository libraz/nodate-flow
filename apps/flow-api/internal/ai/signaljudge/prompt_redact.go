// Package signaljudge — prompt-side secret redaction.
//
// The judge prompt ships two distinct kinds of operator-influenced
// content into the LLM call:
//
//  1. signal.payload_json — upstream-shaped JSON. We walk it and
//     replace values for known secret-bearing keys with [REDACTED].
//  2. ai_settings.judge_instructions — free-form prose. We scan for
//     a small set of secret prefixes and replace them with [REDACTED].
//
// The project conventions forbid regex (see memory:project_conventions),
// so this file uses [json.Unmarshal] + recursive map/slice walks for
// the JSON path, and [strings.Index] + [strings.HasPrefix] for the
// free-form path. The set of blocked keys / prefixes is intentionally
// small and policy-driven — see the comments inline.
package signaljudge

import (
	"bytes"
	"encoding/json"
	"strings"
)

// redactionMarker is the literal string a redacted JSON value is
// replaced with. Same wording the logutil package uses so an operator
// reading an ai_invocations row sees a consistent vocabulary.
const redactionMarker = "[REDACTED]"

// jsonRedactKeys lists the lowercased JSON object keys whose values
// are always replaced with the redaction marker, regardless of value
// shape. The list is intentionally narrower than the slog-side list
// in packages/go-shared/logutil: prompts only ever ingest signal
// payloads, so we only need to block tokens an upstream signal
// provider is likely to emit. Adding a key here is cheap; removing
// one risks a leak, so the bar is "no plausible benign use".
var jsonRedactKeys = map[string]struct{}{
	"access_token":  {},
	"api_key":       {},
	"apikey":        {},
	"authorization": {},
	"auth_token":    {},
	"client_secret": {},
	"id_token":      {},
	"password":      {},
	"refresh_token": {},
	"secret":        {},
	"session_token": {},
	"token":         {},
}

// freeFormRedactPrefixes lists the literal substrings the free-form
// redactor scans for. When a match is found the substring and every
// following non-whitespace byte (up to a closing quote / brace /
// bracket / newline) is replaced with [REDACTED]. The set is
// deliberately short: a longer list trades false negatives for false
// positives, and an operator who wants their token to survive the
// redactor can just not embed one in the policy field.
//
// Order does not matter; the scanner picks the longest match starting
// at each position.
var freeFormRedactPrefixes = []string{
	"Bearer ",
	"sk-ant-",
	"sk-",
	"xoxb-",
	"xoxp-",
	"xoxa-",
	"ghp_",
	"gho_",
	"github_pat_",
	"glpat-",
	"AKIA",
	"AIza",
}

// redactJSON walks raw and returns a deep-redacted JSON encoding of
// the same shape. Invalid JSON falls through to [redactRawBytes],
// which applies the substring blocklist on the raw bytes — that way
// even a malformed upstream payload cannot leak a token.
//
// Numbers, booleans, nulls, and empty objects/arrays pass through
// unchanged. Object values keyed by a member of [jsonRedactKeys] are
// replaced wholesale with the [redactionMarker] string regardless of
// their original type; this prevents a creative payload from
// embedding a JSON-encoded secret inside, say, an array.
func redactJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		// Not valid JSON. Fall back to the raw-bytes path so the
		// substring blocklist still gets a chance to scrub a leaked
		// token. The result is wrapped as a JSON string so the
		// caller can keep treating it as json.RawMessage.
		return json.RawMessage(rawAsJSONString(redactRawBytes(raw)))
	}
	redacted := walkRedact(v)
	out, err := json.Marshal(redacted)
	if err != nil {
		// Should never happen; we only ever marshal the walked tree
		// of strings/numbers/maps/slices. Defensive: fall back to
		// the substring path on the original bytes.
		return json.RawMessage(rawAsJSONString(redactRawBytes(raw)))
	}
	return json.RawMessage(out)
}

// walkRedact recurses over a json.Unmarshal'd tree and returns a
// new tree with secret-bearing values replaced. The function never
// mutates its input — the prompt builder treats the original payload
// as immutable.
func walkRedact(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, vv := range x {
			if _, blocked := jsonRedactKeys[strings.ToLower(k)]; blocked {
				out[k] = redactionMarker
				continue
			}
			out[k] = walkRedact(vv)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, vv := range x {
			out[i] = walkRedact(vv)
		}
		return out
	case string:
		// Free-form string inside the JSON payload: apply the
		// substring scrubber so a token pasted into a benign field
		// (e.g. "description": "Bearer abcdef") still gets caught.
		return redactFreeFormText(x)
	default:
		return v
	}
}

// rawAsJSONString re-encodes a byte slice as a JSON string. Used by
// the [redactJSON] fallback when the upstream payload is not valid
// JSON — we still want the result to round-trip as a json.RawMessage.
func rawAsJSONString(b []byte) []byte {
	out, err := json.Marshal(string(b))
	if err != nil {
		return []byte(`""`)
	}
	return out
}

// redactRawBytes runs the substring blocklist over arbitrary bytes
// and returns the scrubbed result. The scanner is a hand-rolled walk
// (no regex) that, on encountering a matching prefix, replaces every
// byte up to the next token-terminator with the redaction marker.
func redactRawBytes(in []byte) []byte {
	if len(in) == 0 {
		return in
	}
	// Cheap fast path: nothing matches.
	if !containsAnyPrefix(in) {
		return append([]byte(nil), in...)
	}
	var out bytes.Buffer
	out.Grow(len(in))
	i := 0
	for i < len(in) {
		best := ""
		for _, p := range freeFormRedactPrefixes {
			if len(p) > len(best) && hasBytePrefix(in[i:], p) {
				best = p
			}
		}
		if best == "" {
			out.WriteByte(in[i])
			i++
			continue
		}
		// Consume the prefix and the run of token bytes that follows.
		j := i + len(best)
		for j < len(in) && isTokenChar(in[j]) {
			j++
		}
		out.WriteString(redactionMarker)
		i = j
	}
	return out.Bytes()
}

// redactFreeFormText is the string-typed version of [redactRawBytes].
// Used for prose fields (judge instructions, string values inside the
// signal payload).
func redactFreeFormText(in string) string {
	if in == "" {
		return in
	}
	if !containsAnyPrefixStr(in) {
		return in
	}
	var b strings.Builder
	b.Grow(len(in))
	i := 0
	for i < len(in) {
		best := ""
		for _, p := range freeFormRedactPrefixes {
			if len(p) > len(best) && strings.HasPrefix(in[i:], p) {
				best = p
			}
		}
		if best == "" {
			b.WriteByte(in[i])
			i++
			continue
		}
		j := i + len(best)
		for j < len(in) && isTokenChar(in[j]) {
			j++
		}
		b.WriteString(redactionMarker)
		i = j
	}
	return b.String()
}

// containsAnyPrefix is the fast-path probe for [redactRawBytes]. It
// reports whether any of the registered prefixes appears anywhere in
// in. Avoiding the allocation of an output buffer when the input is
// clean — which is the common case — pays for itself across millions
// of prompt renders.
func containsAnyPrefix(in []byte) bool {
	for _, p := range freeFormRedactPrefixes {
		if bytes.Contains(in, []byte(p)) {
			return true
		}
	}
	return false
}

// containsAnyPrefixStr is the string-typed fast-path probe.
func containsAnyPrefixStr(in string) bool {
	for _, p := range freeFormRedactPrefixes {
		if strings.Contains(in, p) {
			return true
		}
	}
	return false
}

// hasBytePrefix is a small allocation-free helper. Equivalent to
// bytes.HasPrefix(in, []byte(p)) but does not allocate a temporary
// slice for p on every call.
func hasBytePrefix(in []byte, p string) bool {
	if len(p) > len(in) {
		return false
	}
	for i := 0; i < len(p); i++ {
		if in[i] != p[i] {
			return false
		}
	}
	return true
}

// isTokenChar reports whether c is part of a contiguous secret token.
// Whitespace, quotes, brackets, braces, and punctuation terminate a
// token; everything printable above space continues it. Same shape
// as logutil.isTokenByte; kept separate so this package does not
// import a private logutil symbol.
func isTokenChar(c byte) bool {
	if c > 0x7E {
		return false
	}
	switch c {
	case ' ', '\t', '\n', '\r', '"', '\'', ',', '}', '{', '[', ']', ';', ')', '(':
		return false
	}
	return c > 0x20
}
