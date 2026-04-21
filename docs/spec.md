# Test System IO — Design Spec

A centralized test system for ingesting test results, managing test cases, driving CI interactions for full testing, retesting, and smart testing, and enforcing release quality gates across all Mattermost product repositories. Replaces the existing Test Automation Dashboard currently used for Cypress. End goal for test management: replace Zephyr Scale with source-of-truth in code.

## Key Principles

- **Focus** — test results ingestion and test case management; keep it simple
- **Central record, distributed truth** — single source of truth for all test results; central index of test cases defined in each product repository
- **Read-only UI** — the web dashboard is for viewing only; all writes, auth, and access control happen through GitHub (OIDC) and Mattermost integrations
- **GitHub and Mattermost as first-class interfaces** — all authentication, authorization, and interaction flow through GitHub (OIDC, PR comments, labels, workflow dispatch) and Mattermost (notifications, commands); the system has no separate user management
- **AI-ready, not AI-embedded** — no AI internally; the system exposes structured data and screenshots (via API, MCP) for external AI tools to perform failure analysis, visual analysis, smart test selection, and trend detection

## Why Build In-House

- **No single platform ingests all our sources** — Cypress Cloud and Playwright Workspaces only serve their own framework; BrowserStack and Currents cover multiple but miss RainforestQA and Zephyr manual results; LambdaTest/TestMu AI and Testim require running tests on their grid
- **Closest alternatives still fall short** — Currents (multi-framework, PR feedback) has no test case management and is pay-walled; BrowserStack (external ingestion, TCM, OIDC) centers on its own grid and is per-seat priced; ReportPortal (broad ingestion, self-hosted) lacks GitHub-native PR interaction
- **GitHub-native authorization** — all interaction happens where staff already work; no separate login or permission model; OIDC for CI auth
- **Centralized test records from version-controlled repositories** — test cases live in code; the system aims to centralize and index them across all repos, eventually replacing Zephyr Scale
- **Open by default** — test results and test cases are publicly visible, enabling open-source contributors to verify quality and understand test coverage without requiring accounts or licenses
- **First-class integration within the Mattermost environment** — built-in notifications, interactions, and workflows through Mattermost channels
- **Custom QA and Release gating** — no third-party tool combines automated results (Playwright, Cypress, Detox) with manual results (Zephyr, RainforestQA) under one gate decision; extensible for future tooling

## Tech Stack

| Layer | Technology |
|-------|------------|
| API server | Go |
| Web frontend | TypeScript, React |
| Database | PostgreSQL |
| Object storage | S3 (screenshots, videos), MinIO in dev (or its alternative) |
| Infrastructure | AWS (ECS Fargate, RDS, S3, ALB) via CDK |
| CI/CD | GitHub Actions with OIDC |

**Repository:** https://github.com/mattermost/mattermost-test-system-io (initial POC in Rust, to be migrated to Go)
**Docker Hub:** https://hub.docker.com/r/mattermostdevelopment/mattermost-test-system-io

## Roadmap

### Features

**Phase 1 — Test Results Ingestion**
- GitHub OIDC authentication for CI uploads
- Multi-framework report ingestion — Playwright, Cypress, Detox natively; any framework via standard JSON or JUnit XML
- Artifact storage (screenshots, videos) in S3
- Read-only web dashboard with PR-level consolidation and cross-framework views
- Mattermost notifications for test run results and flaky test alerts

**Phase 2 — CI and Dev Interactions**
- Test orchestration for automatic parallel load balancing
- Retest recommendations — identify what to re-run and provide data for workflow triggers
- Flaky test detection and alerting

**Phase 3 — Release Gates**
- Consolidated release view combining CI, RainforestQA, and Zephyr Scale results
- Audit trail, test coverage and comparison per release
- Integration with the QA release process — gate checks as part of release workflows

**AI-Ready Data**
- Expose historical data and test cases to enable AI-driven smart test selection
- Expose screenshots for external visual analysis

**Phase 4 — Test Case Management**
- Central registry of all test cases indexed from code across all repos
- Migration path from Zephyr Scale — requires QA buy-in; ultimate goal is for this system to become the central hub for all test management

### Rollout

**Development & Deployment**
- Staging and production environments available throughout development
- CI-driven deployment pipeline from day one

**Repo Integration**
1. `mattermost` monorepo — first integration (Playwright and Cypress)
2. `mattermost-mobile` — Detox
3. `desktop` — Playwright
4. `mattermost-plugin-playbooks` — Cypress
5. `mattermost-plugin-calls` — Playwright
6. `mattermost-plugin-agents` — Playwright
