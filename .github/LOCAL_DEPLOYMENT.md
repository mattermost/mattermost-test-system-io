# Local Deployment Guide

Run the production Docker image locally for testing and validation.

## Prerequisites

- Docker installed and running
- Git (to get the commit SHA)

## Quick Start

```bash
# 1. Start PostgreSQL and MinIO
docker compose -f docker/docker-compose.dev.yml up -d

# 2. Build the Docker image (from repo root)
docker build \
  --build-arg COMMIT_SHA=$(git rev-parse HEAD) \
  --build-arg BUILD_TIME=$(date -u +"%Y-%m-%dT%H:%M:%SZ") \
  -t mattermost-test-system-io:local \
  -f apps/server/Dockerfile .

# 3. Run the container
docker run --rm -p 8080:8080 \
  --network tsio-dev_default \
  -e TSIO_ENVIRONMENT=development \
  -e TSIO_DATABASE_URL=postgres://tsio:tsio@postgres:5432/tsio?sslmode=disable \
  -e TSIO_S3_ENDPOINT=http://minio:9000 \
  -e TSIO_S3_BUCKET=reports \
  -e TSIO_S3_ACCESS_KEY=minioadmin \
  -e TSIO_S3_SECRET_KEY=minioadmin \
  -e TSIO_S3_REGION=us-east-1 \
  -e TSIO_S3_FORCE_PATH_STYLE=true \
  -e TSIO_SESSION_SECRET=dev-session-secret-change-me \
  -e TSIO_GITHUB_ACTIONS_OIDC_AUDIENCE=tsio \
  mattermost-test-system-io:local

# 4. Verify
curl http://localhost:8080/health
curl http://localhost:8080/api/v1/info
open http://localhost:8080/
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

| Service | Host Port | In-network | Purpose |
|---------|-----------|------------|---------|
| PostgreSQL | `localhost:6432` | `postgres:5432` | Database |
| MinIO | `localhost:9100` (API), `localhost:9101` (Console) | `minio:9000` | S3-compatible object storage |
| Adminer | `localhost:8081` | — | Database admin UI |

### 2. Build the Docker image

The Dockerfile lives at `apps/server/Dockerfile`, but the build context must be the repo root so the web stage can read `apps/web/`:

```bash
docker build \
  --build-arg COMMIT_SHA=$(git rev-parse HEAD) \
  --build-arg BUILD_TIME=$(date -u +"%Y-%m-%dT%H:%M:%SZ") \
  -t mattermost-test-system-io:local \
  -f apps/server/Dockerfile .
```

Build args inject metadata into the image:

| Arg | Value | Shows up in |
|-----|-------|-------------|
| `COMMIT_SHA` | Full git commit SHA | `/api/v1/info` → `commit_sha` |
| `BUILD_TIME` | Current UTC timestamp | `/api/v1/info` → `build_time` |
| `VERSION` | Semantic version (optional) | `/api/v1/info` → `server_version` |

A cold build typically takes ~30s for the Node (Vite) stage and <1 minute for the Go stages. Subsequent builds hit Docker layer caching and are faster.

### 3. Run the container

The container needs to reach PostgreSQL and MinIO running in Docker. Join the compose network (`tsio-dev_default`) so service names resolve:

```bash
docker run --rm -p 8080:8080 \
  --network tsio-dev_default \
  -e TSIO_ENVIRONMENT=development \
  -e TSIO_DATABASE_URL=postgres://tsio:tsio@postgres:5432/tsio?sslmode=disable \
  -e TSIO_S3_ENDPOINT=http://minio:9000 \
  -e TSIO_S3_BUCKET=reports \
  -e TSIO_S3_ACCESS_KEY=minioadmin \
  -e TSIO_S3_SECRET_KEY=minioadmin \
  -e TSIO_S3_REGION=us-east-1 \
  -e TSIO_S3_FORCE_PATH_STYLE=true \
  -e TSIO_SESSION_SECRET=dev-session-secret-change-me \
  -e TSIO_GITHUB_ACTIONS_OIDC_AUDIENCE=tsio \
  mattermost-test-system-io:local
```

> **Note**: Inside the Docker network, services are referenced by their compose service name (`postgres:5432`, `minio:9000`), not the host-mapped ports (`6432`, `9100`).

### 4. Verify

```bash
# Liveness (always 200)
curl http://localhost:8080/health

# Readiness (checks DB connectivity)
curl http://localhost:8080/ready

# Build info
curl http://localhost:8080/api/v1/info

# Embedded React web UI
open http://localhost:8080/
```

Expected `/api/v1/info` response:

```json
{
  "server_version": "0.1.0",
  "environment": "development",
  "repo_url": "https://github.com/mattermost/mattermost-test-system-io",
  "commit_sha": "abc1234...",
  "build_time": "2026-02-22T12:00:00Z"
}
```

## Run with env file

For convenience, create a `.env.docker` file (gitignored):

```bash
TSIO_ENVIRONMENT=development
TSIO_DATABASE_URL=postgres://tsio:tsio@postgres:5432/tsio?sslmode=disable
TSIO_S3_ENDPOINT=http://minio:9000
TSIO_S3_BUCKET=reports
TSIO_S3_ACCESS_KEY=minioadmin
TSIO_S3_SECRET_KEY=minioadmin
TSIO_S3_REGION=us-east-1
TSIO_S3_FORCE_PATH_STYLE=true
TSIO_SESSION_SECRET=dev-session-secret-change-me
TSIO_GITHUB_ACTIONS_OIDC_AUDIENCE=tsio
```

Then run:

```bash
docker run --rm -p 8080:8080 \
  --network tsio-dev_default \
  --env-file .env.docker \
  mattermost-test-system-io:local
```

## Run in background

```bash
# Start
docker run -d --name tsio -p 8080:8080 \
  --network tsio-dev_default \
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
  --build-arg COMMIT_SHA=$(git rev-parse HEAD) \
  --build-arg BUILD_TIME=$(date -u +"%Y-%m-%dT%H:%M:%SZ") \
  -t mattermost-test-system-io:local \
  -f apps/server/Dockerfile .
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
