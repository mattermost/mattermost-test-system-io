package verdict

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/triage"
)

// Classes the adjudicator is consulted about. Everything else is decided by
// the engine alone (INFRA, NEW_TEST, ownership of the failing spec itself).
var adjudicable = map[string]bool{
	"REGRESSION": true, "REGRESSION_CLUSTER": true, "REGRESSION_AREA": true, "OWNED_BY_PR": true,
	"INSUFFICIENT_DATA": true, "FLAKY_SUSPICIOUS": true,
	"BROKEN_ON_TRUNK": true, "FLAKY_CONFIRMED": true, "FLAKY_CROSS_PR": true, "QUARANTINED": true,
}

// EvidencePack is everything TSIO knows about one finding, in the shape the
// adjudicator prompt was evaluated with. The producer adds what only GitHub
// knows (PR title, changed files, diff hunks) before calling the model.
type EvidencePack struct {
	Index int `json:"index"`
	Test  struct {
		Title string `json:"title"`
		File  string `json:"file"`
		Lane  string `json:"lane"`
	} `json:"test"`
	Error  string `json:"error"`
	Engine struct {
		Class                                string `json:"class"`
		Reason                               string `json:"reason"`
		SignatureMatchesTrunkDominantFailure bool   `json:"signature_matches_trunk_dominant_failure"`
	} `json:"engine"`
	TrunkHistory struct {
		Runs                   int `json:"runs"`
		Fails                  int `json:"fails"`
		Flaky                  int `json:"flaky"`
		ConsecutiveFailsAtHead int `json:"consecutive_fails_at_head"`
	} `json:"trunk_history_14d"`
	CrossPR struct {
		ID                          string   `json:"id"`
		OtherPRsWhereThisTestFailed []string `json:"other_prs_where_this_test_failed"`
		OtherPRRunsWhereItPassed    int      `json:"other_pr_runs_where_it_passed"`
	} `json:"cross_pr_failures_14d"`
	PR struct {
		Number     *int   `json:"number"`
		Repository string `json:"repository"`
	} `json:"pr"`
	OtherFailuresInSameRun []struct {
		Class string `json:"class"`
		Title string `json:"title"`
	} `json:"other_failures_in_same_run"`
}

// maxEvidencePacks bounds the per-finding queries for a run where almost every
// test failed; the action consults the model for at most a handful of findings.
const maxEvidencePacks = 32

// EvidencePacks builds one pack per adjudicable finding of a persisted verdict
// (first maxEvidencePacks in finding order), using only evidence that existed
// at the verdict's computed_at.
func (s *Store) EvidencePacks(ctx context.Context, id string) ([]EvidencePack, error) {
	v, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	var repository, lane string
	var groupID string
	var pr *int
	if err := s.Evidence.Pool.QueryRow(ctx, `SELECT repository,lane,report_group_id::text,gh_pr_number FROM pr_verdicts WHERE id=$1`, id).Scan(&repository, &lane, &groupID, &pr); err != nil {
		return nil, err
	}
	window := time.Duration(v.ThresholdsUsed.WindowDays) * 24 * time.Hour
	since, until := v.ComputedAt.Add(-window), v.ComputedAt
	others := make([]struct {
		Class string `json:"class"`
		Title string `json:"title"`
	}, 0, len(v.Findings))
	for _, f := range v.Findings {
		if len(others) < 12 {
			others = append(others, struct {
				Class string `json:"class"`
				Title string `json:"title"`
			}{f.Class, truncate(f.FullTitle, 80)})
		}
	}
	packs := []EvidencePack{}
	for i, f := range v.Findings {
		if !adjudicable[f.Class] || f.IdentityID == "" {
			continue
		}
		if len(packs) >= maxEvidencePacks {
			break
		}
		var p EvidencePack
		p.Index = i
		p.Test.Title, p.Test.File, p.Test.Lane = f.FullTitle, f.File, lane
		p.Error = truncate(f.PR.ErrorExcerpt, 2500)
		var full string
		err := s.Evidence.Pool.QueryRow(ctx, `SELECT COALESCE(c.error_message,'') FROM test_cases c JOIN suites st ON st.id=c.suite_id JOIN reports r ON r.id=st.report_id
 WHERE r.report_group_id=$1 AND c.identity_id=$2 AND c.status IN ('failed','timedOut','interrupted') ORDER BY c.ordinal LIMIT 1`, groupID, f.IdentityID).Scan(&full)
		if err == nil && full != "" {
			p.Error = truncate(full, 2500)
		} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		p.Engine.Class, p.Engine.Reason, p.Engine.SignatureMatchesTrunkDominantFailure = f.Class, f.Reason, f.PR.SignatureMatch
		p.TrunkHistory.Runs, p.TrunkHistory.Fails, p.TrunkHistory.Flaky, p.TrunkHistory.ConsecutiveFailsAtHead = f.Trunk.Runs, f.Trunk.Fails, f.Trunk.Flaky, f.Trunk.ConsecutiveFails
		p.CrossPR.ID = "cross_pr"
		p.CrossPR.OtherPRsWhereThisTestFailed = []string{}
		rows, err := s.Evidence.Pool.Query(ctx, `SELECT o.gh_pr_number, o.status, left(o.commit_sha,7), to_char(o.observed_at,'MM-DD') FROM test_observations o
 WHERE o.identity_id=$1 AND o.lane=$2 AND o.branch_kind='pr' AND NOT o.is_infra_stub AND o.gh_pr_number IS NOT NULL AND o.gh_pr_number IS DISTINCT FROM $3
 AND o.observed_at>=$4 AND o.observed_at<=$5 ORDER BY o.observed_at DESC`, f.IdentityID, lane, pr, since, until)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var n int
			var status, sha, day string
			if err := rows.Scan(&n, &status, &sha, &day); err != nil {
				rows.Close()
				return nil, err
			}
			switch {
			case triage.IsFailure(status) && len(p.CrossPR.OtherPRsWhereThisTestFailed) < 12:
				p.CrossPR.OtherPRsWhereThisTestFailed = append(p.CrossPR.OtherPRsWhereThisTestFailed, fmt.Sprintf("PR %d (%s, %s)", n, sha, day))
			case status == triage.StatusPassed:
				p.CrossPR.OtherPRRunsWhereItPassed++
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		p.PR.Number, p.PR.Repository = pr, repository
		p.OtherFailuresInSameRun = others
		packs = append(packs, p)
	}
	return packs, nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// AdjudicatedFinding is the adjudicator's answer for one finding and the
// decision the matrix derived from it.
type AdjudicatedFinding struct {
	Index         int      `json:"index"`
	Class         string   `json:"class"`
	Cause         string   `json:"cause"`
	Confidence    float64  `json:"confidence"`
	CitedEvidence []string `json:"cited_evidence"`
	Explanation   string   `json:"explanation"`
	Decision      string   `json:"decision"` // engine | adjudicator_unblock | adjudicator_veto | unavailable
	Blocking      bool     `json:"blocking"`
}

// Adjudication is the second judge's record for a verdict.
type Adjudication struct {
	Model         string               `json:"model"`
	MinConfidence float64              `json:"min_confidence"`
	FinalVerdict  string               `json:"final_verdict"`
	Blocking      int                  `json:"blocking"`
	Exonerated    int                  `json:"exonerated"`
	Findings      []AdjudicatedFinding `json:"findings"`
	RecordedBy    string               `json:"recorded_by,omitempty"`
	RecordedAt    time.Time            `json:"recorded_at"`
}

var (
	validCause    = map[string]bool{"caused_by_pr": true, "flaky_environment": true, "bug_on_master": true, "test_bug": true}
	validDecision = map[string]bool{"engine": true, "adjudicator_unblock": true, "adjudicator_veto": true, "unavailable": true}
	validFinal    = map[string]bool{"SUCCESS": true, "FAILURE": true, "ACTION_REQUIRED": true, "NEUTRAL": true, "INCOMPLETE": true}
)

// Validate rejects malformed records; the producer is the only writer.
func (a Adjudication) Validate(findings int) error {
	if a.Model == "" || !validFinal[a.FinalVerdict] || a.MinConfidence < 0 || a.MinConfidence > 1 {
		return errors.New("invalid adjudication header")
	}
	for _, f := range a.Findings {
		if f.Index < 0 || f.Index >= findings || !validDecision[f.Decision] || f.Confidence < 0 || f.Confidence > 1 || (f.Decision != "unavailable" && !validCause[f.Cause]) {
			return fmt.Errorf("invalid adjudicated finding at index %d", f.Index)
		}
	}
	return nil
}

// RecordAdjudication stores the second judge's record next to the engine verdict.
func (s *Store) RecordAdjudication(ctx context.Context, id string, a Adjudication) error {
	raw, err := json.Marshal(a)
	if err != nil {
		return err
	}
	tag, err := s.Evidence.Pool.Exec(ctx, `UPDATE pr_verdicts SET adjudication=$2 WHERE id=$1`, id, raw)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}
