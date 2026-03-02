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

### Option 3: GitHub Actions OIDC (Recommended for CI)

No secrets to manage. GitHub Actions provides a short-lived OIDC token per job, and the server validates it against GitHub's JWKS endpoint.

#### Server Setup (one-time)

Enable OIDC on the server and create a policy allowing your repository:

```bash
# Start the server with OIDC enabled
TSIO_GITHUB_OIDC_ENABLED=true \
TSIO_GITHUB_OIDC_ISSUER=https://token.actions.githubusercontent.com \
  cargo run --bin mattermost-tsio

# Create an OIDC policy (allow repos matching a pattern)
curl -X POST https://your-mattermost-tsio-server.example.com/api/v1/auth/oidc-policies \
  -H "X-Admin-Key: $TSIO_ADMIN_KEY" \
  -H "Content-Type: application/json" \
  -d '{"repository_pattern": "mattermost/*", "role": "contributor", "description": "Allow all mattermost repos"}'
```

#### GitHub Actions Workflow

```yaml
name: E2E Tests
on: [push, pull_request]

# Required: allow the workflow to request an OIDC token
permissions:
  id-token: write
  contents: read

env:
  TSIO_API: https://your-mattermost-tsio-server.example.com/api/v1
  SERVER_IMAGE: mattermostdevelopment/mattermost-enterprise-edition:master

jobs:
  e2e-playwright:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        shard: [1, 2, 3, 4]
    steps:
      - uses: actions/checkout@v4

      - name: Run Playwright tests
        run: npx playwright test --shard=${{ matrix.shard }}/4

      # --- Upload results to Mattermost TSIO ---
      # ACTIONS_ID_TOKEN_REQUEST_TOKEN and ACTIONS_ID_TOKEN_REQUEST_URL are
      # automatically set by GitHub when `permissions: id-token: write` is granted.
      # They are used to request a short-lived OIDC JWT for this workflow run.

      - name: Get OIDC token
        if: always()
        id: oidc
        run: |
          RESPONSE=$(curl -sS \
            -H "Authorization: bearer $ACTIONS_ID_TOKEN_REQUEST_TOKEN" \
            "${ACTIONS_ID_TOKEN_REQUEST_URL}&audience=api://mattermost-tsio")
          TOKEN=$(echo "$RESPONSE" | jq -r '.value')
          echo "::add-mask::$TOKEN"
          echo "token=$TOKEN" >> "$GITHUB_OUTPUT"

      - name: Begin report
        if: always()
        run: |
          curl -sf -X POST "$TSIO_API/reports/begin" \
            -H "Authorization: Bearer ${{ steps.oidc.outputs.token }}" \
            -H "Content-Type: application/json" \
            -d '{
              "repository": "${{ github.repository }}",
              "commit": "${{ github.sha }}",
              "gh_run_id": "${{ github.run_id }}",
              "framework": "playwright",
              "name": "playwright-full-enterprise-master"
            }'

      - name: Register report
        if: always()
        id: register
        run: |
          # List JSON result files
          JSON_FILES=$(find playwright-report -name '*.json' -exec sh -c \
            'echo "{\"path\":\"$(basename $1)\",\"size\":$(stat -c%s "$1")}"' _ {} \; \
            | jq -s '.')

          RESPONSE=$(curl -sf -X POST "$TSIO_API/reports/register" \
            -H "Authorization: Bearer ${{ steps.oidc.outputs.token }}" \
            -H "Content-Type: application/json" \
            -d "{
              \"repository\": \"${{ github.repository }}\",
              \"commit\": \"${{ github.sha }}\",
              \"gh_run_id\": \"${{ github.run_id }}\",
              \"framework\": \"playwright\",
              \"name\": \"playwright-full-enterprise\",
              \"gh_job_id\": \"${{ github.job }}-${{ matrix.shard }}\",
              \"gh_job_name\": \"playwright-shard-${{ matrix.shard }}\",
              \"json_files\": $JSON_FILES,
              \"environment_metadata\": {
                \"server\": {\"image\": \"${{ env.SERVER_IMAGE }}\"}
              }
            }")

          echo "report_id=$(echo $RESPONSE | jq -r '.report_id')" >> "$GITHUB_OUTPUT"
          echo "upload_id=$(echo $RESPONSE | jq -r '.upload_id')" >> "$GITHUB_OUTPUT"

      - name: Upload JSON results
        if: always()
        run: |
          # Upload all JSON files in one multipart request
          FILES=""
          for f in playwright-report/*.json; do
            FILES="$FILES -F files=@$f"
          done

          curl -sf -X POST \
            "$TSIO_API/reports/upload/${{ steps.register.outputs.report_id }}/${{ steps.register.outputs.upload_id }}/json" \
            -H "Authorization: Bearer ${{ steps.oidc.outputs.token }}" \
            $FILES

      - name: Upload screenshots
        if: always()
        run: |
          if [ -d "test-results" ]; then
            FILES=""
            find test-results -name '*.png' -o -name '*.jpg' | while read f; do
              FILES="$FILES -F files=@$f"
            done
            if [ -n "$FILES" ]; then
              curl -sf -X POST \
                "$TSIO_API/reports/upload/${{ steps.register.outputs.report_id }}/${{ steps.register.outputs.upload_id }}/screenshots" \
                -H "Authorization: Bearer ${{ steps.oidc.outputs.token }}" \
                $FILES
            fi
          fi

  complete-report:
    needs: e2e-playwright
    if: always()
    runs-on: ubuntu-latest
    permissions:
      id-token: write
    steps:
      - name: Get OIDC token
        id: oidc
        run: |
          RESPONSE=$(curl -sS \
            -H "Authorization: bearer $ACTIONS_ID_TOKEN_REQUEST_TOKEN" \
            "${ACTIONS_ID_TOKEN_REQUEST_URL}&audience=api://mattermost-tsio")
          TOKEN=$(echo "$RESPONSE" | jq -r '.value')
          echo "::add-mask::$TOKEN"
          echo "token=$TOKEN" >> "$GITHUB_OUTPUT"

      - name: Complete report
        run: |
          curl -sf -X POST "$TSIO_API/reports/complete" \
            -H "Authorization: Bearer ${{ steps.oidc.outputs.token }}" \
            -H "Content-Type: application/json" \
            -d '{
              "repository": "${{ github.repository }}",
              "commit": "${{ github.sha }}",
              "gh_run_id": "${{ github.run_id }}",
              "framework": "playwright",
              "name": "playwright-full-enterprise-master"
            }'
```

#### API Flow Summary

| Step | Endpoint | When |
|------|----------|------|
| 1. Begin report | `POST /reports/begin` | Once per name per run (optional) |
| 2. Register report | `POST /reports/register` | Once per shard |
| 3. Upload JSON | `POST /reports/upload/{report_id}/{upload_id}/json` | Per report, multipart |
| 4. Upload screenshots | `POST /reports/upload/{report_id}/{upload_id}/screenshots` | Per report, optional |
| 5. Complete report | `POST /reports/complete` | Once after all shards finish |

All endpoints accept `Authorization: Bearer <OIDC_TOKEN>`. The server extracts repository, commit, and run metadata from the token claims.

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
