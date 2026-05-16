# Test System IO

Go API + React frontend for viewing Playwright, Cypress, and Detox test reports, and for orchestrating Playwright and Cypress test-shard execution on GitHub Actions.

## Quick Start
```bash
make dev    # Start servers (API :8080, Web :3000)
make ci     # Run checks, lint, test, build
```

## Structure
```text
apps/server/   # Go API (chi, pgx/v5, PostgreSQL 18.3)
apps/web/      # React (Vite, TailwindCSS, lucide-react)
```

## API
Reads are public; writes/admin require `X-API-Key`, `Authorization: Bearer`, or the `tsio_session` cookie. Base path for the API is `/api/v1`; `/health`, `/ready`, `/files/*`, and `/swagger-ui/*` are top-level.
- `GET /health` / `GET /ready` — probes (top-level, not under `/api/v1`)
- `GET /api/v1/reports` — list reports (public)
- `GET /api/v1/reports/{id}` — report details (public)
- `GET /api/v1/reports/{id}/suites` — test suites (public)
- `GET /api/v1/reports/{id}/cases` — test cases (public)
- `GET /api/v1/reports/{id}/json` — raw Playwright JSON, 302 → presigned S3 (public)
- Stateless upload (auth): `POST /api/v1/reports/begin` → `register` → `upload/{rid}/{uid}/json` → `upload/{rid}/{uid}/screenshots`. `/reports/begin` declares `total_reports_expected` (the shard count); the report group auto-finalizes once that many shards reach `complete`. Idle groups are flipped to `incomplete` by the staleness reaper.
- `GET /api/v1/artifacts/{id}` — 302 → presigned S3 (auth required)
- `POST /api/v1/orchestration/begin` — register a run with composite identity + dispatch units (idempotent on identity + units hash)
- `POST /api/v1/orchestration/checkout` — atomically dispatch up to N units to a worker (worker identified by gh_job_name + gh_job_id)
- `POST /api/v1/orchestration/complete` — report per-spec results for a worker's lease (late reports accepted; idempotent on (run, gh_job_id))
- `POST /api/v1/orchestration/screenshots` — upload an orchestration-flow screenshot under the `orchestration/` key prefix
- `GET /api/v1/orchestration/status` — poll run status by composite identity (`?repository=...&commit_sha=...&gh_run_id=...&name=...&gh_run_attempt=...`)
- `GET /api/v1/ws` — WebSocket for live ingest progress (anonymous); orchestration subscribers send `subscribe.orchestration` / `unsubscribe.orchestration` frames and receive `orchestration.run.started`, `orchestration.unit.leased`, `orchestration.unit.completed`, `orchestration.lease.expired`, `orchestration.run.completed`, `orchestration.run.timed_out` events
- `POST /api/v1/auth/github/start`, `GET /api/v1/auth/github/callback`, `POST /api/v1/auth/logout`
- `/swagger-ui/` — hand-authored OpenAPI 3.1 spec browser
- Legacy `POST /api/v1/reports` bundle upload now returns 410 Gone.

## Style
- Go: `gofmt -s`, `goimports`, `golangci-lint` (zero warnings)
- TypeScript: `oxlint` + `oxfmt`
- Files: `snake_case.tsx`, `lowercase_with_underscores.go`
- Icons: lucide-react
- UI: shadcn/ui patterns
- CSS: TailwindCSS (dark mode supported)
- Deps: exact versions only (`go.mod` + `go.sum`; npm `--save-exact`)

## Testing
```bash
make test              # All tests (unit + E2E)
make test-server       # Go unit + integration (race)
make test-server-e2e   # Every -tags=e2e suite (testcontainers-go; needs Docker)
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
`apps/server/migrations/NNN_<table>.sql` is the schema source of truth
(embedded into the Go binary via `apps/server/migrations/embed.go`).
Edit the `.sql` file in place, then:
```bash
make db-reset    # drops + reapplies the whole schema locally
make test-server
```

## PR
Run `make ci` then use: `feat(scope): desc` or `fix(scope): desc`
