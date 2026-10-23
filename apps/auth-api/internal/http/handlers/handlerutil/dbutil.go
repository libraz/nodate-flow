// Package handlerutil — duplicate-entry detection helper for auth-api
// handlers. Both services classify the MySQL error through
// packages/go-shared/apierr, so they cannot silently diverge.
package handlerutil

import "github.com/libraz/nodate-flow/packages/go-shared/apierr"

// IsDuplicateEntry reports whether err is a unique-key violation. Callers
// in the avatar-upload path use it to distinguish "another concurrent
// upload won the race for this storage_objects row" from a generic insert
// failure. It delegates to [apierr.IsDuplicateEntry], the one reader of
// the driver's error type.
func IsDuplicateEntry(err error) bool {
	return apierr.IsDuplicateEntry(err)
}
