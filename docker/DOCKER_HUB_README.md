# Mattermost Test System IO

API server and web frontend for uploading and viewing test reports (Playwright, Cypress, Detox).

## Quick Start

See the [local deployment guide](https://github.com/mattermost/mattermost-test-system-io/blob/main/.github/LOCAL_DEPLOYMENT.md) for step-by-step instructions to build and run locally with PostgreSQL and S3.

## Tags

| Tag | Description |
|-----|-------------|
| `latest` | Latest production release |
| `X.Y.Z` (e.g., `0.1.0`) | Specific production release |
| `X.Y.Z-abcdefg.beta` | Staging prerelease (not recommended for production) |

## Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `RUST_ENV` | Yes | `production` | `development` or `production` |
| `TSIO_DB_URL` | Production* | — | PostgreSQL connection string (*or use individual vars below) |
| `TSIO_DB_HOST` | Production* | `localhost` | Database host (*required if `TSIO_DB_URL` is not set) |
| `TSIO_DB_PORT` | No | `5432` | Database port |
| `TSIO_DB_USER` | No | `tsio` | Database user |
| `TSIO_DB_PASSWORD` | Production* | — | Database password (*required if `TSIO_DB_URL` is not set) |
| `TSIO_DB_NAME` | No | `tsio` | Database name |
| `TSIO_API_KEY` | Production | — | API key (min 32 characters) |
| `TSIO_SERVER_HOST` | No | `0.0.0.0` | Server bind address |
| `TSIO_SERVER_PORT` | No | `8080` | Server port |
| `TSIO_S3_ENDPOINT` | Yes* | — | S3-compatible endpoint URL (*not needed for AWS S3, required for MinIO) |
| `TSIO_S3_BUCKET` | Yes | — | S3 bucket name |
| `TSIO_S3_ACCESS_KEY` | Yes | — | S3 access key |
| `TSIO_S3_SECRET_KEY` | Yes | — | S3 secret key |
| `TSIO_S3_REGION` | Yes | `us-east-1` | S3 region |

## Health Checks

| Endpoint | Description |
|----------|-------------|
| `GET /api/v1/health` | Liveness (always 200) |
| `GET /api/v1/ready` | Readiness (checks DB) |
| `GET /api/v1/info` | Build info (version, commit, build time) |

## Image Details

- **Base**: `debian:trixie-slim`
- **Architecture**: `linux/amd64`
- **User**: Non-root (`appuser`)
- **Port**: `8080`

## Source

[github.com/mattermost/mattermost-test-system-io](https://github.com/mattermost/mattermost-test-system-io)
