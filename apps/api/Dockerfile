# syntax=docker/dockerfile:1.7
# Multi-stage build for the nodate-flow Go API.
# Build context is the repo root so go.work is visible.

FROM golang:1.25-alpine AS build
WORKDIR /src

# Copy workspace + module manifests first for layer caching.
COPY go.work go.work.sum* ./
COPY apps/api/go.mod apps/api/go.sum ./apps/api/

RUN --mount=type=cache,target=/go/pkg/mod \
    cd apps/api && go mod download

# Copy the api source tree.
COPY apps/api ./apps/api

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd apps/api && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build /out/api /api
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/api"]
