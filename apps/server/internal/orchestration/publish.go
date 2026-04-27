// Package orchestration provides a Publisher that emits live state-change events
// consumed by the WebSocket Orchestration tab. Backed by internal/events Hub.
//
// Design note: orchestration.CompositeIdentity is a SUPERSET of
// events.CompositeIdentity (it carries Branch, GHPRNumber, Framework in
// addition to the five join-key fields). The five join-key fields are
// projected onto events.CompositeIdentity via toEventsIdentity below; we
// intentionally avoid a Go type alias because the two structs have a
// different field set. The Hub's identity routing only needs the five fields
// that uniquely identify the run.
package orchestration

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/events"
)

// Event-type strings published by Publisher. These are the wire contract
// consumed by the Orchestration tab; renaming any of them is a breaking
// change.
const (
	EventTypeRunStarted    = "orchestration.run.started"
	EventTypeUnitLeased    = "orchestration.unit.leased"
	EventTypeUnitCompleted = "orchestration.unit.completed"
	EventTypeLeaseExpired  = "orchestration.lease.expired"
	EventTypeRunCompleted  = "orchestration.run.completed"
	EventTypeRunTimedOut   = "orchestration.run.timed_out"
)

// Publisher writes orchestration state-change events onto an events.Hub. All
// helpers are best-effort: a failure to marshal or to deliver does NOT abort
// the calling state transition. Mirrors how internal/events.Publisher
// operates for the reports flow.
type Publisher struct {
	Hub    *events.Hub
	Logger *slog.Logger
}

// RunStarted emits "orchestration.run.started" when begin run inserts a new
// orchestration_runs row.
func (p *Publisher) RunStarted(
	ctx context.Context,
	identity CompositeIdentity,
	totalUnits int,
	deadline time.Time,
	leaseTimeoutMs int64,
) {
	p.emit(ctx, EventTypeRunStarted, identity, map[string]any{
		"total_units":      totalUnits,
		"deadline":         deadline.UTC(),
		"lease_timeout_ms": leaseTimeoutMs,
	})
}

// UnitLeased emits "orchestration.unit.leased" after a successful checkout
// transaction.
func (p *Publisher) UnitLeased(
	ctx context.Context,
	identity CompositeIdentity,
	ghJobName, ghJobID string,
	unitIDs []uuid.UUID,
	deadline time.Time,
	isRetest bool,
) {
	ids := make([]string, len(unitIDs))
	for i, u := range unitIDs {
		ids[i] = u.String()
	}
	p.emit(ctx, EventTypeUnitLeased, identity, map[string]any{
		"gh_job_name": ghJobName,
		"gh_job_id":   ghJobID,
		"unit_ids":    ids,
		"deadline":    deadline.UTC(),
		"is_retest":   isRetest,
	})
}

// UnitCompleted emits "orchestration.unit.completed" when a complete call
// updates a unit's outcome.
func (p *Publisher) UnitCompleted(
	ctx context.Context,
	identity CompositeIdentity,
	unitID uuid.UUID,
	outcome string,
	lateReport bool,
	attemptsCount int,
) {
	p.emit(ctx, EventTypeUnitCompleted, identity, map[string]any{
		"unit_id":        unitID.String(),
		"outcome":        outcome,
		"late_report":    lateReport,
		"attempts_count": attemptsCount,
	})
}

// LeaseExpired emits "orchestration.lease.expired" when the reaper releases a
// lease whose deadline passed.
func (p *Publisher) LeaseExpired(
	ctx context.Context,
	identity CompositeIdentity,
	ghJobName, ghJobID string,
	releasedAt time.Time,
	reclaimedUnitIDs []uuid.UUID,
) {
	ids := make([]string, len(reclaimedUnitIDs))
	for i, u := range reclaimedUnitIDs {
		ids[i] = u.String()
	}
	p.emit(ctx, EventTypeLeaseExpired, identity, map[string]any{
		"gh_job_name":        ghJobName,
		"gh_job_id":          ghJobID,
		"released_at":        releasedAt.UTC(),
		"reclaimed_unit_ids": ids,
	})
}

// RunCompleted emits "orchestration.run.completed" when a run transitions to
// completed.
func (p *Publisher) RunCompleted(
	ctx context.Context,
	identity CompositeIdentity,
	terminalAt time.Time,
	counts RunCounts,
) {
	p.emit(ctx, EventTypeRunCompleted, identity, map[string]any{
		"terminal_at": terminalAt.UTC(),
		"counts":      runCountsPayload(counts),
	})
}

// RunTimedOut emits "orchestration.run.timed_out" when a run transitions to
// timed_out via the reaper.
func (p *Publisher) RunTimedOut(
	ctx context.Context,
	identity CompositeIdentity,
	terminalAt time.Time,
	counts RunCounts,
	abandonedCount int,
) {
	p.emit(ctx, EventTypeRunTimedOut, identity, map[string]any{
		"terminal_at":     terminalAt.UTC(),
		"counts":          runCountsPayload(counts),
		"abandoned_count": abandonedCount,
	})
}

// emit marshals the payload, builds an events.Event with the run's composite
// identity stamped on both the envelope and the Scope filter, and publishes
// to the Hub. Best-effort: any failure is logged at warn level and dropped.
func (p *Publisher) emit(ctx context.Context, typ string, identity CompositeIdentity, payload any) {
	if p == nil || p.Hub == nil {
		return
	}
	b, err := json.Marshal(payload)
	if err != nil {
		if p.Logger != nil {
			p.Logger.WarnContext(ctx, "orchestration: marshal event payload",
				slog.String("type", typ),
				slog.String("err", err.Error()),
			)
		}
		return
	}
	id := toEventsIdentity(identity)
	p.Hub.Publish(
		events.Event{
			Type:      typ,
			Timestamp: time.Now().UTC(),
			Payload:   b,
			Identity:  &id,
		},
		events.Scope{Identity: &id},
	)
}

// toEventsIdentity projects the orchestration composite identity onto the
// five-field events.CompositeIdentity used by the Hub's scope filter.
func toEventsIdentity(ci CompositeIdentity) events.CompositeIdentity {
	return events.CompositeIdentity{
		Repository:   ci.Repository,
		CommitSHA:    ci.CommitSHA,
		GHRunID:      ci.GHRunID,
		Name:         ci.Name,
		GHRunAttempt: ci.GHRunAttempt,
	}
}

// runCountsPayload converts the in-memory RunCounts into the JSON shape the
// dashboard consumes. Field names are the wire contract.
func runCountsPayload(c RunCounts) map[string]any {
	return map[string]any{
		"pending":           c.Pending,
		"leased":            c.Leased,
		"completed_pass":    c.CompletedPass,
		"completed_fail":    c.CompletedFail,
		"completed_skipped": c.CompletedSkipped,
		"abandoned":         c.Abandoned,
		"retest_eligible":   c.RetestEligible,
		"total":             c.Total,
	}
}
