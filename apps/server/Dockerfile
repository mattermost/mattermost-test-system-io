# Build web assets
FROM node:22-slim AS web-builder

WORKDIR /app/web

# Copy web package files
COPY apps/web/package*.json ./

# Install dependencies
RUN npm ci

# Copy web source
COPY apps/web/ ./

# Build web assets
RUN npm run build

# Build Rust server
FROM rust:1.84-slim-bookworm AS server-builder

WORKDIR /app

# Install build dependencies
RUN apt-get update && apt-get install -y pkg-config libssl-dev && rm -rf /var/lib/apt/lists/*

# Copy server manifests
COPY apps/server/Cargo.toml apps/server/Cargo.lock ./

# Create dummy main.rs to build dependencies
RUN mkdir src && echo "fn main() {}" > src/main.rs

# Build dependencies (this layer will be cached)
RUN cargo build --release && rm -rf src target/release/deps/rust_report_server*

# Copy actual source code
COPY apps/server/src ./src

# Build the application
RUN cargo build --release

# Runtime stage
FROM debian:bookworm-slim

WORKDIR /app

# Install runtime dependencies
RUN apt-get update && apt-get install -y ca-certificates libssl3 && rm -rf /var/lib/apt/lists/*

# Copy the binary from server builder
COPY --from=server-builder /app/target/release/server /app/server

# Copy web assets from web builder
COPY --from=web-builder /app/web/dist /app/static

# Create data directory
RUN mkdir -p /app/data

# Expose port
EXPOSE 8080

# Run the binary
CMD ["/app/server"]
