package verdicts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	authapi "github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/auth"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/db"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/errorsig"
)

const (
	// MaxBatchSize caps the number of verdicts accepted in one POST /api/v1/verdicts call.
	MaxBatchSize = 500
	// DefaultWindowDays is the default lookback window for signature verdict lookup.
	DefaultWindowDays = 90
	// MinWindowDays is the minimum allowed lookback window for signature verdict lookup.
	MinWindowDays = 1
	// MaxWindowDays is the maximum allowed lookback window for signature verdict lookup.
	MaxWindowDays = 365
)

// Store provides transactional persistence and lineage-aware lookup for verdicts.
type Store struct {
	Pool *pgxpool.Pool
}

// BatchInput holds the validated input for Store.CreateBatch.
type BatchInput struct {
	Repository    string
	ReportGroupID *uuid.UUID
	Items         []CreateItem
}

// CreateItem is a single verdict entry inside BatchInput.
type CreateItem struct {
	TestID       *uuid.UUID
	SignatureID  *uuid.UUID
	Fingerprint  *string
	Label        string
	DecidedBy    *string
	Rule         *string
	Confidence   *float64
	Summary      *string
	Model        *string
	TriageURL    *string
	ErrorContext json.RawMessage
	ReusedFromID *uuid.UUID
}

// Verdict is the read representation of a stored verdict. ErrorContext is
// deliberately omitted so no read path can ever serialize it.
type Verdict struct {
	ID            uuid.UUID  `json:"id"`
	ReportGroupID *uuid.UUID `json:"report_group_id"`
	Repository    string     `json:"repository"`
	TestID        *uuid.UUID `json:"test_id"`
	SignatureID   *uuid.UUID `json:"signature_id"`
	Fingerprint   string     `json:"fingerprint"`
	Label         string     `json:"label"`
	DecidedBy     *string    `json:"decided_by"`
	Rule          *string    `json:"rule"`
	Confidence    *float64   `json:"confidence"`
	Summary       *string    `json:"summary"`
	Model         *string    `json:"model"`
	TriageURL     *string    `json:"triage_url"`
	ReusedFromID  *uuid.UUID `json:"reused_from_id"`
	CreatedAt     time.Time  `json:"created_at"`
}

// CreatedVerdict is the per-item result returned by CreateBatch. ErrorContext is
// deliberately omitted so responses never leak redacted/raw context.
type CreatedVerdict struct {
	ID            uuid.UUID  `json:"id"`
	ReportGroupID *uuid.UUID `json:"report_group_id"`
	Repository    string     `json:"repository"`
	TestID        *uuid.UUID `json:"test_id"`
	SignatureID   *uuid.UUID `json:"signature_id"`
	Label         string     `json:"label"`
	DecidedBy     *string    `json:"decided_by"`
	Rule          *string    `json:"rule"`
	Confidence    *float64   `json:"confidence"`
	Summary       *string    `json:"summary"`
	Model         *string    `json:"model"`
	TriageURL     *string    `json:"triage_url"`
	ReusedFromID  *uuid.UUID `json:"reused_from_id"`
	CreatedAt     time.Time  `json:"created_at"`
	Created       bool       `json:"created"`
	SupersededID  *uuid.UUID `json:"superseded_id"`
}

type preparedItem struct {
	testID       *uuid.UUID
	signatureID  *uuid.UUID
	fingerprint  *string
	label        string
	decidedBy    *string
	rule         *string
	confidence   *float64
	summary      *string
	model        *string
	triageURL    *string
	errorContext *string
	reusedFromID *uuid.UUID
}

type verdictTargetKey struct {
	testID uuid.UUID
	sigID  uuid.UUID
}

// CreateBatch validates, redacts error_context, resolves signatures/FKs, and
// idempotently inserts or supersedes verdicts inside a single transaction.
func (s *Store) CreateBatch(ctx context.Context, in BatchInput) ([]CreatedVerdict, error) {
	if s == nil || s.Pool == nil {
		return nil, errors.New("verdicts store not initialized")
	}

	repo, prepared, err := prepareBatchInput(in)
	if err != nil {
		return nil, err
	}

	results := make([]CreatedVerdict, len(prepared))
	err = db.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
		if err := validateAndResolveTx(ctx, tx, repo, in.ReportGroupID, prepared); err != nil {
			return err
		}
		if in.ReportGroupID != nil {
			if err := lockBatchKeysTx(ctx, tx, *in.ReportGroupID, prepared); err != nil {
				return err
			}
		}
		for i := range prepared {
			res, err := upsertItemTx(ctx, tx, repo, in.ReportGroupID, prepared[i])
			if err != nil {
				return mapPGError(err)
			}
			results[i] = res
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

// LookupBySignatureFingerprint resolves fingerprint (preferring AlgoVersion=2),
// traverses failure_signatures.merged_into_id bidirectionally, and returns the
// latest non-superseded verdict within windowDays (default 90). Never selects
// or returns error_context.
func (s *Store) LookupBySignatureFingerprint(ctx context.Context, fingerprint string, windowDays int, repository string) (*Verdict, error) {
	if s == nil || s.Pool == nil {
		return nil, errors.New("verdicts store not initialized")
	}
	fp := strings.TrimSpace(fingerprint)
	if fp == "" {
		return nil, fmt.Errorf("%w: fingerprint is required", ErrValidation)
	}
	if windowDays == 0 {
		windowDays = DefaultWindowDays
	}
	if windowDays < MinWindowDays || windowDays > MaxWindowDays {
		return nil, fmt.Errorf("%w: window_days must be between %d and %d", ErrValidation, MinWindowDays, MaxWindowDays)
	}
	repo := authapi.NormalizeRepository(repository)

	var v Verdict
	err := s.Pool.QueryRow(ctx, `
		WITH RECURSIVE
		seed AS (
			SELECT id
			FROM failure_signatures
			WHERE fingerprint = $1
			ORDER BY (algo_version = $2) DESC, algo_version DESC, id DESC
			LIMIT 1
		),
		up AS (
			SELECT fs.id, fs.merged_into_id, ARRAY[fs.id] AS path, 0 AS depth
			FROM failure_signatures fs
			JOIN seed s ON s.id = fs.id
			UNION ALL
			SELECT p.id, p.merged_into_id, u.path || p.id, u.depth + 1
			FROM up u
			JOIN failure_signatures p ON p.id = u.merged_into_id
			WHERE u.merged_into_id IS NOT NULL
			  AND NOT (p.id = ANY(u.path))
			  AND u.depth < 32
		),
		root AS (
			SELECT id
			FROM up
			ORDER BY (merged_into_id IS NULL) DESC, depth DESC
			LIMIT 1
		),
		family AS (
			SELECT r.id, ARRAY[r.id] AS path, 0 AS depth
			FROM root r
			UNION ALL
			SELECT c.id, f.path || c.id, f.depth + 1
			FROM family f
			JOIN failure_signatures c ON c.merged_into_id = f.id
			WHERE NOT (c.id = ANY(f.path))
			  AND f.depth < 32
		)
		SELECT v.id, v.report_group_id, v.repository, v.test_id, v.signature_id,
		       fs.fingerprint, v.label, v.decided_by, v.rule, v.confidence::float8,
		       v.summary, v.model, v.triage_url, v.reused_from_id, v.created_at
		FROM verdicts v
		JOIN family fam ON fam.id = v.signature_id
		JOIN failure_signatures fs ON fs.id = v.signature_id
		WHERE v.superseded_at IS NULL
		  AND v.created_at >= now() - make_interval(days => $3)
		  AND ($4 = '' OR lower(v.repository) = lower($4))
		ORDER BY v.created_at DESC, v.id DESC
		LIMIT 1
	`, fp, errorsig.AlgoVersion, windowDays, repo).Scan(
		&v.ID, &v.ReportGroupID, &v.Repository, &v.TestID, &v.SignatureID,
		&v.Fingerprint, &v.Label, &v.DecidedBy, &v.Rule, &v.Confidence,
		&v.Summary, &v.Model, &v.TriageURL, &v.ReusedFromID, &v.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lookup verdict by signature: %w", err)
	}
	return &v, nil
}

func prepareBatchInput(in BatchInput) (string, []preparedItem, error) {
	repo := authapi.NormalizeRepository(in.Repository)
	if repo == "" {
		return "", nil, fmt.Errorf("%w: repository is required", ErrValidation)
	}
	if len(in.Items) == 0 {
		return "", nil, fmt.Errorf("%w: verdicts must not be empty", ErrValidation)
	}
	if len(in.Items) > MaxBatchSize {
		return "", nil, fmt.Errorf("%w: verdicts exceeds max batch size %d", ErrValidation, MaxBatchSize)
	}

	out := make([]preparedItem, len(in.Items))
	for i, item := range in.Items {
		label := sanitizeUTF8(item.Label)
		if strings.TrimSpace(label) == "" {
			return "", nil, fmt.Errorf("%w: verdicts[%d].label is required", ErrValidation, i)
		}
		if item.Confidence != nil {
			c := *item.Confidence
			if math.IsNaN(c) || math.IsInf(c, 0) || c < 0 || c > 1 {
				return "", nil, fmt.Errorf("%w: verdicts[%d].confidence must be between 0 and 1", ErrValidation, i)
			}
		}
		redacted, err := RedactErrorContext(item.ErrorContext)
		if err != nil {
			return "", nil, fmt.Errorf("%w: verdicts[%d]: %s", ErrValidation, i, err.Error())
		}
		var errCtxStr *string
		if len(redacted) > 0 {
			s := string(redacted)
			errCtxStr = &s
		}
		var fpPtr *string
		if item.Fingerprint != nil {
			fp := strings.TrimSpace(sanitizeUTF8(*item.Fingerprint))
			if fp != "" {
				fpPtr = &fp
			}
		}
		out[i] = preparedItem{
			testID:       item.TestID,
			signatureID:  item.SignatureID,
			fingerprint:  fpPtr,
			label:        label,
			decidedBy:    sanitizeUTF8Ptr(item.DecidedBy),
			rule:         sanitizeUTF8Ptr(item.Rule),
			confidence:   item.Confidence,
			summary:      sanitizeUTF8Ptr(item.Summary),
			model:        sanitizeUTF8Ptr(item.Model),
			triageURL:    sanitizeUTF8Ptr(item.TriageURL),
			errorContext: errCtxStr,
			reusedFromID: item.ReusedFromID,
		}
	}
	return repo, out, nil
}

func validateAndResolveTx(ctx context.Context, tx pgx.Tx, repo string, reportGroupID *uuid.UUID, items []preparedItem) error {
	if reportGroupID != nil {
		var rgRepo string
		err := tx.QueryRow(ctx, `SELECT repository FROM report_groups WHERE id = $1`, *reportGroupID).Scan(&rgRepo)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: unknown report_group_id", ErrValidation)
		}
		if err != nil {
			return fmt.Errorf("lookup report_group: %w", err)
		}
		if !strings.EqualFold(authapi.NormalizeRepository(rgRepo), repo) {
			return fmt.Errorf("%w: report_group_id belongs to a different repository", ErrRepoMismatch)
		}
	}

	seen := make(map[verdictTargetKey]struct{}, len(items))
	for i := range items {
		it := &items[i]
		if it.testID != nil {
			var testRepo string
			err := tx.QueryRow(ctx, `SELECT repository FROM tests WHERE id = $1`, *it.testID).Scan(&testRepo)
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: verdicts[%d]: unknown test_id", ErrValidation, i)
			}
			if err != nil {
				return fmt.Errorf("lookup test: %w", err)
			}
			if !strings.EqualFold(authapi.NormalizeRepository(testRepo), repo) {
				return fmt.Errorf("%w: test_id belongs to a different repository", ErrRepoMismatch)
			}
		}

		if err := resolveSignatureTx(ctx, tx, i, it); err != nil {
			return err
		}

		if it.reusedFromID != nil {
			var one int
			err := tx.QueryRow(ctx, `SELECT 1 FROM verdicts WHERE id = $1`, *it.reusedFromID).Scan(&one)
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: verdicts[%d]: unknown reused_from_id", ErrValidation, i)
			}
			if err != nil {
				return fmt.Errorf("lookup reused_from_id: %w", err)
			}
		}

		if reportGroupID != nil {
			key := verdictTargetKey{
				testID: derefUUID(it.testID),
				sigID:  derefUUID(it.signatureID),
			}
			if _, dup := seen[key]; dup {
				return fmt.Errorf("%w: duplicate verdict target in batch", ErrValidation)
			}
			seen[key] = struct{}{}
		}
	}
	return nil
}

func resolveSignatureTx(ctx context.Context, tx pgx.Tx, idx int, it *preparedItem) error {
	if it.signatureID != nil {
		var dbFP string
		err := tx.QueryRow(ctx, `SELECT fingerprint FROM failure_signatures WHERE id = $1`, *it.signatureID).Scan(&dbFP)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: verdicts[%d]: unknown signature_id", ErrValidation, idx)
		}
		if err != nil {
			return fmt.Errorf("lookup signature_id: %w", err)
		}
		if it.fingerprint != nil && *it.fingerprint != dbFP {
			return fmt.Errorf("%w: verdicts[%d]: signature_id does not match fingerprint", ErrValidation, idx)
		}
		return nil
	}
	if it.fingerprint != nil {
		var sigID uuid.UUID
		err := tx.QueryRow(ctx, `
			SELECT id
			FROM failure_signatures
			WHERE fingerprint = $1
			ORDER BY (algo_version = $2) DESC, algo_version DESC, id DESC
			LIMIT 1
		`, *it.fingerprint, errorsig.AlgoVersion).Scan(&sigID)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: verdicts[%d]: unknown signature fingerprint", ErrValidation, idx)
		}
		if err != nil {
			return fmt.Errorf("lookup signature fingerprint: %w", err)
		}
		it.signatureID = &sigID
	}
	return nil
}

func lockBatchKeysTx(ctx context.Context, tx pgx.Tx, rgID uuid.UUID, items []preparedItem) error {
	keys := make([]string, 0, len(items))
	for i := range items {
		k := fmt.Sprintf("%s:%s:%s", rgID.String(), derefUUID(items[i].testID).String(), derefUUID(items[i].signatureID).String())
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, k); err != nil {
			return fmt.Errorf("acquire verdict lock: %w", err)
		}
	}
	return nil
}

func upsertItemTx(ctx context.Context, tx pgx.Tx, repo string, rgID *uuid.UUID, it preparedItem) (CreatedVerdict, error) {
	if rgID == nil {
		return insertVerdictTx(ctx, tx, repo, nil, it, nil)
	}

	var existing CreatedVerdict
	var isSame bool
	err := tx.QueryRow(ctx, `
		SELECT id, report_group_id, repository, test_id, signature_id,
		       label, decided_by, rule, confidence::float8, summary, model,
		       triage_url, reused_from_id, created_at,
		       (
		           repository IS NOT DISTINCT FROM $4
		           AND label IS NOT DISTINCT FROM $5
		           AND decided_by IS NOT DISTINCT FROM $6
		           AND rule IS NOT DISTINCT FROM $7
		           AND confidence IS NOT DISTINCT FROM $8::numeric
		           AND summary IS NOT DISTINCT FROM $9
		           AND model IS NOT DISTINCT FROM $10
		           AND triage_url IS NOT DISTINCT FROM $11
		           AND error_context IS NOT DISTINCT FROM $12::jsonb
		           AND reused_from_id IS NOT DISTINCT FROM $13
		       ) AS is_same
		FROM verdicts
		WHERE report_group_id = $1
		  AND COALESCE(test_id, '00000000-0000-0000-0000-000000000000'::uuid) = $2
		  AND COALESCE(signature_id, '00000000-0000-0000-0000-000000000000'::uuid) = $3
		  AND superseded_at IS NULL
		LIMIT 1
		FOR UPDATE
	`, *rgID, derefUUID(it.testID), derefUUID(it.signatureID),
		repo, it.label, it.decidedBy, it.rule, it.confidence,
		it.summary, it.model, it.triageURL, it.errorContext, it.reusedFromID,
	).Scan(
		&existing.ID, &existing.ReportGroupID, &existing.Repository, &existing.TestID, &existing.SignatureID,
		&existing.Label, &existing.DecidedBy, &existing.Rule, &existing.Confidence, &existing.Summary, &existing.Model,
		&existing.TriageURL, &existing.ReusedFromID, &existing.CreatedAt, &isSame,
	)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return CreatedVerdict{}, fmt.Errorf("select active verdict: %w", err)
	}
	if err == nil {
		if isSame {
			existing.Created = false
			existing.SupersededID = nil
			return existing, nil
		}
		if _, err := tx.Exec(ctx, `UPDATE verdicts SET superseded_at = now() WHERE id = $1`, existing.ID); err != nil {
			return CreatedVerdict{}, fmt.Errorf("supersede verdict: %w", err)
		}
		supersededID := existing.ID
		return insertVerdictTx(ctx, tx, repo, rgID, it, &supersededID)
	}

	return insertVerdictTx(ctx, tx, repo, rgID, it, nil)
}

func insertVerdictTx(ctx context.Context, tx pgx.Tx, repo string, rgID *uuid.UUID, it preparedItem, supersededID *uuid.UUID) (CreatedVerdict, error) {
	out := CreatedVerdict{
		ReportGroupID: rgID,
		Repository:    repo,
		TestID:        it.testID,
		SignatureID:   it.signatureID,
		Label:         it.label,
		DecidedBy:     it.decidedBy,
		Rule:          it.rule,
		Confidence:    it.confidence,
		Summary:       it.summary,
		Model:         it.model,
		TriageURL:     it.triageURL,
		ReusedFromID:  it.reusedFromID,
		Created:       true,
		SupersededID:  supersededID,
	}
	err := tx.QueryRow(ctx, `
		INSERT INTO verdicts (
			report_group_id, repository, test_id, signature_id,
			label, decided_by, rule, confidence, summary, model,
			triage_url, error_context, reused_from_id
		) VALUES (
			$1, $2, $3, $4,
			$5, $6, $7, $8::numeric, $9, $10,
			$11, $12::jsonb, $13
		)
		RETURNING id, created_at
	`, rgID, repo, it.testID, it.signatureID,
		it.label, it.decidedBy, it.rule, it.confidence, it.summary, it.model,
		it.triageURL, it.errorContext, it.reusedFromID,
	).Scan(&out.ID, &out.CreatedAt)
	if err != nil {
		return CreatedVerdict{}, fmt.Errorf("insert verdict: %w", err)
	}
	return out, nil
}

func derefUUID(id *uuid.UUID) uuid.UUID {
	if id == nil {
		return uuid.Nil
	}
	return *id
}

func mapPGError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return fmt.Errorf("%w: referenced entity does not exist", ErrValidation)
	}
	return err
}
