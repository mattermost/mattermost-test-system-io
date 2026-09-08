# Triage completion contract

Implementation scope: TSIO decisions and durable work ownership; Mattermost callers,
Guardian repair verification and mutually exclusive Jira defect escalation. Impact
Gate remains owned by the other task. Historical master failures are observations,
not proof that a PR is innocent. Automatic clearance remains disabled pending
independently reviewed shadow measurements.

## Interfaces and ownership

- Assessment leaf owns `internal/triageassessment`, migration 000031 and its tests.
  Exports `Handlers{Pool, Logger, MasterCadence}` with `Attribution` (GET, read-only), `Record`
  (POST, durable assessment), `Verdict` (GET by id). Base path `/triage`.
  Run selector JSON/query: `repository`, `commit_sha`, `gh_run_id`,
  `gh_run_attempt`, `name`. Server derives failed tests from the complete group;
  caller cannot choose which failures to omit. Response includes `id` for recorded
  decisions, `outcome`, `can_unblock`, `reasons`, `tests`, exact `run` identity.
  Outcomes: no_failure, pr_suspect, observed_on_master, unknown. A master match alone
  never permits clearance. Read-only previews cannot authorize any action.
- Work leaf owns `internal/triagework`, migrations 000032 and tests. Exports
  `Handlers{Pool, Logger, Jira, QuarantineCap, LeaseTTL, SourceWorkflowRefs}` and concrete config/client types
  agreed with root; root owns main/config/router/OpenAPI wiring.
  API: GET `/triage/repairs?repository=...`, POST `/triage/repairs/enqueue`
  with `{report_group_id, stable_key, owner, ticket?}`. Ticket is optional for the
  repair queue and required for quarantine. Derive file/title/framework/
  project/commit/image/suite from a complete trusted master report; reject aliases
  spanning multiple file/title/project identities. Work responses use `{items:[]}`.
  POST `/triage/repairs/claim` `{repository, worker}` returns `item` (null if empty)
  with `id`, `lease_token`, `lease_expires_at`, run/test evidence and `attempt`.
  POST `/triage/repairs/{id}/heartbeat` `{lease_token}` renews active ownership.
  POST `/triage/repairs/{id}/complete` `{lease_token,outcome,account,evidence_url,pr_url}`
  where outcome is `failed|blocked|repair_pr|product_suspect`; append attempts, fence
  expired workers, stop at three failed/blocked/expired attempts. Terminal
  product_suspect must prevent subsequent test-repair outcomes for that claim.
  POST `/triage/repairs/{id}/resolve` `{account,evidence_url,pr_url}` records a
  trusted human resolution of `needs_human` or `repair_pr` work without allowing
  a fourth automatic attempt. A strictly newer verified master failure can start
  a new cycle; replaying old evidence cannot.
  POST `/triage/repairs/{id}/defect` `{lease_token,summary,description}` uses live
  unresolved Jira dedup and durable serialized submission ownership. Preserve an
  uncertain submission (timeout/crash) for reconciliation instead of blind retry.
  GET `/triage/defects?repository=...` returns caught-product-suspect events, no
  invented escaped-release metric or locally mirrored Jira resolution.
  Its `id` is a composite repair/group string, not a UUID. Jira receipts are
  optional; repeated observations remain visible when an unresolved issue is reused.
  Quarantine POST `/triage/quarantine` `{repair_id,owner,ticket,expires_at}`; GET list.
  Expiry restores blocking eligibility, never deletes tests or changes raw rows.
- Workflow leaf owns ONLY new `.github/scripts/triage-*` and new
  `.github/workflows/e2e-triage-*` files in `/private/tmp/mattermost-pr38356-demo`.
  Includes diagnosis/reconciliation callers, Guardian harness/provider execution,
  mechanical edit policy and tests. It coordinates final JSON shapes directly with
  work/assessment leaves. Root owns existing-file changes and TSIO integration.
- Root owns auth boundary, config, server registration, OpenAPI, CLI integration,
  documentation and final verification. Mutations require a trusted workflow or
  dedicated triage credential; ordinary public PR upload credentials are insufficient.
  `X-Triage-Key` or verified allowlisted workflow OIDC authenticates a writer.
  `X-TSIO-Triage-Actor` and `X-TSIO-Triage-Repository` are overwritten by middleware;
  OIDC requests remain restricted to their authenticated repository.
  Repair source evidence separately requires verified master producer claims and
  immutable report receipts. Migration 33 binds upload ownership and stores verified
  Begin receipts for uploaders that omit the shard count during registration.
  Historical rows are not retrospectively attested.

No automatic merge, no test removal on expiry, no change to existing full-suite
coverage. Live credential-dependent paths fail visibly when enabled and unconfigured.
Credentials are never logged. Exact digests and retry-free nonempty verification are
required before a repair PR. Missing causal labels, production history or human PR
review cannot be replaced by fixture metrics.
