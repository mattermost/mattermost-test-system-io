# ==============================================================================
# Rust Report Viewer - Production Dockerfile
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

# Install build dependencies
RUN apt-get update && apt-get install -y \
    pkg-config \
    libssl-dev \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app/server

# Create a dummy project to cache dependencies
RUN cargo init --name rust-report-server
COPY apps/server/Cargo.toml apps/server/Cargo.lock* ./

# Build dependencies only (cache layer)
RUN cargo build --release && rm -rf src target/release/deps/rust_report* target/release/server*

# Copy actual source code
COPY apps/server/src ./src

# Build the actual application
RUN cargo build --release --bin server

# ------------------------------------------------------------------------------
# Stage 3: Production Runtime
# ------------------------------------------------------------------------------
FROM debian:bookworm-slim AS runtime

# Install runtime dependencies
RUN apt-get update && apt-get install -y \
    ca-certificates \
    libssl3 \
    && rm -rf /var/lib/apt/lists/* \
    && useradd -r -s /bin/false appuser

WORKDIR /app

# Copy the compiled binary
COPY --from=backend-builder /app/server/target/release/server /app/server

# Copy frontend static assets
COPY --from=frontend-builder /app/web/dist /app/static

# Create data directories
RUN mkdir -p /app/data/files /app/data/backups \
    && chown -R appuser:appuser /app

# Switch to non-root user
USER appuser

# Environment variables with defaults
ENV RUST_ENV=production \
    RRV_HOST=0.0.0.0 \
    RRV_PORT=8080 \
    RRV_DATABASE_URL=file:/app/data/reports.db \
    RRV_DATA_DIR=/app/data/files \
    RRV_BACKUP_DIR=/app/data/backups \
    RRV_STATIC_DIR=/app/static \
    RUST_LOG=info,actix_web=info

# Expose port
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD ["/app/server", "--health-check"] || exit 1

# Run the server
CMD ["/app/server"]
