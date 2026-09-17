CREATE TABLE quarantine_entries (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    identity_id uuid NOT NULL REFERENCES test_identities(id) ON DELETE CASCADE,
    lane text,
    base_ref text NOT NULL,
    source text NOT NULL CHECK (source IN ('auto','manual')),
    reason text NOT NULL CHECK (reason IN ('flaky','broken','manual')),
    status text NOT NULL CHECK (status IN ('active','released','expired')),
    evidence jsonb NOT NULL,
    issue_url text,
    created_by text,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz,
    released_at timestamptz,
    released_by text
);
CREATE UNIQUE INDEX quarantine_entries_active_idx ON quarantine_entries (identity_id, COALESCE(lane, '*'), base_ref) WHERE status = 'active';
