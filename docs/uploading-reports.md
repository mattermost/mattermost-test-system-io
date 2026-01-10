# Uploading Reports

This guide explains how to upload Playwright HTML reports to the server.

## Prerequisites

- Server running at `http://localhost:8080` (or your configured URL)
- Valid API key (default for development: `dev-api-key-do-not-use-in-production`)
- Report files including at minimum `index.html`

## Required Files

- `index.html` (required) - The main Playwright HTML report
- `results.json` (optional) - Test results data for extraction
- `results.xml` (optional) - JUnit XML report
- `data/*` (optional) - Screenshots, videos, and other attachments

## Upload Methods

### 1. Using the Upload Script (Recommended)

The easiest way to upload reports is using the provided script:

```bash
# From project root - uploads both seed directories by default
./scripts/upload-seed.sh

# Upload a specific directory
./scripts/upload-seed.sh seed/playwright-reporter

# Upload multiple specific directories
./scripts/upload-seed.sh seed/playwright-reporter /path/to/another/report

# Upload any directory with report files
./scripts/upload-seed.sh /path/to/your/playwright-report

# With custom API URL and key
API_URL=http://your-server:8080/api/v1/reports \
RRV_API_KEY=your-api-key \
./scripts/upload-seed.sh /path/to/report
```

### 2. Using curl (Manual)

For a single directory with all report files:

```bash
cd /path/to/your/playwright-report

curl -X POST http://localhost:8080/api/v1/reports \
  -H "X-API-Key: dev-api-key-do-not-use-in-production" \
  $(find . -type f ! -name ".DS_Store" | sed 's|^\./||' | while read f; do printf -- "-F files=@./$f;filename=$f "; done)
```

For explicit file uploads:

```bash
curl -X POST http://localhost:8080/api/v1/reports \
  -H "X-API-Key: dev-api-key-do-not-use-in-production" \
  -F "files=@index.html;filename=index.html" \
  -F "files=@results.json;filename=results.json" \
  -F "files=@results.xml;filename=results.xml"
```

## Response

A successful upload returns:

```json
{
  "id": "019ba779-11c2-7522-a80c-da412c316210",
  "created_at": "2026-01-10T10:34:48.130393+00:00",
  "files_count": 26,
  "extraction_status": "completed",
  "message": "Report uploaded and extracted successfully"
}
```

## Extraction Status

- `completed` - Report data successfully extracted from results.json
- `pending` - Extraction in progress
- `failed` - Extraction failed (check error_message)
- `skipped` - No results.json found, HTML report still viewable

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `API_URL` | `http://localhost:8080/api/v1/reports` | Server upload endpoint |
| `RRV_API_KEY` | `dev-api-key-do-not-use-in-production` | API authentication key |

## Troubleshooting

### "Missing required file: index.html"

Ensure your upload includes `index.html` with the correct filename. When using curl with `-F`, the `filename=` parameter must not include `./` prefix.

### "Invalid API key"

Check that you're passing the correct `X-API-Key` header matching your server configuration.

### "Invalid Header provided"

This usually happens when curl arguments are malformed. Use the recommended script or the manual curl command format above.
