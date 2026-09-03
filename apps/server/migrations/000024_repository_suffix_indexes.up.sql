-- Callers may address a report_group / orchestration_run by either the full
-- "owner/repo" slug (what CI sends, from the OIDC token identity) or just
-- the trailing "repo" segment (what dashboard URLs carry). The prior
-- `repository LIKE '%/' || $1` suffix match cannot use a plain btree index —
-- a leading wildcard forces Postgres to consider every row for that branch
-- of the OR regardless of how selective the other conditions are. A
-- functional index on the trailing segment turns that branch into a plain
-- indexed equality lookup instead.
CREATE INDEX report_groups_repo_suffix_idx
    ON report_groups (split_part(repository, '/', 2));

CREATE INDEX orchestration_runs_repo_suffix_idx
    ON orchestration_runs (split_part(repository, '/', 2));
