-- W14/W5 — stabilization queue promotions.
--
-- The queue itself is a DERIVED view (flakiness leaderboard top-N, plus
-- amnesty-expired tests promoted to the top). Promotions that come from
-- outside the ranking — the release-cut guard filing listed tests (W5),
-- SLA breaches routing a test in (W15c) — are recorded here so the queue
-- endpoint can surface them above the organic ranking, with the reason and
-- the actor that promoted them.
--
-- One live promotion per (repository, external_test_id): re-promoting a test
-- that is already queued updates the reason, it does not stack rows.

CREATE TABLE stabilization_promotions (
    id                uuid        PRIMARY KEY DEFAULT uuidv7(),
    repository        text        NOT NULL,
    external_test_id  text        NOT NULL,
    -- Who/what promoted: a human subject, 'release-guard', 'sla-clock', …
    promoted_by       text        NOT NULL,
    reason            text        NOT NULL DEFAULT '',
    -- release-guard | sla-breach | amnesty-expired | manual
    source            text        NOT NULL DEFAULT 'manual',
    resolved          boolean     NOT NULL DEFAULT false,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX stabilization_promotions_live_idx
    ON stabilization_promotions (repository, external_test_id)
    WHERE NOT resolved;
CREATE INDEX stabilization_promotions_repo_idx ON stabilization_promotions (repository, created_at DESC);
