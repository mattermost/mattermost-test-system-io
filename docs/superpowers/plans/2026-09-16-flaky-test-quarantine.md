# Flaky Test Detection and CI Quarantine implementation ledger

The specification is the complete blueprint supplied by the user in this task, sections 0–9. Work stays local on `codex/flaky-test-quarantine`. Never push. Existing main has a local checkpoint and is behind the remote; preserve that state.

## Implementation sequence

- [x] 1. Exact migrations 000024–000030, identity enrichment in both transactional ingest paths, idempotent backfill, metadata propagation, unit and PostgreSQL tests.
- [x] 2. Trunk health projection, event subscriber, nightly recovery refresh, quarantine lifecycle and lane alarm.
- [x] 3. Pure verdict engine, ten ordered rules, chronology, clusters, area limits, confidence, deterministic input hashing and markdown.
- [x] 4. Authenticated verdict API, public reads, privileged quarantine/policy API, OpenAPI contracts, server wiring and integration tests.
- [x] 5. Health, identity and verdict UI, navigation, filtering, sparklines and admin actions.
- [x] 6. OIDC triage action, GitHub check/status/comment publishing, explicit override precedence, tests and committed distribution files.
- [x] 7. Separate producer patches against the local mobile and webapp repositories, with workflow validation.
- [x] 8. Runbook, full file-content artifact, CI transcript and blast-radius report.

## Integration and decisions

| Components | Shared contract | Decision |
| --- | --- | --- |
| Identity / health / verdict | Exact seven migrations; observations keyed by identity, group, attempt index and lane | Preserve DDL; do not infer test identity from MM-T ID alone. |
| Health / verdict | Distinct completed trunk groups per test, lane and base ref | Collapse retries within a group into one health trial; never count infra stubs. |
| Verdict / API / action | Section 7 response DTO and rendered markdown | Server computes decisions; GitHub token remains in the action. |
| API / UI | Public reads and existing authenticated role model | Privileged mutations require admin role; never accept a caller-supplied role. |
| Action / producers | Default shadow and existing human overrides | Off is a no-op; shadow preserves required raw status. |

Ruling: Infra classification cannot be overwritten by cluster/area rules — the hard rule requires rerun/action_required regardless of statistical evidence.

Ruling: When the health table's stated ranges leave a gap, use unknown. In particular, with 30 runs, all passes yield a smoothed rate of 1/32, above the specified healthy cutoff of 0.02; do not silently alter the specified formula or threshold.

Ruling: A missing exact base-SHA observation does not establish Git ancestry. Time ordering is only a conservative proxy; document the fallback and avoid treating newer fixes as inherited by the PR.

Ruling: The requested webapp shell expression can pass an enforce-mode blocking verdict when the raw summary passes. Use an explicit mode branch so enforce obeys the verdict, preserving manual override precedence.

## Verification

Initial focused tests could not write the default Go cache. Use `/private/tmp/tsio-go-build` instead. The installed `/usr/local/bin/docker` link is broken; investigate a working Docker runtime before claiming E2E verification.

Final verification: full `make ci` passed with writable temporary Go/lint caches and Docker Desktop socket override. All Go race/unit/contract/PostgreSQL suites, 56 web tests, 14 infra tests, 36 action tests, format/lint/typechecks and production builds passed. New action npm audit: zero vulnerabilities. Producer patches passed local apply/YAML/actionlint and script checks. No commits or pushes.
