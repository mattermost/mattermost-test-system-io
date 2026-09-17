CREATE TABLE test_identities (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    repository text NOT NULL,
    framework text NOT NULL CHECK (framework IN ('playwright','cypress','detox','maestro')),
    stable_key bytea NOT NULL,
    normalized_file text NOT NULL,
    normalized_title text NOT NULL,
    mm_t_id text,
    first_seen_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (stable_key)
);
CREATE INDEX test_identities_repo_fw_idx ON test_identities (repository, framework);
CREATE INDEX test_identities_mm_t_idx ON test_identities (mm_t_id) WHERE mm_t_id IS NOT NULL;
