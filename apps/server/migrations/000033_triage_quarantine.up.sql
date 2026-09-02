-- R7-L3 — explicit, owned, expiring quarantine.
--
-- WHY THIS EXISTS. Before it, a chronically flaky test had exactly two fates:
-- fixed (slow — the stabilization queue drains 0.10-0.37/day against 1.5/day
-- arrival) or waived on every PR forever. There was no third state, so the
-- amnesty message that tells people to "fix or quarantine explicitly" pointed
-- at a mechanism that did not exist.
--
-- WHY IT IS NOT THE OLD BUCKET LIST. The bucket list failed because tests went
-- in and were never seen again. Every column below is one of the guardrails it
-- lacked, and all of them are mandatory:
--
--   owner       NOT NULL — a quarantine with no owner is an orphan
--   expires_at  NOT NULL — quarantine is a deadline, not a destination; an
--                          expired row stops taking effect on its own, with no
--                          cron and no cleanup job, so a forgotten test goes
--                          red again by default rather than staying hidden
--   reason      NOT NULL — why, in the author's words
--   created_by  NOT NULL — who decided
--
-- WHAT IT DELIBERATELY DOES NOT DO. It does not stop the test running on
-- master and it does not touch the raw pass-rate. Quarantine is consulted only
-- when deciding a PR check; master keeps executing the test, keeps counting its
-- failures in raw_failures, and keeps it in the stabilization ranking. The
-- number the team is judged by cannot be improved by quarantining anything.

CREATE TABLE triage_quarantine (
    id               uuid        PRIMARY KEY DEFAULT uuidv7(),
    repository       text        NOT NULL,
    external_test_id text        NOT NULL,
    -- The four guardrails. None are nullable on purpose.
    owner            text        NOT NULL,
    reason           text        NOT NULL,
    created_by       text        NOT NULL,
    expires_at       timestamptz NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    -- Early release: the test was fixed before the deadline.
    released_at      timestamptz,
    released_by      text,
    release_reason   text,
    -- Running count of PR checks this quarantine greened, so a review can see
    -- what it actually bought and what it hid.
    applied_count    integer     NOT NULL DEFAULT 0,

    CONSTRAINT triage_quarantine_expiry_ck CHECK (expires_at > created_at),
    -- A release must be complete or absent, never half-recorded.
    CONSTRAINT triage_quarantine_release_ck CHECK (
        (released_at IS NULL AND released_by IS NULL) OR
        (released_at IS NOT NULL AND released_by IS NOT NULL)
    )
);

-- At most one OPEN quarantine row per test per repo.
--
-- The predicate is released_at IS NULL rather than "not expired", because a
-- partial index cannot call now(). So an expired-but-unreleased row would
-- otherwise block re-quarantining forever. The POST handler closes that gap in
-- the same transaction as the insert: it first stamps any expired open row as
-- released_by = 'system:expiry' with released_at = expires_at, which both frees
-- the index and leaves an honest audit trail showing the quarantine lapsed on
-- its deadline rather than being cancelled by a person.
--
-- Renewal is therefore always an explicit NEW row with a new deadline and a new
-- decision — never a silent extension of the old one.
CREATE UNIQUE INDEX triage_quarantine_live_idx
    ON triage_quarantine (repository, external_test_id)
    WHERE released_at IS NULL;

CREATE INDEX triage_quarantine_lookup_idx
    ON triage_quarantine (repository, external_test_id, expires_at DESC);
