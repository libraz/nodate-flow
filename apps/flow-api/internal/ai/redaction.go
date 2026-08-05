// Package ai contains the LLM provider abstraction's user-facing helpers
// (redaction, cost guard, task orchestration). The concrete provider
// implementations and the only allowed callers of go-shared/crypto live in
// the sub-package internal/ai/providers.
package ai

import (
	"github.com/libraz/nodate-flow/packages/go-shared/logutil"
)

// SecretPrefixes is the fixed list of literal prefixes scanned for in
// strings passed to Redact. This re-exports the canonical list from
// packages/go-shared/logutil so existing callers and tests continue to
// compile without changes.
var SecretPrefixes = logutil.SecretPrefixes

// RegisterPrefix adds a literal secret prefix to the redaction scanner.
// Safe to call from init functions; delegates to logutil.RegisterPrefix.
var RegisterPrefix = logutil.RegisterPrefix

// Redact scans s for any registered secret prefix and replaces every match
// with "[REDACTED:<prefix>]". Delegates to logutil.Redact.
var Redact = logutil.Redact

// RedactJSONFields walks raw JSON-ish text and replaces the value of any
// object field whose key matches sensitive keys. Delegates to
// logutil.RedactJSONFields.
var RedactJSONFields = logutil.RedactJSONFields
