# ==============================================================================
# Test System IO - Production Dockerfile
# ==============================================================================
# Multi-stage build using cargo-chef for reliable dependency caching
# Final image: ~100MB (Rust binary + static assets + minimal runtime)
# ==============================================================================

# ------------------------------------------------------------------------------
# Stage 1: Build Frontend (Node.js)
# ------------------------------------------------------------------------------
FROM node:24-alpine AS frontend-builder

WORKDIR /app/web

# Install dependencies first (cache layer)
COPY apps/web/package.json apps/web/package-lock.json* ./
RUN npm ci --ignore-scripts

# Copy source and build
COPY apps/web/ ./
RUN npm run build

# ------------------------------------------------------------------------------
# Stage 2: Chef — install cargo-chef on the Rust base image
# ------------------------------------------------------------------------------
FROM rust:1.93.1-slim-trixie AS chef

RUN cargo install cargo-chef

# Install build dependencies
RUN rm -rf /var/lib/apt/lists/* \
    && apt-get update && apt-get install -y \
    pkg-config \
    libssl-dev \
    curl \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app/server

# ------------------------------------------------------------------------------
# Stage 3: Planner — generate recipe.json (dependency-only build plan)
# ------------------------------------------------------------------------------
FROM chef AS planner

COPY apps/server/ .
RUN cargo chef prepare --recipe-path recipe.json

# ------------------------------------------------------------------------------
# Stage 4: Builder — cook dependencies (cached), then build source
# ------------------------------------------------------------------------------
FROM chef AS builder

# Cook dependencies from recipe (only re-runs when Cargo.toml/Cargo.lock change)
COPY --from=planner /app/server/recipe.json recipe.json
RUN --mount=type=cache,target=/usr/local/cargo/registry \
    --mount=type=cache,target=/usr/local/cargo/git \
    cargo chef cook --release --recipe-path recipe.json

# Copy actual source code and build
COPY apps/server/ .
RUN --mount=type=cache,target=/usr/local/cargo/registry \
    --mount=type=cache,target=/usr/local/cargo/git \
    cargo build --release --bin mattermost-tsio

# ------------------------------------------------------------------------------
# Stage 5: Production Runtime
# ------------------------------------------------------------------------------
# Build info args (passed from CI/CD workflow)
ARG BUILD_VERSION=dev
ARG BUILD_SHA=unknown
ARG BUILD_TIME=unknown

FROM debian:trixie-slim AS runtime

# Install runtime dependencies
RUN apt-get update && apt-get install -y \
    ca-certificates \
    libssl3t64 \
    && rm -rf /var/lib/apt/lists/* \
    && useradd -r -s /bin/false appuser

WORKDIR /app

# Copy the compiled binary
COPY --from=builder /app/server/target/release/mattermost-tsio /app/mattermost-tsio

# Copy frontend static assets
COPY --from=frontend-builder /app/web/dist /app/static

# Set ownership
RUN chown -R appuser:appuser /app

# Switch to non-root user
USER appuser

# Re-declare ARGs after FROM (Docker resets ARGs across stages)
ARG BUILD_VERSION=dev
ARG BUILD_SHA=unknown
ARG BUILD_TIME=unknown

# Environment variables with defaults
ENV RUST_ENV=production \
    TSIO_SERVER_HOST=0.0.0.0 \
    TSIO_SERVER_PORT=8080 \
    TSIO_SERVER_STATIC_DIR=/app/static \
    TSIO_COMMIT_SHA=${BUILD_SHA} \
    TSIO_BUILD_TIME=${BUILD_TIME} \
    RUST_LOG=info,actix_web=info

# Expose port
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD ["/app/mattermost-tsio", "--health-check"] || exit 1

# Run the server
CMD ["/app/mattermost-tsio"]
