# Stored E2E observations and policy replay

This measures retained TSIO test data and replays a simple history policy. It
**does not measure actual blocked GitHub checks, why a PR failed, or the effect
of waivers**. Those need terminal GitHub context/run/attempt outcomes plus
separately recorded decisions and human adjudication. Labels do not rewrite the
raw test rows queried here; save both the raw observations and decision records.

Run either entry point; they execute the same SQL:

```bash
tsioctl db baseline --repo mattermost/mattermost --days 90 > baseline.json
psql "$TSIO_DATABASE_URL" -v repo=mattermost/mattermost -v days=90 \
  -f scripts/baseline/baseline.sql
```

`tsioctl` takes `TSIO_DATABASE_URL` from its existing deployment configuration.
Options are `--branch master`, `--min-runs 5` and `--timeout 5m`. The CLI and psql
wrapper use a read-only repeatable-read transaction. The query reads retained
history to distinguish pre-window failures from first observed arrivals; this
may scan significant data, and the timeout bounds the operation. Record the
output, exact TSIO commit, window and any producer/retention changes with every
comparison. The embedded query is
`apps/server/cmd/tsioctl/db/baseline.sql`.

| Output | Interpretation |
|---|---|
| `coverage` | All registered groups, including incomplete and zero-test groups. Entirely missing workflows/groups cannot be counted from this database. |
| `identity_coverage` | Attempt rows with missing keys, unknown Playwright projects, and old Cypress rows without attempt columns. Populated attempt columns alone do not prove that a Cypress producer supplied retries. |
| `policy_replay` | Observed failed PR run attempts and candidates under K=10/20/50 prior master observations. A candidate is a heuristic, never permission to override a check. |
| `master_test_rates_by_suite` | Rates over executed test observations in complete groups, separately by framework and group name. These are test rates, not whole-CI success rates. |
| `pr_group_fanout` | All registered PR groups per run ID + attempt + commit, including missing test rows. |
| `current_failure_patterns_by_suite` | Each test's own latest suite observation. A persistent candidate needs at least two outright failures with no success after the first non-clean observation in the window. Earlier clean observations do not erase a later break. This is a failure-pattern heuristic, not a root-cause verdict. |
| `observed_arrivals_by_suite` | First non-clean observation in all retained complete master data, if inside the window. Prior clean evidence and absent prior clean evidence are counted separately. These are not proven newly introduced flakes. |
| `master_cadence_by_suite` | Complete registered master groups/day by framework and group name. |

The replay includes only outright failed test outcomes. A failed attempt followed
by a passing retry is a flaky observation and does not create an observed failed
PR run. Run attempts remain separate. Every observed group must have exactly its
expected complete reports, test rows, and keyed identities before a candidate
can qualify. This still cannot account for an entirely unregistered group or an
infrastructure failure absent from test reports.

A history match requires the exact repository, framework, group name and stable
key. Master observations also require the configured branch, no PR identity,
and complete ingestion. The policy uses only observations preceding the PR,
within its configured lookback. Group names must distinguish smoke/full,
enterprise/FIPS and other configurations; this query cannot recover dimensions
that producers merged under one name.

## Historical coverage boundaries

Older Cypress Mochawesome reports omitted retries. A parser that accepts
`attempts[]` does not fix that producer: the producer must actually emit the
array. Historical rows containing only a final pass cannot reconstruct a failed
attempt. Start a new observed-flake baseline after the verified producer rollout;
reporting an increase across that boundary as a regression is misleading.

Older Playwright ingestion dropped project identity. Backfill leaves those rows
with their old unprefixed keys; new keys such as `chrome :: MM-T1234` deliberately
start separate histories. Until enough fresh observations exist, the new series
has insufficient evidence. Retained raw JSON may permit recovery by replaying
its verified project names, but this command neither fetches nor invents them.
Retention and changed titles/configurations also censor history.

## Migration and backfill

Pending migrations 27/28/29 add nullable columns and catalog objects; they do not
populate the test table or build indexes. Migration 30 supports databases which
already applied the original generated-column version of migration 28. It adds
the missing project column and drops the generation expression while preserving
stored values and indexes, then installs the same trigger as a fresh database.
Catalog changes can wait for locks; no fixed deployment duration is promised.

After migration:

```bash
tsioctl db backfill-stable-key --batch-size 5000 --pause 100ms
```

This uses committed primary-key batches, repairs IDs from full title then title,
sets file, and lets the trigger compute the key. It reports unresolved keys and
Playwright rows without project identity. Only one instance runs at a time; each
statement has a five-minute timeout and a ten-second lock timeout. Interruption
is resumable. The three history indexes are built concurrently; an invalid
index from an interrupted build is dropped concurrently and rebuilt on rerun.
Already-valid legacy indexes remain in place. Stop on error and inspect the
reported operation; do not replace this with an unbounded UPDATE on a serving DB.

## Private staging database

The deployment image already contains `/tsioctl`. ECS Exec is disabled and the
runtime image has no shell or psql. The smallest execution route is a one-off
Fargate task using the **deployed app image**, task role, execution role, secret
bindings, logging, subnets and security groups. This does not require exposing
the DB or granting an interactive shell.

From the existing authorized staging deployment job:

1. Read the app service's task definition and network configuration with
   `aws ecs describe-services` and `aws ecs describe-task-definition`.
2. Register a temporary task-definition family copied from it, omitting response
   fields (`taskDefinitionArn`, `revision`, `status`, `requiresAttributes`,
   `compatibilities`, `registeredAt`, `registeredBy`, `deregisteredAt`). In
   container `app`, set `entryPoint` to `["/tsioctl"]` and `command` to
   `["db","baseline","--repo","mattermost/mattermost","--days","90"]`.
   ECS `run-task` overrides cannot override the image entry point. Preserve the
   existing `wait-for-db` dependency and container log configuration.
3. Run that task with `aws ecs run-task --cluster "$CLUSTER" --launch-type FARGATE
   --task-definition "$TEMP_TASK_DEF_ARN" --network-configuration "$NETWORK_JSON"`.
   Read the task's exit code and its CloudWatch `app` log stream. A successful
   dispatch alone is not a successful measurement. Save the JSON with the
   deployed commit, then deregister the temporary task definition.
4. To backfill, use the same procedure with command
   `["db","backfill-stable-key","--batch-size","5000","--pause","100ms"]`,
   wait for successful completion, then run the read-only baseline.

The deployment role needs `ecs:RunTask`, `ecs:DescribeTasks`, access to the
existing logs and the existing role pass permissions. Verify its actual policy
before dispatch; this documentation does not assert those permissions exist.
Preserve staging's existing database during this demo: the deployment's default
fresh-Postgres migration exercise destroys its previous historical observations.

## Regression evidence

Run `go test -tags=e2e ./cmd/tsioctl/db -count=1` from `apps/server` with Docker.
The disposable PostgreSQL 18.3 fixtures exercise a fresh v26 upgrade, the literal
old staging v27/v28 schema, row preservation, project triggers, resumable
backfill and invalid concurrent-index recovery. The baseline fixture includes
a passing retry, two attempts of the same CI run, distinct suite configurations,
an incomplete group without tests, differing latest suite times and a known
failure predating the window. These establish query behavior on planted cases;
they are not a production impact measurement.
