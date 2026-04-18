package errors

import (
	"errors"
	"fmt"
)

// Spec describes an error code.
type Spec struct {
	Code    string
	Status  int
	Message string
}

// APIError is the runtime error value carried through the API.
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

// Wrap constructs an APIError from a Spec and an underlying cause.
func Wrap(spec *Spec, cause error) *APIError {
	return &APIError{Spec: spec, Cause: cause}
}

// Common error specs shared across the time-api.
var (
	InternalUnexpected        = &Spec{Code: "INTERNAL.UNEXPECTED", Status: 500, Message: "An unexpected internal error occurred"}
	AuthTokenSignatureInvalid = &Spec{Code: "AUTH.TOKEN.SIGNATURE_INVALID", Status: 401, Message: "Token signature is invalid"}
	AuthSessionRevoked        = &Spec{Code: "AUTH.SESSION.REVOKED", Status: 401, Message: "Session has been revoked"}
	CalendarNotFound          = &Spec{Code: "CALENDAR.NOT_FOUND", Status: 404, Message: "Calendar not found"}
	CalendarAccessDenied      = &Spec{Code: "CALENDAR.ACCESS_DENIED", Status: 403, Message: "You do not have access to this calendar"}

	AuthRegisterPasswordTooWeak  = &Spec{Code: "AUTH.REGISTER.PASSWORD_TOO_WEAK", Status: 422, Message: "Password must be at least 8 characters"}
	AuthRegisterEmailAlreadyTaken = &Spec{Code: "AUTH.REGISTER.EMAIL_ALREADY_TAKEN", Status: 409, Message: "An account with this email already exists"}
	AuthLoginInvalidCredentials  = &Spec{Code: "AUTH.LOGIN.INVALID_CREDENTIALS", Status: 401, Message: "Invalid email or password"}
	AuthLoginAccountLocked       = &Spec{Code: "AUTH.LOGIN.ACCOUNT_LOCKED", Status: 429, Message: "Account temporarily locked due to too many failed attempts"}
	AuthLoginRateLimited         = &Spec{Code: "AUTH.LOGIN.RATE_LIMITED", Status: 429, Message: "Too many failed attempts; account locked for 15 minutes"}
	AuthTokenRefreshInvalid      = &Spec{Code: "AUTH.TOKEN.REFRESH_INVALID", Status: 401, Message: "Refresh token is invalid"}
	AuthTokenRefreshExpired      = &Spec{Code: "AUTH.TOKEN.REFRESH_EXPIRED", Status: 401, Message: "Refresh token has expired"}
)
