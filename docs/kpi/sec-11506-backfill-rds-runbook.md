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
3. **Fresh snapshot** (`aws rds create-db-snapshot`) and record its id.
4. Run the deploy containing migration `000033` first; the server must already
   write `test_id` / `signature_id` for new uploads.

## 2. Backfill

Run from a host that can reach RDS and the report bucket, with
`TSIO_DATABASE_URL` (or split `TSIO_DB_*`) and `TSIO_S3_*` set. Only
`TSIO_S3_BUCKET` is required; credentials fall back to the task role.

```bash
# 1. Dry run on a slice: zero writes, no checkpoint.
tsioctl backfill --dry-run --repo mattermost/mattermost --since 2026-01-01 --format json

# 2. Real run, throttled and resumable.
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
- **Done when** these return 0 (a checkpointed rerun trivially scans nothing):
  ```sql
  SELECT count(*) FROM test_cases WHERE test_id IS NULL;
  SELECT count(*) FROM test_cases
  WHERE signature_id IS NULL AND status NOT IN ('passed', 'flaky', 'skipped', 'pending');
  ```
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
WHERE c.relname IN ('test_cases_test_id_idx', 'test_cases_signature_id_idx');
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

- Prefer restoring the snapshot from §1. The backfill only fills empty
  columns, but its `error_stack` / `project` / `tags` / `annotations`
  enrichment is indistinguishable from ingest-written values, and blanket
  NULLing of `test_id` / `signature_id` would also wipe live-ingest rows. If a
  targeted undo is unavoidable, scope it to reports created before the
  `000033` deploy (and the `--repo` / `--since` slice that was run).
- Drop indexes with `DROP INDEX CONCURRENTLY IF EXISTS <name>;`.
