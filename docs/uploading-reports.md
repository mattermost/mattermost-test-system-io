# Uploading Reports

## Quick Start

```bash
# Upload a report directory
node scripts/upload-seed.js path/to/report

# Upload all seed data
node scripts/upload-seed.js
```

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `API_URL` | `http://localhost:8080/api/v1/reports` | Upload endpoint |
| `RRV_API_KEY` | `dev-api-key-do-not-use-in-production` | API key |

## Supported Frameworks

### Playwright

Required: `index.html`
Optional: `results.json`, `data/*` (screenshots, traces)

### Cypress (Mochawesome)

Required: `all.json` or `mochawesome.json`
Optional: `mochawesome.html`, `assets/*`, `screenshots/*`

## Notes

- Framework is auto-detected from files
- Video files are rejected to save storage
- Large uploads (100+ files) work fine with the Node.js script
