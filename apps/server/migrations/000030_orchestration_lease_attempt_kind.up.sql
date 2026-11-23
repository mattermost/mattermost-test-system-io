ALTER TABLE leases
    ADD COLUMN IF NOT EXISTS kind text NOT NULL DEFAULT 'fresh'
        CHECK (kind IN ('fresh', 'reclaim', 'retest'));

ALTER TABLE attempts
    ADD COLUMN IF NOT EXISTS kind text NOT NULL DEFAULT 'fresh'
        CHECK (kind IN ('fresh', 'reclaim', 'retest'));

-- Backfill attempt kinds from per-unit chronological history:
--   1. First attempt on a unit -> 'fresh'
--   2. Prior attempt expired or prior lease expired -> 'reclaim'
--   3. Otherwise (prior attempt completed without expiry) -> 'retest'
WITH ordered AS (
    SELECT a.id,
           ROW_NUMBER() OVER (
               PARTITION BY a.dispatch_unit_id
               ORDER BY a.created_at, a.id
           ) AS rn,
           LAG(a.expired) OVER (
               PARTITION BY a.dispatch_unit_id
               ORDER BY a.created_at, a.id
           ) AS prev_expired,
           LAG(l.release_reason) OVER (
               PARTITION BY a.dispatch_unit_id
               ORDER BY a.created_at, a.id
           ) AS prev_release_reason
      FROM attempts a
      JOIN leases l ON l.id = a.lease_id
),
classified AS (
    SELECT id,
           CASE
               WHEN rn = 1 THEN 'fresh'
               WHEN prev_expired IS TRUE OR prev_release_reason = 'expired' THEN 'reclaim'
               ELSE 'retest'
           END AS computed_kind
      FROM ordered
)
UPDATE attempts a
   SET kind = c.computed_kind
  FROM classified c
 WHERE a.id = c.id
   AND c.computed_kind <> 'fresh';

-- Aggregate lease kind from its attempts (precedence: retest > reclaim > fresh).
WITH lease_kinds AS (
    SELECT lease_id,
           CASE
               WHEN BOOL_OR(kind = 'retest') THEN 'retest'
               WHEN BOOL_OR(kind = 'reclaim') THEN 'reclaim'
               ELSE 'fresh'
           END AS computed_kind
      FROM attempts
     GROUP BY lease_id
)
UPDATE leases l
   SET kind = lk.computed_kind
  FROM lease_kinds lk
 WHERE l.id = lk.lease_id
   AND lk.computed_kind <> 'fresh';
