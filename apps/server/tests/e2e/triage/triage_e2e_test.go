//go:build e2e

package triage_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"
	"github.com/google/uuid"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/events"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/ingest"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/orchestration"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/triage"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/triage/health"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/triage/verdict"
	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

type fixture struct {
	t      *testing.T
	env    *testenv.Env
	key    string
	router routers.Router
	repo   string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	env := testenv.Start(t)
	doc, err := openapi3.NewLoader().LoadFromFile("../../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatal(err)
	}
	doc.Servers = nil
	router, err := gorillamux.NewRouter(doc)
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, env: env, key: env.IssueAPIKey(t, "triage"), router: router, repo: "mattermost/triage-test"}
}
func (f *fixture) call(method, path string, body any, want int, admin bool) []byte {
	f.t.Helper()
	data, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(context.Background(), method, f.env.ServerURL+path, bytes.NewReader(data))
	if err != nil {
		f.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", f.key)
	if admin {
		req.Header.Set("X-Admin-Key", "test-admin-key")
	}
	route, params, err := f.router.FindRoute(req)
	if err != nil {
		f.t.Fatalf("missing OpenAPI route %s: %v", path, err)
	}
	input := &openapi3filter.RequestValidationInput{Request: req, PathParams: params, Route: route, Options: &openapi3filter.Options{AuthenticationFunc: func(context.Context, *openapi3filter.AuthenticationInput) error { return nil }}}
	if err := openapi3filter.ValidateRequest(context.Background(), input); err != nil {
		f.t.Fatalf("request contract %s: %v", path, err)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		f.t.Fatal(err)
	}
	if response.StatusCode != want {
		f.t.Fatalf("%s %s status=%d want=%d: %s", method, path, response.StatusCode, want, raw)
	}
	output := &openapi3filter.ResponseValidationInput{RequestValidationInput: input, Status: response.StatusCode, Header: response.Header, Body: io.NopCloser(bytes.NewReader(raw))}
	if err := openapi3filter.ValidateResponse(context.Background(), output); err != nil {
		f.t.Fatalf("response contract %s: %v\n%s", path, err, raw)
	}
	return raw
}
func (f *fixture) seed(run, status, kind string, at time.Time) string {
	f.t.Helper()
	ctx := context.Background()
	var groupID, reportID uuid.UUID
	branch := "main"
	if kind == "pr" {
		branch = "feature"
	}
	err := f.env.Pool.QueryRow(ctx, `INSERT INTO report_groups(repository,framework,name,commit_sha,gh_run_id,gh_run_attempt,branch,branch_kind,base_ref,environment_metadata,status,created_at) VALUES($1,'detox','mobile-main-detox-ios',$2,$2,'1',$3,$4,'main','{"lane":"ios"}','completed',$5) RETURNING id`, f.repo, run, branch, kind, at.Add(-time.Minute)).Scan(&groupID)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.env.Pool.QueryRow(ctx, `INSERT INTO reports(report_group_id,name,status,created_at) VALUES($1,'job','complete',$2) RETURNING id`, groupID, at).Scan(&reportID); err != nil {
		f.t.Fatal(err)
	}
	file := "detox/e2e/a.js"
	message := "Error expected visible"
	if _, err := ingest.Consolidate(ctx, f.env.Pool, reportID, []ingest.ExtractedSuite{{Title: "suite", FilePath: &file, StartTime: &at, Cases: []ingest.ExtractedCase{{Title: "MM-T1 - works", FullTitle: "suite MM-T1 - works", Status: status, StartTime: &at, ErrorMessage: &message}}}}, &at, &at); err != nil {
		f.t.Fatal(err)
	}
	return groupID.String()
}
func (f *fixture) verdictBody(run string) map[string]any {
	return map[string]any{"composite_identity": map[string]string{"repository": f.repo, "commit_sha": run, "gh_run_id": run, "name": "mobile-main-detox-ios", "gh_run_attempt": "1"}, "context": "e2e-test/detox-ios", "base_ref": "main", "lane": "ios"}
}
func TestTriageRoundTrip(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	for i := 0; i < 9; i++ {
		status := "passed"
		if i >= 5 {
			status = "failed"
		}
		f.seed(fmt.Sprintf("trunk-%02d", i), status, "trunk", now.Add(time.Duration(i-120)*time.Minute))
	}
	prID := f.seed("pr-0001", "failed", "pr", now.Add(-time.Minute))
	var v verdict.Verdict
	raw := f.call("POST", "/api/v1/triage/verdicts", f.verdictBody("pr-0001"), 200, false)
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if v.Verdict != "SUCCESS" || v.Mode != "shadow" || v.Findings[0].Class != "BROKEN_ON_TRUNK" {
		t.Fatalf("unexpected verdict %+v", v)
	}
	raw = f.call("POST", "/api/v1/triage/verdicts", f.verdictBody("pr-0001"), 200, false)
	var replay verdict.Verdict
	_ = json.Unmarshal(raw, &replay)
	if replay.ID != v.ID {
		t.Fatal("duplicate polling created another audit row")
	}
	f.call("GET", "/api/v1/triage/verdicts/"+v.ID, nil, 200, false)
	f.call("GET", "/api/v1/triage/verdicts?repository="+url.QueryEscape(f.repo), nil, 200, false)
	f.call("POST", "/api/v1/triage/verdicts/"+v.ID+"/override", map[string]string{"actor": "reviewer", "label": "E2E/Verified", "resulting_state": "success"}, 200, false)
	policyPath := "/api/v1/triage/policies/" + url.PathEscape(f.repo) + "/" + url.PathEscape("e2e-test/detox-ios")
	f.call("PUT", policyPath, map[string]any{"mode": "enforce", "thresholds": map[string]any{"max_exonerated_ratio": 1, "max_trunk_runs": 50}}, 403, false)
	f.call("PUT", policyPath, map[string]any{"mode": "enforce", "thresholds": map[string]any{"max_exonerated_ratio": 1, "max_trunk_runs": 50}}, 200, true)
	raw = f.call("POST", "/api/v1/triage/verdicts", f.verdictBody("pr-0001"), 200, false)
	_ = json.Unmarshal(raw, &replay)
	if replay.Mode != "enforce" || replay.ID == v.ID {
		t.Fatal("policy change not reflected in idempotency")
	}
	f.call("GET", "/api/v1/triage/policies?repository="+url.QueryEscape(f.repo), nil, 200, false)
	f.call("POST", "/api/v1/triage/health/refresh", map[string]string{"repository": f.repo, "base_ref": "main", "lane": "ios"}, 200, true)
	var identityID, quarantineID string
	var refreshes int
	if err := f.env.Pool.QueryRow(ctx, `SELECT h.identity_id::text,h.consecutive_refreshes,q.id::text FROM test_health h JOIN quarantine_entries q ON q.identity_id=h.identity_id WHERE q.status='active' AND q.source='auto'`).Scan(&identityID, &refreshes, &quarantineID); err != nil {
		t.Fatal(err)
	}
	f.call("POST", "/api/v1/triage/health/refresh", map[string]string{"repository": f.repo, "base_ref": "main", "lane": "ios"}, 200, true)
	var again int
	_ = f.env.Pool.QueryRow(ctx, `SELECT consecutive_refreshes FROM test_health WHERE identity_id=$1`, identityID).Scan(&again)
	if again != refreshes {
		t.Fatal("refresh replay advanced quarantine counter")
	}
	f.call("GET", "/api/v1/triage/health?repository="+url.QueryEscape(f.repo), nil, 200, false)
	f.call("GET", "/api/v1/triage/tests/"+identityID, nil, 200, false)
	f.call("GET", "/api/v1/triage/tests/"+identityID+"/observations?limit=2", nil, 200, false)
	for _, format := range []string{"json", "playwright-grep-invert", "jest-name-list", "spec-files"} {
		raw = f.call("GET", "/api/v1/triage/quarantine?repository="+url.QueryEscape(f.repo)+"&lane=ios&base_ref=main&format="+format, nil, 200, false)
		if format == "jest-name-list" && string(raw) != "suite MM-T1 - works" {
			t.Fatalf("Jest feed lost exact original title: %s", raw)
		}
		if len(raw) == 0 {
			t.Fatalf("empty %s feed", format)
		}
	}
	for i := 0; i < 50; i++ {
		f.seed(fmt.Sprintf("fixed-%02d", i), "passed", "trunk", now.Add(time.Duration(i-60)*time.Minute))
	}
	refresher := health.Refresher{Store: &triage.Store{Pool: f.env.Pool}}
	if _, err := refresher.Refresh(ctx, f.repo, "main", "ios", "e2e-test/detox-ios"); err != nil {
		t.Fatal(err)
	}
	var state string
	_ = f.env.Pool.QueryRow(ctx, `SELECT status FROM quarantine_entries WHERE id=$1`, quarantineID).Scan(&state)
	if state != "released" {
		t.Fatalf("auto quarantine not released: %s", state)
	}
	raw = f.call("POST", "/api/v1/triage/quarantine", map[string]any{"identity_id": identityID, "base_ref": "main", "lane": "ios", "reason": "manual"}, 201, true)
	var q triage.Quarantine
	_ = json.Unmarshal(raw, &q)
	if _, err := refresher.Refresh(ctx, f.repo, "main", "ios", "e2e-test/detox-ios"); err != nil {
		t.Fatal(err)
	}
	_ = f.env.Pool.QueryRow(ctx, `SELECT status FROM quarantine_entries WHERE id=$1`, q.ID).Scan(&state)
	if state != "active" {
		t.Fatal("manual entry auto-released")
	}
	f.call("DELETE", "/api/v1/triage/quarantine/"+q.ID, nil, 200, true)
	if _, err := f.env.Pool.Exec(ctx, `UPDATE report_groups SET status='in_progress' WHERE id=$1`, prID); err != nil {
		t.Fatal(err)
	}
	f.call("POST", "/api/v1/triage/verdicts", f.verdictBody("pr-0001"), 202, false)
	if _, err := f.env.Pool.Exec(ctx, `UPDATE report_groups SET status='incomplete' WHERE id=$1`, prID); err != nil {
		t.Fatal(err)
	}
	raw = f.call("POST", "/api/v1/triage/verdicts", f.verdictBody("pr-0001"), 409, false)
	if !strings.Contains(string(raw), "INCOMPLETE") {
		t.Fatal("incomplete verdict not stored")
	}
}

func TestAsOfReplayUsesOnlyEvidenceThatExisted(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	for i := 0; i < 10; i++ {
		status := "passed"
		if i >= 5 {
			status = "failed" // trunk breaks at t-50m and stays broken
		}
		f.seed(fmt.Sprintf("trunk-%02d", i), status, "trunk", now.Add(time.Duration(-100+10*i)*time.Minute))
	}
	f.seed("pr-0001", "failed", "pr", now)
	var before int
	if err := f.env.Pool.QueryRow(ctx, `SELECT count(*) FROM pr_verdicts`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	body := f.verdictBody("pr-0001")
	body["as_of"] = now.Add(-55 * time.Minute).Format(time.RFC3339) // only the five passes exist
	f.call("POST", "/api/v1/triage/verdicts", body, 403, false)     // admin only
	raw := f.call("POST", "/api/v1/triage/verdicts", body, 200, true)
	var replay struct {
		ID       string `json:"id"`
		Verdict  string `json:"verdict"`
		Findings []struct {
			Class string `json:"class"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(raw, &replay); err != nil {
		t.Fatal(err)
	}
	if replay.Verdict != "FAILURE" || len(replay.Findings) != 1 || replay.Findings[0].Class != "REGRESSION" || replay.ID != "00000000-0000-0000-0000-000000000000" {
		t.Fatalf("replay before the break must be a regression: %s", raw)
	}
	body["as_of"] = now.Add(-5 * time.Minute).Format(time.RFC3339) // the streak exists now
	raw = f.call("POST", "/api/v1/triage/verdicts", body, 200, true)
	if !strings.Contains(string(raw), `"BROKEN_ON_TRUNK"`) || !strings.Contains(string(raw), `"verdict":"SUCCESS"`) {
		t.Fatalf("replay after the break must exonerate: %s", raw)
	}
	body["wait_for_completion_ms"] = 1000
	f.call("POST", "/api/v1/triage/verdicts", body, 400, true)
	var after int
	if err := f.env.Pool.QueryRow(ctx, `SELECT count(*) FROM pr_verdicts`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("replay persisted %d verdict rows", after-before)
	}
}

func TestEvidenceAndAdjudicationRoundTrip(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UTC()
	for i := 0; i < 8; i++ {
		f.seed(fmt.Sprintf("trunk-%02d", i), "passed", "trunk", now.Add(time.Duration(-90+10*i)*time.Minute))
	}
	f.seed("pr-0001", "failed", "pr", now)
	raw := f.call("POST", "/api/v1/triage/verdicts", f.verdictBody("pr-0001"), 200, false)
	var v struct {
		ID       string `json:"id"`
		Verdict  string `json:"verdict"`
		Findings []struct {
			Class string `json:"class"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if v.Verdict != "FAILURE" || len(v.Findings) != 1 || v.Findings[0].Class != "REGRESSION" {
		t.Fatalf("expected a REGRESSION to adjudicate: %s", raw)
	}
	raw = f.call("GET", "/api/v1/triage/verdicts/"+v.ID+"/evidence", nil, 200, false)
	var ev struct {
		Packs []struct {
			Index int `json:"index"`
			Test  struct {
				Title string `json:"title"`
			} `json:"test"`
			Error  string `json:"error"`
			Engine struct {
				Class string `json:"class"`
			} `json:"engine"`
			Trunk struct {
				Runs int `json:"runs"`
			} `json:"trunk_history_14d"`
		} `json:"packs"`
	}
	if err := json.Unmarshal(raw, &ev); err != nil {
		t.Fatal(err)
	}
	if len(ev.Packs) != 1 || ev.Packs[0].Engine.Class != "REGRESSION" || !strings.Contains(ev.Packs[0].Error, "Error expected visible") || ev.Packs[0].Trunk.Runs != 8 || !strings.Contains(ev.Packs[0].Test.Title, "MM-T1") {
		t.Fatalf("evidence pack mismatch: %s", raw)
	}
	bad := map[string]any{"model": "claude-haiku-4-5", "min_confidence": 0.85, "final_verdict": "SUCCESS", "blocking": 0, "exonerated": 1,
		// The schema catches bad enums; the server must still reject an index that does not exist in this verdict.
		"findings": []map[string]any{{"index": 5, "class": "REGRESSION", "cause": "flaky_environment", "confidence": 0.9, "cited_evidence": []string{"cross_pr"}, "explanation": "x", "decision": "adjudicator_unblock", "blocking": false}}}
	f.call("POST", "/api/v1/triage/verdicts/"+v.ID+"/adjudication", bad, 400, false)
	good := map[string]any{"model": "claude-haiku-4-5", "min_confidence": 0.85, "final_verdict": "SUCCESS", "blocking": 0, "exonerated": 1,
		"findings": []map[string]any{{"index": 0, "class": "REGRESSION", "cause": "flaky_environment", "confidence": 0.91, "cited_evidence": []string{"cross_pr"}, "explanation": "Recurs on other PRs.", "decision": "adjudicator_unblock", "blocking": false}}}
	raw = f.call("POST", "/api/v1/triage/verdicts/"+v.ID+"/adjudication", good, 200, false)
	if !strings.Contains(string(raw), `"final_verdict":"SUCCESS"`) || !strings.Contains(string(raw), `"adjudicator_unblock"`) {
		t.Fatalf("adjudication not persisted: %s", raw)
	}
	raw = f.call("GET", "/api/v1/triage/verdicts/"+v.ID, nil, 200, false)
	if !strings.Contains(string(raw), `"adjudication":{`) || !strings.Contains(string(raw), `"verdict":"FAILURE"`) {
		t.Fatalf("engine verdict must stay FAILURE with the adjudication attached: %s", raw)
	}
}

func TestFailedSpecWithoutCasesRequiresAction(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	store := &orchestration.Store{Pool: f.env.Pool}
	subject := "test"
	ci := orchestration.CompositeIdentity{Repository: f.repo, CommitSHA: "uncaptured", GHRunID: "uncaptured", GHRunAttempt: "1", Name: "playwright-full-enterprise", Branch: "feature", Framework: "playwright"}
	_, _, group, err := store.BeginRun(ctx, ci, orchestration.BeginRunOptions{TotalReportsExpected: 1, BranchKind: "pr", BaseRef: "master", EnvironmentMetadata: json.RawMessage(`{"lane":"enterprise"}`)}, []string{"good.spec.ts", "bad.spec.ts"}, orchestration.OwnerInfo{OIDCSubject: &subject})
	if err != nil {
		t.Fatal(err)
	}
	worker := orchestration.WorkerIdentity{GHJobName: "worker", GHJobID: "worker"}
	if _, _, _, err := store.AtomicCheckout(ctx, ci, worker, 2); err != nil {
		t.Fatal(err)
	}
	_, err = store.RecordCompletion(ctx, ci, worker, []orchestration.SpecResult{{SpecPath: "good.spec.ts", Status: "passed", TestCases: json.RawMessage(`[{"title":"passes","full_title":"passes","status":"passed"}]`)}, {SpecPath: "bad.spec.ts", Status: "failed", TestCases: json.RawMessage(`[]`)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.env.Pool.Exec(ctx, `UPDATE report_groups SET status='completed' WHERE id=$1`, group.ID); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"composite_identity": map[string]string{"repository": ci.Repository, "commit_sha": ci.CommitSHA, "gh_run_id": ci.GHRunID, "gh_run_attempt": ci.GHRunAttempt, "name": ci.Name}, "context": "e2e-test/playwright-full/enterprise", "base_ref": "master", "lane": "enterprise"}
	raw := f.call("POST", "/api/v1/triage/verdicts", body, 200, false)
	var v verdict.Verdict
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if v.Verdict != "ACTION_REQUIRED" {
		t.Fatalf("missing failed-spec evidence was green: %+v", v)
	}
}

func TestQuarantineGrepDoesNotSkipSharedUnquarantinedID(t *testing.T) {
	f := newFixture(t)
	f.seed("trunk", "passed", "trunk", time.Now())
	var id string
	if err := f.env.Pool.QueryRow(context.Background(), `SELECT id::text FROM test_identities LIMIT 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	f.call("POST", "/api/v1/triage/quarantine", map[string]any{"identity_id": id, "base_ref": "main", "lane": "ios", "reason": "manual"}, 201, true)
	if _, err := f.env.Pool.Exec(context.Background(), `INSERT INTO test_identities(repository,framework,stable_key,normalized_file,normalized_title,mm_t_id) VALUES($1,'detox',decode('01','hex'),'other.js','MM-T1 unrelated','MM-T1')`, f.repo); err != nil {
		t.Fatal(err)
	}
	raw := f.call("GET", "/api/v1/triage/quarantine?repository="+url.QueryEscape(f.repo)+"&lane=ios&base_ref=main&format=playwright-grep-invert", nil, 200, false)
	if string(raw) != "(?!)" {
		t.Fatalf("unsafe shared-ID skip regex: %s", raw)
	}
}

func TestHealthHistoryShowsRecoveredRetestAsFlaky(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	group := f.seed("recovered", "failed", "trunk", time.Now().Add(-time.Minute))
	_, err := f.env.Pool.Exec(ctx, `INSERT INTO test_observations(identity_id,report_group_id,branch_kind,branch,commit_sha,lane,status,attempt_index,observed_at) SELECT identity_id,report_group_id,branch_kind,branch,commit_sha,lane,'passed',1,observed_at+interval '1 second' FROM test_observations WHERE report_group_id=$1`, group)
	if err != nil {
		t.Fatal(err)
	}
	f.call("POST", "/api/v1/triage/health/refresh", map[string]string{"repository": f.repo, "base_ref": "main", "lane": "ios"}, 200, true)
	raw := f.call("GET", "/api/v1/triage/health?repository="+url.QueryEscape(f.repo), nil, 200, false)
	var response struct {
		Items []struct {
			History []string `json:"history"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 1 || len(response.Items[0].History) != 1 || response.Items[0].History[0] != "flaky" {
		t.Fatalf("misleading health sparkline: %s", raw)
	}
}

func TestCompletionEventRefreshesHealthAndQuarantine(t *testing.T) {
	f := newFixture(t)
	now := time.Now()
	for i := 0; i < 5; i++ {
		f.seed(fmt.Sprintf("pass-%d", i), "passed", "trunk", now.Add(time.Duration(i-20)*time.Minute))
	}
	hub := events.NewHub()
	ch, unsubscribe := hub.Subscribe(nil, "")
	defer unsubscribe()
	worker := health.Worker{Refresher: &health.Refresher{Store: &triage.Store{Pool: f.env.Pool}, Hub: hub}, Interval: time.Hour}
	worker.Start(context.Background())
	defer worker.Stop()
	waitFor := func(kind string) {
		t.Helper()
		timer := time.NewTimer(10 * time.Second)
		defer timer.Stop()
		for {
			select {
			case e := <-ch:
				if e.Type == kind {
					return
				}
			case <-timer.C:
				t.Fatalf("missing worker event %s", kind)
			}
		}
	}
	waitFor("triage.health.refreshed")
	var group string
	for i := 0; i < 4; i++ {
		group = f.seed(fmt.Sprintf("fail-%d", i), "failed", "trunk", now.Add(time.Duration(i-10)*time.Minute))
	}
	publisher := events.Publisher{Hub: hub}
	publisher.ReportUpdated(uuid.MustParse(group), "completed", 1, nil, time.Now())
	waitFor("triage.quarantine.opened")
	waitFor("triage.health.refreshed")
	var classification string
	var active int
	if err := f.env.Pool.QueryRow(context.Background(), `SELECT classification FROM test_health`).Scan(&classification); err != nil {
		t.Fatal(err)
	}
	if err := f.env.Pool.QueryRow(context.Background(), `SELECT count(*) FROM quarantine_entries WHERE status='active'`).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if classification != "broken" || active != 1 {
		t.Fatalf("completion did not project/quarantine: %s %d", classification, active)
	}
}
