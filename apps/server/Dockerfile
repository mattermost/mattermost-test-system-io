# syntax=docker/dockerfile:1.7

# Multi-stage build: node (vite build) → golang builder → distroless/static.
# Both the web bundle (apps/server/internal/webui/dist) and SQL migrations
# (apps/server/migrations) are embedded into the Go binary. Image target: < 60 MB.

# ----- Stage 1: web bundle -----
FROM node:25-alpine AS web-builder

WORKDIR /src/apps/web

# Install deps first for better layer caching; skip postinstall since it needs
# full source + the server embed directory, which we copy next.
COPY apps/web/package.json apps/web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm \
    npm ci --ignore-scripts

# Copy web source + server embed target directory (vite.config.ts writes
# build.outDir = ../server/internal/webui/dist).
COPY apps/web/ ./
COPY apps/server/internal/webui/ /src/apps/server/internal/webui/

RUN npm run build
# Output now lives at /src/apps/server/internal/webui/dist/

# ----- Stage 2: go builder -----
FROM golang:1.26-alpine AS go-builder

ARG VERSION=dev
ARG COMMIT_SHA=unknown
ARG BUILD_TIME=unknown

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /src/apps/server

COPY apps/server/go.mod apps/server/go.sum* ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY apps/server/ ./
# Replace the placeholder dist with the real bundle from the web stage.
COPY --from=web-builder /src/apps/server/internal/webui/dist /src/apps/server/internal/webui/dist

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux \
    go build -trimpath -buildvcs=true \
        -ldflags="-s -w -X main.version=${VERSION} -X main.commitSHA=${COMMIT_SHA} -X main.buildTime=${BUILD_TIME}" \
        -o /out/tsio ./cmd/tsio

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux \
    go build -trimpath -buildvcs=true -ldflags="-s -w" \
        -o /out/tsioctl ./cmd/tsioctl

# ----- Stage 3: runtime -----
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=go-builder /out/tsio /tsio
COPY --from=go-builder /out/tsioctl /tsioctl
COPY --from=go-builder /src/apps/server/api/openapi.yaml /api/openapi.yaml

ENV TSIO_HTTP_LISTEN_ADDR=0.0.0.0:8080
EXPOSE 8080

USER nonroot:nonroot

ENTRYPOINT ["/tsio"]
