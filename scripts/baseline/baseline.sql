\set ON_ERROR_STOP on
\if :{?repo}
\else
  \set repo mattermost/mattermost
\endif
\if :{?branch}
\else
  \set branch master
\endif
\if :{?days}
\else
  \set days 90
\endif
\if :{?min_runs}
\else
  \set min_runs 5
\endif
BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;
SET LOCAL statement_timeout = '5min';
SELECT set_config('tsio.baseline_repo', :'repo', true),
       set_config('tsio.baseline_branch', :'branch', true),
       set_config('tsio.baseline_days', :'days', true),
       set_config('tsio.baseline_min_runs', :'min_runs', true);
\ir ../../apps/server/cmd/tsioctl/db/baseline.sql
COMMIT;
