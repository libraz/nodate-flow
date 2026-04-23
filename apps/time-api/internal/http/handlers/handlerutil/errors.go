// Package handlerutil provides shared helper functions used across multiple
// handler packages in time-api.
package handlerutil

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	apierrors "github.com/nodate-flow/nodate-flow/apps/time-api/internal/errors"
)

// HTTPErr converts an apierrors.Spec into a Huma status error so the
// canonical problem+json envelope is emitted by the framework. All
// handler packages should call this instead of defining a local httpErr.
//
// The envelope is RFC 9457-compliant:
//
//   - type:   the machine-readable error code (e.g.
//     "SHARE.SHARE.TOKEN_INVALID"). Clients should branch on this field.
//   - title:  the HTTP status text (e.g. "Not Found"). Populated by Huma
//     from the status when omitted, set explicitly here for determinism.
//   - detail: the human-readable message from the Spec. Must NOT be
//     prefixed with the code — clients read `type` for that.
//   - status: the HTTP status code.
func HTTPErr(spec *apierrors.Spec) error {
	return &huma.ErrorModel{
		Type:   spec.Code,
		Title:  http.StatusText(spec.Status),
		Status: spec.Status,
		Detail: spec.Message,
	}
}
