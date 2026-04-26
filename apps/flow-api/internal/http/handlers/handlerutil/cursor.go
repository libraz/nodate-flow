// Package handlerutil — keyset cursor encoding helpers.
//
// List endpoints that ship a *Keyset query alongside the historical
// OFFSET path encode the (created_at, public_id) tuple of the LAST row
// of a page as an opaque base64url string. The wire format is a tiny
// JSON object:
//
//	{"t": <unix_seconds>, "p": "<canonical-uuid>"}
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

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
)

// cursorPayload is the on-the-wire shape carried inside the base64url
// cursor string. The JSON keys are deliberately one-letter to keep the
// encoded blob short; this is opaque to clients so brevity costs us
// nothing in readability and saves bytes per page response.
type cursorPayload struct {
	T int64  `json:"t"`
	P string `json:"p"`
}

// EncodeCursor packs a (created_at, public_id) tuple into a base64url
// JSON string suitable for round-tripping in a `nextCursor` response
// field and an opaque `cursor` query parameter on the next request.
//
// The time is written as unix seconds; sub-second precision is dropped,
// which matches the keyset SQL ORDER BY granularity (the public_id
// tiebreaker disambiguates rows that share the same second).
func EncodeCursor(t time.Time, pid types.PublicID) string {
	payload := cursorPayload{
		T: t.UTC().Unix(),
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
	return time.Unix(payload.T, 0).UTC(), pid, nil
}
