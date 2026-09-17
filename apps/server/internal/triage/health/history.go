package health

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/triage"
)

// history loads a bounded number of distinct runs per identity, including old
// identities needed for retirement. Ranking occurs in SQL so retention growth
// does not turn a periodic refresh into an unbounded in-process history scan.
func history(ctx context.Context, tx pgx.Tx, repository, baseRef, lane string, p triage.Thresholds, now time.Time) (map[string][]triage.Observation, error) {
	// Each of the last quarantine/release candidates needs its own full rolling
	// window. Extra prefix runs avoid classifying truncated prefixes as unknown.
	limit := p.MaxTrunkRuns + max(p.AutoQuarantineAfterRuns, p.ReleaseAfterPasses) - 1
	rows, err := tx.Query(ctx, `WITH scoped_groups AS (
 SELECT o.identity_id,o.report_group_id,g.created_at
 FROM test_observations o JOIN test_identities i ON i.id=o.identity_id JOIN report_groups g ON g.id=o.report_group_id
 WHERE i.repository=$1 AND o.branch=$2 AND o.lane=$3 AND o.branch_kind='trunk'
 AND NOT o.is_infra_stub AND g.status='completed' AND o.observed_at<=$5
 GROUP BY o.identity_id,o.report_group_id,g.created_at
), ranked AS (
 SELECT *,row_number() OVER(PARTITION BY identity_id ORDER BY created_at DESC,report_group_id DESC) AS position FROM scoped_groups
)
SELECT row_to_json(fact) FROM (
 SELECT o.id,o.identity_id,o.report_group_id,o.attempt_index,o.status,o.observed_at,o.commit_sha,
 COALESCE(encode(o.error_signature,'hex'),'') error_signature,COALESCE(o.error_excerpt,'') error_excerpt,
 COALESCE(o.failure_locus,'') failure_locus,r.created_at AS group_created_at FROM ranked r JOIN test_observations o
 ON (o.identity_id,o.report_group_id)=(r.identity_id,r.report_group_id)
 WHERE r.position<=$4 AND o.branch=$2 AND o.lane=$3 AND o.branch_kind='trunk' AND NOT o.is_infra_stub AND o.observed_at<=$5
) fact`, repository, baseRef, lane, limit, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byID := map[string][]triage.Observation{}
	for rows.Next() {
		var raw []byte
		var o triage.Observation
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &o); err != nil {
			return nil, err
		}
		byID[o.IdentityID] = append(byID[o.IdentityID], o)
	}
	return byID, rows.Err()
}

// project replays the last distinct run refreshes with a complete rolling
// window at each run. Repeated calls and duplicate events cannot advance the
// count. Counts are bounded to the retained, independently verifiable history.
func project(all []triage.Observation, now time.Time, p triage.Thresholds) (triage.Stats, string, time.Time, int) {
	latest := time.Time{}
	for _, o := range all {
		if o.ObservedAt.After(latest) {
			latest = o.ObservedAt
		}
	}
	stats := triage.Summarize(triage.Trials(all, now, p))
	class := Classify(stats, latest, now, p)
	if class == classificationRetired {
		return stats, class, latest.Add(time.Duration(p.RetireDays) * 24 * time.Hour), 1
	}
	// Trials supplies deterministic chronological group ordering, without the
	// current window trimming the evidence needed by an earlier refresh.
	historical := p
	historical.WindowDays = 0
	historical.MaxTrunkRuns = len(all)
	runs := triage.Trials(all, now, historical)
	refreshes, since := 0, now
	for n := len(runs); n > 0; n-- {
		at := runs[n-1].ObservedAt
		// Only prefix runs were available at that point in trunk chronology.
		prefix := triage.Trials(runs[:n], at, p)
		if Classify(triage.Summarize(prefix), at, at, p) != class {
			break
		}
		refreshes++
		since = at
	}
	return stats, class, since, max(1, refreshes)
}
