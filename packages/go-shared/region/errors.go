package region

import "errors"

// ErrInvalidTimezone is returned when a string is not a resolvable IANA
// timezone identifier. Handlers should map this to a 400 Bad Request with
// the appropriate error code.
var ErrInvalidTimezone = errors.New("invalid IANA timezone")

// ErrInvalidCountry is returned when a string is not a supported ISO 3166-1
// alpha-2 country code. The handler layer is responsible for translating this
// into an API error.
var ErrInvalidCountry = errors.New("invalid ISO 3166-1 alpha-2 country code")
