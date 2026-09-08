//go:build e2e

package triageassessment

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/db"
)

type fixture struct {
	pool *pgxpool.Pool
	h    *Handlers
	seq  int
	now  time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, "postgres:18.3", tcpostgres.WithDatabase("tsio"), tcpostgres.WithUsername("tsio"), tcpostgres.WithPassword("tsio"), tcpostgres.BasicWaitStrategies())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = container.Terminate(cleanup)
	})
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(dsn); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return &fixture{pool: pool, h: &Handlers{Pool: pool}, now: time.Now().UTC().Truncate(time.Second)}
}

func (f *fixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(), query, args...); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) group(t *testing.T, repo, name, branch string, age time.Duration) (string, Selector) {
	t.Helper()
	f.seq++
	s := Selector{Repository: repo, CommitSHA: fmt.Sprintf("%040x", f.seq), GHRunID: strconv.Itoa(f.seq), GHRunAttempt: "1", Name: name}
	var id string
	err := f.pool.QueryRow(context.Background(), `INSERT INTO report_groups (repository,commit_sha,gh_run_id,gh_run_attempt,name,branch,framework,status,total_reports_expected,created_at,updated_at)
 VALUES ($1,$2,$3,$4,$5,$6,'cypress','completed',1,$7,$7) RETURNING id::text`, s.Repository, s.CommitSHA, s.GHRunID, s.GHRunAttempt, s.Name, branch, f.now.Add(-age)).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id, s
}

func (f *fixture) report(t *testing.T, group string, tests ...observation) string {
	t.Helper()
	var report, suite string
	failed := 0
	for _, test := range tests {
		if test.failed {
			failed++
		}
	}
	err := f.pool.QueryRow(context.Background(), `INSERT INTO reports (report_group_id,name,status,total_cases,failed_cases,passed_cases) VALUES ($1,'tests','complete',$2,$3,$4) RETURNING id::text`, group, len(tests), failed, len(tests)-failed).Scan(&report)
	if err != nil {
		t.Fatal(err)
	}
	err = f.pool.QueryRow(context.Background(), `INSERT INTO suites (report_id,title,ordinal) VALUES ($1,'suite',0) RETURNING id::text`, report).Scan(&suite)
	if err != nil {
		t.Fatal(err)
	}
	for i, test := range tests {
		status := "passed"
		if test.failed {
			status = "failed"
		}
		f.exec(t, `INSERT INTO test_cases (suite_id,title,full_title,status,ordinal,file,project,external_test_id) VALUES ($1,$2,$2,$3,$4,$5,$6,$7)`, suite, test.title, status, i, test.file, test.project, test.key)
	}
	return report
}

func testObservation(key string, failed bool) observation {
	return observation{identity: identity{key: key, file: key + ".cy.ts", title: "suite " + key, framework: "cypress"}, failed: failed, passed: !failed}
}

func (f *fixture) baseline(t *testing.T, repo, name string, tests ...observation) {
	t.Helper()
	for i := range 3 {
		id, _ := f.group(t, repo, name, "master", time.Duration(i+1)*time.Hour)
		f.report(t, id, tests...)
	}
}

func (f *fixture) assess(t *testing.T, s Selector) Assessment {
	t.Helper()
	a, err := f.h.evaluate(context.Background(), s, "")
	if err != nil {
		t.Fatal(err)
	}
	if a.CanUnblock {
		t.Fatalf("assessment authorized a check: %+v", a)
	}
	return a
}

func TestPostgresAssessment(t *testing.T) {
	f := newFixture(t)
	const repo = "mattermost/mattermost"
	t.Run("all failure keys and deterministic precedence", func(t *testing.T) {
		const name = "all-failures"
		f.baseline(t, repo, name, testObservation("MM-T1", true), testObservation("MM-T2", false))
		id, s := f.group(t, repo, name, "pull/1", 0)
		f.exec(t, `UPDATE report_groups SET total_reports_expected=2 WHERE id=$1`, id)
		f.report(t, id, testObservation("MM-T1", true))
		f.report(t, id, testObservation("MM-T2", true))
		a := f.assess(t, s)
		if a.Outcome != PRSuspect || len(a.Tests) != 2 || a.Tests[0].Outcome != ObservedOnMaster || a.Tests[1].Outcome != PRSuspect {
			t.Fatalf("%+v", a)
		}
		if a.Tests[0].BaselineRuns != 3 || a.Tests[0].BaselineDistinctCommits != 3 {
			t.Fatalf("baseline: %+v", a.Tests[0])
		}
		if !slices.Contains(a.Reasons, "historical_upload_source_not_proven") {
			t.Fatal("missing provenance disclosure")
		}
		// A preview is a real read-only path, not merely a POST without an id.
		q := url.Values{"repository": {s.Repository}, "commit_sha": {s.CommitSHA}, "gh_run_id": {s.GHRunID}, "gh_run_attempt": {s.GHRunAttempt}, "name": {s.Name}}
		res := httptest.NewRecorder()
		f.h.Attribution(res, httptest.NewRequest(http.MethodGet, "/?"+q.Encode(), nil))
		if res.Code != 200 {
			t.Fatalf("preview: %d %s", res.Code, res.Body.String())
		}
		var count int
		if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM triage_assessments`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("preview wrote %d records", count)
		}
	})
	t.Run("exact selectors never fall back", func(t *testing.T) {
		id, s := f.group(t, repo, "exact", "pull/2", 0)
		f.report(t, id, testObservation("MM-T3", true))
		for _, change := range []func(*Selector){func(s *Selector) { s.Repository = "other/mattermost" }, func(s *Selector) { s.CommitSHA = strings.Repeat("f", 40) }, func(s *Selector) { s.GHRunID = "999999" }, func(s *Selector) { s.GHRunAttempt = "2" }, func(s *Selector) { s.Name = "other" }} {
			other := s
			change(&other)
			a := f.assess(t, other)
			if a.Outcome != Unknown || !slices.Contains(a.Reasons, "exact_run_not_found") {
				t.Fatalf("%+v", a)
			}
		}
	})
	t.Run("partial overfull failed empty or corrupted groups are unknown", func(t *testing.T) {
		for _, kind := range []string{"in_progress", "incomplete", "missing_shard", "overfull", "failed_shard", "report_error", "summary_mismatch", "empty"} {
			t.Run(kind, func(t *testing.T) {
				id, s := f.group(t, repo, "partial-"+kind, "pull/3", 0)
				var report string
				if kind == "empty" {
					report = f.report(t, id)
				} else {
					report = f.report(t, id, testObservation("MM-T4", true))
				}
				switch kind {
				case "in_progress", "incomplete":
					f.exec(t, `UPDATE report_groups SET status=$2 WHERE id=$1`, id, kind)
				case "missing_shard":
					f.exec(t, `UPDATE report_groups SET total_reports_expected=2 WHERE id=$1`, id)
				case "overfull":
					f.report(t, id, testObservation("MM-T5", true))
				case "failed_shard":
					f.exec(t, `UPDATE reports SET status='failed' WHERE id=$1`, report)
				case "report_error":
					f.exec(t, `UPDATE reports SET error_message='browser setup failed' WHERE id=$1`, report)
				case "summary_mismatch":
					f.exec(t, `UPDATE reports SET total_cases=2,failed_cases=2 WHERE id=$1`, report)
				}
				if a := f.assess(t, s); a.Outcome != Unknown {
					t.Fatalf("%+v", a)
				}
			})
		}
	})
	t.Run("green report does not clear an infrastructural red check", func(t *testing.T) {
		id, s := f.group(t, repo, "green", "pull/4", 0)
		f.report(t, id, testObservation("MM-T6", false))
		a := f.assess(t, s)
		if a.Outcome != NoFailure || !slices.Contains(a.Reasons, "github_job_conclusion_not_verified") {
			t.Fatalf("%+v", a)
		}
	})
	t.Run("retry failure is never omitted", func(t *testing.T) {
		id, s := f.group(t, repo, "retry", "pull/4", 0)
		report := f.report(t, id, testObservation("MM-T7", false))
		f.exec(t, `UPDATE test_cases SET attempts=2,attempts_failed=1,run_failed=false WHERE suite_id IN (SELECT id FROM suites WHERE report_id=$1)`, report)
		if a := f.assess(t, s); len(a.Tests) != 1 || a.Outcome != Unknown {
			t.Fatalf("%+v", a)
		}
	})
	t.Run("skipped-only reports cannot imply execution succeeded", func(t *testing.T) {
		id, s := f.group(t, repo, "skipped", "pull/4", 0)
		report := f.report(t, id, testObservation("MM-T13", false))
		f.exec(t, `UPDATE test_cases SET status='skipped' WHERE suite_id IN (SELECT id FROM suites WHERE report_id=$1)`, report)
		if a := f.assess(t, s); a.Outcome != Unknown || !slices.Contains(a.Reasons, "no_test_execution_observed") {
			t.Fatalf("skipped-only: %+v", a)
		}
	})
	t.Run("baseline boundaries and verified claim mismatches excluded", func(t *testing.T) {
		for _, kind := range []string{"fork", "pr_tagged_master", "late_completion", "wrong_commit_claim", "wrong_ref_claim", "same_commit", "stale", "different_name"} {
			t.Run(kind, func(t *testing.T) {
				name := "baseline-" + kind
				id, s := f.group(t, repo, name, "pull/5", 0)
				f.report(t, id, testObservation("MM-T8", true))
				for i := range 3 {
					age := time.Duration(i+1) * time.Hour
					if kind == "stale" {
						age += 72 * time.Hour
					}
					b, bs := f.group(t, repo, name, "master", age)
					report := f.report(t, b, testObservation("MM-T8", true))
					switch kind {
					case "fork":
						f.exec(t, `UPDATE report_groups SET repository='fork/mattermost' WHERE id=$1`, b)
					case "pr_tagged_master":
						f.exec(t, `UPDATE report_groups SET gh_pr_number=5 WHERE id=$1`, b)
					case "late_completion":
						f.exec(t, `UPDATE report_groups SET updated_at=$2 WHERE id=$1`, b, f.now.Add(time.Hour))
					case "same_commit":
						f.exec(t, `UPDATE report_groups SET commit_sha=$2 WHERE id=$1`, b, s.CommitSHA)
					case "different_name":
						f.exec(t, `UPDATE report_groups SET name='different-name' WHERE id=$1`, b)
					case "wrong_commit_claim", "wrong_ref_claim":
						sha, ref := bs.CommitSHA, "refs/heads/master"
						if kind == "wrong_commit_claim" {
							sha = strings.Repeat("f", 40)
						} else {
							ref = "refs/pull/5/merge"
						}
						f.exec(t, `INSERT INTO oidc_claims(report_id,issuer,subject,audience,repository,ref,raw_claims) VALUES($1,'https://token.actions.githubusercontent.com','subject','tsio',$2,$3,jsonb_build_object('sha',$4::text))`, report, repo, ref, sha)
					}
				}
				if a := f.assess(t, s); a.Outcome != Unknown {
					t.Fatalf("%+v", a)
				}
			})
		}
	})
	t.Run("current external id aliases and master renames are unknown", func(t *testing.T) {
		for _, master := range []bool{false, true} {
			name := fmt.Sprintf("aliases-%t", master)
			good, bad := testObservation("MM-T9", true), testObservation("MM-T9", false)
			bad.file = "other.cy.ts"
			bad.title = "renamed test"
			if master {
				f.baseline(t, repo, name, bad)
			} else {
				f.baseline(t, repo, name, good)
			}
			id, s := f.group(t, repo, name, "pull/6", 0)
			if master {
				f.report(t, id, good)
			} else {
				f.report(t, id, good, bad)
			}
			if a := f.assess(t, s); a.Outcome != Unknown || len(a.Tests) != 1 {
				t.Fatalf("%+v", a)
			}
		}
	})
	t.Run("explicit master suite mappings preserve edition and framework", func(t *testing.T) {
		good := testObservation("MM-T11", false)
		f.baseline(t, repo, "cypress-full-enterprise-master", good)
		// The wrong edition and framework both fail under the same external id;
		// neither may contaminate the matched clean enterprise Cypress baseline.
		bad := good
		bad.failed = true
		f.baseline(t, repo, "cypress-full-fips-master", bad)
		for i := range 3 {
			id, _ := f.group(t, repo, "cypress-full-enterprise-master", "master", time.Duration(i+1)*time.Hour)
			f.exec(t, `UPDATE report_groups SET framework='playwright' WHERE id=$1`, id)
			f.report(t, id, bad)
		}
		id, s := f.group(t, repo, "cypress-full-enterprise", "pull/8", 0)
		f.report(t, id, bad)
		if a := f.assess(t, s); a.Outcome != PRSuspect || a.Tests[0].BaselineRuns != 3 {
			t.Fatalf("mapped baseline: %+v", a)
		}
		const otherRepo = "other/mattermost"
		f.baseline(t, otherRepo, "cypress-full-enterprise", good)
		f.baseline(t, otherRepo, "cypress-full-enterprise-master", bad)
		id, s = f.group(t, otherRepo, "cypress-full-enterprise", "pull/8", 0)
		f.report(t, id, bad)
		if a := f.assess(t, s); a.Outcome != PRSuspect || a.Tests[0].BaselineRuns != 3 {
			t.Fatalf("unrelated repository mapping: %+v", a)
		}
	})
	t.Run("current upload claims cannot contradict exact run", func(t *testing.T) {
		id, s := f.group(t, repo, "claim-mismatch", "pull/9", 0)
		report := f.report(t, id, testObservation("MM-T12", false))
		f.exec(t, `INSERT INTO oidc_claims(report_id,issuer,subject,audience,repository,ref,raw_claims) VALUES($1,'https://token.actions.githubusercontent.com','subject','tsio',$2,'refs/pull/9/merge','{"sha":"wrong"}')`, report, repo)
		if a := f.assess(t, s); a.Outcome != Unknown || !slices.Contains(a.Reasons, "run_upload_claims_mismatch") {
			t.Fatalf("claim mismatch: %+v", a)
		}
	})
	t.Run("record is append only authored and survives source deletion", func(t *testing.T) {
		id, s := f.group(t, repo, "record", "pull/7", 0)
		f.report(t, id, testObservation("MM-T10", false))
		body, _ := json.Marshal(s)
		request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
		request.Header.Set("X-TSIO-Triage-Actor", "workflow:trusted")
		res := httptest.NewRecorder()
		f.h.Record(res, request)
		if res.Code != 201 {
			t.Fatalf("%d %s", res.Code, res.Body.String())
		}
		var a Assessment
		if err := json.Unmarshal(res.Body.Bytes(), &a); err != nil {
			t.Fatal(err)
		}
		if a.ID == "" || a.Author != "workflow:trusted" || a.RecordedAt == nil || a.CanUnblock {
			t.Fatalf("%+v", a)
		}
		second, err := f.h.evaluate(context.Background(), s, "workflow:trusted")
		if err != nil {
			t.Fatal(err)
		}
		if second.ID == a.ID {
			t.Fatal("assessment overwritten")
		}
		for _, q := range []string{`UPDATE triage_assessments SET author='changed' WHERE id=$1`, `DELETE FROM triage_assessments WHERE id=$1`} {
			if _, err := f.pool.Exec(context.Background(), q, a.ID); err == nil {
				t.Fatal("mutation allowed")
			}
		}
		if _, err := f.pool.Exec(context.Background(), `TRUNCATE triage_assessments`); err == nil {
			t.Fatal("truncate allowed")
		}
		if _, err := f.pool.Exec(context.Background(), `INSERT INTO triage_assessments(repository,author,assessment) VALUES('x/y','trusted','{"can_unblock":true}')`); err == nil {
			t.Fatal("clearance allowed")
		}
		f.exec(t, `DELETE FROM report_groups WHERE id=$1`, id)
		router := chi.NewRouter()
		router.Get("/{id}", f.h.Verdict)
		get := httptest.NewRecorder()
		router.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/"+a.ID, nil))
		var saved Assessment
		if err := json.Unmarshal(get.Body.Bytes(), &saved); err != nil {
			t.Fatal(err)
		}
		if get.Code != 200 || saved.ID != a.ID || saved.Run.ReportGroupID != id || saved.Author != a.Author || saved.CanUnblock {
			t.Fatalf("saved: %+v", saved)
		}
	})
}
