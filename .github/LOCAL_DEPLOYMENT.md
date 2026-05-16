# Local Deployment Guide

Run the production Docker image locally for testing and validation.

## Prerequisites

- Docker installed and running
- Git (to get the commit SHA)
- A locally-trusted TLS cert. tsio rejects requests that don't arrive over
  HTTPS, so local dev needs its own cert. The simplest path is
  [mkcert](https://github.com/FiloSottile/mkcert):
  ```bash
  brew install mkcert nss   # mkcert + Firefox trust store
  mkcert -install            # one-time root CA install
  mkdir -p certs
  mkcert -cert-file certs/localhost.pem -key-file certs/localhost-key.pem localhost 127.0.0.1 ::1
  ```
  Point `TSIO_TLS_CERT_FILE` / `TSIO_TLS_KEY_FILE` at the generated files; tsio
  will listen with `ListenAndServeTLS` on `TSIO_HTTP_LISTEN_ADDR`.

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
docker run --rm -p 8443:8443 \
  --network tsio-dev_default \
  -e TSIO_ENVIRONMENT=development \
  -e TSIO_HTTP_LISTEN_ADDR=:8443 \
  -e TSIO_DATABASE_URL=postgres://tsio:tsio@postgres:5432/tsio?sslmode=disable \
  -e TSIO_S3_ENDPOINT=http://minio:9000 \
  -e TSIO_S3_BUCKET=reports \
  -e TSIO_S3_ACCESS_KEY=minioadmin \
  -e TSIO_S3_SECRET_KEY=minioadmin \
  -e TSIO_S3_REGION=us-east-1 \
  -e TSIO_S3_FORCE_PATH_STYLE=true \
  -e TSIO_SESSION_SECRET=dev-session-secret-change-me \
  -e TSIO_GITHUB_ACTIONS_OIDC_AUDIENCE=mattermost-test-system-io \
  -e TSIO_TLS_CERT_FILE=/etc/tsio/localhost.pem \
  -e TSIO_TLS_KEY_FILE=/etc/tsio/localhost-key.pem \
  -v "$(pwd)/certs:/etc/tsio:ro" \
  mattermost-test-system-io:local

# 4. Verify
curl http://localhost:8443/health
curl https://localhost:8443/api/v1/info
# Open in browser (macOS: `open`, Linux: `xdg-open`, Windows: `start`)
open https://localhost:8443/
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
docker run --rm -p 8443:8443 \
  --network tsio-dev_default \
  -e TSIO_ENVIRONMENT=development \
  -e TSIO_HTTP_LISTEN_ADDR=:8443 \
  -e TSIO_DATABASE_URL=postgres://tsio:tsio@postgres:5432/tsio?sslmode=disable \
  -e TSIO_S3_ENDPOINT=http://minio:9000 \
  -e TSIO_S3_BUCKET=reports \
  -e TSIO_S3_ACCESS_KEY=minioadmin \
  -e TSIO_S3_SECRET_KEY=minioadmin \
  -e TSIO_S3_REGION=us-east-1 \
  -e TSIO_S3_FORCE_PATH_STYLE=true \
  -e TSIO_SESSION_SECRET=dev-session-secret-change-me \
  -e TSIO_GITHUB_ACTIONS_OIDC_AUDIENCE=mattermost-test-system-io \
  -e TSIO_TLS_CERT_FILE=/etc/tsio/localhost.pem \
  -e TSIO_TLS_KEY_FILE=/etc/tsio/localhost-key.pem \
  -v "$(pwd)/certs:/etc/tsio:ro" \
  mattermost-test-system-io:local
```

> **Note**: Inside the Docker network, services are referenced by their compose service name (`postgres:5432`, `minio:9000`), not the host-mapped ports (`6432`, `9100`).

### 4. Verify

```bash
# Liveness (always 200) — health endpoints bypass the HTTPS check
curl http://localhost:8443/health

# Readiness (checks DB connectivity)
curl http://localhost:8443/ready

# Build info (HTTPS required)
curl https://localhost:8443/api/v1/info

# Embedded React web UI (macOS: `open`, Linux: `xdg-open`, Windows: `start`)
open https://localhost:8443/
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
TSIO_HTTP_LISTEN_ADDR=:8443
TSIO_TLS_CERT_FILE=/etc/tsio/localhost.pem
TSIO_TLS_KEY_FILE=/etc/tsio/localhost-key.pem
TSIO_DATABASE_URL=postgres://tsio:tsio@postgres:5432/tsio?sslmode=disable
TSIO_S3_ENDPOINT=http://minio:9000
TSIO_S3_BUCKET=reports
TSIO_S3_ACCESS_KEY=minioadmin
TSIO_S3_SECRET_KEY=minioadmin
TSIO_S3_REGION=us-east-1
TSIO_S3_FORCE_PATH_STYLE=true
TSIO_SESSION_SECRET=dev-session-secret-change-me
TSIO_GITHUB_ACTIONS_OIDC_AUDIENCE=mattermost-test-system-io
```

Then run (the host-side `certs/` directory is mounted so tsio can read the
mkcert-issued cert at the in-container paths declared above):

```bash
docker run --rm -p 8443:8443 \
  --network tsio-dev_default \
  --env-file .env.docker \
  -v "$(pwd)/certs:/etc/tsio:ro" \
  mattermost-test-system-io:local
```

## Run in background

```bash
# Start
docker run -d --name tsio -p 8443:8443 \
  --network tsio-dev_default \
  --env-file .env.docker \
  -v "$(pwd)/certs:/etc/tsio:ro" \
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
