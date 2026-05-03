package orchestration

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/storage"
)

// ErrSpecNotInLease signals an orchestration screenshot upload referencing a
// spec_path the worker's lease does not include.
var ErrSpecNotInLease = errors.New("orchestration: spec_path not in worker lease")

// StoreScreenshot persists an orchestration-flow screenshot to the object
// store and returns the resulting key. Composes the key under the
// `orchestration/{run_uuid}/{lease_uuid}/screenshots/...` prefix so a single
// `/files/{key}` redirect can serve both reports-flow and orchestration-flow
// uploads via prefix-based sandboxing.
//
// Worker auth: must already have (or have had) a lease on the run. Active
// leases are preferred; recently-released leases are accepted within the
// retention window so that late attachments can still be uploaded after
// `complete`.
//
// Returns ErrUnknownLease when no lease was ever issued for the worker on
// this run, ErrSpecNotInLease when specPath is not part of the worker's
// lease unit set.
func (s *Store) StoreScreenshot(
	ctx context.Context,
	identity CompositeIdentity,
	worker WorkerIdentity,
	specPath string,
	relativePath string,
	body io.Reader,
	contentType string,
	size int64,
	store storage.ObjectStore,
) (string, error) {
	if specPath == "" {
		return "", errors.New("orchestration screenshot: spec_path is required")
	}
	if relativePath == "" {
		return "", errors.New("orchestration screenshot: relative_path is required")
	}

	run, err := s.FindRunByIdentity(ctx, identity)
	if err != nil {
		return "", err
	}
	lease, err := s.FindLeaseByWorker(ctx, run.ID, worker)
	if err != nil {
		return "", err
	}

	belongs, err := s.specPathInLeaseUnits(ctx, lease.ID, specPath)
	if err != nil {
		return "", err
	}
	if !belongs {
		return "", ErrSpecNotInLease
	}

	key := composeScreenshotKey(run.ID.String(), lease.ID.String(), specPath, relativePath)

	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if err := store.Put(ctx, key, body, contentType, size); err != nil {
		return "", fmt.Errorf("orchestration screenshot put: %w", err)
	}
	logEvent(ctx, s.Logger, "orchestration.screenshot.uploaded", "orchestration screenshot uploaded", run,
		slog.String("gh_job_id", worker.GHJobID),
		slog.String("lease_id", lease.ID.String()),
		slog.String("spec_path", specPath),
		slog.Int64("size_bytes", size),
	)
	logMetric(ctx, s.Logger, "orchestration_screenshot_uploads_total", "", 1)
	return key, nil
}

// specPathInLeaseUnits returns true when specPath matches the spec_path of
// any dispatch_unit attached to the lease.
func (s *Store) specPathInLeaseUnits(ctx context.Context, leaseID uuid.UUID, specPath string) (bool, error) {
	var exists bool
	err := s.Pool.QueryRow(ctx, `
		SELECT EXISTS(
		    SELECT 1 FROM dispatch_units du
		    JOIN leases l ON du.id = ANY(l.unit_ids)
		     WHERE l.id = $1 AND du.spec_path = $2
		)
	`, leaseID, specPath).Scan(&exists)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("check spec in lease: %w", err)
	}
	return exists, nil
}

// composeScreenshotKey builds the object-store key for an orchestration
// screenshot. Path segments are kept verbatim — object stores accept
// spaces and parens in keys, and `aws-sdk-go-v2`'s presigner handles URL
// encoding when it generates the presigned URL. URL-encoding the key at
// storage time previously caused the read path's chi-decoded lookup to
// miss the object (stored at `page%20%28failed%29.png`, looked up at
// `page (failed).png`).
func composeScreenshotKey(runUUID, leaseUUID, specPath, relativePath string) string {
	return strings.Join([]string{
		"orchestration",
		runUUID,
		leaseUUID,
		"screenshots",
		joinNonEmpty(specPath),
		joinNonEmpty(relativePath),
	}, "/")
}

// joinNonEmpty rejoins p's `/`-delimited segments after skipping empty
// ones, so leading/trailing slashes and `//` runs collapse to a single
// `/`. Preserves all other characters (spaces, parens, etc.) verbatim.
func joinNonEmpty(p string) string {
	parts := strings.Split(p, "/")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return strings.Join(out, "/")
}
