package verdict

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/triage"
)

// Request names exactly one report-group attempt and the comparison context.
type Request struct {
	CompositeIdentity struct {
		Repository   string `json:"repository"`
		CommitSHA    string `json:"commit_sha"`
		GHRunID      string `json:"gh_run_id"`
		GHRunAttempt string `json:"gh_run_attempt"`
		Name         string `json:"name"`
	} `json:"composite_identity"`
	Context             string   `json:"context"`
	BaseRef             string   `json:"base_ref"`
	BaseSHA             string   `json:"base_sha"`
	Lane                string   `json:"lane"`
	ChangedFiles        []string `json:"changed_files,omitempty"`
	WaitForCompletionMS int      `json:"wait_for_completion_ms,omitempty"`
	// AsOf replays the decision with only the evidence that existed at that
	// instant (trunk observations, quarantine, freshness). Admin-only; the
	// result is returned but never persisted, so backtests cannot pollute the
	// audit trail or the idempotency cache.
	AsOf *time.Time `json:"as_of,omitempty"`
}

// Store persists audit projections using the shared evidence store.
type Store struct{ Evidence *triage.Store }

// Compute returns an existing audit row for identical evidence and policy.
func (s *Store) Compute(ctx context.Context, g triage.Group, req Request) (Verdict, error) {
	now := time.Now().UTC()
	replay := req.AsOf != nil
	if replay {
		now = req.AsOf.UTC()
	}
	p, err := s.Evidence.LoadPolicy(ctx, g.Repository, req.Context)
	if err != nil {
		return Verdict{}, err
	}
	in := Inputs{Now: now, Group: g, Policy: p, Context: req.Context, BaseRef: req.BaseRef, BaseSHA: req.BaseSHA, Lane: req.Lane, ChangedFiles: req.ChangedFiles}
	if in.BaseRef == "" {
		in.BaseRef = g.BaseRef
	}
	if in.BaseSHA == "" {
		in.BaseSHA = g.BaseSHA
	}
	if in.Lane == "" {
		in.Lane = g.Lane
	}
	in.PR, err = s.Evidence.AllObservations(ctx, triage.ObservationFilter{GroupID: g.ID, MatchLane: true, Lane: in.Lane})
	if err != nil {
		return Verdict{}, err
	}
	in.Trunk, err = s.Evidence.AllObservations(ctx, triage.ObservationFilter{Repository: g.Repository, Framework: g.Framework, MatchLane: true, Lane: in.Lane, BaseRef: in.BaseRef, BranchKind: triage.BranchTrunk, Since: now.Add(-time.Duration(p.Thresholds.WindowDays) * 24 * time.Hour), Until: now, CompletedOnly: true, MaxRunsPerIdentity: p.Thresholds.MaxTrunkRuns})
	if err != nil {
		return Verdict{}, err
	}
	in.Quarantine, err = s.Evidence.ActiveQuarantine(ctx, g.Repository, g.Framework, in.BaseRef, in.Lane, now)
	if err != nil {
		return Verdict{}, err
	}
	err = s.Evidence.Pool.QueryRow(ctx, `SELECT COALESCE(max(o.observed_at),'0001-01-01'::timestamptz) FROM test_observations o JOIN test_identities i ON i.id=o.identity_id JOIN report_groups g ON g.id=o.report_group_id WHERE i.repository=$1 AND i.framework=$2 AND o.lane=$3 AND o.branch=$4 AND o.branch_kind='trunk' AND NOT o.is_infra_stub AND g.status='completed' AND o.observed_at<=$5`, g.Repository, g.Framework, in.Lane, in.BaseRef, now).Scan(&in.LatestTrunkAt)
	if err != nil {
		return Verdict{}, err
	}
	in.TrunkSeen, err = s.Evidence.SeenOnTrunk(ctx, in.PR)
	if err != nil {
		return Verdict{}, err
	}
	in.CrossPR, in.CrossPRSignatures, err = s.crossPR(ctx, g, in, now.Add(-time.Duration(p.Thresholds.WindowDays)*24*time.Hour), now)
	if err != nil {
		return Verdict{}, err
	}
	err = s.Evidence.Pool.QueryRow(ctx, `SELECT COALESCE(
 (SELECT max(g.created_at) FROM report_groups g JOIN test_observations o ON o.report_group_id=g.id
  WHERE g.repository=$1 AND g.framework=$2 AND g.commit_sha=$3 AND o.branch=$4 AND o.lane=$5 AND o.branch_kind='trunk' AND g.status='completed' AND NOT o.is_infra_stub),
 (SELECT max(g.created_at) FROM report_groups g JOIN test_observations o ON o.report_group_id=g.id
  WHERE g.repository=$1 AND g.framework=$2 AND g.created_at<=$6 AND o.branch=$4 AND o.lane=$5 AND o.branch_kind='trunk' AND g.status='completed' AND NOT o.is_infra_stub),
 '0001-01-01'::timestamptz)`, g.Repository, g.Framework, in.BaseSHA, in.BaseRef, in.Lane, g.CreatedAt).Scan(&in.BaseTime)
	if err != nil {
		return Verdict{}, err
	}
	rows, err := s.Evidence.Pool.Query(ctx, `SELECT u.spec_path,u.state FROM dispatch_units u JOIN orchestration_runs r ON r.id=u.run_id
 WHERE r.repository=$1 AND r.commit_sha=$2 AND r.gh_run_id=$3 AND r.name=$4 AND r.gh_run_attempt=$5
 AND (u.state='abandoned' OR (u.state='completed_fail' AND NOT EXISTS (
  SELECT 1 FROM test_observations o WHERE o.lane=$6 AND o.status IN ('failed','timedOut','interrupted')
  AND o.attempt_id=(SELECT a.id FROM attempts a WHERE a.dispatch_unit_id=u.id AND a.reported_at IS NOT NULL ORDER BY a.created_at DESC,a.id DESC LIMIT 1)
 ))) ORDER BY u.spec_path,u.id`, g.Repository, g.CommitSHA, g.GHRunID, g.Name, g.GHRunAttempt, in.Lane)
	if err != nil {
		return Verdict{}, err
	}
	for rows.Next() {
		var spec, state string
		if err := rows.Scan(&spec, &state); err != nil {
			rows.Close()
			return Verdict{}, err
		}
		if state == "abandoned" {
			in.Abandoned = append(in.Abandoned, spec)
		} else {
			in.UnrepresentedFailures = append(in.UnrepresentedFailures, spec)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Verdict{}, err
	}
	result := Classify(in)
	// ASSUMPTION: a completed group with no test facts cannot prove a green run.
	if len(in.PR) == 0 && len(in.Abandoned) == 0 && len(in.UnrepresentedFailures) == 0 && g.Status == "completed" {
		result.Verdict = resultActionRequired
		result.Reason = "No test observations were received for this lane; verify upload and rerun."
		result.Markdown = Render(result)
	}
	if replay {
		// Replays are never persisted; the nil UUID marks the result as such.
		result.ID = "00000000-0000-0000-0000-000000000000"
		return result, nil
	}
	hash := InputsHash(in)
	counts, _ := json.Marshal(result.Counts)
	findings, _ := json.Marshal(result.Findings)
	thresholds, _ := json.Marshal(result.ThresholdsUsed)
	var id string
	err = s.Evidence.Pool.QueryRow(ctx, `INSERT INTO pr_verdicts(report_group_id,repository,context,gh_pr_number,head_sha,base_ref,base_sha,lane,mode,verdict,confidence,counts,findings,thresholds_used,engine_version,inputs_hash) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8,$9,$10,$11,$12,$13,$14,$15,$16) ON CONFLICT(report_group_id,engine_version,inputs_hash) DO UPDATE SET inputs_hash=EXCLUDED.inputs_hash RETURNING id::text`, g.ID, g.Repository, req.Context, g.GHPRNumber, g.CommitSHA, in.BaseRef, in.BaseSHA, in.Lane, p.Mode, result.Verdict, result.Confidence, counts, findings, thresholds, triage.EngineVersion, hash[:]).Scan(&id)
	if err != nil {
		return Verdict{}, err
	}
	return s.Get(ctx, id)
}

// crossPR loads each PR identity's executions on other PRs in the window and
// the distinct-PR count per failure signature (same repository, framework and
// lane; completed groups only; never this PR).
func (s *Store) crossPR(ctx context.Context, g triage.Group, in Inputs, since, until time.Time) (map[string]triage.CrossPRStats, map[string]int, error) {
	ids := make([]string, 0, len(in.PR))
	seen := map[string]bool{}
	for _, o := range in.PR {
		if !seen[o.IdentityID] {
			seen[o.IdentityID] = true
			ids = append(ids, o.IdentityID)
		}
	}
	stats := map[string]triage.CrossPRStats{}
	sigs := map[string]int{}
	if len(ids) == 0 {
		return stats, sigs, nil
	}
	var pr int
	if g.GHPRNumber != nil {
		pr = *g.GHPRNumber
	}
	rows, err := s.Evidence.Pool.Query(ctx, `SELECT o.identity_id::text,
 count(DISTINCT o.gh_pr_number) FILTER (WHERE o.status IN ('failed','timedOut','interrupted')),
 count(*) FILTER (WHERE o.status IN ('failed','timedOut','interrupted')),
 count(*) FILTER (WHERE o.status='passed')
 FROM test_observations o JOIN report_groups rg ON rg.id=o.report_group_id
 WHERE o.identity_id=ANY($1::uuid[]) AND o.branch_kind='pr' AND o.lane=$2 AND NOT o.is_infra_stub AND rg.status='completed'
 AND o.gh_pr_number IS NOT NULL AND o.gh_pr_number<>$3 AND o.observed_at>=$4 AND o.observed_at<=$5
 GROUP BY 1`, ids, in.Lane, pr, since, until)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var id string
		var c triage.CrossPRStats
		if err := rows.Scan(&id, &c.DistinctPRs, &c.Fails, &c.Passes); err != nil {
			rows.Close()
			return nil, nil, err
		}
		stats[id] = c
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	rows, err = s.Evidence.Pool.Query(ctx, `SELECT encode(o.error_signature,'hex'), count(DISTINCT o.gh_pr_number)
 FROM test_observations o JOIN test_identities i ON i.id=o.identity_id JOIN report_groups rg ON rg.id=o.report_group_id
 WHERE i.repository=$1 AND i.framework=$2 AND o.branch_kind='pr' AND o.lane=$3 AND NOT o.is_infra_stub AND rg.status='completed'
 AND o.status IN ('failed','timedOut','interrupted') AND o.error_signature IS NOT NULL
 AND o.gh_pr_number IS NOT NULL AND o.gh_pr_number<>$4 AND o.observed_at>=$5 AND o.observed_at<=$6
 GROUP BY 1 HAVING count(DISTINCT o.gh_pr_number)>=2`, g.Repository, g.Framework, in.Lane, pr, since, until)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sig string
		var n int
		if err := rows.Scan(&sig, &n); err != nil {
			return nil, nil, err
		}
		sigs[sig] = n
	}
	return stats, sigs, rows.Err()
}

// Get reconstructs markdown from the immutable persisted decision.
func (s *Store) Get(ctx context.Context, id string) (Verdict, error) {
	items, err := s.List(ctx, []string{id})
	if err != nil {
		return Verdict{}, err
	}
	if len(items) == 0 {
		return Verdict{}, pgx.ErrNoRows
	}
	return items[0], nil
}

// List loads persisted verdicts in the order of ids with a single query.
func (s *Store) List(ctx context.Context, ids []string) ([]Verdict, error) {
	rows, err := s.Evidence.Pool.Query(ctx, `SELECT id::text,report_group_id::text,mode,verdict,confidence,counts,findings,thresholds_used,engine_version,human_override,computed_at,adjudication FROM pr_verdicts WHERE id=ANY($1::uuid[])`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byID := map[string]Verdict{}
	for rows.Next() {
		v, err := scanVerdict(rows)
		if err != nil {
			return nil, err
		}
		byID[v.ID] = v
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]Verdict, 0, len(ids))
	for _, id := range ids {
		if v, ok := byID[id]; ok {
			out = append(out, v)
		}
	}
	return out, nil
}

func scanVerdict(rows pgx.Rows) (Verdict, error) {
	var v Verdict
	var counts, findings, thresholds, override, adjudication []byte
	if err := rows.Scan(&v.ID, &v.ReportGroupID, &v.Mode, &v.Verdict, &v.Confidence, &counts, &findings, &thresholds, &v.EngineVersion, &override, &v.ComputedAt, &adjudication); err != nil {
		return Verdict{}, err
	}
	if len(adjudication) > 0 {
		if err := json.Unmarshal(adjudication, &v.Adjudication); err != nil {
			return Verdict{}, err
		}
	}
	for _, part := range []struct {
		raw    []byte
		target any
	}{{counts, &v.Counts}, {findings, &v.Findings}, {thresholds, &v.ThresholdsUsed}} {
		if err := json.Unmarshal(part.raw, part.target); err != nil {
			return Verdict{}, err
		}
	}
	if len(override) > 0 {
		if err := json.Unmarshal(override, &v.HumanOverride); err != nil {
			return Verdict{}, err
		}
	}
	// The exact DDL has no top-level reason column; recover guard reasons from
	// the persisted verdict and evidence rather than relying on process memory.
	switch {
	case v.Verdict == resultIncomplete:
		v.Reason = "The report group did not finish; rerun missing work."
	case v.Verdict == resultFailure && v.Counts.Blocking == 0 && v.Counts.Exonerated > 0 && v.Confidence < v.ThresholdsUsed.ConfidenceFloor:
		v.Reason = "LOW_CONFIDENCE: the evidence is below the configured confidence floor."
	case v.Verdict == resultActionRequired:
		v.Reason = "Infrastructure, missing observations, or stale trunk evidence require a rerun."
	}
	v.Markdown = Render(v)
	return v, nil
}

// RecordOverride retains the engine decision and separately audits a human choice.
func (s *Store) RecordOverride(ctx context.Context, id string, override Override) error {
	raw, err := json.Marshal(override)
	if err != nil {
		return err
	}
	tag, err := s.Evidence.Pool.Exec(ctx, `UPDATE pr_verdicts SET human_override=$2 WHERE id=$1`, id, raw)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// ErrPending signals that no verdict is computed until all uploads finish.
var ErrPending = errors.New("report group is still in progress")
