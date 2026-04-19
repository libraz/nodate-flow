package errors

import "github.com/nodate-flow/nodate-flow/packages/go-shared/apierr"

// Spec describes an error code.
type Spec = apierr.Spec

// APIError is the runtime error value carried through the API.
type APIError = apierr.APIError

// New constructs an APIError from a Spec.
var New = apierr.New

// Wrap constructs an APIError from a Spec and an underlying cause.
var Wrap = apierr.Wrap

// Newf constructs an APIError from a Spec and wraps a formatted cause.
var Newf = apierr.Newf

// Error specs are now generated from errors/*.yaml into per-domain files
// (auth.go, calendar.go, internal.go, etc.) by gen-errors.
