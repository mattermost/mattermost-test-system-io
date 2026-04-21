# Mattermost Test System IO

API server and web dashboard for collecting, storing, and viewing Test Automation reports. Currently supports Playwright, Cypress, and Detox.

## Stack

- **Server**: Go 1.26 (chi, pgx/v5), PostgreSQL 18.3, S3-compatible storage
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
apps/server/       # Go API server (chi + pgx)
apps/web/          # React frontend
infra/             # AWS CDK infrastructure
docker/            # Docker Compose for local dev
```

## API

Reads are public. Writes and admin endpoints require `X-API-Key`, `Authorization: Bearer`, or the `tsio_session` cookie. Base path for the API is `/api/v1`; `/health`, `/ready`, `/files/*`, and `/swagger-ui/*` are top-level.

| Endpoint | Description |
|----------|-------------|
| `GET /health` / `GET /ready` | Liveness / readiness probes |
| `GET /api/v1/reports` | List reports (public) |
| `GET /api/v1/reports/{id}` | Report details (public) |
| `GET /api/v1/reports/{id}/suites` | Test suites (public) |
| `GET /api/v1/reports/{id}/cases` | Test cases (public, optional `?status=` filter) |
| `GET /api/v1/reports/{id}/json` | Raw Playwright JSON, presigned redirect (public) |
| `POST /api/v1/reports/begin` → `register` → `upload/{rid}/{uid}/json` → `upload/{rid}/{uid}/screenshots` → `complete` | Stateless upload lifecycle (auth required) |
| `GET /api/v1/artifacts/{id}` | Artifact download, presigned redirect (auth required) |
| `GET /api/v1/ws` | WebSocket for live ingest progress (anonymous) |
| `POST /api/v1/auth/github/start`, `GET /api/v1/auth/github/callback` | GitHub OAuth sign-in |
| `POST /api/v1/auth/logout` | Clear session |
| `/swagger-ui/` | Interactive OpenAPI browser |

The legacy bundle endpoint `POST /api/v1/reports` returns `410 Gone`.

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
make test-server-e2e   # All -tags=e2e suites (admin_cli, oidc, contract) — needs Docker
make lint              # Run all linters (golangci-lint + oxlint)
make fmt               # Format all code (gofmt + goimports + oxfmt)
make build             # Build server binaries + web bundle
make ci                # Full CI gate: lint + typecheck + test + build
make db-reset          # Drop tables and re-apply per-table schema
make seed              # Seed dev fixtures
make docker-up         # Start dev services (Postgres + MinIO)
make docker-down       # Stop dev services
make tools             # Install developer CLI tools (golangci-lint, goimports)
```

## Deployment

Deployments are triggered via GitHub Actions workflow dispatch:

- **Staging**: Builds a beta image, resets the database, deploys latest release then beta (tests migrations)
- **Production**: Promotes a validated beta tag, retags as release + latest, deploys to ECS

See [`infra/README.md`](infra/README.md) for infrastructure details.

## Docker

The image is available on Docker Hub: [`mattermostdevelopment/mattermost-test-system-io`](https://hub.docker.com/r/mattermostdevelopment/mattermost-test-system-io)

See [`docker/DOCKER_HUB_README.md`](docker/DOCKER_HUB_README.md) for image tags, environment variables, and setup instructions.
