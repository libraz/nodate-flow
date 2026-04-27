// Package handlerutil provides shared helper functions used across multiple
// handler packages in auth-api.
package handlerutil

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	apierrors "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/errors"
)

// ProblemDetails extends huma.ErrorModel with the developer-facing
// description and end-user recovery hint sourced from errors/*.yaml.
//
// The embedded ErrorModel keeps the wire payload RFC 9457 compatible
// (type / title / status / detail), while the extra `description` and
// `userAction` fields let the frontend render a richer toast (and the
// SDK surface them as typed properties) without losing backwards
// compatibility with generic problem+json clients, which simply ignore
// unknown members.
type ProblemDetails struct {
	huma.ErrorModel
	Description string `json:"description,omitempty" doc:"Developer-facing explanation of when this error fires."`
	UserAction  string `json:"userAction,omitempty" doc:"Short imperative the UI can render to tell the end user how to recover."`
}

// GetStatus implements huma.StatusError so Huma sets the response code.
func (p *ProblemDetails) GetStatus() int { return p.ErrorModel.Status }

// Error implements the error interface, mirroring huma.ErrorModel's
// formatting so existing log lines remain stable.
func (p *ProblemDetails) Error() string { return p.ErrorModel.Error() }

// HTTPErr converts an apierrors.Spec into a Huma status error so the
// canonical problem+json envelope is emitted by the framework. All
// handler packages should call this instead of defining a local httpErr.
//
// The envelope is RFC 9457-compliant and additionally includes
// description + userAction copied from the error catalog:
//
//   - type:        the machine-readable error code (e.g.
//     "AUTH.LOGIN.INVALID_CREDENTIALS"). Clients should branch on this field.
//   - title:       the HTTP status text (e.g. "Unauthorized"). Populated
//     explicitly for determinism.
//   - detail:      the human-readable message from the Spec. Must NOT be
//     prefixed with the code — clients read `type` for that.
//   - status:      the HTTP status code.
//   - description: developer-facing explanation (omitted when empty).
//   - userAction:  end-user recovery hint (omitted when empty).
func HTTPErr(spec *apierrors.Spec) error {
	return &ProblemDetails{
		ErrorModel: huma.ErrorModel{
			Type:   spec.Code,
			Title:  http.StatusText(spec.Status),
			Status: spec.Status,
			Detail: spec.Message,
		},
		Description: spec.Description,
		UserAction:  spec.UserAction,
	}
}
