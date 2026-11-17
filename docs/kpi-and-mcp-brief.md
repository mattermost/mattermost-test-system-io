# Brief: E2E KPIs and MCP for TSIO

Working brief for engineers and AI agents on two epics. Read this before changing code for them.

- **SEC-11376** — capture KPI information in TSIO (due 2026-12-25)
- **SEC-11375** — MCP server for TSIO (due 2026-11-30)

Team proposals (human-facing): "TSIO Proposals for Team Review" doc. This brief is the agreed scope; when the two differ, ask before acting.

## Goal in one line

Make TSIO's E2E data trustworthy and sliceable by stage, then expose it through a small read-only MCP and a handful of KPIs for QA, developers and managers. No AI features.

## Decisions already made (do not reopen)

- **AI triage keeps clearing red lanes** in enforce mode (`mattermost-test-automation-toolkit`, `actions/e2e-test-triage`). We store its verdicts; we do not change how it decides.
- **Triage labels are stored as-is.** No mapping to a separate failure taxonomy.
- **Server E2E reports to staging TSIO on purpose.** Do not change the target.
- **Flaky has one definition:** a test that both passed and failed on the same commit (runner retry, TSIO retest, workflow rerun, or two lanes on one SHA).
- **Out of scope:** `pr-test-analysis`, toolkit security hygiene, toolkit refactors, test selection beyond fail-fast ordering, AI classification or AI summaries, custom charts in the React app.

## Consumers (how data arrives)

| Repo | Framework | How it uses TSIO |
|---|---|---|
| `mattermost/mattermost` | Playwright, Cypress | Orchestrated: `dispatch-begin` → `dispatch-run` ×30–40 workers → `summary` |
| `mattermost/mattermost-mobile` | Detox, Maestro | Upload-only: `report-upload`, one group per platform job |
| `mattermost/desktop` | Playwright | Upload-only: `report-upload`, one group per run, ~10 shard reports |
| `mattermost-test-automation-toolkit` | — | Reads TSIO REST (`actions/shared/src/tsio.ts`) to triage, rerun, autofix |

Stage (PR / master / release / compatibility matrix) is not stored today. It is encoded in `report_groups.name` (`playwright-full-enterprise-master`, `mobile-pr-ios`, `desktop-pr`, `cmt-desktop`) and `gh_pr_number`.

## KPIs (v1)

Defined once, server-side, sliced by stage, repo, lane, platform and owning team. Skipped tests and synthetic "spec did not run" stubs are excluded from every pass-rate denominator.

1. **Master first-attempt pass rate:** tests passing on their first attempt ÷ tests run, on master/main, per week.
2. **PR false-red rate:** red PR lanes that went green with no code change (rerun, retest, same SHA) or were cleared/overridden ÷ red PR lanes.
3. **Flaky tests**, ranked by damage: PR runs turned red and rerun minutes, by owning team.
4. **Regressions caught at PR vs escaped to master/release**, from stored triage verdicts.
5. **PR E2E time**, p50 and p90 wall clock per lane.

Also wanted, lower priority: master red time and time to recover; flaky age (first/last seen); verdict mix per stage, AI clear rate, escaped-after-clear, verdict coverage; "didn't really test" rate (incomplete groups, stubs); slowest specs and duration trend; skipped-test trend.

## Phase 0: data fixes (do first)

All verified in code at `c31c00f`.

| Problem | Where | Fix |
|---|---|---|
| Cypress retries dropped: `RetryCount` / `Retry` hard-coded 0 | `apps/server/internal/ingest/cypress.go:146`, `:204` | Read Cypress `attempts`; one `test_cases` row per attempt, like Playwright |
| Playwright `projectName` parsed but not stored; tags and annotations dropped | `apps/server/internal/ingest/playwright.go:25`, `:100-130` | Store project, tags, annotations (column `test_cases.annotations` exists, unused) |
| Unknown Playwright status maps to **passed** | `apps/server/internal/ingest/playwright.go:159-172` (default branch) | Map unknown to failed or reject; never silently pass |
| Pass-rate logic exists 3 times and disagrees on `interrupted` | `apps/web/src/components/report_card_parts/utils.ts` (`calculatePassRate`), `apps/server/internal/htmlpage/home_row.go:242`, report rollups in `apps/server/internal/api/reports/` | One Go package owns pass/fail/flaky/skip/interrupted/stub; SSR, JSON API and KPI SQL use it |
| Suite line/col never written | `apps/server/internal/ingest/consolidate.go:83` | Write `line` so a repro command can point at the test |
| Traces never stored: `artifacts` table has no writer | schema `apps/server/migrations/000006_artifacts.up.sql`, no `INSERT INTO artifacts` anywhere | Upload Playwright traces on failure and record them |
| History error excerpt truncated to 400 chars; triage compares it to the full error, so its "same failure twice" rule never fires on long errors | `apps/server/internal/api/reports/history.go:20` (`historyErrorChars`) | Compute an error signature at ingest (normalize first error line, hash) and return it |
| Late report can finalize a unit another worker now holds (checks unit state, not lease) | `apps/server/internal/orchestration/complete.go:461-470` | Compare the reporting lease with `dispatch_units.current_lease_id` |
| Any authenticated caller can delete any run; CI token's repo is not matched to the body's repo | `apps/server/internal/api/reports/handlers.go:583`, `apps/server/internal/api/auth/middleware.go` | Enforce roles; bind OIDC `repository` claim to the request. Required before any verdict write |
| OpenAPI documents routes that don't exist (`/report-groups`, `/admin/*`) and omits `/reports/begin`, `/register`, `/upload/*` | `apps/server/api/openapi.yaml` (see `:1173`, `:1216-1306`); routes in `apps/server/internal/server/server.go:153-222` | Make spec match code; extend `tests/contract` to fail on drift |

## Phase 1–2: new data

Additive migrations only (new `NNNNNN_*.up.sql` / `.down.sql` in `apps/server/migrations/`).

- **Run context on `report_groups`:** `stage`, `trigger`, `product`, `platform`, `lane`, `base_branch`, `server_version`, `app_version`, `edition`. Optional inputs on `POST /orchestration/begin`, `/reports/begin`, `/reports/register`, and on the `dispatch-begin` / `report-upload` actions. Derive `stage` for old rows from `name` + `gh_pr_number`, flagged `stage_source = 'derived'`.
- **Test identity on `test_cases`:** `test_key` = hash(repo, normalized spec path with `/`, full title, project); `zephyr_id` parsed from `MM-T\d+` in the title; `tags`; `error_signature`; `synthetic` flag for stubs.
- **Owner map:** a config file mapping Playwright tags / Cypress `// Group:` / spec paths → team and Mattermost channel.
- **Verdict storage:** `POST /api/v1/verdicts` + `test_verdicts` table. Triage already writes a findings file (schema 2) ready to send. Written by the triage action through REST, never through the MCP.
- **KPI layer:** SQL views + materialized views refreshed by the existing reaper (`apps/server/internal/api/reports/reaper.go`). Dashboards in Metabase or Grafana on a read replica; TSIO UI adds only a test-history page.

## MCP (SEC-11375)

- **Shape:** separate binary (e.g. `apps/server/cmd/tsio-mcp`) in the same Go module, calling the TSIO REST API. Official Go MCP SDK, pinned to an exact version ≥ v1.4.1 (latest seen: v1.8.0). stdio first; any HTTP endpoint internal-only.
- **Read-only tools (v1):**
  - `find_run(repository, sha | pr, lane?)` — needs a new read-only lookup endpoint; consumers currently call `POST /reports/begin` (a write) to find a group id.
  - `get_run_summary(run)` — counts, verdict, durations, missing shards.
  - `list_failures(run, limit)` — error (truncated), stack, error signature, tries, screenshot links, spec file.
  - `get_test_history(test, branch?, days?)` — also answers "is master already broken for this test?".
  - `get_orchestration_status(run)` — queue, leases, timeouts.
  - `list_flaky_tests(repo?, team?, limit)` — ranked by damage.
  - Later: `signature_matches` (across repos), `get_verdicts`, KPI tools.
- **Rules:**
  - Every CI-authored string (test titles, errors, stacks, annotations, branch, name) is returned in a field marked untrusted, length-capped, control characters stripped.
  - Every result is bounded; `/cases` and `/orchestration/status` are unpaginated today, so add pagination first.
  - No presigned S3 URLs by default.
  - No write tools.
- **Done when** it answers, with bounded output: why is lane X red on PR N; is test T flaky on master; what timed out in run R.

## Developer help (same data)

- Label each failed test in the PR job summary: new / known flaky / broken on master / infra.
- Copy-paste repro: `npx playwright test <file> -g "<title>"` (add `:line` once line is stored).
- Playwright trace and test-history link on every failure.
- Weekly per-team digest to Mattermost from the KPI views (needs the owner map).
- Fail-fast spec order (server E2E only): order the TSIO queue by failed earlier on this PR → failing on master → longest-first as today.

## Order of work

1. Phase 0 fixes, OpenAPI repair, pagination, read-only `find_run` endpoint.
2. MCP v1.
3. Run context, test identity, error signature, owner map.
4. Role checks → verdict endpoint.
5. KPI views → dashboards → digest.

## Working in this repo

- Follow `AGENTS.md` and `CLAUDE.md`. Code conventions win over docs when they disagree.
- **Migrations:** `AGENTS.md` says edit migrations in place (pre-v1.0), but prod runs migrations at startup on a retained database and history is additive. Add new migrations; do not edit existing ones.
- Tests: `make test-server` (Go unit + integration), `make test-server-e2e` (testcontainers, needs Docker), `make ci` before a PR.
- PR title: `feat(scope): desc` or `fix(scope): desc`.

## Known doc/code mismatches (trust the code)

- `README.md` / `AGENTS.md` say `/orchestration/status` needs auth; it is public.
- `README.md` / `AGENTS.md` omit Maestro, Detox/Maestro orchestration, `/orchestration/runs`, `/reports/grouped`, `/consolidated`, `/history`.
- OpenAPI says orchestration "requires Uploader role" (no role is enforced) and that begin "does NOT touch report_groups" (it seeds one).
- `TSIO_SESSION_SECRET` is documented as signing sessions; it is unused.

## Open questions

- Which verdict-based numbers matter most to QA (pending team review).
- Metabase or Grafana: use whichever Mattermost already runs.
- Who adds the verdict write to the triage action.
