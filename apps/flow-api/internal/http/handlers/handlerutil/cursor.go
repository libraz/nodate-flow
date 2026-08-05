// Package handlerutil — keyset cursor encoding helpers.
//
// List endpoints that ship a *Keyset query alongside the historical
// OFFSET path encode the (created_at, public_id) tuple of the LAST row
// of a page as an opaque base64url string. The wire format is a tiny
// JSON object:
//
//	{"t": <unix_milliseconds>, "p": "<canonical-uuid>"}
//
// Choosing JSON-in-base64 over a binary packing keeps the cursor
// trivially debuggable from a curl session (decode and read), keeps
// forward-compat headroom (more fields can be added without breaking
// older clients that ignore unknown keys), and avoids hand-rolling a
// little-endian binary protocol — the cursor is opaque to callers and
// CPU cost is negligible at one encode/decode per page.
//
// An empty input string is treated as "first page" and decodes to the
// zero values (zero [time.Time], zero [types.PublicID]) with a nil
// error, matching the SQL contract that the keyset queries accept
// NULL/NULL for the first page.
package handlerutil

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

// Millis is unix-epoch milliseconds. It exists as a distinct named
// type — not just an int64 — so the cursor wire format is
// type-checked: the keyset queries compare against DATETIME(3) columns
// and a future maintainer who reaches for `t.Unix()` (seconds) instead
// of `t.UnixMilli()` (milliseconds) would silently drop sub-second
// precision and produce cursors that skip rows. Forcing the explicit
// `Millis(t.UTC().UnixMilli())` conversion at the encode site means
// any "let me just use Unix() here" refactor is rejected by the
// compiler instead of by a flaky pagination test.
//
// See [cursorPayload] and [EncodeCursor] for how this is used on the
// wire; the package doc explains the JSON-in-base64 framing.
type Millis int64

// cursorPayload is the on-the-wire shape carried inside the base64url
// cursor string. The JSON keys are deliberately one-letter to keep the
// encoded blob short; this is opaque to clients so brevity costs us
// nothing in readability and saves bytes per page response.
//
// The `t` field is unix MILLISECONDS, not seconds: the underlying
// timestamp columns are DATETIME(3) and the keyset queries do strict
// inequality comparisons on the (created_at, public_id) tuple. Encoding
// at second granularity silently drops the millisecond component and
// produces a cursor that excludes valid rows whose true timestamp lies
// between the truncated second and the actual last-row time, which
// shows up as missing pages on dense fixtures (see
// TestKeysetHandlerListTasksWorkspace). The [Millis] named type is the
// compile-time guard that pins this contract.
type cursorPayload struct {
	T Millis `json:"t"`
	P string `json:"p"`
}

// EncodeCursor packs a (created_at, public_id) tuple into a base64url
// JSON string suitable for round-tripping in a `nextCursor` response
// field and an opaque `cursor` query parameter on the next request.
//
// The time is written as unix milliseconds, matching the DATETIME(3)
// granularity of the timestamp columns the keyset queries order on.
// Encoding at second granularity loses information and breaks the
// strict-inequality cursor compare for rows that happen to share the
// same second as the page boundary.
func EncodeCursor(t time.Time, pid types.PublicID) string {
	payload := cursorPayload{
		T: Millis(t.UTC().UnixMilli()),
		P: pid.String(),
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		// json.Marshal cannot fail for this fixed-shape struct.
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

// DecodeCursor parses an opaque cursor string back into the
// (created_at, public_id) tuple consumed by the *Keyset sqlc params.
//
// An empty input is the explicit "first page" sentinel and returns the
// zero values with a nil error so handlers can pass the result straight
// into [sql.NullTime]{Valid: false} / zero PublicID without a special
// case. Any decode error (bad base64, bad JSON, malformed UUID) is
// returned to the caller so it can map to a 400 / WS.VALIDATION error.
//
// The returned errors are unwrapped fmt.Errorf values on purpose:
// callers must translate them via
// httpErr(apierrors.ValidationQueryFieldInvalid) (or an equivalent
// validation code) before returning to the HTTP layer. Returning a
// typed apierror here would tie this helper package to a concrete
// error code and prevent callers from substituting a more specific
// validation code where appropriate.
//
// The decoded time preserves millisecond precision; see the package
// doc on the wire format and EncodeCursor for the rationale.
func DecodeCursor(s string) (time.Time, types.PublicID, error) {
	if s == "" {
		return time.Time{}, types.PublicID{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, types.PublicID{}, fmt.Errorf("cursor: invalid base64: %w", err)
	}
	var payload cursorPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return time.Time{}, types.PublicID{}, fmt.Errorf("cursor: invalid json: %w", err)
	}
	pid, err := types.Parse(payload.P)
	if err != nil {
		return time.Time{}, types.PublicID{}, fmt.Errorf("cursor: invalid public id: %w", err)
	}
	return time.UnixMilli(int64(payload.T)).UTC(), pid, nil
}
