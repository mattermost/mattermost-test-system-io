# Mattermost Test System IO

API server and web dashboard for collecting, storing, and viewing Test
Automation reports — and for orchestrating test-shard execution across an
arbitrary number of CI workers. Currently supports Playwright, Cypress, and
Detox for report ingestion; orchestration targets Playwright on GitHub Actions.

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
| `POST /api/v1/reports/begin` → `register` → `upload/{rid}/{uid}/json` → `upload/{rid}/{uid}/screenshots` | Stateless upload lifecycle (auth required). The report group auto-finalizes server-side once `total_reports_expected` shards reach `complete`; idle groups are flipped to `incomplete` by the staleness reaper. |
| `GET /api/v1/artifacts/{id}` | Artifact download, presigned redirect (auth required) |
| `POST /api/v1/orchestration/begin` → `checkout` → `complete` | Test-shard orchestration: register a run by composite identity, dispatch units to workers, report results (auth required) |
| `POST /api/v1/orchestration/screenshots` | Upload an orchestration-flow screenshot; resolvable via `/files/{key}` (auth required) |
| `GET /api/v1/orchestration/status` | Poll a run's status by composite identity (auth required) |
| `GET /api/v1/ws` | WebSocket for live ingest progress and orchestration events; clients send a `subscribe.orchestration` frame to receive run-scoped events (anonymous) |
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
make test-server-e2e   # All -tags=e2e suites (admin_cli, oidc, contract, orchestration) — needs Docker
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
