// Package sessionstore is the driver abstraction for refresh-token
// backed sessions. The default driver is MySQL (wraps the existing
// sqlc [generated.Queries]); a Redis driver lives behind the `redis`
// build tag so the binary can be compiled without pulling go-redis
// into the default dependency graph.
//
// Usage from cmd/api:
//
//	var store sessionstore.Store
//	switch cfg.SessionStore {
//	case "redis":
//	    store = sessionstore.NewRedisStore(...)   // requires -tags redis
//	default:
//	    store = sessionstore.NewMySQLStore(queries)
//	}
//
// Handlers depend only on the [Store] interface; they never import
// generated.Queries for sessions.
package sessionstore

import (
	"context"
	"errors"
	"time"

	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
)

// ErrNotFound is returned when a lookup finds no matching session.
// Callers map this to the auth.session.not_found error code.
var ErrNotFound = errors.New("sessionstore: session not found")

// Session is the driver-neutral representation of a refresh-token
// session. It intentionally avoids sql.NullXxx so Redis and other
// non-SQL drivers can map it without importing database/sql.
type Session struct {
	InternalID  uint32          // Implementation-specific; zero for stores without integer PKs.
	PublicID    dbtype.PublicID // UUID v7, the only externally visible id.
	UserID      uint32
	RefreshHash string // SHA-256 hex of the refresh token plaintext.
	UserAgent   string
	IPAddress   string
	ExpiresAt   time.Time
	RevokedAt   *time.Time // nil when active.
	LastUsedAt  *time.Time
	CreatedAt   time.Time
}

// CreateParams is the narrow input shape for [Store.Create]. It
// mirrors the required columns so callers do not need to populate a
// full [Session] value with zero fields.
type CreateParams struct {
	PublicID    dbtype.PublicID
	UserID      uint32
	RefreshHash string
	UserAgent   string
	IPAddress   string
	ExpiresAt   time.Time
}

// Store is the driver interface. Implementations must be safe for
// concurrent use by multiple goroutines.
type Store interface {
	// Create inserts a new active session. Returns the internal id
	// (or 0 for non-SQL drivers) on success.
	Create(ctx context.Context, p CreateParams) (uint32, error)

	// FindByRefreshHash looks up an active (not-revoked, enabled)
	// session by its SHA-256 refresh hash. Returns [ErrNotFound]
	// when no row matches.
	FindByRefreshHash(ctx context.Context, hash string) (*Session, error)

	// FindAnyByRefreshHash looks up a session by its SHA-256 refresh
	// hash regardless of its revoked / enabled state. It powers
	// refresh-token reuse detection: a hash that resolves only to a
	// revoked (rotated) row means a previously-invalidated token was
	// replayed. Returns [ErrNotFound] when no row at all matches.
	FindAnyByRefreshHash(ctx context.Context, hash string) (*Session, error)

	// RotateRefreshHash replaces the refresh hash and expiry on the
	// session identified by [Session.InternalID] (MySQL) or
	// [Session.RefreshHash] (Redis; the implementation may rekey).
	RotateRefreshHash(ctx context.Context, oldHash, newHash string, expiresAt time.Time) error

	// Revoke marks a session as revoked by (userID, publicID). A
	// missing row is not an error; callers treat it as idempotent.
	Revoke(ctx context.Context, userID uint32, publicID dbtype.PublicID) error

	// ListActive returns every active session for a user ordered by
	// most recent first. Used by the /settings/security sessions list.
	ListActive(ctx context.Context, userID uint32) ([]Session, error)

	// RevokeAllExcept revokes every active session for a user except
	// the one whose publicID matches `keep`. Used by "sign out of all
	// other devices". A missing match is not an error.
	RevokeAllExcept(ctx context.Context, userID uint32, keep dbtype.PublicID) error

	// RevokeAllForUser revokes every active session for a user. Used by
	// the refresh-token reuse detector to tear down the whole session
	// family the moment a rotated token is replayed. A user with no
	// active session is not an error.
	RevokeAllForUser(ctx context.Context, userID uint32) error
}
