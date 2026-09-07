-- Baseline measurement for E2E triage.
--
--     psql "$TSIO_DATABASE_URL" -v repo=mattermost/mattermost -v days=90 -f baseline.sql
--
-- Run this BEFORE any triage behaviour changes an outcome. Once a system starts
-- greening checks, the "before" it was supposed to improve on no longer exists,
-- and every claim about whether it helped becomes unfalsifiable.
--
-- Every query below reads only columns that existed before the retry-semantics
-- fix (5fc97ba). None of them touch test_cases.attempts, attempts_failed or
-- run_failed: those are NULL for every historical row, and a query that used
-- them would silently report the window since the fix rather than the window
-- asked for. Outcomes are derived from per-row status, exactly as
-- groupRollupSQL in internal/api/testhistory derives them.
--
-- READ scripts/baseline/README.md BEFORE INTERPRETING ANY NUMBER HERE.
-- One of them — flake_rate for Cypress — is structurally unrecoverable from
-- historical rows and reads as ~0 for a reason that has nothing to do with
-- Cypress being stable.

\set ON_ERROR_STOP on
\timing off
\pset footer off

-- Defaults, overridable with -v on the command line.
\if :{?repo}
\else
  \set repo 'mattermost/mattermost'
\endif
\if :{?days}
\else
  \set days 90
\endif
\if :{?branch}
\else
  \set branch 'master'
\endif

\echo ''
\echo '════════════════════════════════════════════════════════════════════'
\echo ' E2E triage baseline'
\echo '════════════════════════════════════════════════════════════════════'
SELECT :'repo' AS repository, :'branch' AS baseline_branch, :days AS window_days,
       now() AS generated_at;

-- ---------------------------------------------------------------------------
-- Shared: one outcome per (test, report group).
--
-- A test's rows inside one report group are its ATTEMPTS. Rolling them up here
-- is what makes every rate below a rate over runs rather than over attempts —
-- retried attempts share the leaked state or slow container that failed the
-- first one and are not independent draws, so counting them would inflate both
-- the sample size and the failure rate, worst for exactly the tests these
-- numbers are about.
-- ---------------------------------------------------------------------------
CREATE TEMP VIEW run_outcomes AS
SELECT
    g.id            AS group_id,
    g.gh_run_id,
    g.gh_pr_number,
    g.branch,
    g.framework,
    g.created_at,
    tc.stable_key,
    CASE
        WHEN bool_or(tc.status = 'flaky')                                      THEN 'flaky'
        WHEN bool_or(tc.status IN ('passed','flaky'))
             AND bool_or(tc.status IN ('failed','timedOut','interrupted'))     THEN 'flaky'
        WHEN bool_or(tc.status IN ('failed','timedOut','interrupted'))         THEN 'failed'
        WHEN bool_or(tc.status IN ('passed','flaky'))                          THEN 'passed'
        ELSE 'skipped'
    END AS outcome
FROM report_groups g
JOIN reports    r  ON r.report_group_id = g.id
JOIN suites     s  ON s.report_id       = r.id
JOIN test_cases tc ON tc.suite_id       = s.id
WHERE (g.repository = :'repo' OR split_part(g.repository, '/', 2) = :'repo')
  AND g.created_at >= now() - make_interval(days => :days)
  AND tc.stable_key IS NOT NULL
GROUP BY g.id, g.gh_run_id, g.gh_pr_number, g.branch, g.framework, g.created_at, tc.stable_key;

-- Master runs only. gh_pr_number IS NULL as well as branch = master: a pull
-- request can be opened against a branch called master in a fork, and a
-- baseline mixed with other people's unmerged changes is not a baseline.
CREATE TEMP VIEW master_runs AS
SELECT * FROM run_outcomes
WHERE branch = :'branch' AND gh_pr_number IS NULL;

\echo ''
\echo '── 0. Data coverage ────────────────────────────────────────────────'
\echo 'Sanity first. If any of these is small, every number below is thin,'
\echo 'and thin numbers presented without their denominator are how a'
\echo 'baseline becomes a story.'
SELECT
    count(DISTINCT group_id)                                          AS report_groups,
    count(DISTINCT gh_run_id)                                         AS ci_runs,
    count(DISTINCT stable_key)                                        AS distinct_tests,
    count(*)                                                          AS test_runs,
    count(DISTINCT gh_run_id) FILTER (WHERE gh_pr_number IS NOT NULL) AS pr_ci_runs,
    count(DISTINCT group_id)  FILTER (WHERE branch = :'branch'
                                        AND gh_pr_number IS NULL)     AS master_groups,
    min(created_at)::date                                             AS earliest,
    max(created_at)::date                                             AS latest
FROM run_outcomes;

\echo ''
\echo '── 0b. Coverage per framework ──────────────────────────────────────'
\echo 'Read this beside number 2. A framework with few master runs cannot'
\echo 'support a rate, however precise the rate looks.'
SELECT framework,
       count(DISTINCT group_id) AS master_groups,
       count(*)                 AS test_runs,
       count(DISTINCT stable_key) AS distinct_tests
FROM master_runs
GROUP BY framework
ORDER BY test_runs DESC;

-- ---------------------------------------------------------------------------
-- NUMBER 1 — share of PR blocks caused by non-PR failures. Target: under 5%.
--
--   Denominator: PR CI runs that finished red.
--   Numerator:   those where NO failing test was pr_suspect — i.e. every
--                failure was already explainable by master.
--
-- pr_suspect is evaluated the way the rule would: for each failing test in a
-- PR run, look at the K master runs of that same test that happened BEFORE the
-- PR run. If the test never failed in those, the PR is the suspect.
--
-- "Before the PR run", not "in the window": using master runs that happened
-- after the PR would let hindsight clear a PR that genuinely broke something,
-- which is the single most flattering way to get this number wrong.
--
-- A test with fewer than :min_runs master runs before the PR is `unknown`, not
-- `pr_suspect`. Unknown does not clear a run either — a run containing one is
-- counted as NOT cleared, because unknown never grants a green.
-- ---------------------------------------------------------------------------
\if :{?min_runs}
\else
  \set min_runs 5
\endif

\echo ''
\echo '── 1. Share of PR blocks caused by non-PR failures ─────────────────'
\echo 'Sensitivity to K is shown, not a single number: K is a policy choice'
\echo 'and the answer moving sharply with it is itself the finding.'

CREATE TEMP VIEW pr_failures AS
SELECT DISTINCT gh_run_id, stable_key, created_at
FROM run_outcomes
WHERE gh_pr_number IS NOT NULL
  AND outcome IN ('failed','flaky');

-- For one K: classify every failing test in every red PR run.
CREATE OR REPLACE FUNCTION pg_temp.n1(k int, min_runs int)
RETURNS TABLE(k_used int, red_pr_runs bigint, cleared bigint, share numeric)
LANGUAGE sql AS $$
    WITH classified AS (
        SELECT f.gh_run_id,
               f.stable_key,
               (
                 SELECT count(*) FILTER (WHERE m.outcome IN ('failed','flaky'))
                 FROM (
                    SELECT outcome FROM master_runs m2
                    WHERE m2.stable_key = f.stable_key
                      AND m2.created_at < f.created_at
                      AND m2.outcome <> 'skipped'
                    ORDER BY m2.created_at DESC
                    LIMIT k
                 ) m
               ) AS master_failures,
               (
                 SELECT count(*)
                 FROM (
                    SELECT 1 FROM master_runs m3
                    WHERE m3.stable_key = f.stable_key
                      AND m3.created_at < f.created_at
                      AND m3.outcome <> 'skipped'
                    ORDER BY m3.created_at DESC
                    LIMIT k
                 ) m
               ) AS master_runs_seen
        FROM pr_failures f
    ),
    verdicts AS (
        SELECT gh_run_id,
               -- Only "already failing on master, on enough evidence" clears a
               -- failure. Everything else — spotless, or too thin to read —
               -- leaves the run un-cleared.
               bool_and(master_runs_seen >= min_runs AND master_failures > 0) AS all_cleared
        FROM classified
        GROUP BY gh_run_id
    )
    SELECT k,
           count(*)                                        AS red_pr_runs,
           count(*) FILTER (WHERE all_cleared)             AS cleared,
           round(100.0 * count(*) FILTER (WHERE all_cleared)
                 / nullif(count(*), 0), 2)                 AS share
    FROM verdicts;
$$;

SELECT * FROM pg_temp.n1(10, :min_runs)
UNION ALL SELECT * FROM pg_temp.n1(20, :min_runs)
UNION ALL SELECT * FROM pg_temp.n1(50, :min_runs)
ORDER BY k_used;

\echo ''
\echo 'Why runs are NOT cleared — the residue the reproduction path would pay for:'
SELECT
    count(*)                                                        AS red_pr_runs,
    count(*) FILTER (WHERE spotless > 0)                            AS with_a_spotless_test,
    count(*) FILTER (WHERE spotless = 0 AND thin > 0)               AS only_thin_baselines,
    count(*) FILTER (WHERE spotless = 0 AND thin = 0)               AS fully_cleared
FROM (
    SELECT f.gh_run_id,
           count(*) FILTER (WHERE c.master_runs_seen >= :min_runs AND c.master_failures = 0) AS spotless,
           count(*) FILTER (WHERE c.master_runs_seen <  :min_runs)                           AS thin
    FROM pr_failures f
    JOIN LATERAL (
        SELECT
          (SELECT count(*) FILTER (WHERE m.outcome IN ('failed','flaky'))
             FROM (SELECT outcome FROM master_runs m2
                    WHERE m2.stable_key = f.stable_key AND m2.created_at < f.created_at
                      AND m2.outcome <> 'skipped'
                    ORDER BY m2.created_at DESC LIMIT 20) m) AS master_failures,
          (SELECT count(*) FROM (SELECT 1 FROM master_runs m3
                    WHERE m3.stable_key = f.stable_key AND m3.created_at < f.created_at
                      AND m3.outcome <> 'skipped'
                    ORDER BY m3.created_at DESC LIMIT 20) m) AS master_runs_seen
    ) c ON true
    GROUP BY f.gh_run_id
) per_run;

-- ---------------------------------------------------------------------------
-- NUMBER 2 — master flake rate per suite.
--
--   run_failure_rate = master runs that failed outright / all master runs
--   flake_rate       = master runs where some attempt failed but the run
--                      passed / all master runs
--
-- ⚠ flake_rate IS NOT COMPARABLE ACROSS FRAMEWORKS ON HISTORICAL DATA.
--   Before 5fc97ba, Cypress stored a retry-survivor as a single `passed` row,
--   so its flakes are not merely uncounted — they were never written, and no
--   query can recover them. Cypress flake_rate over any window ending before
--   that fix reads ~0 because the evidence is absent, NOT because Cypress is
--   stable. Playwright's reads correctly, because the old parser stamped every
--   attempt of a flaky test `flaky`.
--   Detox and Maestro report one attempt per test and have no retry concept,
--   so ~0 there is structurally true rather than a gap.
--   See README.md § "The Cypress gap".
-- ---------------------------------------------------------------------------
\echo ''
\echo '── 2. Master rates per suite ───────────────────────────────────────'
\echo 'run_failure_rate is sound for every framework.'
\echo 'flake_rate is sound for playwright ONLY — see README § The Cypress gap.'
SELECT framework,
       count(*)                                                          AS master_test_runs,
       count(*) FILTER (WHERE outcome = 'failed')                        AS failed,
       count(*) FILTER (WHERE outcome = 'flaky')                         AS flaky,
       round(100.0 * count(*) FILTER (WHERE outcome = 'failed')
             / nullif(count(*) FILTER (WHERE outcome <> 'skipped'), 0), 3) AS run_failure_rate_pct,
       round(100.0 * count(*) FILTER (WHERE outcome = 'flaky')
             / nullif(count(*) FILTER (WHERE outcome <> 'skipped'), 0), 3) AS flake_rate_pct,
       CASE framework
         WHEN 'playwright' THEN 'flake_rate: sound'
         WHEN 'cypress'    THEN 'flake_rate: UNRECOVERABLE before 5fc97ba — do not read'
         ELSE                   'flake_rate: n/a, single-attempt framework'
       END AS flake_rate_caveat
FROM master_runs
GROUP BY framework
ORDER BY master_test_runs DESC;

-- ---------------------------------------------------------------------------
-- G — report groups a PR CI run touches.
--
-- A distribution, not a mean. This is the fan-out that decides whether a better
-- classifier can move number 1 at all: if a PR run touches many groups, the
-- chance that at least one carries a failure the rule cannot clear grows with
-- it, and the binding constraint is fan-out rather than the verdict.
-- ---------------------------------------------------------------------------
\echo ''
\echo '── G. Report groups per PR CI run (fan-out) ────────────────────────'
SELECT
    count(*)                                                       AS pr_ci_runs,
    min(groups)                                                    AS min,
    percentile_disc(0.50) WITHIN GROUP (ORDER BY groups)           AS p50,
    percentile_disc(0.90) WITHIN GROUP (ORDER BY groups)           AS p90,
    percentile_disc(0.99) WITHIN GROUP (ORDER BY groups)           AS p99,
    max(groups)                                                    AS max,
    round(avg(groups), 2)                                          AS mean
FROM (
    SELECT gh_run_id, count(DISTINCT group_id) AS groups
    FROM run_outcomes
    WHERE gh_pr_number IS NOT NULL
    GROUP BY gh_run_id
) per_run;

-- ---------------------------------------------------------------------------
-- h — fraction of master failures that are hard breaks rather than flakes.
--
-- Proxy: of the tests failing in the most recent master run, how many have an
-- unbroken failing streak (never passed since their first failure in the
-- window) versus how many have passed at some point since.
-- ---------------------------------------------------------------------------
\echo ''
\echo '── h. Hard breaks vs flakes among currently-failing master tests ───'
WITH latest AS (
    SELECT max(created_at) AS at FROM master_runs
),
failing_now AS (
    SELECT DISTINCT m.stable_key
    FROM master_runs m, latest l
    WHERE m.created_at = l.at AND m.outcome IN ('failed','flaky')
),
streaks AS (
    SELECT f.stable_key,
           bool_or(m.outcome = 'passed') AS passed_at_some_point
    FROM failing_now f
    JOIN master_runs m ON m.stable_key = f.stable_key
    GROUP BY f.stable_key
)
SELECT count(*)                                                 AS failing_in_latest_master_run,
       count(*) FILTER (WHERE NOT passed_at_some_point)         AS hard_breaks,
       count(*) FILTER (WHERE passed_at_some_point)             AS intermittent,
       round(100.0 * count(*) FILTER (WHERE NOT passed_at_some_point)
             / nullif(count(*), 0), 2)                          AS hard_break_pct
FROM streaks;

-- ---------------------------------------------------------------------------
-- Master cadence. Feeds the staleness guard: "master is broken" decays, and
-- how fast it decays is 2 x this.
-- ---------------------------------------------------------------------------
\echo ''
\echo '── cadence. Master runs per day ────────────────────────────────────'
SELECT
    count(DISTINCT group_id)                                                   AS master_groups,
    round(count(DISTINCT group_id)::numeric / greatest(1, :days), 2)           AS groups_per_day,
    round(EXTRACT(epoch FROM (max(created_at) - min(created_at)))
          / greatest(1, count(DISTINCT group_id) - 1) / 3600.0, 2)             AS mean_gap_hours
FROM master_runs;

-- ---------------------------------------------------------------------------
-- New-flake arrival rate: tests whose FIRST non-clean master run falls inside
-- the window. Reported per day.
--
-- Note the Cypress gap applies here too — a Cypress test whose only instability
-- was retry-survival never registers as arriving at all.
-- ---------------------------------------------------------------------------
\echo ''
\echo '── arrival. New tests going non-clean on master, per day ───────────'
WITH first_bad AS (
    SELECT stable_key, framework, min(created_at) AS first_bad_at
    FROM master_runs
    WHERE outcome IN ('failed','flaky')
    GROUP BY stable_key, framework
),
first_seen AS (
    SELECT stable_key, min(created_at) AS first_seen_at
    FROM master_runs
    GROUP BY stable_key
)
SELECT fb.framework,
       count(*)                                                        AS newly_unstable_tests,
       round(count(*)::numeric / greatest(1, :days), 3)                AS per_day
FROM first_bad fb
JOIN first_seen fs ON fs.stable_key = fb.stable_key
-- Exclude tests whose first-ever appearance is also their first failure only
-- when they appear at the very start of the window: those may have been
-- failing before it and the window simply cannot see.
WHERE fb.first_bad_at > (SELECT min(created_at) FROM master_runs) + interval '1 day'
GROUP BY fb.framework
ORDER BY newly_unstable_tests DESC;

\echo ''
\echo '════════════════════════════════════════════════════════════════════'
\echo ' Record the output with its window and this file'"'"'s git SHA.'
\echo ' The "after" must be computed by the SAME file or it is not a'
\echo ' comparison. Every number above is MEASURED; nothing here is'
\echo ' projected, and no number should be quoted without its denominator'
\echo ' from section 0.'
\echo '════════════════════════════════════════════════════════════════════'
