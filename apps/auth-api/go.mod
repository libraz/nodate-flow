module github.com/nodate-flow/nodate-flow/apps/auth-api

go 1.25.0

replace github.com/nodate-flow/nodate-flow/packages/go-shared => ../../packages/go-shared

require (
	github.com/caarlos0/env/v11 v11.4.0
	github.com/coreos/go-oidc/v3 v3.18.0
	github.com/danielgtaylor/huma/v2 v2.37.3
	github.com/go-chi/chi/v5 v5.2.5
	github.com/go-chi/cors v1.2.2
	github.com/go-sql-driver/mysql v1.9.3
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/google/uuid v1.6.0
	github.com/nodate-flow/nodate-flow/packages/go-shared v0.0.0-00010101000000-000000000000
	golang.org/x/crypto v0.50.0
	golang.org/x/oauth2 v0.36.0
)

require (
	cloud.google.com/go/compute/metadata v0.3.0 // indirect
	filippo.io/edwards25519 v1.1.0 // indirect
	github.com/go-jose/go-jose/v4 v4.1.4 // indirect
	golang.org/x/sys v0.43.0 // indirect
)
