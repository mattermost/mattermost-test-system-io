# Test System IO — Server (Go)

HTTP API Server for uploading and viewing test reports.

## Prerequisites

- Go 1.26+ (pinned via the `toolchain` directive in `go.mod`; the project
  tracks the latest stable Go release)
- Docker (for the local Postgres 18.3 + MinIO stack and for `testcontainers-go`
  during integration tests)
- Node.js (for the web client in `apps/web`, not needed for backend-only work)

## Layout

```
apps/server/
├── cmd/
│   ├── tsio/           # HTTP server binary
│   └── tsioctl/        # admin CLI (keys issue/list/rotate/revoke, db reset/seed)
├── internal/
│   ├── api/            # chi router, handlers, middleware, error mapper
│   ├── auth/           # apikey, oauth, oidc, policy, session
│   ├── config/         # typed config loaded from env
│   ├── db/             # pgx pool, migration runner (migrations embedded via embed.FS)
│   ├── events/         # WebSocket hub + publisher
│   ├── ingest/         # multipart → zip extract → consolidate
│   ├── storage/        # S3/MinIO wrapper
│   ├── telemetry/      # slog logger + request middleware
│   └── testreport/     # domain types (Report, Suite, TestCase, Artifact)
├── migrations/         # per-table SQL files — embedded into the binary via embed.FS
├── api/openapi.yaml    # hand-authored API contract
├── tests/
│   ├── e2e/oidc/       # OIDC E2E suite
│   ├── e2e/admin_cli/  # tsioctl lifecycle E2E
│   └── contract/       # OpenAPI contract tests
└── Dockerfile
```

## Setup

From the repository root:

```bash
make install      # go mod download + npm ci
make tools        # installs golangci-lint + goimports to GOBIN (optional; other targets use `go run`)
cp .env.example .env
```

## Run

```bash
make docker-up    # Postgres 18.3 + MinIO + adminer
make db-reset     # apply migrations/*.sql
make seed         # fixtures: default report group + DEV_API_KEY
make dev          # server :8080 and web :3000
```

Or just the server:

```bash
make dev-server
```

## Admin CLI

```bash
go run ./cmd/tsioctl keys issue --name ci-playwright
go run ./cmd/tsioctl keys list
go run ./cmd/tsioctl keys rotate <id>
go run ./cmd/tsioctl keys revoke <id>
go run ./cmd/tsioctl db reset
go run ./cmd/tsioctl db seed
```

## Testing

```bash
make test-server         # unit + integration (race)
make test-server-e2e     # every -tags=e2e package (admin_cli, oidc, contract);
                         # testcontainers-go; needs Docker
```

## Schema changes (pre-v1.0)

The source of truth is `migrations/NNN_<table>.sql`. Migrations are embedded
into the Go binary via `apps/server/migrations/embed.go`, so the binary ships
with its own schema. To change a table:

1. Edit the `.sql` file in place (add/remove columns, indexes, constraints).
2. `make db-reset` — drops and re-applies the whole schema locally.
3. `make test-server`.

This "direct-edit" pattern is explicit pre-v1.0 behavior; post-v1.0 will switch
to append-only additive migrations.

## Troubleshooting

- **Docker not running** — `make dev` / `make test-server-e2e` fail with
  connection errors. Start Docker Desktop.
- **`port 5432 in use`** — the dev stack publishes Postgres on `6432` by
  default; check `docker/docker-compose.dev.yml` if your `TSIO_DATABASE_URL`
  expects a different port.
- **`Too many open files` on macOS** — `ulimit -n 4096`. The Makefile already
  raises the limit automatically for `dev` and `test-*` targets.
