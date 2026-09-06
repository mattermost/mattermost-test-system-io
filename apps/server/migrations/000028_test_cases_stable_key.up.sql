-- One test identity that works whether or not a repository annotates its tests.
--
-- History is keyed on external_test_id, the MM-T case id. mattermost/mattermost
-- carries those ids; mattermost/desktop (104 specs) and mattermost-mobile (152
-- specs) carry none. For those repositories every row had a NULL key and no
-- history could be found for any test.
--
-- stable_key is the MM-T id when there is one and the full title when there is
-- not. A title is a weaker key — a reworded test starts a fresh series — but
-- for a repository with no ids it is the only key there is, and a renamed test
-- is arguably a new test for flakiness purposes anyway. Where an MM-T id
-- exists it still wins, so mattermost/mattermost loses nothing.
--
-- STORED rather than an expression index so call sites can name a column
-- instead of repeating one exact expression to stay on the index. Adding a
-- stored generated column rewrites the table; on the production test_cases
-- table that is a one-time cost of the same order as migration 27's backfill
-- UPDATE, which set the precedent.
ALTER TABLE test_cases
    ADD COLUMN stable_key text
    GENERATED ALWAYS AS (
        coalesce(nullif(external_test_id, ''), nullif(full_title, ''), title)
    ) STORED;

-- Every history lookup is (stable_key -> recent rows), same shape as the
-- external_test_id partial index it supersedes for those queries.
CREATE INDEX test_cases_stable_key_idx ON test_cases (stable_key);
