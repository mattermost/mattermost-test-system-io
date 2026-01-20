# Rust Report Server Architecture

API server for uploading and viewing test reports from Playwright, Cypress, and Detox frameworks.

## Table of Contents

- [Overview](#overview)
- [Architecture Pattern](#architecture-pattern)
- [Module Structure](#module-structure)
- [Web Application](#web-application)
- [Authentication](#authentication)
- [Upload Flow](#upload-flow)
- [File Storage](#file-storage)
- [Data Flow](#data-flow)
- [Configuration](#configuration)
- [Background Services](#background-services)
- [Error Handling](#error-handling)
- [API Documentation](#api-documentation)

## Overview

**Key Technologies:**
- **Web Framework:** Actix-web 4.x (async Rust)
- **Database:** PostgreSQL via SeaORM 1.1.19 (async ORM with migrations)
- **File Storage:** S3-compatible (AWS S3 or MinIO)
- **Authentication:** API key-based with RBAC
- **Serialization:** Serde + serde_json
- **API Docs:** Utoipa (OpenAPI/Swagger)

### Design Philosophy: Cloud-Native & Scalable

The server uses cloud-native infrastructure for production deployments:

| Component | Choice | Purpose |
|-----------|--------|---------|
| Database | PostgreSQL | Reliable, scalable relational database |
| File Storage | S3/MinIO | Scalable object storage for report artifacts |
| Authentication | API keys in PostgreSQL | Persistent, queryable key management |

**Why This Approach:**

- **Horizontal Scaling** - Stateless server allows multiple instances behind load balancer
- **Durability** - PostgreSQL and S3 provide reliable data persistence
- **Separation of Concerns** - Database for metadata, S3 for large artifacts
- **Flexibility** - Use AWS S3 in cloud, MinIO for self-hosted deployments

**Development Environment:**

For local development, Docker Compose provides the full infrastructure:
- PostgreSQL 18.1 on port 6432
- MinIO (S3-compatible) on ports 9100 (API) and 9101 (console)
- Adminer database UI on port 8081

## Architecture Pattern

The server follows a **layered architecture** with four main layers:

1. **HTTP Layer** - Actix-web with middleware for request logging and CORS
2. **API Routes** - Stateless HTTP handlers in `src/api/`
3. **Services** - Business logic in `src/services/`
4. **Database Layer** - SeaORM entities and queries in `src/entity/` and `src/db/`

## Module Structure

| Directory | Purpose |
|-----------|---------|
| `src/main.rs` | Server entry point, route setup |
| `src/lib.rs` | Library crate for CLI utilities |
| `src/config.rs` | Environment-based configuration |
| `src/error.rs` | Error types and HTTP mapping |
| `src/api/` | HTTP handlers (health, reports, detox) |
| `src/services/` | Business logic (upload, extraction, cleanup, auth) |
| `src/services/upload/` | Two-phase upload system (playwright, cypress, detox) |
| `src/db/` | Database pool, queries, connection management |
| `src/entity/` | SeaORM entities (report, test_suite, api_key, etc.) |
| `src/migration/` | SeaORM database migrations |
| `src/models/` | Domain models and DTOs |
| `src/auth/` | Authentication extractors and admin key wrapper |
| `src/middleware/` | Request logging middleware |
| `src/bin/` | CLI utilities for API key management |

## Web Application

The frontend is a React single-page application located in `apps/web/`.

### Tech Stack

- **Framework:** React 19 with TypeScript
- **Build:** Vite 7
- **Routing:** React Router v7
- **State:** TanStack React Query for server state and caching
- **Styling:** Tailwind CSS with lucide-react icons

### Pages

| Route | Description |
|-------|-------------|
| `/` | Home page with paginated report list |
| `/reports/:id` | Report detail view with test results |

### Features

**Report List**
- Paginated view (100 reports per page)
- Extraction status indicators (completed, failed, pending)
- Framework and platform display (Playwright, Cypress, Detox iOS/Android)
- GitHub context badges (repository, branch, PR, commit)
- Test statistics with pass rate percentage

**Report Detail**
- Tab interface: Test Results and HTML Report
- Hierarchical test suite explorer with filtering (All, Passed, Failed, Flaky, Skipped)
- Pass rate progress bar with color-coded distribution
- Expandable test specs with error details and stack traces
- Screenshot galleries with lazy loading
- Embedded HTML report viewer via iframe

**Additional Features**
- Dark mode support (light/dark/system)
- Breadcrumb navigation
- Error handling with retry hints
- 1-minute cache with React Query

## Authentication

### Machine-to-Machine Authentication

The server uses API key authentication designed for machine-to-machine (M2M) communication. There is no human user authentication - all interactions are expected from automated systems such as CI/CD pipelines (GitHub Actions, Jenkins, etc.).

### Use Cases

- **CI Upload** - Automated test runners upload reports after test execution
- **CI Retrieval** - Scripts fetch report data for notifications or dashboards
- **Admin Operations** - CLI tools manage API keys for different environments

### API Keys

- **Format:** `rrv_` prefix + 32 alphanumeric characters
- **Storage:** SHA-256 hash stored in database (original key shown only once at creation)
- **Headers:** `X-API-Key` for standard auth, `X-Admin-Key` for bootstrap
- **Roles:** Admin, Contributor, Viewer for role-based access control

### Bootstrap Flow

1. Server starts with `RRV_ADMIN_KEY` environment variable
2. Use admin key to create the first API key via `/api/v1/auth/keys`
3. Store generated API key as CI secret (e.g., GitHub Actions secret)
4. CI pipelines use the API key for uploads

### Security Features

- SecretString wrapper prevents accidental logging
- Constant-time comparison for timing attack prevention
- Memory zeroized on drop
- Role-based authorization in handlers

## Upload Flow

### Request-Then-Transfer Design

**Upload Request**

`POST /api/v1/reports/upload/{framework}/request`

- Validates metadata and filenames
- Performs framework-specific validation
- Creates report entry and registers files
- Returns report_id and upload limits

**Transfer**

`POST /api/v1/reports/upload/{report_id}/files`

- Acquires semaphore permit for concurrency control
- Streams multipart files to temporary storage
- Uploads files to S3 on completion
- Marks files as uploaded in database
- Triggers extraction when all files complete

### Concurrency Control

- Semaphore limits concurrent batches (default: 10)
- Queue timeout prevents blocking (default: 30s)
- Returns 503 Service Unavailable if queue full

## File Storage

### S3 Object Structure

Files are stored in S3 with the following key structure:

| Key Pattern | Contents |
|-------------|----------|
| `{report_id}/` | Report root prefix |
| `{report_id}/index.html` | Playwright HTML report |
| `{report_id}/results.json` | Playwright test results |
| `{report_id}/data/` | Playwright screenshots and traces |
| `{report_id}/mochawesome.html` | Cypress HTML report |
| `{report_id}/all.json` | Cypress test results |
| `{report_id}/screenshots/` | Cypress screenshots |
| `{report_id}/{job}/` | Detox job folders |

### Storage Behavior

- Files are uploaded to S3 after processing
- Original directory structure from the client is preserved
- Each report gets a UUID-based prefix for isolation
- Database tracks file metadata and upload status

### Cleanup Process

Reports and their files are automatically cleaned up based on retention settings:

1. **Discovery** - Background service queries for reports older than `RRV_ARTIFACT_RETENTION_HOURS`
2. **S3 Deletion** - Objects with the report prefix are deleted from S3
3. **Database Update** - `files_deleted_at` timestamp is set (report metadata retained)
4. **Screenshot Markers** - Detox screenshots marked as deleted for UI display

### Retention Settings

| Environment | Default Retention | Cleanup Interval |
|-------------|-------------------|------------------|
| Development | 1 hour | 60 seconds |
| Production | 7 days (168 hours) | 1 hour |

### Data Preservation

- Report metadata remains in database after file cleanup
- Test results, statistics, and GitHub context are preserved
- Only file artifacts (HTML, screenshots, traces) are deleted from S3
- Web UI shows "files deleted" status for cleaned reports

## Data Flow

1. **Upload Request** - Client sends file manifest, receives report_id
2. **Transfer** - Client uploads files in batches, stored temporarily then uploaded to S3
3. **Extraction** - Automatic parsing of JSON results into normalized tables
4. **Retrieval** - API returns report metadata and statistics
5. **Viewing** - Serve HTML reports from S3, extracted test data from PostgreSQL

## Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `RUST_ENV` | required | `development` or `production` |
| `RRV_HOST` | 127.0.0.1 | Bind address |
| `RRV_PORT` | 8080 | Server port |
| `DATABASE_URL` | (dev default) | PostgreSQL connection string |
| `RRV_ADMIN_KEY` | (dev default) | Bootstrap admin key |
| `RRV_DATA_DIR` | ./data/files | Temporary file storage |
| `RRV_BACKUP_DIR` | ./data/backups | Backup directory |
| `RRV_STATIC_DIR` | - | Frontend assets (prod) |
| `RRV_MAX_UPLOAD_SIZE` | 52428800 | Max size per report (50MB) |
| `RRV_MAX_FILES_PER_REQUEST` | 20 | Files per batch |
| `RRV_MAX_CONCURRENT_UPLOADS` | 10 | Concurrent batches |
| `RRV_UPLOAD_QUEUE_TIMEOUT_SECS` | 30 | Queue wait timeout |
| `RRV_ARTIFACT_RETENTION_HOURS` | 1 (dev) / 168 (prod) | Cleanup retention |
| `S3_ENDPOINT` | http://localhost:9100 | S3/MinIO endpoint |
| `S3_BUCKET` | reports | S3 bucket name |
| `S3_REGION` | us-east-1 | S3 region |
| `S3_ACCESS_KEY` | (dev default) | S3 access key |
| `S3_SECRET_KEY` | (dev default) | S3 secret key |

### Mode Differences

| Feature | Development | Production |
|---------|-------------|------------|
| CORS | Permissive (localhost:3000) | Same-origin only |
| Workers | 4 | CPU count |
| Retention | 1 hour | 7 days (168h) |
| Defaults | Uses dev defaults | Requires explicit config |
| S3 Endpoint | MinIO (localhost:9100) | AWS S3 or configured |

### Development Setup

Start the development infrastructure:

```bash
docker compose -f docker/docker-compose.dev.yml up -d
```

This provides:
- PostgreSQL on `localhost:6432` (user: rrv, password: rrv, database: rrv)
- MinIO on `localhost:9100` (API) and `localhost:9101` (console)
- Adminer on `localhost:8081` for database management

## Background Services

### Cleanup Service

A background task runs periodically to remove expired report files from S3. See [File Storage](#file-storage) for details on cleanup process and retention settings.

## Error Handling

### Error Types

| Error | HTTP Status |
|-------|-------------|
| Database | 500 Internal Server Error |
| S3 | 500 Internal Server Error |
| NotFound | 404 Not Found |
| InvalidInput | 400 Bad Request |
| Unauthorized | 401 Unauthorized |
| ExtractionFailed | 500 Internal Server Error |
| ServiceUnavailable | 503 Service Unavailable |

## API Documentation

### Swagger UI

Access interactive API documentation:

- **Swagger UI:** `http://localhost:8080/swagger-ui/`
- **OpenAPI JSON:** `http://localhost:8080/api-docs/openapi.json`

### API Tags

| Tag | Description |
|-----|-------------|
| Health | Liveness/readiness endpoints |
| Reports | Report CRUD operations |
| Detox | Detox-specific endpoints |
| Upload | Two-phase upload system |
| Auth | API key management |

### Authentication in Swagger

1. Click the **"Authorize"** button
2. Enter your API key
3. Click "Authorize"
4. Locked endpoints will now include the X-API-Key header

## Request Logging

Middleware logs all requests with method, path, query params, masked API key prefix, response status, and duration.

## Database Schema

The database uses SeaORM migrations. Schema is organized into the following tables:

### Core Tables

| Table | Purpose |
|-------|---------|
| `reports` | Uploaded test reports with metadata |
| `report_stats` | Aggregated statistics per report |
| `test_suites` | Test suite groupings within a report |
| `test_specs` | Individual test specifications |
| `test_results` | Test execution results with retries |

### Framework-Specific Tables

| Table | Purpose |
|-------|---------|
| `detox_jobs` | Detox job metadata with stats |
| `detox_screenshots` | Screenshot references for Detox tests |

### System Tables

| Table | Purpose |
|-------|---------|
| `api_keys` | Authentication keys with roles |
| `upload_files` | File upload tracking |
| `server_metadata` | Server configuration state |
