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
