package calendars

import (
	"database/sql"

	generated "github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/generated"
)

// Deps holds the dependencies required by calendar handlers.
type Deps struct {
	Queries *generated.Queries
	DB      *sql.DB
}
