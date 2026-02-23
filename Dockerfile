# ==============================================================================
# Test System IO - Production Dockerfile
# ==============================================================================
# Multi-stage build for minimal image size
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
# Stage 2: Build Backend (Rust)
# ------------------------------------------------------------------------------
FROM rust:1.92-slim-bookworm AS backend-builder

# Install build dependencies (clear stale apt lists from base image first)
RUN rm -rf /var/lib/apt/lists/* \
    && apt-get update && apt-get install -y \
    pkg-config \
    libssl-dev \
    curl \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app/server

# Create dummy project to cache dependencies
RUN cargo init --name tsio --lib
RUN echo 'fn main() {}' > src/main.rs && \
    mkdir -p src/bin && \
    echo 'fn main() {}' > src/bin/generate_api_key.rs && \
    echo 'fn main() {}' > src/bin/manage_api_keys.rs
COPY apps/server/Cargo.toml apps/server/Cargo.lock* ./

# Build dependencies only (cache layer)
RUN cargo build --release && rm -rf src target/release/deps/mattermost_tsio* target/release/mattermost-tsio* target/release/deps/libtsio*

# Copy actual source code
COPY apps/server/src ./src

# Build the actual application
RUN cargo build --release --bin mattermost-tsio

# ------------------------------------------------------------------------------
# Stage 3: Production Runtime
# ------------------------------------------------------------------------------
# Build info args (passed from CI/CD workflow)
ARG BUILD_VERSION=dev
ARG BUILD_SHA=unknown
ARG BUILD_TIME=unknown

FROM debian:bookworm-slim AS runtime

# Install runtime dependencies
RUN apt-get update && apt-get install -y \
    ca-certificates \
    libssl3 \
    && rm -rf /var/lib/apt/lists/* \
    && useradd -r -s /bin/false appuser

WORKDIR /app

# Copy the compiled binary
COPY --from=backend-builder /app/server/target/release/mattermost-tsio /app/mattermost-tsio

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
