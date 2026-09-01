-- W7/W8 — alert firing records.
--
-- Two jobs, one row per (repository, rule, subject):
--   * channel dedup: at most one channel post per 24h per alert subject —
--     a cluster failing 12 consecutive runs is one story, not twelve posts.
--   * issue lifecycle: a GitHub issue is opened ONCE when a cluster is
--     persistent (firing across >= 3 master runs spanning >= 2 days), then
--     updated in place; never re-opened, never duplicated.
--
-- resolved_at closes a story (test fixed, alert condition cleared); a new
-- firing after resolution starts a fresh record via re-insert semantics in
-- the upsert (ON CONFLICT ... WHERE resolved_at IS NULL is not expressible on
-- a plain unique index, so the handler resolves-then-inserts).

CREATE TABLE alert_firings (
    id                 uuid        PRIMARY KEY DEFAULT uuidv7(),
    repository         text        NOT NULL,
    rule               text        NOT NULL,
    subject            text        NOT NULL,
    first_fired_at     timestamptz NOT NULL DEFAULT now(),
    last_fired_at      timestamptz NOT NULL DEFAULT now(),
    last_channel_post  timestamptz,
    channel_posts      integer     NOT NULL DEFAULT 0,
    issue_url          text,
    issue_number       integer,
    last_issue_update  timestamptz,
    fire_count         integer     NOT NULL DEFAULT 1,
    evidence           jsonb       NOT NULL DEFAULT '[]',
    resolved_at        timestamptz
);

CREATE UNIQUE INDEX alert_firings_live_idx
    ON alert_firings (repository, rule, subject)
    WHERE resolved_at IS NULL;
CREATE INDEX alert_firings_repo_time_idx ON alert_firings (repository, last_fired_at DESC);
