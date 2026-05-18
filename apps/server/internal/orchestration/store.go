package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SpecResult is the per-spec payload reported by a worker on complete. The
// store fans these out across the lease's attempts rows and computes the
// resulting unit-state transitions.
type SpecResult struct {
	SpecPath         string
	Status           string // one of AttemptStatus*
	ActualDurationMs *int64
	ErrorMessage     *string
	ErrorStack       *string
	TestCases        json.RawMessage // JSONB payload as raw bytes; nil when omitted
}

// UnitStateChange describes one dispatch_units state transition produced by
// a complete or reaper operation. Returned by store methods so that the
// caller can emit one live event per change.
type UnitStateChange struct {
	UnitID    uuid.UUID
	FromState string
	ToState   string
	Outcome   string // mirrors ToState; convenience for the UI payload
	At        time.Time
}

// Store is the orchestration data-access layer. It is a thin wrapper around
// a pgx connection pool; transactions are scoped per operation.
//
// Store deliberately does NOT hold a Publisher reference. Each operation
// returns enough information for handlers (and the reaper) to call the
// Publisher on success after the transaction commits. This keeps the data
// layer pure and avoids events being emitted on rolled-back work.
//
// Logger is optional: when nil, the structured-event and metric helpers in
// logging.go are no-ops. Tests in this package construct Store{Pool: ...}
// without a logger and must keep working.
type Store struct {
	Pool   *pgxpool.Pool
	Logger *slog.Logger
}

// SeededReportGroup describes the report_groups row created (or already
// present) when BeginRun seeded a matching report_group inside its
// transaction. Returned to the caller so it can fan out a report_created
// event after commit; ID is uuid.Nil when no row was created (the row
// already existed).
type SeededReportGroup struct {
	ID         uuid.UUID
	Created    bool
	Repository string
	Branch     string
	CommitSHA  string
	GHRunID    string
	GHPRNumber *int
	Framework  string
}

// inTx runs fn inside a read-committed transaction. Mirrors db.InTx but kept
// local so the orchestration package does not import db.
func (s *Store) inTx(ctx context.Context, fn func(pgx.Tx) error) (err error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
	}()
	if err = fn(tx); err != nil {
		if rbErr := tx.Rollback(ctx); rbErr != nil {
			return fmt.Errorf("%w (rollback: %v)", err, rbErr)
		}
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// FindLeaseByWorker resolves the lease for the given worker on the given
// run, preferring an active (released_at IS NULL) lease and falling back to
// the most recently issued released lease if none is active. Returns
// ErrUnknownLease when the worker has never been issued a lease on this run.
//
// This is the lookup path used by complete: the worker-facing API does not
// carry lease_id; the server resolves it from (run_id, gh_job_id).
func (s *Store) FindLeaseByWorker(ctx context.Context, runID uuid.UUID, worker WorkerIdentity) (*Lease, error) {
	row := s.Pool.QueryRow(ctx, `
		SELECT id, run_id, gh_job_name, gh_job_id, unit_ids,
		       issued_at, deadline, released_at, release_reason,
		       auth_oidc_subject, auth_api_key_id
		  FROM leases
		 WHERE run_id = $1 AND gh_job_id = $2
		 ORDER BY (released_at IS NULL) DESC, issued_at DESC
		 LIMIT 1
	`, runID, worker.GHJobID)
	lease, err := scanLeaseRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUnknownLease
	}
	if err != nil {
		return nil, fmt.Errorf("find lease by worker: %w", err)
	}
	return lease, nil
}

// scanLeaseRow scans a row in the canonical lease column order into a Lease.
func scanLeaseRow(row pgx.Row) (*Lease, error) {
	var (
		l               Lease
		releasedAt      *time.Time
		releaseReason   *string
		authOIDCSubject *string
		authAPIKeyID    *uuid.UUID
	)
	err := row.Scan(
		&l.ID, &l.RunID, &l.Worker.GHJobName, &l.Worker.GHJobID, &l.UnitIDs,
		&l.IssuedAt, &l.Deadline, &releasedAt, &releaseReason,
		&authOIDCSubject, &authAPIKeyID,
	)
	if err != nil {
		return nil, err
	}
	l.ReleasedAt = releasedAt
	l.ReleaseReason = releaseReason
	l.AuthOIDCSubject = authOIDCSubject
	l.AuthAPIKeyID = authAPIKeyID
	return &l, nil
}
