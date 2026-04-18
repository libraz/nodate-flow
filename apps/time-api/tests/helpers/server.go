package helpers

import (
	"database/sql"
	"fmt"
	"net/http/httptest"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/http/router"
)

// TestServer is a running httptest.Server bound to the time-api route set.
type TestServer struct {
	BaseURL string
	Server  *httptest.Server
	DB      *sql.DB
	JWT     *auth.JWTIssuer
}

// NewTestServer boots an httptest.Server with the full time-api router.
// Callers must invoke the returned cleanup func when done.
func NewTestServer(db *sql.DB) (*TestServer, func(), error) {
	if db == nil {
		return nil, nil, fmt.Errorf("NewTestServer requires a non-nil *sql.DB")
	}

	jwtIssuer, err := auth.NewJWTIssuer(nil, "nodate-flow", "api", 15*time.Minute)
	if err != nil {
		return nil, nil, fmt.Errorf("init jwt issuer: %w", err)
	}

	handler := router.Build(router.Deps{
		DB:  db,
		JWT: jwtIssuer,
	})

	srv := httptest.NewServer(handler)
	cleanup := func() { srv.Close() }

	return &TestServer{
		BaseURL: srv.URL,
		Server:  srv,
		DB:      db,
		JWT:     jwtIssuer,
	}, cleanup, nil
}
