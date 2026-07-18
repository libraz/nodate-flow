# syntax=docker/dockerfile:1.7
# Multi-stage build for the nodate-flow scheduled job worker.
# Build context is the repo root so go.work is visible.

FROM golang:1.26-alpine AS build
WORKDIR /src

# Copy workspace + module manifests first for layer caching.
# All modules listed in go.work must be resolvable, even when only
# one is built here.
COPY go.work go.work.sum* ./
COPY apps/flow-worker/go.mod apps/flow-worker/go.sum ./apps/flow-worker/
COPY apps/flow-api/go.mod apps/flow-api/go.sum ./apps/flow-api/
COPY apps/auth-api/go.mod apps/auth-api/go.sum ./apps/auth-api/
COPY packages/go-shared/go.mod packages/go-shared/go.sum ./packages/go-shared/

RUN --mount=type=cache,target=/go/pkg/mod \
    cd apps/flow-worker && go mod download

# Copy the worker and shared source trees.
COPY apps/flow-worker ./apps/flow-worker
COPY packages/go-shared ./packages/go-shared

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd apps/flow-worker && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/flow-worker ./cmd/worker

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build /out/flow-worker /usr/local/bin/flow-worker
USER nonroot:nonroot
# Internal-only Prometheus /metrics listener. Not published to the host
# in compose — prometheus scrapes via the docker network.
EXPOSE 9091
ENTRYPOINT ["/usr/local/bin/flow-worker"]
