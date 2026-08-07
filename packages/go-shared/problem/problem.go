// Package problem holds the single writer for the API's error
// envelope. Every layer that answers a request without going through
// Huma — authentication, rate limiting, ACL, raw chi handlers, SSE
// upgrades — writes its errors from here.
//
// It lives in go-shared rather than in either service because the
// emitters do too: the authentication middleware and the rate limiter
// are shared code that runs in front of both flow-api and auth-api, so
// a writer owned by one service could not be reached from them. It is
// its own package rather than part of httputil because authn is one of
// the emitters and httputil already imports authn; folding the writer
// into httputil would close that import cycle.
//
// The shape is RFC 9457 problem+json. The SDK reads `type` for the
// machine-readable code, `detail` for the message and `status` for the
// HTTP status, and that last one is why the shape has to be uniform:
// the frontend's terminal-401 handling and its "do not retry a 4xx"
// rule both branch on the status carried in the body, so an envelope
// that omits it leaves an expired session looking like a network
// blip — the user stays apparently signed in against a dead token.
//
// New error responses go through [Write]; a response built any other
// way is rejected by the guard in envelope_guard_test.go.
package problem

import (
	"encoding/json"
	"net/http"

	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

// ContentType is the media type every error response carries.
const ContentType = "application/problem+json; charset=utf-8"

// Details is the wire shape of an error response.
//
//   - type:        the machine-readable error code (e.g. "WS.TASK.NOT_FOUND").
//     Clients branch on this field.
//   - title:       the HTTP status text (e.g. "Not Found"), written
//     explicitly so the payload is deterministic.
//   - status:      the HTTP status code, repeated in the body because
//     clients that only have the parsed envelope need it.
//   - detail:      the human-readable message. Never prefixed with the
//     code — clients read `type` for that.
//   - description: developer-facing explanation, omitted when empty.
//   - userAction:  end-user recovery hint, omitted when empty.
//   - extensions:  RFC 9457 extension members, omitted when empty.
type Details struct {
	Type        string         `json:"type"`
	Title       string         `json:"title"`
	Status      int            `json:"status"`
	Detail      string         `json:"detail"`
	Description string         `json:"description,omitempty"`
	UserAction  string         `json:"userAction,omitempty"`
	Extensions  map[string]any `json:"extensions,omitempty"`
}

// fallback is what a nil spec collapses to. A caller that reached the
// writer with nothing to say still has to produce a valid envelope:
// answering with an empty body would hand the client the same
// undecidable response the uniform shape exists to eliminate.
var fallback = &apierr.Spec{
	Code:    apierr.CodeInternalUnexpected,
	Status:  http.StatusInternalServerError,
	Message: "Unexpected server error",
}

// FromSpec builds the envelope for a catalog spec, including any
// extension members the spec opts into.
func FromSpec(spec *apierr.Spec) Details {
	if spec == nil {
		spec = fallback
	}
	return Details{
		Type:        spec.Code,
		Title:       http.StatusText(spec.Status),
		Status:      spec.Status,
		Detail:      spec.Message,
		Description: spec.Description,
		UserAction:  spec.UserAction,
		Extensions:  Extensions(spec),
	}
}

// Extensions returns the RFC 9457 extension map for a spec, or nil when
// the spec has no opt-in metadata. Today this only populates
// `x-i18n-key`, letting the frontend prefer a hand-curated translation
// over the catalog default; new extension members belong here so every
// emitter picks them up at once. Returning nil lets `omitempty` drop
// the member from the payload entirely.
func Extensions(spec *apierr.Spec) map[string]any {
	if spec == nil || spec.I18nKey == "" {
		return nil
	}
	return map[string]any{"x-i18n-key": spec.I18nKey}
}

// Write writes the canonical envelope for spec, using the spec's own
// status as the HTTP status.
func Write(w http.ResponseWriter, spec *apierr.Spec) {
	writeDetails(w, FromSpec(spec))
}

// WriteWithExtensions is [Write] plus additional extension members —
// diagnostic context that is not in the static catalog, such as the
// error slug an identity provider attached to a rejected callback.
// Supplied keys win over the spec's own; a nil or empty map behaves
// exactly like [Write], so callers can build the map conditionally
// without branching.
func WriteWithExtensions(w http.ResponseWriter, spec *apierr.Spec, extra map[string]any) {
	d := FromSpec(spec)
	if len(extra) > 0 {
		merged := make(map[string]any, len(d.Extensions)+len(extra))
		for k, v := range d.Extensions {
			merged[k] = v
		}
		for k, v := range extra {
			merged[k] = v
		}
		d.Extensions = merged
	}
	writeDetails(w, d)
}

// WriteCode writes the canonical envelope for an error that has no
// catalog spec at hand. Shared middleware is the case that needs it:
// authentication and rate limiting run in front of both services and so
// cannot import either one's generated error package, but the codes
// they emit are catalog codes all the same, and the SDK resolves the
// translation from the code. Handlers, which do have the spec, use
// [Write] instead so description and userAction survive.
func WriteCode(w http.ResponseWriter, status int, code, message string) {
	writeDetails(w, Details{
		Type:   code,
		Title:  http.StatusText(status),
		Status: status,
		Detail: message,
	})
}

func writeDetails(w http.ResponseWriter, d Details) {
	w.Header().Set("Content-Type", ContentType)
	w.WriteHeader(d.Status)
	_ = json.NewEncoder(w).Encode(d)
}
