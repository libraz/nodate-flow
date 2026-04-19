// Package sessionstore re-exports the shared sessionstore types and
// provides app-specific adapters that bridge sqlc-generated query
// bundles to the shared [sessionstore.SessionQueries] interface.
//
// Handlers depend only on the [Store] interface; they never import
// generated.Queries for sessions.
package sessionstore

import (
	ss "github.com/nodate-flow/nodate-flow/packages/go-shared/sessionstore"
)

// Re-export shared types so existing callers compile without changes.

// ErrNotFound is returned when a lookup finds no matching session.
var ErrNotFound = ss.ErrNotFound

// Session is the driver-neutral representation of a refresh-token session.
type Session = ss.Session

// CreateParams is the narrow input shape for [Store.Create].
type CreateParams = ss.CreateParams

// Store is the driver interface.
type Store = ss.Store
