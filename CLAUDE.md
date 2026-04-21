# Test System IO Development Guidelines

Read [AGENTS](./AGENTS.md)

## Active Technologies
- Go 1.26 (latest stable; project tracks latest stable, pinned via `go.mod` toolchain directive) + chi (router), pgx/v5 (Postgres driver), sqlc (codegen), aws-sdk-go-v2 (S3), coreos/go-oidc/v3, golang.org/x/oauth2, coder/websocket, getkin/kin-openapi (serve + validate openapi.yaml), caarlos0/env/v11, log/slog (stdlib), spf13/cobra (admin CLI) (006-rust-to-go)
- PostgreSQL 18.3 (pinned — matches stdlib `uuidv7()` used for PKs); S3-compatible object store (AWS S3 in prod, MinIO in dev) (006-rust-to-go)

## Recent Changes
- 006-rust-to-go: Pinned PostgreSQL 18.3 (enables stdlib `uuidv7()` for PK defaults).
- 006-rust-to-go: Pinned Go 1.26 (policy: track latest stable).
- 006-rust-to-go: Added Go 1.26 backend stack (chi, pgx/v5, sqlc, aws-sdk-go-v2, coreos/go-oidc/v3, golang.org/x/oauth2, coder/websocket, getkin/kin-openapi, caarlos0/env/v11, log/slog, spf13/cobra), replacing the Rust prototype.
