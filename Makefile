.PHONY: sqlc gen-errors gen-sdk

# Generate Go code from SQL using sqlc.
# Requires: go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
sqlc:
	sqlc generate -f sql/sqlc.yaml

# Generate Go + TS error modules, locale stubs, and per-code docs from errors/*.yaml.
# The script lives in its own tiny Go module under scripts/ so its deps
# don't pollute apps/api.
gen-errors:
	go -C scripts run gen-errors.go

# Generate the TypeScript SDK from the Go Huma OpenAPI schema.
# Step 1: dump the merged OpenAPI 3.1 document to packages/sdk/openapi.json
# Step 2: run openapi-typescript to produce packages/sdk/src/openapi.ts
gen-sdk:
	cd apps/api && go run ./cmd/dump-openapi -o ../../packages/sdk/openapi.json
	cd packages/sdk && npx --yes openapi-typescript openapi.json -o src/openapi.ts
