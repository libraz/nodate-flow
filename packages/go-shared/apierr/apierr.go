// Package apierr provides the base error types (Spec, APIError) and
// constructors shared by all backend services. Domain-specific error
// specs remain in each service's own errors package.
package apierr

import (
	"errors"
	"fmt"
)

// Spec describes an error code defined in errors/*.yaml.
//
// Description is a developer-facing explanation of when the code is
// emitted; UserAction is a short imperative sentence the UI can render
// to tell the end user how to recover. Both are populated from the
// errors/*.yaml source by gen-errors and surfaced on the wire so
// frontends can localise toast bodies without round-tripping the
// catalog.
type Spec struct {
	Code        string
	Status      int
	Message     string
	Description string
	UserAction  string
}

// APIError is the runtime error value carried through the API. It wraps a
// Spec and optional details for structured rendering.
type APIError struct {
	Spec    *Spec
	Details map[string]any
	Cause   error
}

// Error implements the error interface.
func (e *APIError) Error() string {
	if e == nil || e.Spec == nil {
		return "<nil api error>"
	}
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Spec.Code, e.Spec.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Spec.Code, e.Spec.Message)
}

// Unwrap exposes the underlying cause for errors.Is / errors.As chains.
func (e *APIError) Unwrap() error { return e.Cause }

// Is reports whether target is an APIError carrying the same Spec pointer.
func (e *APIError) Is(target error) bool {
	var other *APIError
	if !errors.As(target, &other) {
		return false
	}
	return other != nil && other.Spec == e.Spec
}

// New constructs an APIError from a Spec.
func New(spec *Spec) *APIError {
	return &APIError{Spec: spec}
}

// Newf constructs an APIError from a Spec and wraps a formatted cause.
func Newf(spec *Spec, format string, args ...any) *APIError {
	return &APIError{Spec: spec, Cause: fmt.Errorf(format, args...)}
}

// Wrap constructs an APIError from a Spec and an underlying cause.
func Wrap(spec *Spec, cause error) *APIError {
	return &APIError{Spec: spec, Cause: cause}
}

// WithDetail returns a copy of the error with an added detail key.
func (e *APIError) WithDetail(key string, value any) *APIError {
	d := make(map[string]any, len(e.Details)+1)
	for k, v := range e.Details {
		d[k] = v
	}
	d[key] = value
	return &APIError{Spec: e.Spec, Details: d, Cause: e.Cause}
}
