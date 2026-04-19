// Package handlerutil provides shared helper functions used across multiple
// handler packages in auth-api.
package handlerutil

import (
	"github.com/danielgtaylor/huma/v2"

	apierrors "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/errors"
)

// HTTPErr converts an apierrors.Spec into a Huma status error so the
// canonical error envelope is emitted by the framework. All handler
// packages should call this instead of defining a local httpErr.
func HTTPErr(spec *apierrors.Spec) error {
	return huma.NewError(spec.Status, spec.Code+": "+spec.Message)
}
