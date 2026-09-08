-- Derived operational state only. Raw reports and test outcomes remain immutable.
CREATE TABLE triage_repairs (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    repository text NOT NULL,
    framework text NOT NULL CHECK (framework IN ('playwright','cypress')),
    name text NOT NULL,
    stable_key text NOT NULL,
    report_group_id uuid NOT NULL REFERENCES report_groups(id),
    evidence jsonb NOT NULL,
    owner text NOT NULL,
    ticket text NOT NULL,
    author text NOT NULL,
    state text NOT NULL DEFAULT 'queued' CHECK (state IN ('queued','leased','needs_human','repair_pr','product_suspect','resolved')),
    attempt integer NOT NULL DEFAULT 0 CHECK (attempt BETWEEN 0 AND 3),
    worker text,
    lease_token uuid,
    lease_expires_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX triage_repairs_active_identity_idx ON triage_repairs(repository,framework,name,stable_key) WHERE state<>'resolved';
CREATE INDEX triage_repairs_queue_idx ON triage_repairs (repository, created_at) WHERE state IN ('queued','leased');

CREATE TABLE triage_repair_resolutions (
    repair_id uuid PRIMARY KEY REFERENCES triage_repairs(id),
    author text NOT NULL,
    account text NOT NULL,
    evidence_url text NOT NULL,
    pr_url text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE triage_product_observations (
    repair_id uuid NOT NULL REFERENCES triage_repairs(id),
    report_group_id uuid NOT NULL REFERENCES report_groups(id),
    evidence jsonb NOT NULL,
    author text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (repair_id, report_group_id)
);

-- One append-only final account for each claimed attempt, including expiration.
CREATE TABLE triage_repair_attempts (
    repair_id uuid NOT NULL REFERENCES triage_repairs(id),
    attempt integer NOT NULL CHECK (attempt BETWEEN 1 AND 3),
    lease_token uuid NOT NULL,
    worker text NOT NULL,
    author text NOT NULL,
    outcome text NOT NULL CHECK (outcome IN ('failed','blocked','expired','repair_pr','product_suspect')),
    account text NOT NULL,
    evidence_url text NOT NULL DEFAULT '',
    pr_url text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (repair_id, attempt)
);

CREATE TABLE triage_quarantines (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    repair_id uuid NOT NULL REFERENCES triage_repairs(id),
    owner text NOT NULL,
    ticket text NOT NULL,
    author text NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (expires_at > created_at)
);
CREATE INDEX triage_quarantines_expiry_idx ON triage_quarantines (expires_at, repair_id);

-- Submission intent is committed BEFORE external creation. There is deliberately
-- no resolved_at: Jira is the source of truth for live resolution and recurrence.
CREATE TABLE triage_defect_submissions (
    id uuid PRIMARY KEY,
    repair_id uuid NOT NULL REFERENCES triage_repairs(id),
    report_group_id uuid NOT NULL REFERENCES report_groups(id),
    author text NOT NULL,
    state text NOT NULL CHECK (state IN ('submitting','uncertain','linked')),
    summary text NOT NULL,
    description text NOT NULL,
    jira_key text,
    jira_url text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX triage_defect_pending_idx ON triage_defect_submissions (repair_id) WHERE state IN ('submitting','uncertain');
CREATE UNIQUE INDEX triage_defect_link_idx ON triage_defect_submissions (repair_id, jira_key) WHERE jira_key IS NOT NULL;

-- One Jira submission may serve several later observations while unresolved.
-- Keep those receipts, so closing it cannot reopen an already handled sample.
CREATE TABLE triage_defect_observation_links (
    repair_id uuid NOT NULL,
    report_group_id uuid NOT NULL,
    submission_id uuid NOT NULL REFERENCES triage_defect_submissions(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (repair_id, report_group_id),
    FOREIGN KEY (repair_id, report_group_id) REFERENCES triage_product_observations(repair_id, report_group_id)
);
