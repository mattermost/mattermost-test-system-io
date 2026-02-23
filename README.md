# Mattermost Test System IO

API server and web dashboard for collecting, storing, and viewing Test Automation reports. Currently supports Playwright, Cypress, and Detox.

## Stack

- **Server**: Rust (Actix-web), PostgreSQL, S3-compatible storage
- **Web**: React, Vite, TailwindCSS
- **Infrastructure**: AWS CDK (ECS Fargate, RDS, S3, ALB)

## Quick Start

```bash
make install       # Install all dependencies
make docker-up     # Start PostgreSQL + MinIO + Adminer
make dev           # Run server (:8080) and web (:3000) concurrently
```

## Project Structure

```
apps/server/       # Rust API server
apps/web/          # React frontend
infra/             # AWS CDK infrastructure
docker/            # Docker Compose for local dev
```

## API

Base path: `/api/v1` | Auth: `X-API-Key` header

| Endpoint | Description |
|----------|-------------|
| `GET /health` | Health check |
| `GET /ready` | Readiness check |
| `GET /reports` | List reports |
| `POST /reports` | Create report |
| `GET /reports/{id}` | Report details |
| `GET /reports/{id}/suites` | Test suites |
| `POST /reports/{id}/jobs/init` | Initialize job |
| `GET /jobs` | Query jobs |
| `GET /ws` | WebSocket for real-time updates |

## Environments

| Environment | URL |
|-------------|-----|
| Production | `https://test-io.test.mattermost.com` |
| Staging | `https://staging-test-io.test.mattermost.com` |

## Development Commands

```bash
make help              # Show all available targets
make dev               # Run server + web concurrently (with auto-reload)
make run               # Run server + web concurrently (no auto-reload)
make test              # Run all tests
make lint              # Run all linters
make fmt               # Format all code
make build             # Build for production
make docker-up         # Start dev services
make docker-down       # Stop dev services
```

## Deployment

Deployments are triggered via GitHub Actions workflow dispatch:

- **Staging**: Builds a beta image, resets the database, deploys latest release then beta (tests migrations)
- **Production**: Promotes a validated beta tag, retags as release + latest, deploys to ECS

See [`infra/README.md`](infra/README.md) for infrastructure details.

## Docker

The image is available on Docker Hub: [`mattermostdevelopment/mattermost-test-system-io`](https://hub.docker.com/r/mattermostdevelopment/mattermost-test-system-io)

See [`docker/DOCKER_HUB_README.md`](docker/DOCKER_HUB_README.md) for image tags, environment variables, and setup instructions.
