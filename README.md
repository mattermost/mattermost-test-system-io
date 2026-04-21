# Mattermost Test System IO

API server and web dashboard for collecting, storing, and viewing Test Automation reports. Currently supports Playwright, Cypress, and Detox.

## Stack

- **Server**: Go 1.26 (chi, pgx/v5 + sqlc), PostgreSQL 18.3, S3-compatible storage
- **Web**: React, Vite, TailwindCSS
- **Infrastructure**: AWS CDK (ECS Fargate, RDS, S3, ALB)

## Quick Start

```bash
make install       # Install all dependencies (Go modules + npm)
make docker-up     # Start PostgreSQL 18.3 + MinIO + Adminer
make db-reset      # Apply migrations (fresh schema)
make seed          # Seed default group + dev API key
make dev           # Run server (:8080) and web (:3000) concurrently
```

## Project Structure

```
apps/server/       # Go API server (chi + pgx + sqlc)
apps/web/          # React frontend
infra/             # AWS CDK infrastructure
docker/            # Docker Compose for local dev
```

## API

Base path: `/api/v1` | Auth: `X-API-Key`, `Authorization: Bearer`, or `tsio_session` cookie

| Endpoint | Description |
|----------|-------------|
| `GET /health` / `GET /ready` | Probes |
| `GET /reports` | List reports |
| `POST /reports` | Upload a Playwright bundle (multipart) |
| `GET /reports/{id}` | Report details |
| `GET /reports/{id}/suites` | Test suites |
| `GET /reports/{id}/cases` | Test cases (optional `?status=` filter) |
| `GET /reports/{id}/json` | Raw Playwright JSON (presigned redirect) |
| `GET /report-groups`, `POST /report-groups` | Report groups |
| `GET /artifacts/{id}` | Artifact download (presigned redirect) |
| `GET /events` | WebSocket for live ingest progress |
| `POST /auth/github/start`, `GET /auth/github/callback` | GitHub OAuth sign-in |
| `POST /auth/logout` | Clear session |
| `/swagger-ui/` | Interactive OpenAPI browser |

## Environments

| Environment | URL |
|-------------|-----|
| Production | `https://test-io.test.mattermost.com` |
| Staging | `https://staging-test-io.test.mattermost.com` |

## Development Commands

```bash
make help              # Show all available targets
make dev               # Run server + web concurrently
make test              # Run all tests (unit + E2E)
make test-server       # Go unit + integration (race detector)
make test-server-oidc  # OIDC E2E suite (needs Docker)
make lint              # Run all linters (golangci-lint + eslint)
make fmt               # Format all code (gofmt + goimports + prettier)
make build             # Build server binaries + web bundle
make ci                # Full CI gate: lint + typecheck + test + build
make db-reset          # Drop tables and re-apply per-table schema
make seed              # Seed dev fixtures
make sqlc              # Regenerate sqlc code
make docker-up         # Start dev services (Postgres + MinIO)
make docker-down       # Stop dev services
make tools             # Install developer CLI tools (sqlc, goimports)
```

## Deployment

Deployments are triggered via GitHub Actions workflow dispatch:

- **Staging**: Builds a beta image, resets the database, deploys latest release then beta (tests migrations)
- **Production**: Promotes a validated beta tag, retags as release + latest, deploys to ECS

See [`infra/README.md`](infra/README.md) for infrastructure details.

## Docker

The image is available on Docker Hub: [`mattermostdevelopment/mattermost-test-system-io`](https://hub.docker.com/r/mattermostdevelopment/mattermost-test-system-io)

See [`docker/DOCKER_HUB_README.md`](docker/DOCKER_HUB_README.md) for image tags, environment variables, and setup instructions.
