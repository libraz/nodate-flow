// Package handlerutil — time helpers.
//
// Per the api-types convention, the wire boundary uses unix seconds
// (int64) for `*_at` fields and `YYYY-MM-DD` strings for `*_on`
// fields. All conversion between [time.Time] and these wire shapes
// is meant to live in this package (or in mappers) — handlers should
// not call [time.Now] / [time.Unix] directly.
package handlerutil

import "time"

// NowUnix returns the current UTC unix timestamp in seconds. Use this
// from a handler when you need to stamp a "createdAt" / "updatedAt"
// field on a synthesised DTO row that did not come back from the DB.
//
// For DB-sourced rows, prefer the row's own time column projected
// through [NullTimeUnix] / [NullTimeUnixVal] in the mapper.
func NowUnix() int64 {
	return time.Now().UTC().Unix()
}

// UnixToTime converts a unix-seconds wire value into a UTC
// [time.Time]. Centralising the call removes "did the caller remember
// .UTC()?" from the review surface area at every handler.
func UnixToTime(s int64) time.Time {
	return time.Unix(s, 0).UTC()
}

// TimeToUnix converts a [time.Time] into unix seconds, returning 0
// for the zero value (so a NULL-equivalent stays representable in
// non-pointer DTO fields). Non-UTC inputs are normalised — unix
// seconds are timezone-agnostic, but going through .UTC() keeps the
// behaviour consistent with the rest of the helper set.
func TimeToUnix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UTC().Unix()
}
