// Package handlerutil provides shared helper functions used across multiple
// handler packages. These small utilities avoid duplicating conversion and
// detection logic in every feature handler.
package handlerutil

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
)

// mysqlErrDuplicateEntry is the MySQL error number for a unique-constraint
// violation (ER_DUP_ENTRY). See https://dev.mysql.com/doc/mysql-errors/8.4/en/
const mysqlErrDuplicateEntry = 1062

// IsDuplicateEntry reports whether the given error is a MySQL duplicate-entry
// error (error 1062 / ER_DUP_ENTRY). It uses a type assertion against the
// mysql driver error type for reliable detection.
func IsDuplicateEntry(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == mysqlErrDuplicateEntry
}

// ActorPtr returns a pointer to the actor's internal user id (as int64) for
// use in eventbus.Event payloads, or nil when the actor is not present in
// the context.
func ActorPtr(ctx context.Context) *int64 {
	uid, ok := middleware.ActorFromContext(ctx)
	if !ok {
		return nil
	}
	v := int64(uid)
	return &v
}

// PublicIDOrEmpty returns the UUID string representation of a types.PublicID,
// or an empty string when it is the zero value (e.g. a LEFT JOIN returned
// NULL).
func PublicIDOrEmpty(p types.PublicID) string {
	var zero types.PublicID
	if p == zero {
		return ""
	}
	return p.String()
}

// NullStr converts a sql.NullString to a plain Go string, returning the
// empty string when the column is NULL.
func NullStr(s sql.NullString) string {
	if s.Valid {
		return s.String
	}
	return ""
}

// NullStrPtr converts a sql.NullString to a *string, returning nil when
// the column is NULL so the field is omitted from JSON.
func NullStrPtr(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	return &s.String
}

// NullTime converts a sql.NullTime to a *time.Time, returning nil when the
// column is NULL so the field is omitted from JSON instead of being
// serialised as Go zero-time.
func NullTime(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	tt := t.Time
	return &tt
}

// NullTimeUnix converts a sql.NullTime to *int64 unix seconds. Returns nil
// for the NULL case so the field is omitted from JSON. This is the single
// conversion point for nullable _at columns, per the api-types convention.
func NullTimeUnix(t sql.NullTime) *int64 {
	if !t.Valid {
		return nil
	}
	v := t.Time.Unix()
	return &v
}

// NullTimeUnixVal converts a sql.NullTime to a unix-seconds int64 value.
// Returns 0 for the NULL case. Use this variant when the DTO field is a
// non-pointer int64 (e.g. updatedAt on rows that always have a value after
// the first mutation).
func NullTimeUnixVal(t sql.NullTime) int64 {
	if !t.Valid {
		return 0
	}
	return t.Time.Unix()
}

// TotalAsInt64 normalises the COUNT(*) OVER() return type into int64.
// MySQL drivers may surface the value as int64, int, uint64 or as a
// decimal byte slice depending on the underlying column type, so all four
// shapes are accepted.
func TotalAsInt64(v interface{}) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case uint64:
		return int64(x)
	case []byte:
		var n int64
		for _, c := range x {
			if c < '0' || c > '9' {
				return n
			}
			n = n*10 + int64(c-'0')
		}
		return n
	default:
		return 0
	}
}
