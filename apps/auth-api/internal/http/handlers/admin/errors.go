package admin

import (
	"github.com/danielgtaylor/huma/v2"

	apierrors "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/errors"
)

// httpErr converts an APIError Spec into a Huma status error so the
// canonical error envelope is emitted by the framework.
func httpErr(spec *apierrors.Spec) error {
	return huma.NewError(spec.Status, spec.Code+": "+spec.Message)
}
