package health

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/events"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/triage"
)

// Refresher rolls completed trunk facts into health and quarantine in one transaction.
type Refresher struct {
	Store *triage.Store
	Hub   *events.Hub
}

// ContextName maps current producer identity names back to required status contexts.
func ContextName(name, lane string) string {
	if strings.HasPrefix(name, "mobile-") {
		for _, prefix := range []string{"mobile-pr-", "mobile-main-"} {
			if strings.HasPrefix(name, prefix) {
				return "e2e-test/" + strings.TrimPrefix(name, prefix)
			}
		}
	}
	if lane != "" && strings.HasSuffix(name, "-"+lane) {
		return "e2e-test/" + strings.TrimSuffix(name, "-"+lane) + "/" + lane
	}
	return "e2e-test/" + name
}

// Refresh rebuilds a lane. Repeated refreshes never advance quarantine counters:
// sustained classification is derived from distinct historical group outcomes.
func (r *Refresher) Refresh(ctx context.Context, repository, baseRef, lane, contextName string) (int, error) {
	p, err := r.Store.LoadPolicy(ctx, repository, contextName)
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	tx, err := r.Store.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Serialize refreshes across API requests, workers, and multiple server replicas.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, fmt.Sprintf("%q/%q/%q", repository, baseRef, lane)); err != nil {
		return 0, err
	}
	byID, err := history(ctx, tx, repository, baseRef, lane, p.Thresholds, now)
	if err != nil {
		return 0, err
	}
	// Expire entries before opening new ones, otherwise the active uniqueness
	// index would postpone renewal until the next refresh.
	if _, err = tx.Exec(ctx, `UPDATE quarantine_entries q SET status='expired',released_at=$4,released_by='system' FROM test_identities i WHERE q.identity_id=i.id AND i.repository=$1 AND q.base_ref=$2 AND (q.lane=$3 OR q.lane IS NULL) AND q.status='active' AND q.expires_at<=$4`, repository, baseRef, lane, now); err != nil {
		return 0, err
	}
	emitted := []events.Event{}
	unhealthy, total := 0, 0
	for id, all := range byID {
		s, class, since, refreshes := project(all, now, p.Thresholds)
		raw, _ := json.Marshal(Projection{
			IdentityID: id, Lane: lane, BaseRef: baseRef, WindowRuns: s.Runs,
			PassCount: s.Passes, FailCount: s.Fails, FlakyCount: s.Flaky,
			InstabilityRate: s.InstabilityRate, ConsecutiveFails: s.ConsecutiveFails,
			LastPassAt: s.LastPassAt, LastFailAt: s.LastFailAt,
			DominantSignature: s.DominantSignature, DominantLocus: s.DominantLocus,
			Classification: class, ClassificationSince: since, ConsecutiveRefreshes: refreshes,
			ComputedAt: now, EngineVersion: triage.EngineVersion,
		})
		_, err = tx.Exec(ctx, `INSERT INTO test_health(identity_id,lane,base_ref,window_runs,pass_count,fail_count,flaky_count,instability_rate,consecutive_fails,last_pass_at,last_fail_at,dominant_signature,dominant_locus,classification,classification_since,consecutive_refreshes,computed_at,engine_version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,decode(NULLIF($12,''),'hex'),NULLIF($13,''),$14,$15,$16,$17,$18) ON CONFLICT(identity_id,lane,base_ref) DO UPDATE SET window_runs=EXCLUDED.window_runs,pass_count=EXCLUDED.pass_count,fail_count=EXCLUDED.fail_count,flaky_count=EXCLUDED.flaky_count,instability_rate=EXCLUDED.instability_rate,consecutive_fails=EXCLUDED.consecutive_fails,last_pass_at=EXCLUDED.last_pass_at,last_fail_at=EXCLUDED.last_fail_at,dominant_signature=EXCLUDED.dominant_signature,dominant_locus=EXCLUDED.dominant_locus,classification=EXCLUDED.classification,classification_since=CASE WHEN test_health.classification=EXCLUDED.classification THEN LEAST(test_health.classification_since,EXCLUDED.classification_since) ELSE EXCLUDED.classification_since END,consecutive_refreshes=EXCLUDED.consecutive_refreshes,computed_at=EXCLUDED.computed_at,engine_version=EXCLUDED.engine_version`, id, lane, baseRef, s.Runs, s.Passes, s.Fails, s.Flaky, s.InstabilityRate, s.ConsecutiveFails, s.LastPassAt, s.LastFailAt, s.DominantSignature, s.DominantLocus, class, since, max(1, refreshes), now, triage.EngineVersion)
		if err != nil {
			return 0, err
		}
		if class != classificationRetired {
			total++
		}
		if class == classificationBroken || class == triage.StatusFlaky {
			unhealthy++
		}
		if p.Mode != "off" && (class == classificationBroken || class == triage.StatusFlaky) && refreshes >= p.Thresholds.AutoQuarantineAfterRuns {
			var qid string
			err = tx.QueryRow(ctx, `INSERT INTO quarantine_entries(identity_id,lane,base_ref,source,reason,status,evidence,created_by) VALUES($1,$2,$3,'auto',$4,'active',$5,'system') ON CONFLICT DO NOTHING RETURNING id::text`, id, lane, baseRef, class, raw).Scan(&qid)
			if err != nil && err != pgx.ErrNoRows {
				return 0, err
			}
			if err == nil {
				emitted = append(emitted, event("triage.quarantine.opened", map[string]string{"quarantine_id": qid}))
			}
		}
		if class == classificationHealthy && s.ConsecutivePasses >= p.Thresholds.ReleaseAfterPasses {
			ids, err := releaseAuto(ctx, tx, id, lane, baseRef, now)
			if err != nil {
				return 0, err
			}
			for _, qid := range ids {
				emitted = append(emitted, event("triage.quarantine.released", map[string]string{"quarantine_id": qid}))
			}
		}
	}
	if total > 0 && float64(unhealthy)/float64(total) > p.Thresholds.MaxExoneratedRatio {
		// The lane's producer context is known; do not downgrade unrelated
		// platforms in the same repository or enable an off policy.
		tag, err := tx.Exec(ctx, `UPDATE triage_policies SET mode='shadow',updated_by='system:lane-alarm',updated_at=$2 WHERE repository=$1 AND context=$3 AND mode='enforce'`, repository, now, contextName)
		if err != nil {
			return 0, err
		}
		if tag.RowsAffected() > 0 {
			emitted = append(emitted, event("triage.lane.alarm", map[string]any{"repository": repository, "lane": lane, "base_ref": baseRef, "ratio": float64(unhealthy) / float64(total)}))
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	emitted = append(emitted, event("triage.health.refreshed", map[string]any{"repository": repository, "lane": lane, "changed_identities": len(byID)}))
	if r.Hub != nil {
		for _, e := range emitted {
			r.Hub.Publish(e, events.Scope{})
		}
	}
	return len(byID), nil
}

func releaseAuto(ctx context.Context, tx pgx.Tx, id, lane, baseRef string, now time.Time) ([]string, error) {
	rows, err := tx.Query(ctx, `UPDATE quarantine_entries SET status='released',released_at=$4,released_by='system' WHERE identity_id=$1 AND lane=$2 AND base_ref=$3 AND source='auto' AND status='active' RETURNING id::text`, id, lane, baseRef, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func event(name string, payload any) events.Event {
	raw, _ := json.Marshal(payload)
	return events.Event{Type: name, Timestamp: time.Now().UTC(), Payload: raw}
}

// RefreshAll is also the recovery path for dropped in-memory completion events.
func (r *Refresher) RefreshAll(ctx context.Context, since time.Time) error {
	rows, err := r.Store.Pool.Query(ctx, `SELECT DISTINCT i.repository,o.branch,o.lane,g.name FROM test_observations o JOIN test_identities i ON i.id=o.identity_id JOIN report_groups g ON g.id=o.report_group_id WHERE o.branch_kind='trunk' AND NOT o.is_infra_stub AND g.status='completed' AND g.updated_at>=$1`, since)
	if err != nil {
		return err
	}
	type scope struct{ repo, branch, lane, name string }
	var scopes []scope
	for rows.Next() {
		var s scope
		if err := rows.Scan(&s.repo, &s.branch, &s.lane, &s.name); err != nil {
			rows.Close()
			return err
		}
		scopes = append(scopes, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	// One failing scope must not starve the others; errors are collected and
	// returned together, except cancellation, which stops the sweep.
	var errs []error
	for _, s := range scopes {
		if _, err := r.Refresh(ctx, s.repo, s.branch, s.lane, ContextName(s.name, s.lane)); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			errs = append(errs, fmt.Errorf("refresh %s/%s/%s: %w", s.repo, s.branch, s.lane, err))
		}
	}
	return errors.Join(errs...)
}
