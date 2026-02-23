# Test System IO

Rust API + React frontend for viewing Playwright test reports.

## Quick Start
```bash
make dev    # Start servers (API :8080, Web :3000)
make ci     # Run checks, lint, test, build
```

## Structure
```
apps/server/   # Rust API (Actix-web, PostgreSQL)
apps/web/      # React (Vite, TailwindCSS, lucide-react)
```

## API (`/api/v1`, auth: `X-API-Key` header)
- `GET /health` - Health check
- `GET /reports` - List reports
- `GET /reports/{id}` - Report details
- `GET /reports/{id}/suites` - Test suites
- `POST /reports` - Upload (multipart)

## Style
- Rust: `cargo fmt` + `clippy`
- TypeScript: `eslint` + `prettier`
- Files: `snake_case.tsx`
- Icons: lucide-react
- UI: shadcn/ui patterns
- CSS: TailwindCSS (dark mode supported)
- Deps: exact versions only

## PR
Run `make ci` then use: `feat(scope): desc` or `fix(scope): desc`
