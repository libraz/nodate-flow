# syntax=docker/dockerfile:1.7
# Multi-stage build for the nodate-flow auth API.
# Build context is the repo root so go.work is visible.

FROM golang:1.25-alpine AS build
WORKDIR /src

# Copy workspace + module manifests first for layer caching.
COPY go.work go.work.sum* ./
COPY apps/auth-api/go.mod apps/auth-api/go.sum ./apps/auth-api/
COPY packages/go-shared/go.mod packages/go-shared/go.sum ./packages/go-shared/

RUN --mount=type=cache,target=/go/pkg/mod \
    cd apps/auth-api && go mod download

# Copy the api and shared source trees.
COPY apps/auth-api ./apps/auth-api
COPY packages/go-shared ./packages/go-shared

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd apps/auth-api && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

# Tiny health-check binary for distroless (no curl/wget available).
COPY apps/auth-api/cmd/healthcheck/main.go /tmp/healthcheck/main.go
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/healthcheck /tmp/healthcheck/main.go

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build /out/api /api
COPY --from=build /out/healthcheck /healthcheck
USER nonroot:nonroot
EXPOSE 8082
ENTRYPOINT ["/api"]
