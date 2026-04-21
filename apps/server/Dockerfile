# syntax=docker/dockerfile:1.7

# Multi-stage Go build: builder (golang:1.26-alpine) → distroless/static.
# Image target: < 40 MB.

FROM golang:1.26-alpine AS builder

ARG VERSION=dev
ARG COMMIT_SHA=unknown
ARG BUILD_TIME=unknown

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /src

# Cache deps layer
COPY go.mod go.sum* ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux \
    go build \
        -trimpath \
        -buildvcs=true \
        -ldflags="-s -w -X main.version=${VERSION} -X main.commitSHA=${COMMIT_SHA} -X main.buildTime=${BUILD_TIME}" \
        -o /out/tsio \
        ./cmd/tsio

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux \
    go build \
        -trimpath \
        -buildvcs=true \
        -ldflags="-s -w" \
        -o /out/tsioctl \
        ./cmd/tsioctl

FROM gcr.io/distroless/static-debian12:nonroot

# Migrations are embedded into the binaries via apps/server/migrations/embed.go
# (see internal/db/migrate.go). No migrations/ directory is shipped in the
# image — the binary is self-contained.
COPY --from=builder /out/tsio /tsio
COPY --from=builder /out/tsioctl /tsioctl
COPY --from=builder /src/api/openapi.yaml /api/openapi.yaml

ENV TSIO_HTTP_LISTEN_ADDR=0.0.0.0:8080
EXPOSE 8080

USER nonroot:nonroot

ENTRYPOINT ["/tsio"]
