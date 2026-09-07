-- One test identity that works whether or not a repository annotates its tests.
--
-- History is keyed on external_test_id, the MM-T case id. mattermost/mattermost
-- carries those ids; mattermost/desktop (104 specs) and mattermost-mobile (152
-- specs) carry none. For those repositories every row had a NULL key and no
-- history could be found for any test.
--
-- stable_key is the MM-T id when there is one and the full title when there is
-- not, in both cases prefixed by the project the test ran under. A title is a
-- weaker key — a reworded test starts a fresh series — but for a repository
-- with no ids it is the only key there is, and a renamed test is arguably a new
-- test for flakiness purposes anyway. Where an MM-T id exists it still wins, so
-- mattermost/mattermost loses nothing.
--
-- Two disambiguators, both denormalized onto test_cases:
--
--   file    from suites.file. Playwright's full_title is already file-prefixed
--           — its root suite title is the spec path — so file adds nothing
--           there, and the expression below leaves an already-prefixed
--           full_title untouched. Cypress, Detox and Maestro build full_title
--           from the describe/it chain only, with no file in it anywhere:
--           without this, "renders the sidebar" in two different spec files
--           collapses into one flakiness history, and a real regression in one
--           file reads as a flake because the other file's test keeps passing.
--
--   project Playwright's projectName (chrome, firefox, ...), the parallel
--           dimension the report already carries and the ingest path used to
--           throw away. The same title under two projects is two independent
--           series: a browser-specific regression that folds into one key
--           reads as a flake because the other browser keeps passing — the
--           identical failure mode `file` exists to prevent, one axis over.
--           The prefix is applied outside the coalesce, so it disambiguates
--           the MM-T branch too. It has to: mattermost/mattermost is the
--           repository that both carries MM-T ids and runs multiple projects,
--           so prefixing only the fallback branch would leave the one
--           repository this matters most for still collapsing.
--
-- Frameworks with no project concept (Cypress, Detox, Maestro) write NULL and
-- their keys keep the unprefixed form.
--
--
-- LOCKING — why this migration is DDL only.
--
-- The first version of this migration did three things that each hold ACCESS
-- EXCLUSIVE on test_cases for time proportional to the table size, in one
-- transaction: an unbatched `UPDATE ... FROM suites` backfill, an
-- `ADD COLUMN ... GENERATED ALWAYS AS (...) STORED` (which rewrites every
-- heap page, unconditionally, regardless of backfill), and a non-concurrent
-- CREATE INDEX. Migrations run at deploy, so that lock blocks every reader
-- and every ingest for its whole duration.
--
-- The production row count could not be measured from a development checkout:
-- there is no reachable production or staging database here, and nothing in
-- the repository records the number. Rather than ship a rewrite whose cost is
-- unknown, this migration is written so the cost does not depend on the count.
-- Every statement below is catalog-only and completes in milliseconds on a
-- table of any size:
--
--   * ADD COLUMN of a nullable column with no default is a catalog update in
--     PostgreSQL 11+; no heap pages are touched.
--   * stable_key is a plain column maintained by a BEFORE trigger rather than
--     a STORED generated column. A generated column keeps the database
--     authoritative — which is why it was chosen — but it cannot be added
--     without a full rewrite. The trigger keeps the same property (no writer
--     can forget to set the key, and the expression lives in one place) at no
--     lock cost.
--   * CREATE TRIGGER takes a brief ACCESS EXCLUSIVE to write one catalog row.
--
-- What is NOT here, deliberately: the backfill of pre-existing rows and the
-- stable_key index. Both are proportional to the table and both belong outside
-- the migration transaction. golang-migrate executes each file as a single
-- statement batch, which PostgreSQL runs as one implicit transaction, so
-- CREATE INDEX CONCURRENTLY cannot appear here and a batched backfill loop
-- cannot commit between batches. Run them after deploying this migration:
--
--     tsioctl db backfill-stable-key
--
-- which commits one batch at a time and then builds the index concurrently.
-- Until it is run, rows written before this migration have a NULL stable_key
-- and no history; rows written after it are correct immediately, because the
-- trigger fires on every insert. Queries against stable_key work throughout —
-- they are sequential scans until the index lands.

ALTER TABLE test_cases ADD COLUMN file text;
ALTER TABLE test_cases ADD COLUMN project text;
ALTER TABLE test_cases ADD COLUMN stable_key text;

-- The key expression, in one place. IMMUTABLE so it can be reused by the
-- backfill and by any future expression index without re-deriving it.
--
-- left(...) is an exact-substring check, not a LIKE pattern, so a file path
-- containing '_' or '%' can't produce a spurious match the way a LIKE
-- wildcard would.
CREATE FUNCTION test_cases_stable_key(
    external_test_id text,
    full_title text,
    title text,
    file text,
    project text
) RETURNS text
LANGUAGE sql
IMMUTABLE
AS $$
    SELECT CASE
        WHEN project IS NOT NULL AND project <> ''
            THEN project || ' :: ' || base.key
        ELSE base.key
    END
    FROM (
        SELECT coalesce(
            nullif(external_test_id, ''),
            CASE
                WHEN full_title IS NULL OR full_title = '' THEN
                    CASE WHEN file IS NOT NULL AND file <> ''
                        THEN file || ' :: ' || title
                        ELSE title
                    END
                WHEN file IS NOT NULL AND file <> ''
                     AND left(full_title, length(file)) <> file THEN
                    file || ' :: ' || full_title
                ELSE full_title
            END
        ) AS key
    ) AS base;
$$;

-- BEFORE INSERT OR UPDATE, not just INSERT: the out-of-band backfill sets
-- file and project on existing rows, and stable_key has to be recomputed from
-- them in the same statement. Without the UPDATE branch the backfill would
-- populate the inputs and leave the key NULL.
CREATE FUNCTION test_cases_set_stable_key() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    NEW.stable_key := test_cases_stable_key(
        NEW.external_test_id, NEW.full_title, NEW.title, NEW.file, NEW.project
    );
    RETURN NEW;
END;
$$;

CREATE TRIGGER test_cases_stable_key_trg
    BEFORE INSERT OR UPDATE ON test_cases
    FOR EACH ROW EXECUTE FUNCTION test_cases_set_stable_key();
