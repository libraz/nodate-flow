package calendars

import (
	"database/sql"

	"github.com/danielgtaylor/huma/v2"

	generated "github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/time-api/internal/errors"
)

// Deps holds the dependencies required by calendar handlers.
type Deps struct {
	Queries *generated.Queries
	DB      *sql.DB
}

// httpErr converts an error Spec into a Huma error.
func httpErr(spec *apierrors.Spec) error {
	return huma.NewError(spec.Status, spec.Code+": "+spec.Message)
}

// int64Ptr returns a pointer to an int64 value.
func int64Ptr(v int64) *int64 {
	return &v
}
