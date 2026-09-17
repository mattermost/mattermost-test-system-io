package identity

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type group struct {
	ID                                                                      uuid.UUID
	Repository, Framework, Name, Branch, Kind, Commit, RunAttempt, RunGroup string
	BaseRef, BaseSHA                                                        *string
	PR                                                                      *int
	Metadata                                                                json.RawMessage
	Created                                                                 time.Time
	Orchestrated                                                            bool
}
type observation struct {
	ID, ReportID, AttemptID        *uuid.UUID
	File, Title, FullTitle, Status string
	RetryCount                     int
	DurationMS                     int64
	Message, Stack                 string
	Observed                       time.Time
}

// lockGroup serializes enrichment of the same group, making index allocation
// and source deduplication safe across concurrent uploads, completes and backfills.
func lockGroup(ctx context.Context, tx pgx.Tx, id uuid.UUID) (group, error) {
	var g group
	err := tx.QueryRow(ctx, `SELECT id,repository,framework,name,branch,COALESCE(branch_kind,''),commit_sha,
 gh_run_attempt,COALESCE(run_group,''),base_ref,base_sha,gh_pr_number,environment_metadata,created_at,
 EXISTS(SELECT 1 FROM orchestration_runs r WHERE (r.repository,r.commit_sha,r.gh_run_id,r.name,r.gh_run_attempt)=
 (g.repository,g.commit_sha,g.gh_run_id,g.name,g.gh_run_attempt)) FROM report_groups g WHERE id=$1 FOR UPDATE`, id).Scan(
		&g.ID, &g.Repository, &g.Framework, &g.Name, &g.Branch, &g.Kind, &g.Commit, &g.RunAttempt, &g.RunGroup, &g.BaseRef, &g.BaseSHA, &g.PR, &g.Metadata, &g.Created, &g.Orchestrated)
	if err != nil {
		return g, err
	}
	// Serialize shared identity upserts per repository/framework, including backfills
	// spanning several sources. Group locks alone cannot prevent cross-group cycles.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, fmt.Sprintf("identity:%q/%q", g.Repository, g.Framework)); err != nil {
		return g, err
	}
	if g.Kind == "" {
		g.Kind = InferBranchKind(g.Branch, g.RunGroup+" "+g.Name, g.PR)
	}
	return g, err
}

// EnrichReport links cases and records upload-path facts in the caller's transaction.
// ASSUMPTION: orchestration attempts are authoritative for orchestrated groups;
// uploaded framework reports describe the same executions and only enrich case links.
func EnrichReport(ctx context.Context, tx pgx.Tx, reportID uuid.UUID) error {
	var groupID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT report_group_id FROM reports WHERE id=$1`, reportID).Scan(&groupID); err != nil {
		return err
	}
	g, err := lockGroup(ctx, tx, groupID)
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT c.id,COALESCE(s.file,''),c.title,c.full_title,c.status,c.retry_count,
 COALESCE(c.duration_ms,0),COALESCE(c.error_message,''),COALESCE(c.error_stack,''),COALESCE(s.start_time,r.start_time,r.created_at)
 FROM test_cases c JOIN suites s ON s.id=c.suite_id JOIN reports r ON r.id=s.report_id
 WHERE r.id=$1 ORDER BY s.ordinal,c.ordinal,c.id`, reportID)
	if err != nil {
		return err
	}
	var cases []observation
	for rows.Next() {
		var o observation
		var id uuid.UUID
		o.ReportID = &reportID
		if err := rows.Scan(&id, &o.File, &o.Title, &o.FullTitle, &o.Status, &o.RetryCount, &o.DurationMS, &o.Message, &o.Stack, &o.Observed); err != nil {
			rows.Close()
			return err
		}
		o.ID = &id
		cases = append(cases, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	return persistBatch(ctx, tx, g, cases, !g.Orchestrated)
}

// EnrichLease records completed attempt facts inside the completion transaction.
func EnrichLease(ctx context.Context, tx pgx.Tx, leaseID uuid.UUID) error {
	var groupID uuid.UUID
	err := tx.QueryRow(ctx, `SELECT g.id FROM leases l JOIN orchestration_runs r ON r.id=l.run_id JOIN report_groups g
 ON (g.repository,g.commit_sha,g.gh_run_id,g.name,g.gh_run_attempt)=(r.repository,r.commit_sha,r.gh_run_id,r.name,r.gh_run_attempt)
 WHERE l.id=$1`, leaseID).Scan(&groupID)
	if err != nil {
		return err
	}
	g, err := lockGroup(ctx, tx, groupID)
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT id,spec_path,test_cases,reported_at FROM attempts WHERE lease_id=$1 AND reported_at IS NOT NULL ORDER BY spec_path,id`, leaseID)
	if err != nil {
		return err
	}
	var cases []observation
	for rows.Next() {
		var id uuid.UUID
		var file string
		var data []byte
		var at time.Time
		if err := rows.Scan(&id, &file, &data, &at); err != nil {
			rows.Close()
			return err
		}
		var decoded []struct {
			Title     string `json:"title"`
			FullTitle string `json:"full_title"`
			Status    string `json:"status"`
			Retry     int    `json:"retry_count"`
			Duration  int64  `json:"duration_ms"`
			Message   string `json:"error_message"`
			Stack     string `json:"error_stack"`
		}
		if len(data) > 0 {
			if err := json.Unmarshal(data, &decoded); err != nil {
				rows.Close()
				return fmt.Errorf("decode attempt cases: %w", err)
			}
		}
		for _, c := range decoded {
			cases = append(cases, observation{AttemptID: &id, File: file, Title: c.Title, FullTitle: c.FullTitle, Status: c.Status, RetryCount: c.Retry, DurationMS: c.Duration, Message: c.Message, Stack: c.Stack, Observed: at})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	return persistBatch(ctx, tx, g, collapseAttemptRetries(g, cases), true)
}

// persistBatch upserts every identity of the batch in one statement, then
// links cases and appends facts with two statements per case. The batch is
// deduplicated by stable key first because a single INSERT ... ON CONFLICT
// cannot touch the same row twice. Everything runs under the group lock taken
// by lockGroup, so the attempt_index subquery is race-free.
func persistBatch(ctx context.Context, tx pgx.Tx, g group, cases []observation, writeFact bool) error {
	if len(cases) == 0 {
		return nil
	}
	type ident struct {
		key                 []byte
		file, title, mmTID  string
		firstSeen, lastSeen time.Time
	}
	order := []string{}
	idents := map[string]*ident{}
	keys := make([]string, len(cases))
	for i, o := range cases {
		rawTitle := o.FullTitle
		if rawTitle == "" {
			rawTitle = o.Title
		}
		key := StableKey(g.Repository, g.Framework, o.File, rawTitle)
		keys[i] = string(key)
		if d, ok := idents[keys[i]]; ok {
			if o.Observed.Before(d.firstSeen) {
				d.firstSeen = o.Observed
			}
			if o.Observed.After(d.lastSeen) {
				d.lastSeen = o.Observed
			}
			continue
		}
		idents[keys[i]] = &ident{key: key, file: NormalizeFile(g.Repository, o.File), title: NormalizeTitle(g.Framework, rawTitle), mmTID: MMTID(o.Title), firstSeen: o.Observed, lastSeen: o.Observed}
		order = append(order, keys[i])
	}
	var sb strings.Builder
	args := make([]any, 0, len(order)*7+2)
	args = append(args, g.Repository, g.Framework)
	for i, k := range order {
		d := idents[k]
		if i > 0 {
			sb.WriteString(",")
		}
		n := len(args)
		fmt.Fprintf(&sb, "($1,$2,$%d,$%d,$%d,NULLIF($%d,''),$%d,$%d)", n+1, n+2, n+3, n+4, n+5, n+6)
		args = append(args, d.key, d.file, d.title, d.mmTID, d.firstSeen, d.lastSeen)
	}
	rows, err := tx.Query(ctx, `INSERT INTO test_identities(repository,framework,stable_key,normalized_file,normalized_title,mm_t_id,first_seen_at,last_seen_at) VALUES `+sb.String()+`
 ON CONFLICT(stable_key) DO UPDATE SET first_seen_at=LEAST(test_identities.first_seen_at,EXCLUDED.first_seen_at),last_seen_at=GREATEST(test_identities.last_seen_at,EXCLUDED.last_seen_at)
 RETURNING id, stable_key`, args...)
	if err != nil {
		return err
	}
	ids := map[string]uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		var key []byte
		if err := rows.Scan(&id, &key); err != nil {
			rows.Close()
			return err
		}
		ids[string(key)] = id
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	lane := InferLane(g.Name, g.Metadata)
	runAttempt, err := strconv.Atoi(g.RunAttempt)
	if err != nil || runAttempt < 1 {
		runAttempt = 1
	}
	for i, o := range cases {
		identityID, ok := ids[keys[i]]
		if !ok {
			return fmt.Errorf("identity upsert returned no id for %q", o.FullTitle)
		}
		file := idents[keys[i]].file
		sig, excerpt := ErrorSignature(o.Message)
		failureLocus := FailureLocus(o.Stack + "\n" + o.Message)
		if o.ID != nil {
			if _, err := tx.Exec(ctx, `UPDATE test_cases SET identity_id=$2,error_signature=$3,failure_locus=NULLIF($4,'') WHERE id=$1`, o.ID, identityID, sig, failureLocus); err != nil {
				return err
			}
		}
		if !writeFact {
			continue
		}
		status := o.Status
		if status == "timedOut" {
			status = "failed"
		}
		// Replay-safe: the same immutable source (test_case_id / attempt_id)
		// never produces a second fact; attempt_index continues the sequence.
		if _, err := tx.Exec(ctx, `INSERT INTO test_observations(identity_id,report_group_id,report_id,test_case_id,attempt_id,branch_kind,branch,base_ref,base_sha,
 commit_sha,gh_pr_number,gh_run_attempt,attempt_index,lane,status,retry_count,duration_ms,error_signature,error_excerpt,failure_locus,is_infra_stub,observed_at)
 SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,
   COALESCE((SELECT MAX(attempt_index)+1 FROM test_observations WHERE identity_id=$1 AND report_group_id=$2 AND lane=$13),0),
   $13,$14,$15,$16,$17,NULLIF($18,''),NULLIF($19,''),$20,$21
 WHERE NOT EXISTS (SELECT 1 FROM test_observations WHERE identity_id=$1 AND report_group_id=$2 AND lane=$13 AND
   (($4::uuid IS NOT NULL AND test_case_id=$4) OR ($5::uuid IS NOT NULL AND attempt_id=$5)))`,
			identityID, g.ID, o.ReportID, o.ID, o.AttemptID, g.Kind, g.Branch, g.BaseRef, g.BaseSHA, g.Commit, g.PR, runAttempt, lane, status, o.RetryCount, o.DurationMS, sig, excerpt, failureLocus, IsInfraStub(file, o.Title), o.Observed); err != nil {
			return err
		}
	}
	return nil
}

// Backfill processes one source transaction at a time, ordered chronologically.
// Filters are group-level and rerunning the command leaves existing facts unchanged.
func Backfill(ctx context.Context, pool *pgxpool.Pool, repository string, since time.Time) (int, error) {
	rows, err := pool.Query(ctx, `SELECT g.id FROM report_groups g WHERE ($1='' OR repository=$1) AND created_at >= $2 ORDER BY created_at,id`, repository, since)
	if err != nil {
		return 0, err
	}
	var groups []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		groups = append(groups, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	count := 0
	for _, id := range groups {
		err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			// Lock before enumerating source rows to serialize concurrent backfill workers.
			if _, err := lockGroup(ctx, tx, id); err != nil {
				return err
			}
			leases, err := tx.Query(ctx, `SELECT l.id FROM leases l JOIN orchestration_runs r ON r.id=l.run_id JOIN report_groups g ON
 (g.repository,g.commit_sha,g.gh_run_id,g.name,g.gh_run_attempt)=(r.repository,r.commit_sha,r.gh_run_id,r.name,r.gh_run_attempt)
 WHERE g.id=$1 ORDER BY l.issued_at,l.id`, id)
			if err != nil {
				return err
			}
			var lids []uuid.UUID
			for leases.Next() {
				var lid uuid.UUID
				if err := leases.Scan(&lid); err != nil {
					leases.Close()
					return err
				}
				lids = append(lids, lid)
			}
			leases.Close()
			if err := leases.Err(); err != nil {
				return err
			}
			for _, lid := range lids {
				if err := EnrichLease(ctx, tx, lid); err != nil {
					return err
				}
			}
			reports, err := tx.Query(ctx, `SELECT id FROM reports WHERE report_group_id=$1 ORDER BY created_at,id`, id)
			if err != nil {
				return err
			}
			var rids []uuid.UUID
			for reports.Next() {
				var rid uuid.UUID
				if err := reports.Scan(&rid); err != nil {
					reports.Close()
					return err
				}
				rids = append(rids, rid)
			}
			reports.Close()
			if err := reports.Err(); err != nil {
				return err
			}
			for _, rid := range rids {
				if err := EnrichReport(ctx, tx, rid); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return count, fmt.Errorf("backfill group %s: %w", id, err)
		}
		count++
	}
	return count, nil
}
