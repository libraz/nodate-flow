.PHONY: sqlc gen-errors

# Generate Go code from SQL using sqlc.
# Requires: go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
sqlc:
	sqlc generate -f sql/sqlc.yaml

# Generate Go + TS error modules, locale stubs, and per-code docs from errors/*.yaml.
# The script lives in its own tiny Go module under scripts/ so its deps
# don't pollute apps/api.
gen-errors:
	go -C scripts run gen-errors.go
