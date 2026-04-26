// Package dbtype null-conversion helpers.
//
// These functions convert sql.Null* values into pointer types suitable for
// JSON DTOs (where nil maps to "field omitted" via omitempty). They are the
// canonical, shared replacements for the per-handler reimplementations
// previously scattered across apps/flow-api and apps/auth-api.
//
// The helpers are intentionally small wrappers so call sites stay readable:
//
//	dto.AvatarURL = dbtype.PtrFromNullString(row.AvatarUrl)
//	dto.UpdatedAt = dbtype.UnixSecondsFromNullTime(row.UpdatedAt)
//	dto.StartedOn = dbtype.DateStringFromNullTime(row.StartedOn)
//
// Time-bearing helpers normalise to UTC so the wire format is stable across
// server timezones — both _at (unix seconds) and _on (YYYY-MM-DD) follow the
// project's api-types convention (see docs/conventions/api-types.md).
package dbtype

import (
	"database/sql"
)

// dateLayout is the canonical YYYY-MM-DD string used for *_on API fields.
const dateLayout = "2006-01-02"

// PtrFromNullString returns nil if v is not Valid, otherwise a pointer to a
// copy of v.String. Use this for nullable VARCHAR / TEXT columns whose DTO
// field is *string with `json:",omitempty"`.
func PtrFromNullString(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}

// PtrFromNullInt32 returns nil if v is not Valid, otherwise a pointer to a
// copy of v.Int32. Use this for nullable INT columns whose DTO field is
// *int32 with `json:",omitempty"`.
func PtrFromNullInt32(v sql.NullInt32) *int32 {
	if !v.Valid {
		return nil
	}
	n := v.Int32
	return &n
}

// PtrFromNullInt64 returns nil if v is not Valid, otherwise a pointer to a
// copy of v.Int64. Use this for nullable BIGINT columns whose DTO field is
// *int64 with `json:",omitempty"`.
func PtrFromNullInt64(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	n := v.Int64
	return &n
}

// PtrFromNullBool returns nil if v is not Valid, otherwise a pointer to a
// copy of v.Bool. Use this for nullable TINYINT(1) / BOOLEAN columns whose
// DTO field is *bool with `json:",omitempty"`.
func PtrFromNullBool(v sql.NullBool) *bool {
	if !v.Valid {
		return nil
	}
	b := v.Bool
	return &b
}

// PtrFromNullFloat64 returns nil if v is not Valid, otherwise a pointer to a
// copy of v.Float64. Use this for nullable DOUBLE / DECIMAL columns whose
// DTO field is *float64 with `json:",omitempty"`.
func PtrFromNullFloat64(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	f := v.Float64
	return &f
}

// UnixSecondsFromNullTime returns nil if v is not Valid, otherwise a pointer
// to v.Time.Unix(). This is the canonical conversion for nullable *_at API
// fields (Go time.Time → wire int64 unix seconds) per the api-types
// convention. Unix() is timezone-independent so the value is stable
// regardless of how the driver loaded the column.
func UnixSecondsFromNullTime(v sql.NullTime) *int64 {
	if !v.Valid {
		return nil
	}
	n := v.Time.Unix()
	return &n
}

// DateStringFromNullTime returns nil if v is not Valid, otherwise a pointer
// to a "YYYY-MM-DD" string formatted in UTC. This is the canonical
// conversion for nullable *_on API fields (DATE → wire string) per the
// api-types convention.
//
// UTC normalisation matters: a DATE column has no timezone, but the driver
// may attach the server's local zone to the resulting time.Time. Formatting
// in UTC ensures the same date string regardless of where the API runs.
func DateStringFromNullTime(v sql.NullTime) *string {
	if !v.Valid {
		return nil
	}
	s := v.Time.UTC().Format(dateLayout)
	return &s
}
