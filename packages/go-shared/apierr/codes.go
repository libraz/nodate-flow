package apierr

// Shared error codes emitted from packages/go-shared middleware before
// service-local generated error packages can participate.
// Each constant mirrors an entry in errors/*.yaml; the services assert
// the two agree (see the catalog tests in each service's middleware
// package) so a rename in the catalog cannot leave a stale literal
// here.
const (
	CodeRateLimitExceeded = "RATE.LIMIT.EXCEEDED"

	// CodeInternalUnexpected is the code a shared writer falls back to
	// when it is handed no spec at all.
	CodeInternalUnexpected = "INTERNAL.UNEXPECTED"
)
