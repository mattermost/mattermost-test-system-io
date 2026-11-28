# SEC-11506 — Backfill + `test_cases` index rollout runbook (prod RDS)

Target: prod RDS PostgreSQL `db.m7g.large` (2 vCPU / 8 GiB, Multi-AZ).
Scope: `tsioctl backfill` (stages, `test_id` / `signature_id`, S3-only case
fields) and two partial indexes that migrations deliberately do **not** create.

> [!IMPORTANT]
> Multi-AZ is a failover standby only — it does not serve reads or absorb
> write load. Every step below runs against the single writer.

## 1. Pre-flight

1. **Storage headroom.** CloudWatch `FreeStorageSpace` must stay above 25%
   of allocated storage. The backfill rewrites every touched `test_cases` row
   (new tuple version + WAL), and each index build needs about its final size
   in temp space.
2. **Storage autoscaling.** Confirm `MaxAllocatedStorage` is set above
   `AllocatedStorage`:
   ```bash
   aws rds describe-db-instances --db-instance-identifier <id> \
     --query 'DBInstances[0].[AllocatedStorage,MaxAllocatedStorage,MultiAZ]'
   ```
   If `MaxAllocatedStorage` is null, enable autoscaling (or grow storage)
   before starting. Autoscaling triggers at <10% free and then waits ≥6 h
   before it can grow again, so do not rely on it mid-run.
3. **Deploy migration `000033` (and `000034`) first** so the `tests` / `failure_signatures`
   tables and `test_cases.{test_id,signature_id}` columns already exist and
   live uploads populate them. Migrations `000033` and `000034` deploy cleanly
   together onto an unpopulated `tests` / `failure_signatures` schema.
4. **Fresh post-`000033` snapshot** (`aws rds create-db-snapshot`) and record
   its id. Live CI uploads keep landing during the backfill, so restoring this
   snapshot loses them (see §5). For a clean restore point, briefly pause
   uploaders **after** migration `000033` lands while the snapshot is taken and
   note the time so later runs can be replayed from CI / S3.

## 2. Backfill

Run from a host that can reach RDS and the report bucket, with
`TSIO_DATABASE_URL` (or split `TSIO_DB_*`) and `TSIO_S3_*` set. Only
`TSIO_S3_BUCKET` is required; credentials fall back to the task role.

```bash
# 1. Dry run on a slice: zero writes, no checkpoint.
tsioctl backfill --dry-run --repo mattermost/mattermost --since 2026-01-01 --format json

# 2. Real run, throttled and resumable. pipefail keeps tsioctl's exit status
#    (S3/DB failure) instead of tee's.
set -o pipefail
tsioctl backfill \
  --batch-size 50 --sleep 200ms \
  --checkpoint-file /var/tmp/sec-11506-backfill.json \
  --format json | tee backfill-$(date +%s).json
```

- **Resume:** rerun the same command. Cursors (`last_group_id`,
  `last_report_id`) come from `--checkpoint-file`, which is replaced atomically
  after every committed batch. `--after-group-id` / `--after-report-id`
  override it per phase.
- **One checkpoint per filter set:** the checkpoint does not record
  `--repo` / `--since` / `--until`. Reusing a sliced run's checkpoint for a
  full run skips every id below its cursor — use a new file per slice.
- **Pause:** Ctrl-C / SIGTERM at any time, or bound a session with
  `--max-batches N`. The in-flight batch rolls back and is redone on resume;
  updates are idempotent and never change `test_cases.id` /
  `report_screenshots.case_id`.
- **Throttle:** `--batch-size` (1..500) counts report groups in the stage phase
  and **reports** in the cases phase (each report may hold thousands of cases).
  Lower it or raise `--sleep` if the alarms in §3 fire. `--skip-s3` gives a
  DB-only pass when S3 reads are the bottleneck; `--skip-stage` /
  `--skip-cases` run one phase.
- **S3 errors:** a missing object is logged, counted in `s3_files_missing`,
  and the report falls back to DB fields. Any other S3 error aborts the run
  without advancing the checkpoint; rerun once S3 is healthy.
- **Done when** both of the following hold:
  1. A dry-run DB check reports `stage_groups_updated: 0`, `reports_updated: 0`, and `cases_updated: 0` (the partial indexes `WHERE test_id IS NOT NULL` / `WHERE signature_id IS NOT NULL` cannot answer full-table `IS NULL` counts without a sequential scan):
     ```bash
     tsioctl backfill --dry-run --skip-s3 --format json
     ```
     For a bounded SQL spot-check over a specific report window (joined through `reports` / `suites` so PostgreSQL uses `reports(created_at)` + `suites(report_id)` instead of scanning `test_cases`):
     ```sql
     SELECT count(*)
     FROM reports r
     JOIN suites s ON s.report_id = r.id
     JOIN test_cases tc ON tc.suite_id = s.id
     WHERE r.created_at >= '<window_start>' AND r.created_at < '<window_end>'
       AND (tc.test_id IS NULL
            OR (tc.signature_id IS NULL AND tc.status NOT IN ('passed', 'flaky', 'skipped', 'pending')));
     ```
  2. Review `s3_files_missing` and stderr S3 warnings (`s3 json unusable`, `s3 json yielded no suites`); if restoring missing S3 objects afterward, re-run that scope with `--force --skip-stage` using a fresh `--checkpoint-file` (or accept the missing S3 enrichment).
- **Known limitation:** `failure_signatures.first_seen_report_group_id` is set
  only when NULL, to the first report (by id, i.e. chronological) that the
  backfill touches. Signatures already attributed to a newer live group are not
  corrected.

## 3. Monitoring (CloudWatch, 1-min)

| Metric | Pause / throttle when |
|--------|-----------------------|
| `CPUUtilization` | > 70% for 5 min |
| `DBLoad` (Performance Insights) | > 2 (vCPU count) sustained |
| `WriteIOPS` / `WriteLatency` | latency > 10 ms sustained |
| `FreeStorageSpace` | < 20% allocated |
| `TransactionLogsDiskUsage` | climbing without recovery |
| `DatabaseConnections` | near `max_connections` |

Also watch the TSIO API p95 latency and `/ready`; stop the backfill first if
user-facing traffic degrades.

## 4. Concurrent indexes (manual, after the backfill)

Run each statement on its own in `psql` (autocommit), **not** through
`golang-migrate`:

```sql
SET lock_timeout = '5s';
SET statement_timeout = 0;
CREATE INDEX CONCURRENTLY IF NOT EXISTS test_cases_test_id_idx
    ON test_cases (test_id) WHERE test_id IS NOT NULL;
CREATE INDEX CONCURRENTLY IF NOT EXISTS test_cases_signature_id_idx
    ON test_cases (signature_id) WHERE signature_id IS NOT NULL;
```

Why not a migration: `golang-migrate` runs each file in a transaction, and
`CREATE INDEX CONCURRENTLY` cannot run inside a transaction block. A plain
`CREATE INDEX` would hold a `SHARE` lock on `test_cases` and block ingest
writes for the whole build. Migration `000028` builds a small index in a
transaction and only repairs it when `indisvalid` is false; the large
`test_cases` indexes need the manual concurrent path instead.

### Mandatory validity check

A failed or cancelled concurrent build leaves an **INVALID** index that is
still maintained on every write but never used for reads, and
`IF NOT EXISTS` will then skip it silently:

```sql
SELECT c.relname, i.indisvalid, i.indisready
FROM pg_index i
JOIN pg_class c ON c.oid = i.indexrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = current_schema()
  AND c.relname IN ('test_cases_test_id_idx', 'test_cases_signature_id_idx');
```

Both rows must show `indisvalid = t`. For any invalid index, rebuild it:

```sql
DROP INDEX CONCURRENTLY IF EXISTS test_cases_test_id_idx;
CREATE INDEX CONCURRENTLY test_cases_test_id_idx
    ON test_cases (test_id) WHERE test_id IS NOT NULL;
-- same for test_cases_signature_id_idx
```

Then refresh planner statistics:

```sql
ANALYZE test_cases;
```

## 5. Rollback

- **Default runs (without `--force`):** backfill only fills empty columns,
  so a targeted undo is valid for runs without `--force`. Scope any SQL undo
  to reports created before the `000033` deploy and to the `--repo` / `--since`
  slice that was run — blanket NULLing of `test_id` / `signature_id` would
  also wipe live-ingest rows, and `error_stack` / `project` / `tags` /
  `annotations` enrichment is indistinguishable from ingest-written values.
- **`--force` runs:** `--force` overwrites existing `test_cases.test_id` and
  `signature_id` values; targeted NULLing cannot restore previous identities,
  and re-running `--force` will re-resolve the same values. Use the quiesced
  post-`000033` pre-backfill RDS snapshot (§1 step 4) as the **only** rollback
  path before any `--force` run. If an older pre-`000033` snapshot is ever
  restored, apply deployment migrations (`tsioctl db migrate` / server boot)
  before reconnecting uploaders or replaying later CI runs from CI / S3.
- Restoring the §1 snapshot discards every upload committed after it. Only do
  so if uploaders were quiesced for the snapshot, and replay later runs from
  CI / S3 after confirming migration `000033` is active.
- Drop indexes with `DROP INDEX CONCURRENTLY IF EXISTS <name>;`.
