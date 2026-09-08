package triagework

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
var imagePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]*@sha256:[a-f0-9]{64}$`)
var commitPattern = regexp.MustCompile(`^[a-fA-F0-9]{40}$`)

type statusError struct {
	status  int
	message string
}

func (e *statusError) Error() string { return e.message }
func bad(message string) error       { return &statusError{http.StatusBadRequest, message} }
func conflict(message string) error  { return &statusError{http.StatusConflict, message} }

func write(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (h *Handlers) fail(w http.ResponseWriter, err error) {
	var se *statusError
	switch {
	case errors.As(err, &se):
		write(w, se.status, map[string]string{"error": se.message})
	case errors.Is(err, pgx.ErrNoRows):
		write(w, http.StatusNotFound, map[string]string{"error": "triage item not found"})
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		write(w, http.StatusGatewayTimeout, map[string]string{"error": "triage operation interrupted"})
	default:
		if h.Logger != nil {
			h.Logger.Error("triage operation failed", "error", err)
		}
		write(w, http.StatusInternalServerError, map[string]string{"error": "triage operation failed"})
	}
}

func bounded(s string, maximum int) bool {
	return strings.TrimSpace(s) != "" && len(s) <= maximum && !strings.ContainsRune(s, '\x00')
}
func validRepository(s string) bool { return len(s) <= 200 && repositoryPattern.MatchString(s) }
func decode(w http.ResponseWriter, r *http.Request, body any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 32768)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(body); err != nil {
		return bad("invalid JSON body")
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return bad("body must contain one JSON object")
	}
	return nil
}

func actor(r *http.Request) (string, error) {
	a := r.Header.Get("X-TSIO-Triage-Actor")
	if !bounded(a, 1000) {
		return "", &statusError{http.StatusUnauthorized, "trusted triage actor required"}
	}
	return a, nil
}
func scope(r *http.Request, repository string) error {
	if allowed := r.Header.Get("X-TSIO-Triage-Repository"); allowed != "" && allowed != repository {
		return &statusError{http.StatusForbidden, "triage credential does not authorize this repository"}
	}
	return nil
}
func itemID(r *http.Request) (string, error) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		return "", bad("invalid repair id")
	}
	return id, nil
}
func listParams(r *http.Request) (string, int, error) {
	q := r.URL.Query()
	for key, values := range q {
		if (key != "repository" && key != "limit") || len(values) != 1 {
			return "", 0, bad("unsupported or repeated query parameter")
		}
	}
	repo := q.Get("repository")
	if !validRepository(repo) {
		return "", 0, bad("repository must be an owner/repo slug")
	}
	limit := 100
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 200 {
			return "", 0, bad("limit must be between 1 and 200")
		}
		limit = n
	}
	return repo, limit, nil
}

func webURL(s string) bool {
	if len(s) > 2048 {
		return false
	}
	u, e := url.Parse(s)
	return e == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Fragment == ""
}
func repairURL(s, repo string) bool {
	if !webURL(s) {
		return false
	}
	u, _ := url.Parse(s)
	prefix := "/" + repo + "/pull/"
	if u.Host != "github.com" || !strings.HasPrefix(u.Path, prefix) || u.RawQuery != "" {
		return false
	}
	n, e := strconv.ParseUint(strings.TrimPrefix(u.Path, prefix), 10, 64)
	return e == nil && n > 0
}

func (h *Handlers) ttl() time.Duration {
	if h.LeaseTTL > 0 && h.LeaseTTL <= time.Hour {
		return h.LeaseTTL
	}
	return 15 * time.Minute
}

const itemColumns = `r.id::text, r.evidence, r.owner, r.ticket, r.state, r.attempt,
 COALESCE(r.lease_token::text,''), r.lease_expires_at, r.created_at, COALESCE(r.worker,''),
 NOT EXISTS (SELECT 1 FROM triage_quarantines q WHERE q.repair_id=r.id AND q.expires_at>clock_timestamp()),
 (SELECT count(DISTINCT g.gh_pr_number) FROM report_groups g
 WHERE g.repository=r.repository AND g.framework=r.framework
 AND g.name=CASE r.name
 WHEN 'cypress-full-enterprise-master' THEN 'cypress-full-enterprise'
 WHEN 'cypress-full-fips-master' THEN 'cypress-full-fips'
 WHEN 'playwright-full-enterprise-master' THEN 'playwright-full-enterprise'
 WHEN 'playwright-full-fips-master' THEN 'playwright-full-fips' END
 AND g.gh_pr_number>0 AND g.status='completed' AND g.total_reports_expected>0
 AND (SELECT count(*)=g.total_reports_expected AND bool_and(rp.status='complete') FROM reports rp WHERE rp.report_group_id=g.id)
 AND g.created_at>=clock_timestamp()-interval '30 days'
 AND (SELECT bool_or(COALESCE(t.run_failed,t.status IN ('failed','timedOut','interrupted')))
 AND NOT bool_or(t.status IN ('passed','flaky'))
 AND count(DISTINCT (COALESCE(t.file,s.file,''),t.full_title,COALESCE(t.project,'')))=1
 AND bool_and(COALESCE(t.file,s.file,'')=r.evidence->>'file' AND t.full_title=r.evidence->>'full_title' AND COALESCE(t.project,'')=r.evidence->>'project')
 FROM reports rp JOIN suites s ON s.report_id=rp.id JOIN test_cases t ON t.suite_id=s.id
 WHERE rp.report_group_id=g.id AND t.stable_key=r.stable_key
 )) AS observed_distinct_prs_failed,
 COALESCE((SELECT jsonb_agg(jsonb_build_object('attempt',a.attempt,'worker',a.worker,'author',a.author,
 'outcome',a.outcome,'account',a.account,'evidence_url',a.evidence_url,'pr_url',a.pr_url,'created_at',a.created_at) ORDER BY a.attempt)
 FROM triage_repair_attempts a WHERE a.repair_id=r.id),'[]'::jsonb)`

func scanItem(row pgx.Row) (*Item, error) {
	item := &Item{}
	var evidence, attempts []byte
	err := row.Scan(&item.ID, &evidence, &item.Owner, &item.Ticket, &item.State, &item.Attempt, &item.LeaseToken, &item.LeaseExpiresAt, &item.CreatedAt, &item.worker, &item.BlockingEligible, &item.ObservedDistinctPRsFailed, &attempts)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(evidence, &item.Evidence); err != nil {
		return nil, err
	}
	if err = json.Unmarshal(attempts, &item.Attempts); err != nil {
		return nil, err
	}
	return item, nil
}
func lockedItem(ctx context.Context, tx pgx.Tx, id string) (*Item, error) {
	return scanItem(tx.QueryRow(ctx, `SELECT `+itemColumns+` FROM triage_repairs r WHERE r.id=$1 FOR UPDATE OF r`, id))
}

// ListRepairs returns bounded work and public attempt history without lease tokens.
func (h *Handlers) ListRepairs(w http.ResponseWriter, r *http.Request) {
	repo, limit, err := listParams(r)
	if err != nil {
		h.fail(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	rows, err := h.Pool.Query(ctx, `SELECT `+itemColumns+` FROM triage_repairs r WHERE r.repository=$1 ORDER BY observed_distinct_prs_failed DESC,r.created_at,r.id LIMIT $2`, repo, limit+1)
	if err != nil {
		h.fail(w, err)
		return
	}
	defer rows.Close()
	items := []*Item{}
	for rows.Next() {
		item, e := scanItem(rows)
		if e != nil {
			h.fail(w, e)
			return
		}
		item.LeaseToken = ""
		item.LeaseExpiresAt = nil
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		h.fail(w, err)
		return
	}
	truncated := len(items) > limit
	if truncated {
		items = items[:limit]
	}
	write(w, http.StatusOK, map[string]any{"items": items, "truncated": truncated})
}

// Enqueue takes identities only: spec, suite, commit and pullable image all come
// from the fully ingested master report, authenticated by stored verified OIDC.
func (h *Handlers) Enqueue(w http.ResponseWriter, r *http.Request) {
	author, err := actor(r)
	if err != nil {
		h.fail(w, err)
		return
	}
	var in struct {
		ReportGroupID string `json:"report_group_id"`
		StableKey     string `json:"stable_key"`
		Owner         string `json:"owner"`
		Ticket        string `json:"ticket"`
	}
	if err = decode(w, r, &in); err != nil {
		h.fail(w, err)
		return
	}
	if _, e := uuid.Parse(in.ReportGroupID); e != nil {
		h.fail(w, bad("invalid report_group_id"))
		return
	}
	if !bounded(in.StableKey, 2000) || !bounded(in.Owner, 200) || (in.Ticket != "" && !bounded(in.Ticket, 1000)) {
		h.fail(w, bad("stable_key and owner are required; optional ticket must be bounded"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		h.fail(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	refs := h.SourceWorkflowRefs
	if len(refs) == 0 {
		refs = []string{"mattermost/mattermost/.github/workflows/e2e-tests-on-merge.yml@refs/heads/master"}
	}
	ev, err := loadEvidence(ctx, tx, in.ReportGroupID, in.StableKey, refs)
	if err != nil {
		h.fail(w, err)
		return
	}
	if err = scope(r, ev.Repository); err != nil {
		h.fail(w, err)
		return
	}
	item, err := enqueueWork(ctx, tx, ev, in.Owner, in.Ticket, author)
	if err != nil {
		h.fail(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		h.fail(w, err)
		return
	}
	if item.State != stateProductSuspect {
		item.LeaseToken = ""
	}
	item.LeaseExpiresAt = nil
	write(w, http.StatusOK, map[string]any{"item": item})
}

func loadEvidence(ctx context.Context, tx pgx.Tx, groupID, key string, sourceRefs []string) (*Evidence, error) {
	ev := &Evidence{ReportGroupID: groupID, StableKey: key}
	var status string
	var pr *int
	var expected int
	err := tx.QueryRow(ctx, `SELECT repository,framework,name,branch,commit_sha,gh_run_id,gh_run_attempt,status,gh_pr_number,COALESCE(total_reports_expected,0) FROM report_groups WHERE id=$1 FOR SHARE`, groupID).Scan(&ev.Repository, &ev.Framework, &ev.Name, &ev.Branch, &ev.CommitSHA, &ev.GHRunID, &ev.GHRunAttempt, &status, &pr, &expected)
	if err != nil {
		return nil, err
	}
	if !validRepository(ev.Repository) || ev.Branch != "master" || pr != nil || status != "completed" || expected < 1 || (ev.Framework != "playwright" && ev.Framework != "cypress") || !commitPattern.MatchString(ev.CommitSHA) {
		return nil, conflict("repair requires a complete master Playwright/Cypress report with no PR identity")
	}
	var count int
	var trusted bool
	err = tx.QueryRow(ctx, `SELECT count(*),COALESCE(bool_and(COALESCE(r.status='complete'
 AND COALESCE(r.upload_principal,'')<>''
 AND r.registration_receipt->>'repository'=$2 AND r.registration_receipt->>'commit'=$3
 AND r.registration_receipt->>'gh_run_id'=$4 AND r.registration_receipt->>'gh_run_attempt'=$5
 AND (r.registration_receipt->'total_reports_expected'=to_jsonb($6::integer)
 OR (COALESCE(r.registration_receipt->>'total_reports_expected','')='' AND EXISTS(
 SELECT 1 FROM report_group_begin_receipts b WHERE b.report_group_id=r.report_group_id
 AND b.receipt->>'repository'=$2 AND b.receipt->>'commit'=$3
 AND b.receipt->>'gh_run_id'=$4 AND b.receipt->>'gh_run_attempt'=$5
 AND b.receipt->'total_reports_expected'=to_jsonb($6::integer)
 AND b.receipt->>'framework'=$7 AND b.receipt->>'name'=$8
 AND b.verified_claims->>'iss'='https://token.actions.githubusercontent.com'
 AND b.verified_claims->>'repository'=$2 AND b.verified_claims->>'ref'='refs/heads/master'
 AND b.verified_claims->>'sha'=$3 AND b.verified_claims->>'run_id'=$4 AND b.verified_claims->>'run_attempt'=$5
 AND b.verified_claims->>'workflow_ref'=ANY($9::text[])
 AND b.verified_claims->>'event_name' IN ('push','schedule','workflow_dispatch'))))
 AND r.registration_receipt->>'framework'=$7 AND r.registration_receipt->>'name'=$8
 AND r.registration_receipt->>'branch'='master' AND COALESCE(r.registration_receipt->>'gh_pr_number','')=''
 AND EXISTS(
 SELECT 1 FROM oidc_claims o WHERE o.report_id=r.id AND o.issuer='https://token.actions.githubusercontent.com'
 AND o.repository=$2 AND o.ref='refs/heads/master' AND o.raw_claims->>'sha'=$3
 AND o.raw_claims->>'run_id'=$4 AND o.raw_claims->>'run_attempt'=$5
 AND o.raw_claims->>'workflow_ref'=ANY($9::text[])
 AND COALESCE(o.raw_claims->>'event_name','') IN ('push','schedule','workflow_dispatch')
 ),false)),false) FROM reports r WHERE r.report_group_id=$1`, groupID, ev.Repository, ev.CommitSHA, ev.GHRunID, ev.GHRunAttempt, expected, ev.Framework, ev.Name, sourceRefs).Scan(&count, &trusted)
	if err != nil {
		return nil, err
	}
	if count != expected || !trusted {
		return nil, conflict("all declared master shards must be complete and carry matching verified GitHub provenance")
	}
	rows, err := tx.Query(ctx, `SELECT DISTINCT COALESCE(t.file,s.file,''),t.full_title,COALESCE(t.project,'')
 FROM test_cases t JOIN suites s ON s.id=t.suite_id JOIN reports r ON r.id=s.report_id WHERE r.report_group_id=$1 AND t.stable_key=$2`, groupID, key)
	if err != nil {
		return nil, err
	}
	identities := 0
	for rows.Next() {
		identities++
		if err = rows.Scan(&ev.File, &ev.FullTitle, &ev.Project); err != nil {
			rows.Close()
			return nil, err
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if identities != 1 {
		return nil, conflict("stable_key must identify exactly one file, full title and project")
	}
	if !bounded(ev.File, 2000) || strings.HasPrefix(ev.File, "/") || strings.Contains(ev.File, "\\") || strings.Contains("/"+ev.File+"/", "/../") || !bounded(ev.FullTitle, 4000) {
		return nil, conflict("master report has no unambiguous relative test spec")
	}
	var failed bool
	err = tx.QueryRow(ctx, `SELECT COALESCE(bool_or(t.status IN ('failed','flaky','timedOut','interrupted') OR COALESCE(t.attempts_failed,0)>0),false) FROM test_cases t JOIN suites s ON s.id=t.suite_id JOIN reports r ON r.id=s.report_id WHERE r.report_group_id=$1 AND t.stable_key=$2`, groupID, key).Scan(&failed)
	if err != nil {
		return nil, err
	}
	if !failed {
		return nil, conflict("test has no observed failure in the selected master report")
	}
	if err = loadTrustedMetadata(ctx, tx, ev); err != nil {
		return nil, err
	}
	return ev, nil
}

func tokenValid(item *Item, token string) bool {
	return item.LeaseToken != "" && item.LeaseToken == token
}
func requireToken(token string) error {
	if _, err := uuid.Parse(token); err != nil {
		return bad("invalid lease_token")
	}
	return nil
}
func checkUpdate(n int64, err error) error {
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("triage update affected %d rows", n)
	}
	return nil
}
