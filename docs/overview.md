# Test System IO (TSIO)

## What is TSIO?

TSIO is a centralized test management system (initially for reports, then test management system as the future). It provides:

- **Unified Storage** - Store test reports from multiple frameworks in one place
- **Web Dashboard** - Browse reports with filtering, search, and detailed test results
- **HTML Report Viewing** - Embedded viewer for framework-native HTML reports
- **GitHub Integration** - Link reports to repositories, branches, PRs, and commits
- **API-First Design** - RESTful API with OpenAPI documentation

## Supported Frameworks

| Framework | Report Type | Features |
|-----------|-------------|----------|
| **Playwright** | HTML + JSON | Test results, screenshots, traces |
| **Cypress** | Mochawesome | Test results, screenshots |
| **Detox** | JSON + Screenshots | iOS/Android test results, screenshot galleries |

## Quick Start

### 1. Start Infrastructure

```bash
# Start PostgreSQL and MinIO
docker compose -f docker/docker-compose.dev.yml up -d
```

### 2. Run the Server

```bash
# Set environment and run
RUST_ENV=development cargo run --bin mattermost-tsio
```

Server starts at `http://localhost:8080`

### 3. Run the Web UI (Development)

```bash
cd apps/web
npm install
npm run dev
```

Web UI available at `http://localhost:3000`

### 4. Create an API Key

```bash
# Using the admin key (development default)
curl -X POST http://localhost:8080/api/v1/auth/keys \
  -H "X-Admin-Key: dev-admin-key-do-not-use-in-production" \
  -H "Content-Type: application/json" \
  -d '{"name": "ci-upload", "role": "contributor"}'
```

### 5. Upload a Report

```bash
# Example: Upload Playwright report
curl -X POST http://localhost:8080/api/v1/reports/upload/playwright/request \
  -H "X-API-Key: tsio_your_api_key_here" \
  -H "Content-Type: application/json" \
  -d '{
    "files": ["index.html", "results.json"],
    "metadata": {
      "repository": "org/repo",
      "branch": "main"
    }
  }'
```

## Project Structure

```
mattermost-tsio/
├── apps/
│   ├── server/          # Rust API server
│   │   ├── src/
│   │   │   ├── api/     # HTTP handlers
│   │   │   ├── db/      # Database queries
│   │   │   ├── entity/  # SeaORM entities
│   │   │   ├── services/# Business logic
│   │   │   └── config.rs
│   │   └── Cargo.toml
│   └── web/             # React frontend
│       ├── src/
│       │   ├── components/
│       │   └── pages/
│       └── package.json
├── docker/              # Docker Compose files
├── docs/                # Documentation
└── Makefile             # Build commands
```

## Key Commands

| Command | Description |
|---------|-------------|
| `make dev-server` | Run server in development mode |
| `make dev-web` | Run web UI in development mode |
| `make check` | Type check Rust and TypeScript |
| `make lint` | Run Clippy and ESLint |
| `make test` | Run all tests |
| `make build` | Build production artifacts |

## API Endpoints

| Endpoint | Description |
|----------|-------------|
| `GET /api/v1/health` | Health check |
| `GET /api/v1/ready` | Readiness check (database) |
| `GET /api/v1/reports` | List reports (paginated) |
| `GET /api/v1/reports/:id` | Get report details |
| `POST /api/v1/reports/upload/:framework/request` | Request upload |
| `POST /api/v1/reports/upload/:id/files` | Upload files |
| `POST /api/v1/auth/keys` | Create API key (admin) |

Interactive API docs: `http://localhost:8080/swagger-ui/`

## Technology Stack

**Server:**
- Rust with Actix-web 4.x
- PostgreSQL via SeaORM
- S3-compatible storage (AWS S3 / MinIO)
- OpenAPI via Utoipa

**Web:**
- React 19 with TypeScript
- Vite 7
- TanStack React Query
- Tailwind CSS

## Documentation

- [Architecture](./architecture.md) - System design and configuration
- [API Keys](./api-keys.md) - Authentication and key management
- [Uploading Reports](./uploading-reports.md) - CI/CD integration guide

## Environment Variables

Minimum required for development:

```bash
RUST_ENV=development
```

For production, see [Configuration](./architecture.md#configuration) for all available options.

## License

See repository root for license information.
