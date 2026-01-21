# Uploading Reports

## Quick Start

```bash
# Upload a report directory
node scripts/upload-seed.js path/to/report

# Upload all seed data
node scripts/upload-seed.js
```

## Authentication

The upload API requires authentication. There are two methods:

### Option 1: Admin Key (Development Only)

In development, use the bootstrap admin key directly:

```bash
curl -X POST http://localhost:8080/api/v1/reports \
  -H "X-Admin-Key: dev-admin-key-do-not-use-in-production" \
  -F "files=@report/index.html"
```

### Option 2: Generate an API Key (Recommended for Production)

#### Using CLI (requires database access)

```bash
cd apps/server

# Generate a new API key
cargo run --bin generate-api-key -- \
  --name "CI - GitHub Actions" \
  --role contributor \
  --expires-in 365d

# Output:
# ────────────────────────────────────────
# API Key Generated
# ────────────────────────────────────────
# ID:      550e8400-e29b-41d4-a716-446655440000
# Name:    CI - GitHub Actions
# Role:    contributor
# Expires: 2027-01-14T00:00:00Z
#
# Key:     tsio_a1B2c3D4e5F6g7H8i9J0k1L2m3N4o5P6
#
# ⚠️  Save this key! It cannot be retrieved later.
# ────────────────────────────────────────
```

#### Using HTTP API

```bash
# Create API key using admin key (first time setup)
curl -X POST http://localhost:8080/api/v1/auth/keys \
  -H "X-Admin-Key: dev-admin-key-do-not-use-in-production" \
  -H "Content-Type: application/json" \
  -d '{"name": "CI Upload Key", "role": "contributor", "expires_in": "365d"}'

# Response includes the key (only shown once):
# {
#   "id": "...",
#   "key": "tsio_a1B2c3D4e5F6g7H8i9J0k1L2m3N4o5P6",
#   "name": "CI Upload Key",
#   ...
# }
```

#### Using the Generated Key

```bash
# Set the API key
export TSIO_API_KEY=tsio_a1B2c3D4e5F6g7H8i9J0k1L2m3N4o5P6

# Upload using the script
node scripts/upload-seed.js path/to/report

# Or use curl directly
curl -X POST http://localhost:8080/api/v1/reports \
  -H "X-API-Key: $TSIO_API_KEY" \
  -F "files=@report/index.html"
```

#### Using a .env File

Create a `.env` file to store your API key:

```bash
# .env
export TSIO_API_KEY=tsio_a1B2c3D4e5F6g7H8i9J0k1L2m3N4o5P6
export API_BASE=http://localhost:8080/api/v1
```

Then source it and run the script:

```bash
source .env && node scripts/upload-seed.js path/to/report
```

### Managing API Keys

See [API Keys Documentation](./api-keys.md) for full details on listing, revoking, and restoring keys.

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `API_BASE` | `http://localhost:8080/api/v1` | API base URL |
| `TSIO_API_KEY` | (none) | Database-backed API key (use for production) |
| `TSIO_ADMIN_KEY` | `dev-admin-key-do-not-use-in-production` | Admin key (for development bootstrap) |

In development, the script uses `X-Admin-Key` header by default. In production, set `TSIO_API_KEY` with a valid database-backed API key.

## Supported Frameworks

### Playwright

Required: `index.html`
Optional: `results.json`, `data/*` (screenshots, traces)

### Cypress (Mochawesome)

Required: `all.json` or `mochawesome.json`
Optional: `mochawesome.html`, `assets/*`, `screenshots/*`

### Detox

Required: `*-data.json` (jest-stare data)
Optional: `*-junit.xml`, `*-main.html`, screenshots

## Notes

- Framework is auto-detected from files
- Video files are rejected to save storage
- Large uploads (100+ files) work fine with the Node.js script
- API keys are stored as SHA-256 hashes (the full key is only shown once at creation)
