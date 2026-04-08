package dto

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/types"
)

// dateLayout is the canonical wire format for DateOnly values.
const dateLayout = "2006-01-02"

// PublicID is the only identifier shape exposed across the API boundary.
// It is a type alias for the shared db/types.PublicID so that values
// returned by sqlc-generated rows can be passed directly into DTOs without
// conversion. Internal numeric ids (workspaces.id, tasks.id, ...) must
// never leak.
type PublicID = types.PublicID

// NewPublicID wraps the given uuid.UUID as a PublicID.
func NewPublicID(u uuid.UUID) PublicID { return PublicID(u) }

// ParsePublicID parses a canonical UUID string into a PublicID.
func ParsePublicID(s string) (PublicID, error) {
	p, err := types.Parse(s)
	if err != nil {
		return PublicID{}, fmt.Errorf("dto: invalid public id %q: %w", s, err)
	}
	return p, nil
}

// UnixSeconds wraps an int64 unix-seconds timestamp.
//
// All `*_at` fields at the API boundary use this type. Sub-second
// precision is intentionally dropped; clients format on display.
type UnixSeconds int64

// UnixSecondsFromTime converts a time.Time into UnixSeconds.
func UnixSecondsFromTime(t time.Time) UnixSeconds {
	return UnixSeconds(t.Unix())
}

// FromTime sets the receiver to the unix-seconds value of t.
func (u *UnixSeconds) FromTime(t time.Time) {
	*u = UnixSeconds(t.Unix())
}

// ToTime returns the value as a UTC time.Time.
func (u UnixSeconds) ToTime() time.Time {
	return time.Unix(int64(u), 0).UTC()
}

// MarshalJSON encodes the value as a JSON number.
func (u UnixSeconds) MarshalJSON() ([]byte, error) {
	return json.Marshal(int64(u))
}

// UnmarshalJSON decodes a JSON number into UnixSeconds.
func (u *UnixSeconds) UnmarshalJSON(data []byte) error {
	var n int64
	if err := json.Unmarshal(data, &n); err != nil {
		return fmt.Errorf("dto: UnixSeconds must be a JSON number: %w", err)
	}
	*u = UnixSeconds(n)
	return nil
}

// DateOnly is a YYYY-MM-DD string suitable for `*_on` fields.
//
// DateOnly is timezone-independent: it represents a calendar date as
// a human reads it. Construction validates the layout.
type DateOnly string

// ParseDateOnly validates and returns a DateOnly value.
func ParseDateOnly(s string) (DateOnly, error) {
	if _, err := time.Parse(dateLayout, s); err != nil {
		return "", fmt.Errorf("dto: invalid date %q (want YYYY-MM-DD): %w", s, err)
	}
	return DateOnly(s), nil
}

// DateOnlyFromTime formats t as a DateOnly using its UTC calendar date.
func DateOnlyFromTime(t time.Time) DateOnly {
	return DateOnly(t.UTC().Format(dateLayout))
}

// FromTime sets the receiver to the UTC calendar date of t.
func (d *DateOnly) FromTime(t time.Time) {
	*d = DateOnly(t.UTC().Format(dateLayout))
}

// ToTime parses the DateOnly back into a time.Time at UTC midnight.
func (d DateOnly) ToTime() (time.Time, error) {
	return time.Parse(dateLayout, string(d))
}

// String returns the underlying YYYY-MM-DD string.
func (d DateOnly) String() string { return string(d) }

// MarshalJSON encodes the DateOnly as a JSON string after validation.
func (d DateOnly) MarshalJSON() ([]byte, error) {
	if string(d) == "" {
		return json.Marshal("")
	}
	if _, err := time.Parse(dateLayout, string(d)); err != nil {
		return nil, fmt.Errorf("dto: invalid DateOnly %q: %w", string(d), err)
	}
	return json.Marshal(string(d))
}

// UnmarshalJSON decodes and validates a YYYY-MM-DD JSON string.
func (d *DateOnly) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("dto: DateOnly must be a JSON string: %w", err)
	}
	parsed, err := ParseDateOnly(s)
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

// Pagination is the standard cursor + limit pagination request shape.
//
// Embed in Huma Input structs as a query parameter group.
type Pagination struct {
	Cursor *string `query:"cursor" doc:"Opaque pagination cursor returned by a previous list call"`
	Limit  int     `query:"limit" minimum:"1" maximum:"200" default:"50" doc:"Maximum number of items to return"`
}

// ListResponse is the generic envelope for list endpoints.
//
// Items holds the page contents, NextCursor is set when more pages
// follow, and Total is the total count across all pages.
type ListResponse[T any] struct {
	Items      []T     `json:"items" doc:"Page items"`
	NextCursor *string `json:"nextCursor,omitempty" doc:"Cursor to pass to the next list call, or null if this is the last page"`
	Total      int64   `json:"total" doc:"Total number of items across all pages"`
}

// ErrorResponse is the DTO-safe view of an API error.
//
// It mirrors the shape of apps/api/internal/errors without importing it,
// so packages that only need the wire format can depend on dto alone.
type ErrorResponse struct {
	Code    string         `json:"code" doc:"Stable error code (e.g. WS.TASK.NOT_FOUND)"`
	Message string         `json:"message" doc:"Human-readable English message"`
	Details map[string]any `json:"details,omitempty" doc:"Optional structured details"`
}

// Timestamps is the standard created/updated metadata block.
//
// Embed in resource DTOs to expose unix-seconds timestamps.
type Timestamps struct {
	CreatedAt UnixSeconds `json:"createdAt" doc:"Creation time (unix seconds)"`
	UpdatedAt UnixSeconds `json:"updatedAt" doc:"Last update time (unix seconds)"`
}
