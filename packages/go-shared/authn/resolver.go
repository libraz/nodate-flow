package authn

import (
	"context"
	"database/sql"
	"errors"

	"github.com/nodate-flow/nodate-flow/packages/go-shared/dbtype"
)

// ErrTokenInvalid is returned when a bearer token cannot be resolved to
// an authenticated user. Callers should translate this into a 401.
var ErrTokenInvalid = errors.New("authn: invalid or expired token")

// ErrUserDisabled is returned when the token is valid but the user
// account is disabled.
var ErrUserDisabled = errors.New("authn: user account disabled or not found")

// TokenResolver resolves a bearer token string into an authenticated
// user id and optional session public id. Implementations handle a
// specific token type (JWT, PAT, MCP) and return ErrTokenInvalid when
// the token does not match their expected format or fails validation.
type TokenResolver interface {
	// Resolve returns the internal user id and session public id for
	// the given bearer token. The session public id may be zero for
	// token types that do not carry session information (PAT, MCP).
	Resolve(ctx context.Context, token string) (userID uint32, sessionPID dbtype.PublicID, err error)
}

// ResolverDB is the minimal database interface needed by [JWTResolver]
// to look up the internal user id from a public id.
type ResolverDB interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// JWTResolver validates a JWT access token and resolves the embedded
// user public id to an internal user id via a database lookup.
type JWTResolver struct {
	JWT *JWTIssuer
	DB  ResolverDB
}

// Resolve verifies the JWT signature and claims, then looks up the
// internal user id from the users table. Returns [ErrTokenInvalid] on
// signature/claim failure and [ErrUserDisabled] when the user row is
// missing or disabled.
func (r *JWTResolver) Resolve(ctx context.Context, token string) (uint32, dbtype.PublicID, error) {
	if r.JWT == nil || r.DB == nil {
		return 0, dbtype.PublicID{}, ErrTokenInvalid
	}
	claims, err := r.JWT.Verify(token)
	if err != nil {
		return 0, dbtype.PublicID{}, ErrTokenInvalid
	}
	const q = `SELECT id FROM users WHERE public_id = ? AND enabled = TRUE LIMIT 1`
	var uid uint32
	if err := r.DB.QueryRowContext(ctx, q, claims.UserPublicID).Scan(&uid); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, dbtype.PublicID{}, ErrUserDisabled
		}
		return 0, dbtype.PublicID{}, err
	}
	return uid, claims.SessionPublicID, nil
}
