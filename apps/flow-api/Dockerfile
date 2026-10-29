# syntax=docker/dockerfile:1.7
# Multi-stage build for the nodate-flow Go API.
# Build context is the repo root so go.work is visible.

FROM golang:1.27-alpine AS build
WORKDIR /src

# Copy workspace + module manifests first for layer caching.
# go mod download in workspace mode loads every module listed in go.work,
# so all five go.mod/go.sum pairs are needed even though one is built.
COPY go.work go.work.sum ./
COPY apps/auth-api/go.mod apps/auth-api/go.sum ./apps/auth-api/
COPY apps/flow-api/go.mod apps/flow-api/go.sum ./apps/flow-api/
COPY apps/flow-worker/go.mod apps/flow-worker/go.sum ./apps/flow-worker/
COPY apps/presence-discord/go.mod apps/presence-discord/go.sum ./apps/presence-discord/
COPY packages/go-shared/go.mod packages/go-shared/go.sum ./packages/go-shared/

RUN --mount=type=cache,target=/go/pkg/mod \
    cd apps/flow-api && go mod download

# Copy the api and shared source trees.
COPY apps/flow-api ./apps/flow-api
COPY packages/go-shared ./packages/go-shared

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd apps/flow-api && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

# Tiny health-check binary for distroless (no curl/wget available).
# Exits 0 when GET /health returns 200, non-zero otherwise.
COPY apps/flow-api/cmd/healthcheck/main.go /tmp/healthcheck/main.go
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/healthcheck /tmp/healthcheck/main.go

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build /out/api /api
COPY --from=build /out/healthcheck /healthcheck
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/api"]
