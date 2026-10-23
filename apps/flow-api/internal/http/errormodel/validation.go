package errormodel

import (
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/validation"

	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

// classify gives a refusal Huma raised before the handler ran the
// catalog code a client branches on, and the status that code carries.
// Huma leaves type empty, so without this every schema failure reaches
// the client with nothing machine-readable in it.
func classify(em *huma.ErrorModel) {
	if em.Type != "" && em.Type != "about:blank" {
		return
	}
	spec := validationSpec(em.Status, em.Errors)
	if spec == nil {
		return
	}
	em.Type = spec.Code
	em.Status = spec.Status
	em.Title = http.StatusText(spec.Status)
}

// validationSpec maps a request-validation refusal to its catalog code
// from the status Huma chose and the locations its details name, or nil
// when the status is not one request validation produces.
//
// Header and cookie parameters have no code of their own; they are
// request parameters like a query member and share its code.
func validationSpec(status int, details []*huma.ErrorDetail) *apierr.Spec {
	switch status {
	case http.StatusUnsupportedMediaType:
		return apierrors.ValidationBodyUnparseable
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
	default:
		return nil
	}
	path, param, body, missingOnly := false, false, false, true
	for _, d := range details {
		if d == nil {
			continue
		}
		section, _, _ := strings.Cut(d.Location, ".")
		switch section {
		case "path":
			path = true
		case "query", "header", "cookie":
			param = true
		default:
			body = true
			if !strings.HasPrefix(d.Message, requiredPropertyPrefix) {
				missingOnly = false
			}
		}
	}
	switch {
	// A 400 against the body is Huma failing to parse it, or the body
	// being absent where one is required.
	case status == http.StatusBadRequest && !path && !param:
		return apierrors.ValidationBodyUnparseable
	case path:
		return apierrors.ValidationPathParamInvalid
	case param:
		return apierrors.ValidationQueryFieldInvalid
	case body && missingOnly:
		return apierrors.ValidationBodyFieldMissing
	default:
		return apierrors.ValidationBodyFieldInvalid
	}
}

// requiredPropertyPrefix is the opening of the message Huma reports a
// missing required property with, up to the property name.
var requiredPropertyPrefix = strings.SplitN(validation.MsgExpectedRequiredProperty, "%s", 2)[0]
