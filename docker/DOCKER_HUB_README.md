# Mattermost Test System IO

API server and web frontend for uploading and viewing test reports
(Playwright, Cypress, Detox), and for orchestrating test-shard execution
across an arbitrary number of CI workers (Playwright on GitHub Actions).

## Quick Start

See the [local deployment guide](https://github.com/mattermost/mattermost-test-system-io/blob/main/.github/LOCAL_DEPLOYMENT.md) for step-by-step instructions to build and run locally with PostgreSQL and S3.

To build the image yourself from a checkout of the repo root:

```bash
docker build -t mattermost-test-system-io:dev -f apps/server/Dockerfile .
```

## Tags

| Tag | Description |
|-----|-------------|
| `latest` | Latest production release |
| `X.Y.Z` (e.g., `0.1.0`) | Specific production release |
| `X.Y.Z-abcdefg.beta` | Staging prerelease (not recommended for production) |

## Environment Variables

All configuration is read from `TSIO_*` environment variables. The list below mirrors `apps/server/internal/config/config.go`.

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `TSIO_HTTP_LISTEN_ADDR` | No | `0.0.0.0:8080` | HTTP bind address (overridden in the image to bind all interfaces). |
| `TSIO_DATABASE_URL` | Yes | — | PostgreSQL 18.3 connection string (e.g. `postgres://tsio:tsio@host:5432/tsio?sslmode=disable`). |
| `TSIO_DB_AUTO_MIGRATE` | No | `true` | Apply embedded migrations on startup. Disable to run `tsioctl db migrate` out-of-band. |
| `TSIO_S3_ENDPOINT` | No | — | S3-compatible endpoint URL. Leave empty for AWS S3; set for MinIO/other. |
| `TSIO_S3_REGION` | No | `us-east-1` | S3 region. |
| `TSIO_S3_BUCKET` | Yes | — | S3 bucket name for artifacts. |
| `TSIO_S3_ACCESS_KEY` | Yes | — | S3 access key. |
| `TSIO_S3_SECRET_KEY` | Yes | — | S3 secret key. |
| `TSIO_S3_FORCE_PATH_STYLE` | No | `false` | Use path-style addressing (required for MinIO). |
| `TSIO_SESSION_SECRET` | Yes | — | Secret used to sign browser sessions. Must be non-empty and unique per deployment. |
| `TSIO_SESSION_TTL` | No | `720h` | Session lifetime. |
| `TSIO_REFRESH_TOKEN_TTL` | No | `720h` | Refresh-token lifetime. |
| `TSIO_ADMIN_KEY` | No | `dev-admin-key-do-not-use-in-production` | Admin auth scheme for `/admin/*` endpoints (e.g. `POST /api/v1/admin/oidc-policies`). MUST be overridden in production; staging/prod CDK auto-generate via Secrets Manager. |
| `TSIO_BOOTSTRAP_OIDC_POLICIES` | No | — | Comma-separated `pattern=role` list seeded into `github_oidc_policies` at startup (`ON CONFLICT (name) DO NOTHING`). Used by ephemeral staging deploys to re-seed the org-wide CI grant after the DB is recreated. Example: `mattermost/*=uploader`. |
| `TSIO_OPENAPI_SPEC_PATH` | No | `api/openapi.yaml` | Path to the OpenAPI spec used for request validation. Pinned to `/api/openapi.yaml` in this image. |
| `TSIO_GITHUB_OAUTH_CLIENT_ID` | No | — | GitHub OAuth app client ID. Leave unset to disable human sign-in. |
| `TSIO_GITHUB_OAUTH_CLIENT_SECRET` | No | — | GitHub OAuth app client secret. |
| `TSIO_GITHUB_OAUTH_REDIRECT_URL` | No | — | OAuth redirect URL (e.g. `https://<host>/api/v1/auth/github/callback`). |
| `TSIO_GITHUB_ACTIONS_OIDC_ISSUER` | No | `https://token.actions.githubusercontent.com` | OIDC issuer for CI workload auth. Empty disables OIDC. |
| `TSIO_GITHUB_ACTIONS_OIDC_AUDIENCE` | No | — | Expected `aud` claim. Empty disables aud validation; production sets `mattermost-test-system-io`. Workflows MUST request the same audience when minting tokens. |
| `TSIO_MAX_UPLOAD_BYTES` | No | `1073741824` (1 GiB) | Max total upload size. |
| `TSIO_MAX_ARTIFACT_BYTES` | No | `104857600` (100 MiB) | Max individual artifact size. |
| `TSIO_ORCH_REAPER_INTERVAL_MS` | No | `5000` | Tick interval for the orchestration lease/run-timeout reaper. Lower values reclaim expired leases faster at the cost of more DB scans. |
| `TSIO_ORCH_MAX_SPECS_PER_RUN` | No | `5000` | Total spec-count cap (summed across every dispatch unit) for a single orchestration `begin run` request. Requests over the cap are rejected with `TOO_MANY_SPECS`. |
| `TSIO_REPORTS_STALENESS_TIMEOUT_MS` | No | `3600000` (1h) | Idle window past which an `in_progress` report group is flipped to `incomplete` by the reports reaper. Bursty shard uploads can have minutes-long gaps, so the default sits well past the worst legitimate case. |
| `TSIO_UPLOAD_TIMEOUT_MS` | No | `3600000` (1h) | Client-facing upload timeout surfaced via `/api/v1/config`. |
| `TSIO_HTML_VIEW_ENABLED` | No | `false` | Enable in-browser HTML report viewing. |
| `TSIO_SEARCH_MIN_LENGTH` | No | `3` | Minimum characters for search queries. |
| `TSIO_CORS_ALLOWED_ORIGINS` | No | — | Comma-separated list of allowed CORS origins. |
| `TSIO_LOG_LEVEL` | No | `info` | `debug` \| `info` \| `warn` \| `error`. |
| `TSIO_LOG_FORMAT` | No | `json` | `json` \| `text`. |
| `TSIO_ENVIRONMENT` | No | `development` | Environment label surfaced via `/api/v1/info`. |
| `TSIO_REPO_URL` | No | `https://github.com/mattermost/mattermost-test-system-io` | Repository URL surfaced via `/api/v1/info`. |

## Health Checks

| Endpoint | Description |
|----------|-------------|
| `GET /health` | Liveness (always 200) |
| `GET /ready` | Readiness (checks DB) |
| `GET /api/v1/info` | Build info (version, commit, build time) |

The React web UI is embedded into the binary and served on the same port:

```bash
open http://localhost:8080/
```

## Image Details

- **Base**: `gcr.io/distroless/static-debian12:nonroot`
- **Architecture**: `linux/amd64`
- **User**: Non-root (`nonroot:nonroot`)
- **Port**: `8080`
- **Contents**: `/tsio` (server), `/api/openapi.yaml`. The web bundle and SQL migrations are embedded into the Go binary. The `tsioctl` admin CLI is intentionally NOT shipped in the runtime image — run it from a separate admin task / dev workstation against the same DSN.

## Source

[github.com/mattermost/mattermost-test-system-io](https://github.com/mattermost/mattermost-test-system-io)
