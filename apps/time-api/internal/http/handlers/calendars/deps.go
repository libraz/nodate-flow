package calendars

import (
	"database/sql"

	generated "github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/http/handlers/handlerutil"
)

// Deps holds the dependencies required by calendar handlers.
type Deps struct {
	Queries *generated.Queries
	DB      *sql.DB
}

// httpErr delegates to handlerutil.HTTPErr.
var httpErr = handlerutil.HTTPErr

// int64Ptr returns a pointer to an int64 value.
func int64Ptr(v int64) *int64 {
	return &v
}

// nullTimeUnixValue returns the unix seconds of a nullable time, or 0 when
// the time is NULL. Used to keep DTO fields stable while start_at / end_at
// are nullable in the schema.
func nullTimeUnixValue(t sql.NullTime) int64 {
	if !t.Valid {
		return 0
	}
	return t.Time.Unix()
}

// nullTimeUnixPtr maps a nullable time to *int64 so DTOs with omitempty
// drop the field entirely when the DB column is NULL (undated event).
func nullTimeUnixPtr(t sql.NullTime) *int64 {
	if !t.Valid {
		return nil
	}
	v := t.Time.Unix()
	return &v
}
