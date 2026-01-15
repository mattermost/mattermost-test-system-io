# API Key Management

The server uses database-backed API keys for authentication. Keys are securely hashed using SHA-256 - the full key is only shown once at creation.

## Key Format

```
rrv_<32 random alphanumeric characters>

Example: rrv_a1B2c3D4e5F6g7H8i9J0k1L2m3N4o5P6
         ^^^^^^^^
         prefix (stored, shown in logs)
                 ^^^^^^^^^^^^^^^^^^^^^^^^
                 secret part (only hash stored)
```

## Roles

| Role | Permissions |
|------|-------------|
| `admin` | Full access: create/revoke keys, upload reports |
| `contributor` | Upload reports only |

## Creating Keys

### Option 1: CLI Tool (Direct Database Access)

```bash
cd apps/server

# Basic key (contributor role, no expiration)
cargo run --bin generate-api-key -- --name "My Key"

# Admin key with 1 year expiration
cargo run --bin generate-api-key -- \
  --name "Admin Key" \
  --role admin \
  --expires-in 365d

# CI key with 90 day expiration
cargo run --bin generate-api-key -- \
  --name "CI - GitHub Actions" \
  --role contributor \
  --expires-in 90d
```

**Output:**
```
────────────────────────────────────────
API Key Generated
────────────────────────────────────────
ID:      550e8400-e29b-41d4-a716-446655440000
Name:    CI - GitHub Actions
Role:    contributor
Expires: 2026-04-14T00:00:00Z

Key:     rrv_a1B2c3D4e5F6g7H8i9J0k1L2m3N4o5P6

⚠️  Save this key! It cannot be retrieved later.
────────────────────────────────────────
```

### Option 2: HTTP API

```bash
# Using admin key (bootstrap)
curl -X POST http://localhost:8080/api/v1/auth/keys \
  -H "X-Admin-Key: dev-admin-key-do-not-use-in-production" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "CI - GitHub Actions",
    "role": "contributor",
    "expires_in": "365d"
  }'

# Using existing admin API key
curl -X POST http://localhost:8080/api/v1/auth/keys \
  -H "X-API-Key: rrv_your_admin_key_here" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "New Contributor Key",
    "role": "contributor"
  }'
```

**Response:**
```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "key": "rrv_a1B2c3D4e5F6g7H8i9J0k1L2m3N4o5P6",
  "name": "CI - GitHub Actions",
  "role": "contributor",
  "expires_at": "2027-01-14T00:00:00Z",
  "created_at": "2026-01-14T12:00:00Z"
}
```

## Listing Keys

### CLI

```bash
cd apps/server
cargo run --bin manage-api-keys -- list
```

**Output:**
```
ID                                   PREFIX       NAME                 ROLE         STATUS
────────────────────────────────────────────────────────────────────────────────────────────
5eff8caa-3f9f-44e4-aa80-96745f9af0fd rrv_ASWr     Admin Key            admin        active
a1b2c3d4-5678-90ab-cdef-1234567890ab rrv_xYz9     CI - GitHub Actions  contributor  active
b2c3d4e5-6789-0abc-def1-234567890abc rrv_pQr7     Old Key              contributor  revoked
```

### HTTP API

```bash
curl http://localhost:8080/api/v1/auth/keys \
  -H "X-Admin-Key: dev-admin-key-do-not-use-in-production"
```

**Response:**
```json
{
  "keys": [
    {
      "id": "550e8400-e29b-41d4-a716-446655440000",
      "key_prefix": "rrv_a1B2",
      "name": "CI - GitHub Actions",
      "role": "contributor",
      "expires_at": "2027-01-14T00:00:00Z",
      "last_used_at": "2026-01-14T15:30:00Z",
      "created_at": "2026-01-14T12:00:00Z",
      "is_revoked": false
    }
  ]
}
```

## Revoking Keys

Revoked keys are soft-deleted (can be restored).

### CLI

```bash
cd apps/server
cargo run --bin manage-api-keys -- revoke --id 550e8400-e29b-41d4-a716-446655440000
```

### HTTP API

```bash
curl -X DELETE http://localhost:8080/api/v1/auth/keys/550e8400-e29b-41d4-a716-446655440000 \
  -H "X-Admin-Key: dev-admin-key-do-not-use-in-production"
```

## Restoring Keys

### CLI

```bash
cd apps/server
cargo run --bin manage-api-keys -- restore --id 550e8400-e29b-41d4-a716-446655440000
```

### HTTP API

```bash
curl -X POST http://localhost:8080/api/v1/auth/keys/550e8400-e29b-41d4-a716-446655440000/restore \
  -H "X-Admin-Key: dev-admin-key-do-not-use-in-production"
```

## Expiration Options

| Format | Example | Duration |
|--------|---------|----------|
| Days | `30d` | 30 days |
| Weeks | `2w` | 2 weeks |
| Months | `6m` | 6 months (180 days) |
| Years | `1y` | 1 year (365 days) |
| None | omit | Never expires |

## Using Keys

### Environment Variable

```bash
export RRV_API_KEY=rrv_a1B2c3D4e5F6g7H8i9J0k1L2m3N4o5P6

# Upload script will use this automatically
node scripts/upload-seed.js path/to/report
```

### HTTP Header

```bash
curl -X POST http://localhost:8080/api/v1/reports \
  -H "X-API-Key: rrv_a1B2c3D4e5F6g7H8i9J0k1L2m3N4o5P6" \
  -F "files=@report/index.html"
```

## Security Notes

1. **Keys are hashed**: Only SHA-256 hash is stored, not the actual key
2. **Show once**: Full key is only displayed at creation - save it immediately
3. **Cannot recover**: Lost keys cannot be retrieved, generate a new one
4. **Prefix for logs**: Only the 8-char prefix appears in logs for identification
5. **Admin key bootstrap**: Remove `RRV_ADMIN_KEY` from production after creating first admin API key

## Authentication Flow

```
Request with X-API-Key: rrv_a1B2c3D4...
    │
    ├─1─► Hash the full key (SHA-256)
    │
    ├─2─► Lookup by key_hash in database
    │     └─► Not found: 401 "Invalid API key"
    │
    ├─3─► Check deleted_at (revocation)
    │     └─► If set: 401 "API key has been revoked"
    │
    ├─4─► Check expires_at
    │     └─► If expired: 401 "API key has expired"
    │
    ├─5─► Update last_used_at timestamp
    │
    └─6─► Allow request, log caller name/prefix
```

## Bootstrap Workflow (First Deployment)

```bash
# 1. Start server with admin key set
export RRV_ADMIN_KEY=your-secure-bootstrap-key
cargo run --bin server

# 2. Create first admin API key
curl -X POST http://localhost:8080/api/v1/auth/keys \
  -H "X-Admin-Key: your-secure-bootstrap-key" \
  -H "Content-Type: application/json" \
  -d '{"name": "Primary Admin", "role": "admin"}'

# Save the returned key!

# 3. Create contributor keys using the admin API key
curl -X POST http://localhost:8080/api/v1/auth/keys \
  -H "X-API-Key: rrv_your_admin_key" \
  -H "Content-Type: application/json" \
  -d '{"name": "CI Upload", "role": "contributor", "expires_in": "365d"}'

# 4. Remove RRV_ADMIN_KEY from environment (optional but recommended)
unset RRV_ADMIN_KEY
```
