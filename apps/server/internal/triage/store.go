package triage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store reads facts shared by the health and verdict projections.
type Store struct {
	Pool     *pgxpool.Pool
	Defaults Thresholds
}

// DefaultPolicy returns the configured fallback; unconfigured contexts are shadow.
func (s *Store) DefaultPolicy(repository, contextName string) Policy {
	t := s.Defaults
	if t.WindowDays == 0 {
		t = DefaultThresholds()
	}
	return Policy{Repository: repository, Context: contextName, Mode: "shadow", Thresholds: t}
}

// LoadPolicy overlays a stored context policy on server defaults.
func (s *Store) LoadPolicy(ctx context.Context, repository, contextName string) (Policy, error) {
	p := s.DefaultPolicy(repository, contextName)
	var raw json.RawMessage
	err := s.Pool.QueryRow(ctx, `SELECT mode,thresholds,COALESCE(updated_by,''),updated_at FROM triage_policies WHERE repository=$1 AND context=$2`, repository, contextName).Scan(&p.Mode, &raw, &p.UpdatedBy, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return Policy{}, err
	}
	p.Thresholds, err = MergeThresholds(p.Thresholds, raw)
	return p, err
}

// GroupByIdentity resolves the immutable composite identity without guessing a run.
func (s *Store) GroupByIdentity(ctx context.Context, repository, sha, runID, name, attempt string) (Group, error) {
	var g Group
	err := s.Pool.QueryRow(ctx, `SELECT id::text,repository,framework,name,commit_sha,gh_run_id,gh_run_attempt,gh_pr_number,branch,COALESCE(branch_kind,''),COALESCE(base_ref,''),COALESCE(base_sha,''),COALESCE(environment_metadata->>'lane',''),status,created_at FROM report_groups WHERE repository=$1 AND commit_sha=$2 AND gh_run_id=$3 AND name=$4 AND gh_run_attempt=$5`, repository, sha, runID, name, attempt).Scan(&g.ID, &g.Repository, &g.Framework, &g.Name, &g.CommitSHA, &g.GHRunID, &g.GHRunAttempt, &g.GHPRNumber, &g.Branch, &g.BranchKind, &g.BaseRef, &g.BaseSHA, &g.Lane, &g.Status, &g.CreatedAt)
	return g, err
}

// ObservationFilter selects a bounded page or projection input. Cursor is a UUID.
type ObservationFilter struct {
	Repository, Framework, Lane, BaseRef, BranchKind, GroupID, IdentityID, Cursor string
	Since                                                                         time.Time
	Until                                                                         time.Time
	Limit                                                                         int
	MaxRunsPerIdentity                                                            int
	MatchLane                                                                     bool
	CompletedOnly                                                                 bool
}

// Observations returns newest-first facts with stable UUID pagination.
func (s *Store) Observations(ctx context.Context, f ObservationFilter) ([]Observation, error) {
	where := []string{"TRUE"}
	args := []any{}
	add := func(expr string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(expr, len(args)))
	}
	if f.Repository != "" {
		add("i.repository=$%d", f.Repository)
	}
	if f.Framework != "" {
		add("i.framework=$%d", f.Framework)
	}
	if f.MatchLane {
		add("o.lane=$%d", f.Lane)
	}
	if f.BaseRef != "" {
		add("o.branch=$%d", f.BaseRef)
	}
	if f.BranchKind != "" {
		add("o.branch_kind=$%d", f.BranchKind)
	}
	if f.GroupID != "" {
		add("o.report_group_id=$%d", f.GroupID)
	}
	if f.IdentityID != "" {
		add("o.identity_id=$%d", f.IdentityID)
	}
	if !f.Since.IsZero() {
		add("o.observed_at >= $%d", f.Since)
	}
	if !f.Until.IsZero() {
		add("o.observed_at <= $%d", f.Until)
	}
	if f.CompletedOnly {
		where = append(where, "g.status='completed'")
	}
	prefix := ""
	if f.MaxRunsPerIdentity > 0 {
		// Bound projection input before paginating observations. Rank distinct
		// groups rather than executions so retries cannot consume the run budget.
		prefix = `WITH ranked_groups AS (SELECT o.identity_id,o.report_group_id,
 row_number() OVER (PARTITION BY o.identity_id ORDER BY g.created_at DESC,o.report_group_id DESC) AS position
 FROM test_observations o JOIN test_identities i ON i.id=o.identity_id JOIN report_groups g ON g.id=o.report_group_id
 WHERE ` + strings.Join(where, " AND ") + ` GROUP BY o.identity_id,o.report_group_id,g.created_at) `
		add("(o.identity_id,o.report_group_id) IN (SELECT identity_id,report_group_id FROM ranked_groups WHERE position<=$%d)", f.MaxRunsPerIdentity)
	}
	if f.Cursor != "" {
		add("o.id<$%d::uuid", f.Cursor)
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	args = append(args, limit)
	q := prefix + `SELECT row_to_json(x) FROM (SELECT o.id,o.identity_id,o.report_group_id,COALESCE(o.test_case_id::text,'') test_case_id,COALESCE(o.attempt_id::text,'') attempt_id,i.repository,i.framework,i.normalized_file AS file,i.normalized_title AS full_title,COALESCE(i.mm_t_id,'') mm_t_id,encode(i.stable_key,'hex') stable_key,i.first_seen_at,i.last_seen_at,o.branch_kind,o.branch,COALESCE(o.base_ref,'') base_ref,COALESCE(o.base_sha,'') base_sha,o.commit_sha,o.lane,o.status,o.attempt_index,o.retry_count,COALESCE(o.duration_ms,0) duration_ms,COALESCE(encode(o.error_signature,'hex'),'') error_signature,COALESCE(o.error_excerpt,'') error_excerpt,COALESCE(o.failure_locus,'') failure_locus,o.is_infra_stub,o.observed_at,g.created_at AS group_created_at FROM test_observations o JOIN test_identities i ON i.id=o.identity_id JOIN report_groups g ON g.id=o.report_group_id WHERE ` + strings.Join(where, " AND ") + fmt.Sprintf(" ORDER BY o.id DESC LIMIT $%d) x", len(args))
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Observation{}
	for rows.Next() {
		var raw []byte
		var o Observation
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &o); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// AllObservations streams bounded SQL pages into the projection snapshot.
func (s *Store) AllObservations(ctx context.Context, f ObservationFilter) ([]Observation, error) {
	out := []Observation{}
	// Large pages: the MaxRunsPerIdentity ranking CTE is re-evaluated on every
	// page, and a webapp lane has ~36k trunk observations in a window. 1,000-row
	// pages made one verdict cost ~36 CTE scans (about two minutes).
	f.Limit = 20000
	for {
		page, err := s.Observations(ctx, f)
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if len(page) < f.Limit {
			return out, nil
		}
		f.Cursor = page[len(page)-1].ID
	}
}

// ActiveQuarantine reads only live entries; expired rows never suppress a test.
func (s *Store) ActiveQuarantine(ctx context.Context, repository, framework, baseRef, lane string, now time.Time) ([]Quarantine, error) {
	// created_at<=now keeps `as_of` replays honest: an entry opened after the
	// replayed instant did not exist for that PR run.
	rows, err := s.Pool.Query(ctx, `SELECT row_to_json(q) FROM quarantine_entries q JOIN test_identities i ON i.id=q.identity_id WHERE q.status='active' AND q.created_at<=$5 AND (q.expires_at IS NULL OR q.expires_at>$5) AND ($1='' OR i.repository=$1) AND ($2='' OR i.framework=$2) AND ($3='' OR q.base_ref=$3) AND (q.lane IS NULL OR q.lane=$4) ORDER BY q.created_at,q.id`, repository, framework, baseRef, lane, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Quarantine{}
	for rows.Next() {
		var raw []byte
		var q Quarantine
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &q); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// SeenOnTrunk distinguishes new tests from histories that aged out or live in another lane.
func (s *Store) SeenOnTrunk(ctx context.Context, observations []Observation) (map[string]bool, error) {
	identities := make([]string, 0, len(observations))
	for _, o := range observations {
		identities = append(identities, o.IdentityID)
	}
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT o.identity_id::text FROM test_observations o JOIN report_groups g ON g.id=o.report_group_id
 WHERE o.identity_id=ANY($1::uuid[]) AND o.branch_kind='trunk' AND NOT o.is_infra_stub AND g.status='completed'`, identities)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		seen[id] = true
	}
	return seen, rows.Err()
}
