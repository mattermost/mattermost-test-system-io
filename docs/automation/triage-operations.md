# Triage, repair and product-defect operation

This implementation adds the missing repair and defect machinery. It does not
establish production accuracy or constitute a live autonomous-repair demo.
Impact Gate supplies advisory selection plans; TSIO records actual execution and
the companion Mattermost workflow compares them without reducing dispatch.

| Capability | Implemented behavior | Remaining live acceptance |
| --- | --- | --- |
| Run assessment | Complete exact-run derivation, collision checks, bounded master history and an append-only decision record | Independently adjudicated shadow measurements; no automatic clearance |
| Master repair | Trusted-master discovery, CODEOWNERS owner, atomic fenced claim, exact server image reproduction, isolated candidate execution, repeated retry-free verification, proposed PR with human review | Configure provider and ownership; run a genuine repair; obtain human review |
| Product defect | Product-suspect branch cannot request/apply a test patch; live Jira Cloud deduplication and durable uncertain-submission recovery | Configure Jira; exercise a genuine product-defect handoff |
| Quarantine | Bounded ownership/ticket/expiry metadata, separate from raw test outcomes | No test-selection or nonblocking-lane rollout is enabled by this change |

## Server configuration

Reads under `/api/v1/triage` are public. Mutations accept either `X-Triage-Key`
with `TSIO_TRIAGE_API_KEY`, or a verified GitHub OIDC identity with audience
`mattermost-test-system-io` and an exact workflow in
`TSIO_TRIAGE_WORKFLOW_REFS`. Upload keys, human session cookies and PR workflow
identities do not gain triage authority. OIDC principals are repository-scoped;
the dedicated service key is an administrative credential across repositories.

Staging infrastructure grants the two Mattermost **master-branch** workflow refs:

```text
mattermost/mattermost/.github/workflows/e2e-triage-shadow.yml@refs/heads/master
mattermost/mattermost/.github/workflows/e2e-triage-repair.yml@refs/heads/master
```

Production has no triage writer allowlist by default. Configure that explicitly
when rolling out the workflows there. No Cursor label PAT is needed for OIDC.

Repair evidence has a different trust boundary:
`TSIO_TRIAGE_SOURCE_WORKFLOW_REFS` defaults to Mattermost's
`e2e-tests-on-merge.yml@refs/heads/master`. A PR test job running a master-hosted
dispatcher is insufficient. Every shard needs matching verified repository,
ref, workflow revision, run and attempt claims, plus its registration receipt.
The tested `commit_sha` comes from the immutable run/receipt identity;
`source_workflow_sha` comes from unanimous verified OIDC claims. These may differ
for a dispatched master run. Guardian independently checks the GitHub run's head
against `source_workflow_sha` and the tested commit's master ancestry. Mixed
workflow revisions or receipt/tested-commit mismatches remain rejected. Legacy
queue items without the source revision cannot authorize repair.
The expected shard count needs a verified Begin receipt or shard declaration.
The target test's receipts supply the image and harness metadata; an earlier
unauthenticated report-group metadata value cannot substitute for them.

Migration 33 adds catalog-only nullable report provenance fields and a small
receipt table. New OIDC registrations persist verified claims atomically with
their report. Upload ownership is checked before any object-storage write.
Historical OIDC/session uploads without a bound principal cannot be resumed
after deployment; finish in-flight runs first or start a fresh run. Historical
rows are retained and are not backfilled with invented provenance.

Default policy settings:

```text
TSIO_TRIAGE_MASTER_CADENCE=24h
TSIO_TRIAGE_QUARANTINE_CAP=5
TSIO_TRIAGE_LEASE_TTL=15m
TSIO_JIRA_ENABLED=false
```

The cadence is a configurable freshness assumption, not a measured schedule.
Assessments use a 14-day window, at least three distinct baseline commits, and
freshness `max(2 × cadence, 4h)` relative to the assessed run. A master history
match remains `observed_on_master`; `can_unblock` is always false in shadow-v1.
The database also rejects clearance values for this policy. Turning a repository
flag on cannot bypass this. `baseline_failures` counts runs with failed attempts;
`baseline_failed_runs` and `baseline_flaky_runs` distinguish terminal failures
from retry survivors.

## Mattermost workflows

The companion changes are in PR #38356 under `.github/scripts/triage-*` and
`.github/workflows/e2e-triage-*`. Scheduled/default-branch workflows become active
only after those files land on master. Updating a PR does not activate its cron
or workflow-run consumers.

Repository variables:

```text
MM_TRIAGE_TSIO_URL=https://staging-test-io.test.mattermost.com/api/v1
MM_TRIAGE_ENABLED=false
MM_TRIAGE_REPAIR_ENABLED=false
MM_TRIAGE_SET_STATUS=false
MM_TRIAGE_PROVIDER=openai
MM_TRIAGE_MODEL=<an explicitly chosen model supporting structured Responses output>
MM_TRIAGE_CLEAN_RUNS=5
```

The Guardian's implemented proposal adapter uses OpenAI's structured Responses
API and repository secret `MM_TRIAGE_OPENAI_API_KEY`. It is separate from the
existing Cursor diagnosis automation; a Cursor API key cannot be substituted.
The model receives the bounded test source and reproduction evidence, with no
tools or repository token. Its product-suspect response has no edit field; only
the repair branch can request replacement source.

OIDC needs no `TSIO_TRIAGE_API_KEY` repository secret. If OIDC is unsuitable,
configure that optional secret to match the server's dedicated triage key.
The existing enterprise license secret is needed when the recorded run used a
license. A GitHub App/PAT in `MM_TRIAGE_GITHUB_TOKEN` is optional for publishing
repair PRs; using the default `github.token` is subject to repository PR-creation
policy and GitHub's restrictions on triggering subsequent workflows. Never
interpret a created PR as one whose normal CI or human review has passed.

Add an agreed owner to Mattermost's trusted CODEOWNERS for the relevant E2E
files. Discovery refuses to invent one. A ticket is optional for queue entry;
quarantine requires its own owner, tracking ticket and expiry.

The repair harness supports recorded Linux x64 on-premises Cypress/Electron and
the implemented Playwright projects. It rejects missing digests, unsupported
environment metadata, missing matching framework versions, empty/skipped
verification, changed test identities and surviving retries. It resolves and
records a runner-image digest too; this is not a historical runner-image record
that did not previously exist. Candidate code runs without controller credentials,
host PID namespace or Docker socket, with read-only source and bounded output.
Successful repeated runs are finite evidence, not proof of zero future flakes.

Publication requires master still equal the tested commit at each publication
fence. Any advancement, including a helper-only or unrelated change, requires
fresh master evidence and revalidation. Reused repair PRs must have that exact
commit as their sole parent. This prevents an old failure such as the deleted
image dependency fixed by Mattermost #38330 from authorizing a stale repair.
Master can still advance after the last read; normal PR CI and human review
remain necessary.

The mechanical E2E policy executes trusted base-branch policy code. Human changes
receive blocking findings for detectable deletions, skips, bare sleeps and raised
literal timeouts, and review annotations for semantic uncertainty. Guardian
proposals have stricter assertion-preservation rules. This check still needs to
be required in branch protection if it is to block merges.

## Durable work and defect lifecycle

- `POST /triage/repairs/enqueue` takes a report group, stable key and owner. The
  server resolves the actual file, title, project and recorded environment.
- `POST /triage/repairs/claim` returns one lease. Heartbeats renew it; expired or
  superseded tokens cannot finish another worker's attempt.
- `POST /triage/repairs/{id}/complete` records `failed`, `blocked`, `repair_pr` or
  `product_suspect`. Three unsuccessful/expired attempts become `needs_human`.
- `POST /triage/repairs/{id}/resolve` records a trusted human resolution and its
  evidence/PR link without granting a fourth autonomous attempt. A later verified
  failure can create a new work cycle; old attempts remain visible.
- `POST /triage/repairs/{id}/defect` requires the product-suspect outcome/token.
  A durable submission intent precedes Jira creation. An uncertain request is
  reconciled by its submission marker; absence from an eventually consistent
  search is not permission to create again.

Enable Jira only with all of:

```text
TSIO_JIRA_ENABLED=true
TSIO_JIRA_BASE_URL=https://<your-site>.atlassian.net
TSIO_JIRA_EMAIL=<service-account email>
TSIO_JIRA_API_TOKEN=<secret reference supplied by deployment>
TSIO_JIRA_PROJECT_KEY=<project>
TSIO_JIRA_ISSUE_TYPE=Bug
```

This adapter targets Jira Cloud REST v3. Enabled but incomplete configuration
fails startup; deliberately disabled filing returns an explicit unavailable
result without interrupting ingestion. The tracker remains the source of issue
resolution. A strictly newer verified master failure can produce a new escalation
after an old ticket closes. Pending uncertain submissions are reconciled first.
No test edit or successful E2E status is a side effect of filing a defect.

`GET /triage/repairs`, `/triage/defects`, and `/triage/quarantine` require a full
repository slug and report truncation. Defect observations distinguish suspected
product catches from flakiness; they do not assert confirmed bugs or escaped
release defects. Queue priority counts distinct PRs with observed matching
terminal test failures, not independently proven causal PR blocks.

Quarantine expiry restores blocking eligibility in the metadata and never deletes
a test. Existing CI selection and all raw test rows remain unchanged. This is
not evidence that the master flake rate has improved.

## Release evidence

Run `make ci` in TSIO and the new Node fixtures/actionlint in Mattermost before
rollout. The PostgreSQL triage suites are explicitly included in the TSIO E2E
Make target, rather than being omitted by their internal-package location.
The independent implementation review is
[`../reviews/2026-09-08-triage-implementation-review.md`](../reviews/2026-09-08-triage-implementation-review.md).
The historical diagnosis/manual-waiver demo remains in `staging-demo-evidence.md`.

Local integration verification on 2026-09-08:

- `make ci` passed vet, formatting, lint, type checking, all Go unit/race tests,
  61 frontend tests, 14 infrastructure tests, and 35 existing automation tests.
  Its PostgreSQL E2E packages for contract, admin, OIDC, reports, the baseline CLI,
  assessment and durable triage work passed.
- That run stopped on an outdated unknown-route fixture and one orchestration
  PostgreSQL container startup timeout. The fixture now uses a genuinely absent
  triage route. An uncached race run of both entire affected packages passed:
  testhistory in 42.506s and orchestration in 55.093s. This was a targeted recovery,
  not a claim that the original uninterrupted `make ci` succeeded.
- The remaining `make build actions-dist-check` stages passed after the test
  retry, including production web/server builds and action-bundle drift checks.
- Mattermost's `.github/scripts/triage-protocol.test.mjs` passed 47 tests; all
  three new workflows passed actionlint. These exercise the caller contracts and
  adversarial cases, not a live Linux browser repair or a real Jira submission.

A new live autonomous demo still requires a fresh provenance-bearing master
failure, an assigned owner, the model/Jira credentials appropriate to the chosen
path, actual verification, and human review of a real repair PR. No fixture or
historical manual waiver satisfies those live acceptance criteria.
