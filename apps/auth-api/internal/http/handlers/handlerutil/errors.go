// Package handlerutil provides shared helper functions used across multiple
// handler packages in auth-api.
package handlerutil

import (
	"encoding/json"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
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
//
// Extensions carries optional RFC 9457 extension members keyed by
// arbitrary strings. Used to surface diagnostic context (e.g. the
// `provider_error` / `provider_error_description` that the IdP
// attached to an OIDC callback rejection). The field is omitted from
// the wire payload entirely when no extensions apply.
type ProblemDetails struct {
	huma.ErrorModel
	Description string         `json:"description,omitempty" doc:"Developer-facing explanation of when this error fires."`
	UserAction  string         `json:"userAction,omitempty" doc:"Short imperative the UI can render to tell the end user how to recover."`
	Extensions  map[string]any `json:"extensions,omitempty" doc:"Optional RFC 9457 extension members carrying diagnostic detail."`
}

// GetStatus implements huma.StatusError so Huma sets the response code.
func (p *ProblemDetails) GetStatus() int { return p.Status }

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

// HTTPErrWithDetails is the extension-aware variant of [HTTPErr]. It
// returns the same RFC 9457 problem+json shape but additionally
// populates the optional `extensions` member with the supplied keys.
// Use this when a handler needs to surface diagnostic context that
// is not part of the static error catalog (e.g. a provider-supplied
// error slug on the OIDC callback rejection path).
//
// Passing a nil or empty map yields an envelope identical to
// [HTTPErr], so callers can build the map conditionally without a
// branch.
func HTTPErrWithDetails(spec *apierrors.Spec, details map[string]any) error {
	var ext map[string]any
	if len(details) > 0 {
		ext = make(map[string]any, len(details))
		for k, v := range details {
			ext[k] = v
		}
	}
	return &ProblemDetails{
		ErrorModel: huma.ErrorModel{
			Type:   spec.Code,
			Title:  http.StatusText(spec.Status),
			Status: spec.Status,
			Detail: spec.Message,
		},
		Description: spec.Description,
		UserAction:  spec.UserAction,
		Extensions:  ext,
	}
}

// WriteSpecError writes a JSON error envelope for raw chi handlers and
// chi-level middlewares (auth, ACL, rate limit) that cannot return
// errors through the Huma pipeline. The envelope mirrors the shape
// produced by [HTTPErr] so clients can branch on the same `type`
// field regardless of which layer emitted the error.
//
// The wire shape is RFC 9457-compliant problem+json:
//
//   - type:        machine-readable error code (e.g. "AUTH.SESSION.UNAUTHORIZED")
//   - title:       HTTP status text (e.g. "Unauthorized")
//   - status:      HTTP status code
//   - detail:      human-readable message from the Spec
//   - description: developer-facing explanation (omitted when empty)
//   - userAction:  end-user recovery hint (omitted when empty)
func WriteSpecError(w http.ResponseWriter, spec *apierrors.Spec) {
	w.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
	w.WriteHeader(spec.Status)
	_ = json.NewEncoder(w).Encode(struct {
		Type        string `json:"type"`
		Title       string `json:"title"`
		Status      int    `json:"status"`
		Detail      string `json:"detail"`
		Description string `json:"description,omitempty"`
		UserAction  string `json:"userAction,omitempty"`
	}{
		Type:        spec.Code,
		Title:       http.StatusText(spec.Status),
		Status:      spec.Status,
		Detail:      spec.Message,
		Description: spec.Description,
		UserAction:  spec.UserAction,
	})
}
