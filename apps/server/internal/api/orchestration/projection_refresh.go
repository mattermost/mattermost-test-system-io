package orchestration

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/reports"
	orchdomain "github.com/mattermost/mattermost-test-system-io/apps/server/internal/orchestration"
)

// projectionRefreshTimeout bounds a detached projection refresh.
const projectionRefreshTimeout = 10 * time.Second

// detachedCtx survives client disconnect so a committed write still refreshes its projection.
func detachedCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), projectionRefreshTimeout)
}

func (h *Handlers) refreshReportGroupProjection(ctx context.Context, identity orchdomain.CompositeIdentity) {
	if h.Pool == nil {
		return
	}
	ctx, cancel := detachedCtx(ctx)
	defer cancel()
	groupID, err := reports.GroupIDForOrchestrationIdentity(ctx, h.Pool,
		identity.Repository, identity.CommitSHA, identity.GHRunID, identity.Name, identity.GHRunAttempt)
	if err != nil || groupID == uuid.Nil {
		if err != nil && h.Logger != nil {
			h.Logger.Warn("resolve report group for projection refresh failed",
				"error", err.Error())
		}
		return
	}
	reports.RefreshGroupSummaryBestEffort(ctx, h.Pool, h.Logger, groupID)
}

func (h *Handlers) refreshReportGroupProjectionByID(ctx context.Context, groupID uuid.UUID) {
	if h.Pool == nil || groupID == uuid.Nil {
		return
	}
	ctx, cancel := detachedCtx(ctx)
	defer cancel()
	reports.RefreshGroupSummaryBestEffort(ctx, h.Pool, h.Logger, groupID)
}
