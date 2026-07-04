package apierr

// Shared error codes emitted from packages/go-shared middleware before
// service-local generated error packages can participate.
const (
	CodeRateLimitExceeded = "RATE.LIMIT.EXCEEDED"
)
