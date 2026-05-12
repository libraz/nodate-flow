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
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/storage"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/crypto"
)

// BuildTestRouter constructs a fully-wired auth-api HTTP handler
// suitable for embedding in an httptest.Server alongside the flow-api
// router. The caller supplies a shared DB and JWT key so both
// routers validate each other's tokens.
//
// Storage is left nil; the avatar handlers degrade to
// AUTH.AVATAR.STORAGE_UNAVAILABLE. Callers that need real avatar
// upload integration should use BuildTestRouterWithStorage instead.
func BuildTestRouter(db *sql.DB, jwtKey ed25519.PrivateKey, cipherKey []byte) (http.Handler, error) {
	return BuildTestRouterWithStorage(db, jwtKey, cipherKey, nil)
}

// BuildTestRouterWithStorage is the same as BuildTestRouter but additionally
// wires the supplied storage client into router.Deps so the avatar
// upload / delete / proxy endpoints exercise the real MinIO path.
func BuildTestRouterWithStorage(db *sql.DB, jwtKey ed25519.PrivateKey, cipherKey []byte, storageClient *storage.Client) (http.Handler, error) {
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
		Storage:          storageClient,
	}), nil
}

// StorageConfig mirrors the connection parameters auth-api's
// internal storage client needs. Re-declared in this exported package
// so callers (notably the flow-api integration test helpers) can build
// an auth-api storage handle without importing apps/auth-api/internal.
type StorageConfig struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
	UseSSL    bool
}

// StorageClient is an opaque alias for the auth-api storage handle.
// Callers receive it from BuildStorageClient and pass it back into
// BuildTestRouterWithStorage; they do not need to dereference its
// methods directly.
type StorageClient = storage.Client

// BuildStorageClient constructs an auth-api storage client and ensures
// the configured bucket exists. The returned *StorageClient (alias of
// the internal type) is suitable for passing to
// BuildTestRouterWithStorage without leaking the internal package.
func BuildStorageClient(cfg StorageConfig) (*StorageClient, error) {
	c, err := storage.NewClient(storage.Config{
		Endpoint:  cfg.Endpoint,
		AccessKey: cfg.AccessKey,
		SecretKey: cfg.SecretKey,
		Bucket:    cfg.Bucket,
		UseSSL:    cfg.UseSSL,
	})
	if err != nil {
		return nil, err
	}
	return c, nil
}
