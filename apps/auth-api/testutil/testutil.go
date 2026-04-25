// Package testutil provides a test-only helper for constructing an
// auth-api HTTP handler. It exists outside internal/ so that sibling
// modules (flow-api tests) can compose the auth-api router into their
// own test servers without duplicating the wiring.
package testutil

import (
	"crypto/ed25519"
	"database/sql"
	"net/http"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/http/router"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/crypto"
)

// BuildTestRouter constructs a fully-wired auth-api HTTP handler
// suitable for embedding in an httptest.Server alongside the flow-api
// router. The caller supplies a shared DB and JWT key so both
// routers validate each other's tokens.
func BuildTestRouter(db *sql.DB, jwtKey ed25519.PrivateKey, cipherKey []byte) (http.Handler, error) {
	queries := generated.New(db)
	jwtIssuer, err := auth.NewJWTIssuer(jwtKey, "nodate-flow", "api", 15*time.Minute)
	if err != nil {
		return nil, err
	}
	var cipher *crypto.Cipher
	if len(cipherKey) > 0 {
		cipher, err = crypto.New(cipherKey)
		if err != nil {
			return nil, err
		}
	}
	return router.Build(router.Deps{
		DB:               db,
		Queries:          queries,
		JWT:              jwtIssuer,
		Cipher:           cipher,
		CookieSecure:     false,
		RegistrationOpen: true,
		DisableRateLimit: true,
	}), nil
}
