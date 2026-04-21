# Test System IO

Go API + React frontend for viewing Playwright test reports.

## Quick Start
```bash
make dev    # Start servers (API :8080, Web :3000)
make ci     # Run checks, lint, test, build
```

## Structure
```
apps/server/   # Go API (chi, pgx/v5, sqlc, PostgreSQL 18.3)
apps/web/      # React (Vite, TailwindCSS, lucide-react)
```

## API (`/api/v1`, auth: `X-API-Key` header, `Authorization: Bearer`, or `tsio_session` cookie)
- `GET /health` / `GET /ready` — probes
- `GET /reports` — list reports
- `POST /reports` — upload (multipart, optional `X-Report-Idempotency-Key`)
- `GET /reports/{id}` — report details
- `GET /reports/{id}/suites` — test suites
- `GET /reports/{id}/cases` — test cases
- `GET /reports/{id}/json` — raw Playwright JSON (302 → presigned S3)
- `GET /report-groups`, `POST /report-groups`
- `GET /artifacts/{id}` — 302 → presigned S3
- `GET /events` — WebSocket for live ingest progress
- `POST /auth/github/start`, `GET /auth/github/callback`, `POST /auth/logout`
- `/swagger-ui/` — hand-authored OpenAPI 3.1 spec browser

## Style
- Go: `gofmt -s`, `goimports`, `golangci-lint` (zero warnings)
- TypeScript: `eslint` + `prettier`
- Files: `snake_case.tsx`, `lowercase_with_underscores.go`
- Icons: lucide-react
- UI: shadcn/ui patterns
- CSS: TailwindCSS (dark mode supported)
- Deps: exact versions only (`go.mod` + `go.sum`; npm `--save-exact`)

## Testing
```bash
make test              # All tests (unit + E2E)
make test-server       # Go unit + integration (race)
make test-server-oidc  # OIDC E2E (testcontainers-go; needs Docker)
make test-web          # Frontend tests
```

### File Descriptor Limit (macOS)
E2E tests create DB connections and HTTP servers. macOS defaults to
256 open files per process, which is too low. The Makefile raises it
automatically, but if you see `Too many open files` errors:
```bash
ulimit -n        # check current limit
ulimit -n 4096   # raise for current shell session
echo 'ulimit -n 4096' >> ~/.zshrc   # make permanent
```

## Schema changes (pre-v1.0)
`apps/server/migrations/NNN_<table>.sql` is the schema source of truth.
Edit the `.sql` file in place, then:
```bash
make db-reset    # drops + reapplies the whole schema
make sqlc        # regenerates typed Go accessors
make test-server
```

## PR
Run `make ci` then use: `feat(scope): desc` or `fix(scope): desc`
