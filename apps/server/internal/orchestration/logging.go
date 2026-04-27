package orchestration

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
)

// Observability for the orchestration domain.
//
// Logging:
//   - Every state-changing operation emits a single Info log line keyed by an
//     `event` attribute (e.g. orchestration.run.begin) plus correlation
//     fields: run_id, repository, short commit_sha, gh_run_id, name,
//     gh_run_attempt, and gh_job_id when applicable.
//
// Metrics:
//   - This service does not currently bundle a Prometheus client (or any
//     metrics framework) — go.opentelemetry.io/otel/metric appears only as an
//     indirect dependency, and there is no /metrics endpoint. Rather than
//     introducing a new runtime dep, counters are emitted as slog Info lines
//     tagged `metric=<name>` with `value=<delta>` (and an optional `outcome`
//     label for partitioned counters). Operators scrape these via the same
//     log aggregation pipeline that ingests request logs.
//
// Both kinds of lines are nil-safe: the helpers below are no-ops when the
// owning Store/Reaper has no Logger configured (notably the unit tests in
// this package, which construct Store{Pool: ...} directly).

// logEvent emits a structured Info line for a state-changing event. attrs are
// appended after the canonical event/run/identity attributes. No-op when
// logger is nil.
func logEvent(logger *slog.Logger, ctx context.Context, event, message string, run *Run, attrs ...slog.Attr) {
	if logger == nil {
		return
	}
	base := []slog.Attr{slog.String("event", event)}
	if run != nil {
		base = append(base,
			slog.String("run_id", run.ID.String()),
			slog.String("repository", run.Identity.Repository),
			slog.String("commit_sha", shortSHA(run.Identity.CommitSHA)),
			slog.String("gh_run_id", run.Identity.GHRunID),
			slog.String("name", run.Identity.Name),
			slog.String("gh_run_attempt", run.Identity.GHRunAttempt),
		)
	}
	base = append(base, attrs...)
	logger.LogAttrs(ctx, slog.LevelInfo, message, base...)
}

// logEventIdentity is the variant used when the caller has a CompositeIdentity
// and a run UUID but not a fully hydrated Run (e.g. the reaper's lease and
// run-timeout paths).
func logEventIdentity(logger *slog.Logger, ctx context.Context, event, message string, runID uuid.UUID, identity CompositeIdentity, attrs ...slog.Attr) {
	if logger == nil {
		return
	}
	base := []slog.Attr{
		slog.String("event", event),
		slog.String("run_id", runID.String()),
		slog.String("repository", identity.Repository),
		slog.String("commit_sha", shortSHA(identity.CommitSHA)),
		slog.String("gh_run_id", identity.GHRunID),
		slog.String("name", identity.Name),
		slog.String("gh_run_attempt", identity.GHRunAttempt),
	}
	base = append(base, attrs...)
	logger.LogAttrs(ctx, slog.LevelInfo, message, base...)
}

// logMetric emits a slog-based counter increment. name is the metric name,
// outcome is an optional partition label ("" omits it), and value is the
// delta (typically 1). No-op when logger is nil.
func logMetric(logger *slog.Logger, ctx context.Context, name, outcome string, value int) {
	if logger == nil {
		return
	}
	attrs := []slog.Attr{
		slog.String("metric", name),
		slog.Int("value", value),
	}
	if outcome != "" {
		attrs = append(attrs, slog.String("outcome", outcome))
	}
	logger.LogAttrs(ctx, slog.LevelInfo, "metric", attrs...)
}

// shortSHA truncates a commit SHA to the conventional 7-char prefix for log
// lines. Pass-through for already-short or empty values.
func shortSHA(sha string) string {
	if len(sha) <= 7 {
		return sha
	}
	return sha[:7]
}
