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
	"strings"
	"testing"
)

type runEvidenceFixture struct {
	group   string
	s       Selector
	reports []string
}

func (f *fixture) trustedRun(t *testing.T, master bool) runEvidenceFixture {
	t.Helper()
	name, branch := "playwright-full-enterprise", "feature"
	var pr *int
	number := 99
	if master {
		name, branch = name+"-master", "master"
	} else {
		pr = &number
	}
	id, selector := f.group(t, "mattermost/mattermost", name, branch, 0)
	f.exec(t, `UPDATE report_groups SET framework='playwright',total_reports_expected=2,gh_pr_number=$2,environment_metadata='{"origin":"mutable-group"}' WHERE id=$1`, id, pr)
	g := RunEvidenceGroup{Selector: selector, Branch: branch, Framework: "playwright"}
	result := runEvidenceFixture{group: id, s: selector}
	for i := range 2 {
		observation := testObservation("MM-T123", i == 0)
		observation.project, observation.title = fmt.Sprintf("chromium-%d", i), fmt.Sprintf("suite MM-T123 variant %d", i)
		report := f.report(t, id, observation)
		result.reports = append(result.reports, report)
		job, jobName := fmt.Sprintf("%d", 1000+i), fmt.Sprintf("playwright-worker-%d", i)
		receipt := map[string]any{"repository": selector.Repository, "commit": selector.CommitSHA, "gh_run_id": selector.GHRunID, "gh_run_attempt": selector.GHRunAttempt,
			"framework": g.Framework, "name": name, "branch": branch, "total_reports_expected": 2, "gh_job_id": job, "gh_job_name": jobName,
			"json_files": nil, "screenshots": nil, "environment_metadata": map[string]any{"origin": "original-registration", "worker_index": i}}
		if pr != nil {
			receipt["gh_pr_number"] = *pr
		}
		raw, err := json.Marshal(receipt)
		if err != nil {
			t.Fatal(err)
		}
		f.exec(t, `UPDATE reports SET gh_job_id=$2,gh_job_name=$3,upload_principal=$4,registration_receipt=$5 WHERE id=$1`, report, job, jobName, "oidc:"+strings.Repeat("a", 64), raw)
		claims, err := json.Marshal(map[string]any{"iss": "https://token.actions.githubusercontent.com", "repository": selector.Repository, "ref": "refs/heads/master", "run_id": selector.GHRunID,
			"run_attempt": selector.GHRunAttempt, "sha": strings.Repeat("b", 40), "workflow_ref": runEvidenceWorkflow(g, pr), "event_name": "workflow_dispatch"})
		if err != nil {
			t.Fatal(err)
		}
		f.exec(t, `INSERT INTO oidc_claims(report_id,issuer,subject,audience,repository,ref,raw_claims) VALUES($1,'https://token.actions.githubusercontent.com','trusted-subject','tsio',$2,'refs/heads/master',$3)`, report, selector.Repository, claims)
	}
	return result
}

func (f *fixture) evidence(t *testing.T, s Selector, limits runEvidenceLimits) RunEvidence {
	t.Helper()
	ev, err := f.h.runEvidence(context.Background(), s, limits)
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

func TestPostgresRunEvidence(t *testing.T) {
	f := newFixture(t)
	t.Run("original complete evidence retains raw identities and nullable rollups", func(t *testing.T) {
		run := f.trustedRun(t, false)
		// A retry is another raw row; it must not disappear into its MM-T key.
		f.exec(t, `INSERT INTO test_cases(suite_id,title,full_title,status,ordinal,file,project,external_test_id,retry_count,run_failed,attempts,attempts_failed,error_message)
 SELECT t.suite_id,t.title,t.full_title,'passed',1,t.file,t.project,t.external_test_id,1,false,2,1,'original retry failure' FROM test_cases t JOIN suites s ON s.id=t.suite_id WHERE s.report_id=$1`, run.reports[0])
		// A different logical test can reuse the same external ID and project.
		f.exec(t, `INSERT INTO test_cases(suite_id,title,full_title,status,ordinal,file,project,external_test_id)
 SELECT t.suite_id,'alias title','alias full title','passed',2,t.file,t.project,t.external_test_id FROM test_cases t JOIN suites s ON s.id=t.suite_id WHERE s.report_id=$1 AND t.ordinal=0`, run.reports[0])
		f.exec(t, `UPDATE reports SET total_cases=2,passed_cases=1 WHERE id=$1`, run.reports[0])
		ev := f.evidence(t, run.s, defaultRunEvidenceLimits)
		if !ev.Complete || ev.Truncated || !ev.TrustedSource || len(ev.Reasons) != 0 || len(ev.Reports) != 2 || len(ev.Tests) != 4 || ev.SourceWorkflowSHA != strings.Repeat("b", 40) || ev.SourceWorkflowSHA == ev.Group.CommitSHA {
			t.Fatalf("evidence: %+v", ev)
		}
		for _, raw := range ev.Reports {
			if !strings.Contains(string(raw), "original-registration") || strings.Contains(string(raw), "mutable-group") {
				t.Fatalf("metadata: %s", raw)
			}
		}
		projects := map[string]bool{}
		identities := map[string]map[string]bool{}
		nulls, retries := 0, 0
		ids := map[string]bool{}
		for _, raw := range ev.Tests {
			var row map[string]any
			if err := json.Unmarshal(raw, &row); err != nil {
				t.Fatal(err)
			}
			key := row["stable_key"].(string)
			if key != row["project"].(string)+" :: MM-T123" || row["report_id"] == "" || row["suite_id"] == "" {
				t.Fatalf("row: %s", raw)
			}
			if identities[key] == nil {
				identities[key] = map[string]bool{}
			}
			identities[key][row["full_title"].(string)] = true
			projects[row["project"].(string)], ids[row["id"].(string)] = true, true
			if row["run_failed"] == nil && row["attempts"] == nil && row["attempts_failed"] == nil {
				nulls++
			}
			if row["retry_count"] == float64(1) && row["attempts"] == float64(2) && row["attempts_failed"] == float64(1) && row["run_failed"] == false && row["error_message"] == "original retry failure" {
				retries++
			}
		}
		if len(projects) != 2 || len(ids) != 4 || nulls != 3 || retries != 1 || len(identities["chromium-0 :: MM-T123"]) != 2 {
			t.Fatalf("projects=%v ids=%v nulls=%d retries=%d", projects, ids, nulls, retries)
		}
		q := url.Values{"repository": {run.s.Repository}, "commit_sha": {run.s.CommitSHA}, "gh_run_id": {run.s.GHRunID}, "gh_run_attempt": {run.s.GHRunAttempt}, "name": {run.s.Name}}
		response := httptest.NewRecorder()
		f.h.RunEvidence(response, httptest.NewRequest(http.MethodGet, "/?"+q.Encode(), nil))
		if response.Code != 200 {
			t.Fatalf("%d %s", response.Code, response.Body)
		}
		for _, forbidden := range []string{"can_unblock", "raw_claims", "upload_principal", "verified_claims", "trusted-subject"} {
			if strings.Contains(response.Body.String(), forbidden) {
				t.Fatalf("export leaked %s", forbidden)
			}
		}
		q.Set("gh_run_attempt", "99")
		response = httptest.NewRecorder()
		f.h.RunEvidence(response, httptest.NewRequest(http.MethodGet, "/?"+q.Encode(), nil))
		if response.Code != 404 {
			t.Fatalf("missing exact run=%d", response.Code)
		}
	})
	t.Run("master workflow mapping", func(t *testing.T) {
		run := f.trustedRun(t, true)
		if ev := f.evidence(t, run.s, defaultRunEvidenceLimits); !ev.TrustedSource || !strings.Contains(ev.SourceWorkflowRef, "/e2e-tests-on-merge.yml@") {
			t.Fatalf("master: %+v", ev)
		}
	})
	for _, key := range []string{"sha", "workflow_ref", "event_name", "run_id", "run_attempt"} {
		t.Run("one shard missing claim "+key, func(t *testing.T) {
			run := f.trustedRun(t, false)
			f.exec(t, `UPDATE oidc_claims SET raw_claims=raw_claims-$2::text WHERE report_id=$1`, run.reports[1], key)
			assertRunEvidenceUntrusted(t, f.evidence(t, run.s, defaultRunEvidenceLimits), "missing_or_mismatched_verified_source_claims")
		})
	}
	for _, mutation := range []struct{ name, query string }{
		{"absent claims", `DELETE FROM oidc_claims WHERE report_id=$1`},
		{"wrong issuer", `UPDATE oidc_claims SET issuer='untrusted' WHERE report_id=$1`},
		{"wrong repository", `UPDATE oidc_claims SET repository='other/repo' WHERE report_id=$1`},
		{"wrong ref", `UPDATE oidc_claims SET ref='refs/heads/feature' WHERE report_id=$1`},
		{"wrong source workflow", `UPDATE oidc_claims SET raw_claims=jsonb_set(raw_claims,'{workflow_ref}','"mattermost/mattermost/.github/workflows/e2e-tests-on-merge.yml@refs/heads/master"') WHERE report_id=$1`},
		{"pull request event", `UPDATE oidc_claims SET raw_claims=jsonb_set(raw_claims,'{event_name}','"pull_request"') WHERE report_id=$1`},
		{"tested sha substituted for source sha", `UPDATE oidc_claims SET raw_claims=jsonb_set(raw_claims,'{sha}',(SELECT to_jsonb(g.commit_sha) FROM reports r JOIN report_groups g ON g.id=r.report_group_id WHERE r.id=$1)) WHERE report_id=$1`},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			run := f.trustedRun(t, false)
			f.exec(t, mutation.query, run.reports[1])
			assertRunEvidenceUntrusted(t, f.evidence(t, run.s, defaultRunEvidenceLimits), "missing_or_mismatched_verified_source_claims")
		})
	}
	for _, field := range []string{"repository", "commit", "gh_run_id", "gh_run_attempt", "framework", "name", "branch", "gh_job_id", "gh_job_name", "environment_metadata", "total_reports_expected"} {
		t.Run("receipt missing "+field, func(t *testing.T) {
			run := f.trustedRun(t, false)
			f.exec(t, `UPDATE reports SET registration_receipt=registration_receipt-$2::text WHERE id=$1`, run.reports[1], field)
			assertRunEvidenceUntrusted(t, f.evidence(t, run.s, defaultRunEvidenceLimits), "missing_or_mismatched_registration_receipts")
		})
	}
	for _, mutation := range []struct{ name, query string }{
		{"legacy upload without receipt", `UPDATE reports SET upload_principal=NULL,registration_receipt=NULL WHERE id=$1`},
		{"source sha substituted for tested sha", `UPDATE reports SET registration_receipt=jsonb_set(registration_receipt,'{commit}',to_jsonb(repeat('b',40))) WHERE id=$1`},
		{"wrong registration job", `UPDATE reports SET registration_receipt=jsonb_set(registration_receipt,'{gh_job_id}','"9999"') WHERE id=$1`},
		{"wrong registration PR", `UPDATE reports SET registration_receipt=jsonb_set(registration_receipt,'{gh_pr_number}','100') WHERE id=$1`},
		{"API key principal", `UPDATE reports SET upload_principal='apikey:legacy' WHERE id=$1`},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			run := f.trustedRun(t, false)
			f.exec(t, mutation.query, run.reports[1])
			assertRunEvidenceUntrusted(t, f.evidence(t, run.s, defaultRunEvidenceLimits), "missing_or_mismatched_registration_receipts")
		})
	}
	t.Run("nonobject original metadata is exported but untrusted", func(t *testing.T) {
		run := f.trustedRun(t, false)
		f.exec(t, `UPDATE reports SET registration_receipt=jsonb_set(registration_receipt,'{environment_metadata}','["legacy-value",123]') WHERE id=$1`, run.reports[1])
		ev := f.evidence(t, run.s, defaultRunEvidenceLimits)
		assertRunEvidenceUntrusted(t, ev, "missing_or_mismatched_registration_receipts")
		found := false
		for _, raw := range ev.Reports {
			var report struct {
				ID       string          `json:"id"`
				Metadata json.RawMessage `json:"environment_metadata"`
			}
			if err := json.Unmarshal(raw, &report); err != nil {
				t.Fatal(err)
			}
			if report.ID == run.reports[1] {
				found = string(report.Metadata) == `["legacy-value",123]`
			}
		}
		if !found || !ev.Complete || ev.Truncated {
			t.Fatalf("original metadata changed: %+v", ev)
		}
	})
	t.Run("begin receipt proves count only for exact tested and source identities", func(t *testing.T) {
		run := f.trustedRun(t, false)
		f.exec(t, `INSERT INTO report_group_begin_receipts(report_group_id,verified_claims,receipt) SELECT r.report_group_id,o.raw_claims,r.registration_receipt-'gh_job_id'-'gh_job_name'-'environment_metadata'-'json_files'-'screenshots' FROM reports r JOIN oidc_claims o ON o.report_id=r.id WHERE r.id=$1`, run.reports[0])
		f.exec(t, `UPDATE reports SET registration_receipt=registration_receipt-'total_reports_expected' WHERE report_group_id=$1`, run.group)
		if ev := f.evidence(t, run.s, defaultRunEvidenceLimits); !ev.TrustedSource {
			t.Fatalf("verified begin: %+v", ev)
		}
		f.exec(t, `UPDATE report_group_begin_receipts SET verified_claims=jsonb_set(verified_claims,'{sha}',to_jsonb($2::text)) WHERE report_group_id=$1`, run.group, run.s.CommitSHA)
		assertRunEvidenceUntrusted(t, f.evidence(t, run.s, defaultRunEvidenceLimits), "missing_or_mismatched_registration_receipts")
	})
	t.Run("wrong receipt count cannot be rescued by verified begin", func(t *testing.T) {
		run := f.trustedRun(t, false)
		f.exec(t, `INSERT INTO report_group_begin_receipts(report_group_id,verified_claims,receipt) SELECT r.report_group_id,o.raw_claims,r.registration_receipt FROM reports r JOIN oidc_claims o ON o.report_id=r.id WHERE r.id=$1`, run.reports[0])
		f.exec(t, `UPDATE reports SET registration_receipt=jsonb_set(registration_receipt,'{total_reports_expected}','1') WHERE id=$1`, run.reports[1])
		assertRunEvidenceUntrusted(t, f.evidence(t, run.s, defaultRunEvidenceLimits), "missing_or_mismatched_registration_receipts")
	})
	for _, mutation := range []struct{ name, query string }{
		{"missing raw cases", `DELETE FROM test_cases WHERE suite_id IN (SELECT id FROM suites WHERE report_id=$1)`},
		{"failed ingestion", `UPDATE reports SET json_upload_status='failed' WHERE id=$1`},
		{"report error", `UPDATE reports SET error_message='ingestion failed' WHERE id=$1`},
		{"processing shard", `UPDATE reports SET status='processing' WHERE id=$1`},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			run := f.trustedRun(t, false)
			f.exec(t, mutation.query, run.reports[1])
			if ev := f.evidence(t, run.s, defaultRunEvidenceLimits); ev.Complete || !slices.Contains(ev.Reasons, "exact_run_incomplete") {
				t.Fatalf("incomplete: %+v", ev)
			}
		})
	}
	for _, bound := range []struct {
		name   string
		limits runEvidenceLimits
	}{
		{"report count", runEvidenceLimits{reports: 1, tests: 100, rowBytes: 10000, outputBytes: 10000}},
		{"test count", runEvidenceLimits{reports: 10, tests: 1, rowBytes: 10000, outputBytes: 10000}},
		{"row bytes", runEvidenceLimits{reports: 10, tests: 100, rowBytes: 32, outputBytes: 10000}},
		{"response bytes", runEvidenceLimits{reports: 10, tests: 100, rowBytes: 10000, outputBytes: 900}},
	} {
		t.Run(bound.name, func(t *testing.T) {
			run := f.trustedRun(t, false)
			ev := f.evidence(t, run.s, bound.limits)
			if ev.Complete || ev.TrustedSource || !ev.Truncated || len(ev.Reasons) == 0 {
				t.Fatalf("bound: %+v", ev)
			}
			encoded, err := json.Marshal(ev)
			if err != nil || len(encoded) > bound.limits.outputBytes {
				t.Fatalf("bounded response bytes=%d err=%v", len(encoded), err)
			}
		})
	}
	t.Run("HTML escaping counts toward actual output limit", func(t *testing.T) {
		run := f.trustedRun(t, false)
		f.exec(t, `UPDATE test_cases SET error_message=repeat('<',200) WHERE suite_id IN (SELECT id FROM suites WHERE report_id=$1)`, run.reports[0])
		limits := runEvidenceLimits{reports: 10, tests: 100, rowBytes: 10000, outputBytes: 2200}
		ev := f.evidence(t, run.s, limits)
		encoded, err := json.Marshal(ev)
		if !ev.Truncated || ev.Complete || ev.TrustedSource || err != nil || len(encoded) > limits.outputBytes {
			t.Fatalf("escaping bound: complete=%v truncated=%v bytes=%d err=%v", ev.Complete, ev.Truncated, len(encoded), err)
		}
	})
}

func assertRunEvidenceUntrusted(t *testing.T, ev RunEvidence, reason string) {
	t.Helper()
	if ev.TrustedSource || !slices.Contains(ev.Reasons, reason) {
		t.Fatalf("untrusted evidence: %+v", ev)
	}
}
