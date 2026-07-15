# Test System IO Development Guidelines

Read [AGENTS](./AGENTS.md)

## Active Technologies
- Go 1.26 (latest stable; project tracks latest stable, pinned via `go.mod` toolchain directive) + chi (router), pgx/v5 (Postgres driver), aws-sdk-go-v2 (S3), coreos/go-oidc/v3, golang.org/x/oauth2, coder/websocket, getkin/kin-openapi (serve + validate openapi.yaml), caarlos0/env/v11, log/slog (stdlib), spf13/cobra (admin CLI).
- PostgreSQL 18.3 (pinned — matches stdlib `uuidv7()` used for PKs); S3-compatible object store (AWS S3 in prod, MinIO in dev).
- React 19 + TypeScript + Vite 8 + Tailwind CSS 4 + TanStack React Query (embedded into the Go binary; served at `:8080` alongside the API).
