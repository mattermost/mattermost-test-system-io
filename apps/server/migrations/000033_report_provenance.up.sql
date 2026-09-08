-- Catalog-only additions: historical uploads remain explicitly unverified.
ALTER TABLE reports ADD COLUMN upload_principal text;
ALTER TABLE reports ADD COLUMN registration_receipt jsonb;

CREATE TABLE report_group_begin_receipts (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    report_group_id uuid NOT NULL REFERENCES report_groups(id) ON DELETE CASCADE,
    verified_claims jsonb NOT NULL,
    receipt jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX report_group_begin_receipts_group_idx ON report_group_begin_receipts(report_group_id);
