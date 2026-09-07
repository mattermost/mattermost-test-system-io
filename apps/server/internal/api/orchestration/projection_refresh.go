package orchestration

import (
	"context"

	"github.com/google/uuid"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/api/reports"
	orchdomain "github.com/mattermost/mattermost-test-system-io/apps/server/internal/orchestration"
)

func (h *Handlers) refreshReportGroupProjection(ctx context.Context, identity orchdomain.CompositeIdentity) {
	if h.Pool == nil {
		return
	}
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
	reports.RefreshGroupSummaryBestEffort(ctx, h.Pool, h.Logger, groupID)
}
