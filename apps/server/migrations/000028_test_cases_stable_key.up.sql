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
-- file is denormalized from suites.file (a generated column cannot reach
-- across tables) so the fallback branch can be disambiguated by spec file.
-- Playwright's full_title is already file-prefixed — its root suite title is
-- the spec path — so file adds nothing there, and the expression below
-- leaves an already-prefixed full_title untouched. Cypress, Detox and
-- Maestro build full_title from the describe/it chain only, with no file in
-- it anywhere: without this, "renders the sidebar" in two different spec
-- files collapses into one flakiness history, and a real regression in one
-- file reads as a flake because the other file's test keeps passing.
--
-- STORED rather than an expression index so call sites can name a column
-- instead of repeating one exact expression to stay on the index. Adding a
-- stored generated column rewrites the table; on the production test_cases
-- table that is a one-time cost of the same order as migration 27's backfill
-- UPDATE, which set the precedent.
ALTER TABLE test_cases ADD COLUMN file text;

-- Backfill for rows that predate this column, same precedent as migration
-- 27's external_test_id backfill: without it, every test_case ingested
-- before this migration keeps file NULL forever, so stable_key falls through
-- to the un-prefixed full_title/title branch for old rows while new rows
-- get the file-qualified form — two eras of key generation for the same
-- test, silently, which is exactly the kind of drift a backfill exists to
-- prevent.
UPDATE test_cases tc
SET file = s.file
FROM suites s
WHERE s.id = tc.suite_id
  AND tc.file IS NULL
  AND s.file IS NOT NULL;

ALTER TABLE test_cases
    ADD COLUMN stable_key text
    GENERATED ALWAYS AS (
        coalesce(
            nullif(external_test_id, ''),
            -- full_title branch: prefix file only when full_title does not
            -- already start with it. left(...) is an exact-substring check,
            -- not a LIKE pattern, so a file path containing '_' or '%' can't
            -- produce a spurious match the way a LIKE wildcard would.
            case
                when full_title is null or full_title = '' then
                    case when file is not null and file <> ''
                        then file || ' :: ' || title
                        else title
                    end
                when file is not null and file <> ''
                     and left(full_title, length(file)) <> file then
                    file || ' :: ' || full_title
                else full_title
            end
        )
    ) STORED;

-- Every history lookup is (stable_key -> recent rows), same shape as the
-- external_test_id partial index it supersedes for those queries.
CREATE INDEX test_cases_stable_key_idx ON test_cases (stable_key);
