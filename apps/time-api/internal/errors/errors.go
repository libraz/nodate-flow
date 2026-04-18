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

// Common error specs shared across the time-api.
var (
	InternalUnexpected        = &Spec{Code: "INTERNAL.UNEXPECTED", Status: 500, Message: "An unexpected internal error occurred"}
	AuthTokenSignatureInvalid = &Spec{Code: "AUTH.TOKEN.SIGNATURE_INVALID", Status: 401, Message: "Token signature is invalid"}
	AuthSessionRevoked        = &Spec{Code: "AUTH.SESSION.REVOKED", Status: 401, Message: "Session has been revoked"}
	CalendarNotFound          = &Spec{Code: "CALENDAR.NOT_FOUND", Status: 404, Message: "Calendar not found"}
	CalendarAccessDenied      = &Spec{Code: "CALENDAR.ACCESS_DENIED", Status: 403, Message: "You do not have access to this calendar"}

	AuthRegisterPasswordTooWeak   = &Spec{Code: "AUTH.REGISTER.PASSWORD_TOO_WEAK", Status: 422, Message: "Password must be at least 8 characters"}
	AuthRegisterEmailAlreadyTaken = &Spec{Code: "AUTH.REGISTER.EMAIL_ALREADY_TAKEN", Status: 409, Message: "An account with this email already exists"}
	AuthLoginInvalidCredentials   = &Spec{Code: "AUTH.LOGIN.INVALID_CREDENTIALS", Status: 401, Message: "Invalid email or password"}
	AuthLoginAccountLocked        = &Spec{Code: "AUTH.LOGIN.ACCOUNT_LOCKED", Status: 429, Message: "Account temporarily locked due to too many failed attempts"}
	AuthLoginRateLimited          = &Spec{Code: "AUTH.LOGIN.RATE_LIMITED", Status: 429, Message: "Too many failed attempts; account locked for 15 minutes"}
	AuthTokenRefreshInvalid       = &Spec{Code: "AUTH.TOKEN.REFRESH_INVALID", Status: 401, Message: "Refresh token is invalid"}
	AuthTokenRefreshExpired       = &Spec{Code: "AUTH.TOKEN.REFRESH_EXPIRED", Status: 401, Message: "Refresh token has expired"}
)
