# Local Deployment Guide

Run the production Docker image locally for testing and validation.

## Prerequisites

- Docker installed and running
- Git (to get the commit SHA)

## Quick Start

```bash
# 1. Start PostgreSQL and MinIO
docker compose -f docker/docker-compose.dev.yml up -d

# 2. Build the Docker image
docker build \
  --build-arg BUILD_SHA=$(git rev-parse HEAD) \
  --build-arg BUILD_TIME=$(date -u +"%Y-%m-%dT%H:%M:%SZ") \
  -t mattermost-test-system-io:local .

# 3. Run the container
docker run --rm -p 8080:8080 \
  --network docker_default \
  -e RUST_ENV=development \
  -e TSIO_DB_URL=postgres://tsio:tsio@postgres:5432/tsio \
  -e TSIO_S3_ENDPOINT=http://minio:9000 \
  -e TSIO_S3_BUCKET=reports \
  -e TSIO_S3_ACCESS_KEY=minioadmin \
  -e TSIO_S3_SECRET_KEY=minioadmin \
  -e TSIO_S3_REGION=us-east-1 \
  mattermost-test-system-io:local

# 4. Verify
curl http://localhost:8080/api/v1/health
curl http://localhost:8080/api/v1/info
```

## Step-by-Step

### 1. Start backing services

PostgreSQL and MinIO are required. Start them with docker compose:

```bash
docker compose -f docker/docker-compose.dev.yml up -d
```

Wait for healthy status:

```bash
docker compose -f docker/docker-compose.dev.yml ps
```

| Service | Port | Purpose |
|---------|------|---------|
| PostgreSQL | `localhost:6432` | Database (mapped from 5432) |
| MinIO | `localhost:9100` (API), `localhost:9101` (Console) | S3-compatible object storage |
| Adminer | `localhost:8081` | Database admin UI |

### 2. Build the Docker image

```bash
docker build \
  --build-arg BUILD_SHA=$(git rev-parse HEAD) \
  --build-arg BUILD_TIME=$(date -u +"%Y-%m-%dT%H:%M:%SZ") \
  -t mattermost-test-system-io:local .
```

Build args inject metadata into the image:

| Arg | Value | Shows up in |
|-----|-------|-------------|
| `BUILD_SHA` | Full git commit SHA | `/api/v1/info` → `commit_sha` |
| `BUILD_TIME` | Current UTC timestamp | `/api/v1/info` → `build_time` |

First build takes ~5 minutes (Rust compilation). Subsequent builds use Docker layer caching and are faster.

### 3. Run the container

The container needs to connect to PostgreSQL and MinIO running in Docker. Use `--network docker_default` to join the compose network:

```bash
docker run --rm -p 8080:8080 \
  --network docker_default \
  -e RUST_ENV=development \
  -e TSIO_DB_URL=postgres://tsio:tsio@postgres:5432/tsio \
  -e TSIO_S3_ENDPOINT=http://minio:9000 \
  -e TSIO_S3_BUCKET=reports \
  -e TSIO_S3_ACCESS_KEY=minioadmin \
  -e TSIO_S3_SECRET_KEY=minioadmin \
  -e TSIO_S3_REGION=us-east-1 \
  mattermost-test-system-io:local
```

> **Note**: Use `RUST_ENV=development` for local Docker testing. The app validates that production credentials aren't using dev defaults — running with `RUST_ENV=production` and dev credentials (e.g., `minioadmin`) will fail.

> **Note**: Inside the Docker network, services are referenced by their compose service name (`postgres`, `minio`), not `localhost`.

### 4. Verify

```bash
# Health check (always 200)
curl http://localhost:8080/api/v1/health

# Readiness (checks DB connectivity)
curl http://localhost:8080/api/v1/ready

# Build info
curl http://localhost:8080/api/v1/info

# Frontend UI
open http://localhost:8080
```

Expected `/api/v1/info` response:

```json
{
  "server_version": "0.1.0",
  "environment": "production",
  "repo_url": "https://github.com/mattermost/mattermost-test-system-io",
  "commit_sha": "abc1234...",
  "build_time": "2026-02-22T12:00:00Z"
}
```

## Run with env file

For convenience, create a `.env.docker` file (gitignored):

```bash
RUST_ENV=development
TSIO_DB_URL=postgres://tsio:tsio@postgres:5432/tsio
TSIO_S3_ENDPOINT=http://minio:9000
TSIO_S3_BUCKET=reports
TSIO_S3_ACCESS_KEY=minioadmin
TSIO_S3_SECRET_KEY=minioadmin
TSIO_S3_REGION=us-east-1
```

Then run:

```bash
docker run --rm -p 8080:8080 \
  --network docker_default \
  --env-file .env.docker \
  mattermost-test-system-io:local
```

## Run in background

```bash
# Start
docker run -d --name tsio -p 8080:8080 \
  --network docker_default \
  --env-file .env.docker \
  mattermost-test-system-io:local

# View logs
docker logs -f tsio

# Stop
docker stop tsio && docker rm tsio
```

## Build without cache

If you need a completely clean build (e.g., dependency changes):

```bash
docker build --no-cache \
  --build-arg BUILD_SHA=$(git rev-parse HEAD) \
  --build-arg BUILD_TIME=$(date -u +"%Y-%m-%dT%H:%M:%SZ") \
  -t mattermost-test-system-io:local .
```

## Cleanup

```bash
# Stop backing services
docker compose -f docker/docker-compose.dev.yml down

# Stop backing services and remove data
docker compose -f docker/docker-compose.dev.yml down -v

# Remove the built image
docker rmi mattermost-test-system-io:local
```
