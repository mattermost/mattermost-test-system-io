//go:build e2e

package triagework

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/db"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func workPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	pg, e := tcpostgres.Run(ctx, "postgres:18.3", tcpostgres.WithDatabase("triage"), tcpostgres.WithUsername("triage"), tcpostgres.WithPassword("triage"), tcpostgres.BasicWaitStrategies())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = pg.Terminate(ctx)
	})
	dsn, e := pg.ConnectionString(ctx, "sslmode=disable")
	if e != nil {
		t.Fatal(e)
	}
	if e = db.Migrate(dsn); e != nil {
		t.Fatal(e)
	}
	pool, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(pool.Close)
	return pool
}
func sqlExec(t *testing.T, pool *pgxpool.Pool, q string, args ...any) {
	t.Helper()
	if _, e := pool.Exec(context.Background(), q, args...); e != nil {
		t.Fatal(e)
	}
}

type fixture struct{ group, report, suite, key string }

func seed(t *testing.T, pool *pgxpool.Pool, repo, title string) fixture {
	t.Helper()
	f := fixture{group: uuid.NewString(), report: uuid.NewString(), suite: uuid.NewString()}
	sha := strings.Repeat("a", 40)
	sqlExec(t, pool, `INSERT INTO report_groups(id,framework,name,repository,branch,commit_sha,gh_run_id,gh_run_attempt,status,total_reports_expected,environment_metadata) VALUES($1,'cypress','cypress-full-enterprise-master',$2,'master',$3,$4,'1','completed',1,$5)`, f.group, repo, sha, f.group, `{"server_image_digest":"registry.example/server@sha256:`+strings.Repeat("a", 64)+`","browser_version":"123"}`)

	metadata := map[string]any{"server_image_digest": "registry.example/server@sha256:" + strings.Repeat("a", 64), "browser_version": "123"}
	receipt, _ := json.Marshal(map[string]any{"repository": repo, "commit": sha, "gh_run_id": f.group, "gh_run_attempt": "1", "framework": "cypress", "name": "cypress-full-enterprise-master", "branch": "master", "total_reports_expected": 1, "environment_metadata": metadata})
	sqlExec(t, pool, `INSERT INTO reports(id,report_group_id,name,status,upload_principal,registration_receipt) VALUES($1,$2,'master','complete','oidc:trusted-master',$3)`, f.report, f.group, receipt)

	raw, _ := json.Marshal(map[string]string{"sha": sha, "run_id": f.group, "run_attempt": "1", "event_name": "push", "workflow_ref": "org/repo/.github/workflows/e2e-tests-on-merge.yml@refs/heads/master"})
	sqlExec(t, pool, `INSERT INTO oidc_claims(report_id,issuer,subject,audience,repository,ref,raw_claims) VALUES($1,'https://token.actions.githubusercontent.com','repo:org/repo:ref:refs/heads/master','tsio',$2,'refs/heads/master',$3)`, f.report, repo, raw)
	sqlExec(t, pool, `INSERT INTO suites(id,report_id,title,file,ordinal) VALUES($1,$2,'suite','test.cy.ts',1)`, f.suite, f.report)
	sqlExec(t, pool, `INSERT INTO test_cases(suite_id,title,full_title,file,status,ordinal,run_failed,attempts,attempts_failed) VALUES($1,$2,$2,'test.cy.ts','failed',1,true,1,1)`, f.suite, title)
	if e := pool.QueryRow(context.Background(), `SELECT stable_key FROM test_cases WHERE suite_id=$1`, f.suite).Scan(&f.key); e != nil {
		t.Fatal(e)
	}
	return f
}
func call(h http.HandlerFunc, id, scopeRepo string, body any) *httptest.ResponseRecorder {
	data, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(data))
	r.Header.Set("X-TSIO-Triage-Actor", "test:guardian")
	r.Header.Set("X-TSIO-Triage-Repository", scopeRepo)
	rc := chi.NewRouteContext()
	rc.URLParams.Add("id", id)
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rc))
	w := httptest.NewRecorder()
	h(w, r)
	return w
}
func resultItem(t *testing.T, w *httptest.ResponseRecorder, want int) *Item {
	t.Helper()
	if w.Code != want {
		t.Fatalf("HTTP %d want %d: %s", w.Code, want, w.Body.String())
	}
	var out struct {
		Item *Item `json:"item"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
		t.Fatal(e)
	}
	return out.Item
}
func enq(t *testing.T, h *Handlers, f fixture) *Item {
	t.Helper()
	return resultItem(t, call(h.Enqueue, "", "", map[string]any{"report_group_id": f.group, "stable_key": f.key, "owner": "QA", "ticket": "MM-100"}), 200)
}
func claim(t *testing.T, h *Handlers, repo string) *Item {
	t.Helper()
	return resultItem(t, call(h.Claim, "", "", map[string]string{"repository": repo, "worker": "guardian"}), 200)
}
func complete(t *testing.T, h *Handlers, item *Item, outcome string, want int) *Item {
	t.Helper()
	body := map[string]string{"lease_token": item.LeaseToken, "outcome": outcome, "account": "Reproduced against the pinned image; evidence attached", "evidence_url": "https://github.com/org/repo/actions/runs/1"}
	if outcome == "repair_pr" {
		body["pr_url"] = "https://github.com/" + item.Repository + "/pull/123"
	}
	return resultItem(t, call(h.Complete, item.ID, "", body), want)
}

func TestWorkPostgres(t *testing.T) {
	pool := workPool(t)
	h := &Handlers{Pool: pool, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), QuarantineCap: 1, SourceWorkflowRefs: []string{"org/repo/.github/workflows/e2e-tests-on-merge.yml@refs/heads/master"}}
	t.Run("trusted master derives evidence and idempotent logical identity", func(t *testing.T) {
		f := seed(t, pool, "org/trust", "test A")
		a := enq(t, h, f)
		again := enq(t, h, f)
		if a.ID != again.ID || a.File != "test.cy.ts" || a.CommitSHA != strings.Repeat("a", 40) || a.ImageDigest == "" {
			t.Fatalf("bad derived evidence %+v", a)
		}
		item := claim(t, h, "org/trust")
		complete(t, h, item, "repair_pr", 200)
		if again = enq(t, h, f); again.State != "repair_pr" || again.ID != a.ID {
			t.Fatal("enqueue reopened a terminal outcome")
		}
		sqlExec(t, pool, `UPDATE oidc_claims SET raw_claims=jsonb_set(raw_claims,'{workflow_ref}','"org/repo/.github/workflows/e2e-tests-ci.yml@refs/heads/master"') WHERE report_id=$1`, f.report)
		if w := call(h.Enqueue, "", "", map[string]string{"report_group_id": f.group, "stable_key": f.key, "owner": "QA"}); w.Code != 409 {
			t.Fatal("PR E2E source workflow forged master evidence")
		}

		sqlExec(t, pool, `UPDATE oidc_claims SET raw_claims=jsonb_set(raw_claims,'{sha}','"wrong"') WHERE report_id=$1`, f.report)
		w := call(h.Enqueue, "", "", map[string]string{"report_group_id": f.group, "stable_key": f.key, "owner": "QA", "ticket": "MM-100"})
		if w.Code != 409 {
			t.Fatalf("untrusted SHA accepted: %s", w.Body.String())
		}
	})
	t.Run("rejects aliases missing shard and mutable image", func(t *testing.T) {
		f := seed(t, pool, "org/invalid", "MM-T12 first title")
		sqlExec(t, pool, `INSERT INTO test_cases(suite_id,title,full_title,file,status,ordinal) VALUES($1,'MM-T12 other title','MM-T12 other title','test.cy.ts','failed',2)`, f.suite)
		sqlExec(t, pool, `UPDATE test_cases SET external_test_id='MM-T12' WHERE suite_id=$1`, f.suite)
		if err := pool.QueryRow(context.Background(), `SELECT stable_key FROM test_cases WHERE suite_id=$1 LIMIT 1`, f.suite).Scan(&f.key); err != nil {
			t.Fatal(err)
		}
		body := map[string]string{"report_group_id": f.group, "stable_key": f.key, "owner": "QA", "ticket": "MM-100"}
		if w := call(h.Enqueue, "", "", body); w.Code != 409 {
			t.Fatalf("alias accepted %s", w.Body.String())
		}
		f = seed(t, pool, "org/incomplete", "test missing")
		body["report_group_id"] = f.group
		body["stable_key"] = f.key
		sqlExec(t, pool, `UPDATE report_groups SET total_reports_expected=2 WHERE id=$1`, f.group)
		if w := call(h.Enqueue, "", "", body); w.Code != 409 {
			t.Fatal("missing shard accepted")
		}
		sqlExec(t, pool, `UPDATE report_groups SET total_reports_expected=1,environment_metadata='{"server_image_digest":"registry/server:latest"}' WHERE id=$1`, f.group)
		if w := call(h.Enqueue, "", "", body); w.Code != 200 {
			t.Fatal("poisoned group metadata overrode trusted receipt")
		}
		sqlExec(t, pool, `UPDATE reports SET registration_receipt=jsonb_set(registration_receipt,'{environment_metadata,server_image_digest}','"registry/server:latest"') WHERE id=$1`, f.report)
		if w := call(h.Enqueue, "", "", body); w.Code != 409 {
			t.Fatal("mutable image accepted")
		}
	})
	t.Run("missing registration count requires verified matching begin receipt", func(t *testing.T) {
		f := seed(t, pool, "org/begin", "begin proof")
		sqlExec(t, pool, `UPDATE reports SET registration_receipt=registration_receipt-'total_reports_expected' WHERE id=$1`, f.report)
		body := map[string]string{"report_group_id": f.group, "stable_key": f.key, "owner": "QA"}
		if w := call(h.Enqueue, "", "", body); w.Code != 409 {
			t.Fatal("missing trusted shard count accepted")
		}
		claims, _ := json.Marshal(map[string]string{"iss": "https://token.actions.githubusercontent.com", "repository": "org/begin", "ref": "refs/heads/master", "sha": strings.Repeat("a", 40), "run_id": f.group, "run_attempt": "1", "event_name": "push", "workflow_ref": "org/repo/.github/workflows/e2e-tests-on-merge.yml@refs/heads/master"})
		sqlExec(t, pool, `INSERT INTO report_group_begin_receipts(report_group_id,verified_claims,receipt) SELECT report_group_id,$2,registration_receipt||'{"total_reports_expected":1}'::jsonb FROM reports WHERE id=$1`, f.report, claims)
		if w := call(h.Enqueue, "", "", body); w.Code != 200 {
			t.Fatalf("trusted Begin count not used: %s", w.Body.String())
		}
		sqlExec(t, pool, `UPDATE report_group_begin_receipts SET verified_claims=jsonb_set(verified_claims,'{sha}','"wrong"') WHERE report_group_id=$1`, f.group)
		if w := call(h.Enqueue, "", "", body); w.Code != 409 {
			t.Fatal("wrong Begin provenance accepted")
		}
	})
	t.Run("matching test reports must agree on authenticated image and harness", func(t *testing.T) {
		first := seed(t, pool, "org/env", "shared test")
		second := seed(t, pool, "org/env", "shared test")
		sqlExec(t, pool, `UPDATE report_groups SET total_reports_expected=2 WHERE id=$1`, first.group)
		sqlExec(t, pool, `UPDATE reports SET report_group_id=$2::uuid,registration_receipt=jsonb_set(jsonb_set(registration_receipt,'{gh_run_id}',to_jsonb($2::text)),'{total_reports_expected}','2') WHERE id IN ($1,$3)`, first.report, first.group, second.report)
		sqlExec(t, pool, `UPDATE oidc_claims SET raw_claims=jsonb_set(raw_claims,'{run_id}',to_jsonb($2::text)) WHERE report_id=$1`, second.report, first.group)
		sqlExec(t, pool, `UPDATE reports SET registration_receipt=jsonb_set(registration_receipt,'{environment_metadata,worker_index}',to_jsonb(id::text)) WHERE report_group_id=$1`, first.group)
		body := map[string]string{"report_group_id": first.group, "stable_key": first.key, "owner": "QA"}
		if w := call(h.Enqueue, "", "", body); w.Code != 200 {
			t.Fatalf("worker-specific metadata rejected: %s", w.Body.String())
		}
		sqlExec(t, pool, `UPDATE reports SET registration_receipt=registration_receipt-'total_reports_expected' WHERE id=$1`, second.report)
		if w := call(h.Enqueue, "", "", body); w.Code != 409 {
			t.Fatal("nullable trust aggregate ignored a shard without count attestation")
		}
		sqlExec(t, pool, `UPDATE reports SET registration_receipt=registration_receipt||'{"total_reports_expected":2}'::jsonb WHERE id=$1`, second.report)

		sqlExec(t, pool, `UPDATE reports SET registration_receipt=jsonb_set(registration_receipt,'{environment_metadata,browser_version}','"different"') WHERE id=$1`, second.report)
		if w := call(h.Enqueue, "", "", body); w.Code != 409 {
			t.Fatal("disagreeing harness environment accepted")
		}
	})

	t.Run("one concurrent claimant stale fencing and three strike exhaustion", func(t *testing.T) {
		f := seed(t, pool, "org/race", "race")
		enq(t, h, f)
		var wg sync.WaitGroup
		var wins atomic.Int32
		winner := make(chan *Item, 20)
		for i := range 20 {
			wg.Go(func() {
				w := call(h.Claim, "", "", map[string]string{"repository": "org/race", "worker": strconv.Itoa(i)})
				if w.Code != 200 {
					t.Errorf("claim: %s", w.Body.String())
					return
				}
				var v struct {
					Item *Item `json:"item"`
				}
				_ = json.Unmarshal(w.Body.Bytes(), &v)
				if v.Item != nil {
					wins.Add(1)
					winner <- v.Item
				}
			})
		}
		wg.Wait()
		close(winner)
		if wins.Load() != 1 {
			t.Fatalf("%d claim winners", wins.Load())
		}
		item := <-winner
		old := *item
		sqlExec(t, pool, `UPDATE triage_repairs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, item.ID)
		item = claim(t, h, "org/race")
		if item.Attempt != 2 || item.LeaseToken == old.LeaseToken {
			t.Fatal("no second fenced attempt")
		}
		if w := call(h.Heartbeat, old.ID, "", map[string]string{"lease_token": old.LeaseToken}); w.Code != 409 {
			t.Fatal("stale heartbeat mutated")
		}
		if w := call(h.Complete, old.ID, "", map[string]string{"lease_token": old.LeaseToken, "outcome": "failed", "account": "late"}); w.Code != 409 {
			t.Fatal("stale completion mutated")
		}
		complete(t, h, item, "blocked", 200)
		item = claim(t, h, "org/race")
		done := complete(t, h, item, "failed", 200)
		if done.State != "needs_human" || len(done.Attempts) != 3 || done.Attempts[0].Outcome != "expired" {
			t.Fatalf("missing durable strikes %+v", done)
		}
		if claim(t, h, "org/race") != nil {
			t.Fatal("fourth attempt claimed")
		}
	})
	t.Run("three expirations count and expired completion cannot renew", func(t *testing.T) {
		f := seed(t, pool, "org/expire", "expire")
		enq(t, h, f)
		for range 3 {
			item := claim(t, h, "org/expire")
			sqlExec(t, pool, `UPDATE triage_repairs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, item.ID)
			if w := call(h.Heartbeat, item.ID, "", map[string]string{"lease_token": item.LeaseToken}); w.Code != 409 {
				t.Fatal("expired heartbeat accepted")
			}
		}
		if claim(t, h, "org/expire") != nil {
			t.Fatal("exhaustion bypass")
		}
		var n int
		if e := pool.QueryRow(context.Background(), `SELECT count(*) FROM triage_repair_attempts a JOIN triage_repairs r ON r.id=a.repair_id WHERE r.repository='org/expire' AND a.outcome='expired'`).Scan(&n); e != nil || n != 3 {
			t.Fatalf("expiration accounts %d %v", n, e)
		}
	})
	t.Run("all mutations enforce stored repository scope", func(t *testing.T) {
		f := seed(t, pool, "org/scope", "scope")
		a := enq(t, h, f)
		item := claim(t, h, "org/scope")
		cases := []struct {
			fn   http.HandlerFunc
			id   string
			body any
		}{
			{h.Enqueue, "", map[string]string{"report_group_id": f.group, "stable_key": f.key, "owner": "QA", "ticket": "MM-1"}},
			{h.Claim, "", map[string]string{"repository": "org/scope", "worker": "w"}},
			{h.Heartbeat, item.ID, map[string]string{"lease_token": item.LeaseToken}},
			{h.Complete, item.ID, map[string]string{"lease_token": item.LeaseToken, "outcome": "failed", "account": "failure"}},
			{h.Quarantine, "", map[string]any{"repair_id": a.ID, "owner": "QA", "ticket": "MM-1", "expires_at": time.Now().Add(time.Hour)}},
		}
		for _, c := range cases {
			if w := call(c.fn, c.id, "other/repo", c.body); w.Code != 403 {
				t.Fatalf("cross repo mutation HTTP %d: %s", w.Code, w.Body.String())
			}
		}
	})
	t.Run("serialized quarantine cap expiry and raw evidence unchanged", func(t *testing.T) {
		a := enq(t, h, seed(t, pool, "org/quarantine", "q1"))
		b := enq(t, h, seed(t, pool, "org/quarantine", "q2"))
		var before string
		if e := pool.QueryRow(context.Background(), `SELECT md5(string_agg(to_jsonb(t)::text,',' ORDER BY t.id)) FROM test_cases t`).Scan(&before); e != nil {
			t.Fatal(e)
		}
		var wg sync.WaitGroup
		var wins atomic.Int32
		for _, item := range []*Item{a, b} {
			wg.Go(func() {
				w := call(h.Quarantine, "", "", map[string]any{"repair_id": item.ID, "owner": "QA", "ticket": "MM-1", "expires_at": time.Now().Add(time.Hour)})
				if w.Code == 201 {
					wins.Add(1)
				} else if w.Code != 409 {
					t.Errorf("quarantine: %s", w.Body.String())
				}
			})
		}
		wg.Wait()
		if wins.Load() != 1 {
			t.Fatalf("quarantine cap winners %d", wins.Load())
		}
		sqlExec(t, pool, `UPDATE triage_quarantines SET created_at=clock_timestamp()-interval '2 hours',expires_at=clock_timestamp()-interval '1 second'`)
		w := httptest.NewRecorder()
		h.ListQuarantine(w, httptest.NewRequest(http.MethodGet, "/?repository=org/quarantine", nil))
		var out struct {
			Items []QuarantineItem `json:"items"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if len(out.Items) != 1 || out.Items[0].Active || !out.Items[0].BlockingEligible {
			t.Fatalf("expiry still suppresses %+v", out)
		}
		if w = call(h.Quarantine, "", "", map[string]any{"repair_id": b.ID, "owner": "QA", "ticket": "MM-2", "expires_at": time.Now().Add(time.Hour)}); w.Code != 201 {
			t.Fatal("expiry did not release cap")
		}
		var after string
		if e := pool.QueryRow(context.Background(), `SELECT md5(string_agg(to_jsonb(t)::text,',' ORDER BY t.id)) FROM test_cases t`).Scan(&after); e != nil || after != before {
			t.Fatal("quarantine mutated raw test outcomes")
		}
	})
	t.Run("prioritize distinct observed PR failures without claiming causality", func(t *testing.T) {
		first := seed(t, pool, "org/priority", "less affected")
		second := seed(t, pool, "org/priority", "more affected")
		enq(t, h, first)
		wanted := enq(t, h, second)
		for i := range 2 {
			pr := seed(t, pool, "org/priority", "more affected")
			sqlExec(t, pool, `UPDATE report_groups SET name='cypress-full-enterprise',branch='feature',gh_pr_number=$2 WHERE id=$1`, pr.group, i+1)
		}
		retest := seed(t, pool, "org/priority", "more affected")
		sqlExec(t, pool, `UPDATE report_groups SET name='cypress-full-enterprise',branch='feature',gh_pr_number=3 WHERE id=$1`, retest.group)
		sqlExec(t, pool, `INSERT INTO test_cases(suite_id,title,full_title,file,status,ordinal,run_failed) VALUES($1,'more affected','more affected','test.cy.ts','passed',2,false)`, retest.suite)
		missing := seed(t, pool, "org/priority", "more affected")
		sqlExec(t, pool, `UPDATE report_groups SET name='cypress-full-enterprise',branch='feature',gh_pr_number=4,total_reports_expected=2 WHERE id=$1`, missing.group)

		item := claim(t, h, "org/priority")
		if item.ID != wanted.ID || item.ObservedDistinctPRsFailed != 2 {
			t.Fatalf("wrong priority %+v", item)
		}
	})
	t.Run("human resolution preserves strikes and never grants a fourth claim", func(t *testing.T) {
		humanSource := seed(t, pool, "org/human", "human needed")
		enq(t, h, humanSource)
		var item *Item
		for range 3 {
			item = claim(t, h, "org/human")
			complete(t, h, item, "failed", 200)
		}
		body := map[string]string{"account": "Maintainer verified and merged the repair", "evidence_url": "https://github.com/org/human/actions/runs/1", "pr_url": "https://github.com/org/human/pull/1"}
		if w := call(h.Resolve, item.ID, "other/repo", body); w.Code != 403 {
			t.Fatal("cross repo resolution accepted")
		}
		resolved := resultItem(t, call(h.Resolve, item.ID, "", body), 200)
		if resolved.State != "resolved" || len(resolved.Attempts) != 3 || claim(t, h, "org/human") != nil {
			t.Fatal("human resolution erased strikes or reopened work")
		}
		if w := call(h.Enqueue, "", "", map[string]string{"report_group_id": humanSource.group, "stable_key": humanSource.key, "owner": "QA"}); w.Code != 409 {
			t.Fatal("resolved source replayed")
		}
		later := seed(t, pool, "org/human", "human needed")
		fresh := resultItem(t, call(h.Enqueue, "", "", map[string]string{"report_group_id": later.group, "stable_key": later.key, "owner": "QA"}), 200)
		if fresh.ID == item.ID || fresh.State != "queued" || fresh.Attempt != 0 || fresh.Ticket != "" {
			t.Fatal("later verified failure did not start a fresh cycle with optional ticket")
		}
		if next := claim(t, h, "org/human"); next.ID != fresh.ID || next.Attempt != 1 {
			t.Fatal("new cycle did not claim attempt one")
		}

		var author string
		if err := pool.QueryRow(context.Background(), `SELECT author FROM triage_repair_resolutions WHERE repair_id=$1`, item.ID).Scan(&author); err != nil || author != "test:guardian" {
			t.Fatal("missing authenticated resolution account")
		}
	})
	t.Run("concurrent Jira submission has one external creator", func(t *testing.T) {
		blocker := &blockingJira{fakeJira: &fakeJira{marker: map[string]*Issue{}}, started: make(chan struct{}, 2), release: make(chan struct{})}
		h.Jira = blocker
		enq(t, h, seed(t, pool, "org/jirarace", "race bug"))
		item := claim(t, h, "org/jirarace")
		complete(t, h, item, "product_suspect", 200)
		body := map[string]string{"lease_token": item.LeaseToken, "summary": "Concurrent bug", "description": "Failure account"}
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() { done <- call(h.Defect, item.ID, "", body) }()
		select {
		case <-blocker.started:
		case <-time.After(5 * time.Second):
			t.Fatal("first create did not start")
		}
		w := call(h.Defect, item.ID, "", body)
		if w.Code != 409 {
			t.Errorf("racing submission expected reconciliation block: %s", w.Body.String())
		}
		close(blocker.release)
		select {
		case w = <-done:
			if w.Code != 201 || blocker.creates != 1 {
				t.Fatalf("duplicate or failed create: %s", w.Body.String())
			}
		case <-time.After(5 * time.Second):
			t.Fatal("create did not finish")
		}
	})
	t.Run("Jira HTTP timeout after creation reconciles without duplication", func(t *testing.T) {
		var creates atomic.Int32
		var marker string
		var mu sync.Mutex
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/rest/api/3/issue" {
				var body struct {
					Fields struct {
						Labels []string `json:"labels"`
					} `json:"fields"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				mu.Lock()
				marker = body.Fields.Labels[1]
				mu.Unlock()
				creates.Add(1)
				<-r.Context().Done()
				return
			}
			var body struct {
				JQL string `json:"jql"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			matched := marker != "" && strings.Contains(body.JQL, marker)
			mu.Unlock()
			if matched {
				write(w, 200, map[string]any{"issues": []any{map[string]any{"key": "MM-90", "fields": map[string]any{"resolution": nil}}}})
			} else {
				write(w, 200, map[string]any{"issues": []any{}})
			}
		}))
		defer server.Close()
		client := server.Client()
		client.Timeout = 100 * time.Millisecond
		var err error
		h.Jira, err = NewJiraClient(Config{Enabled: true, BaseURL: server.URL, Email: "bot", APIToken: "secret", ProjectKey: "MM", IssueType: "Bug", HTTPClient: client})
		if err != nil {
			t.Fatal(err)
		}
		enq(t, h, seed(t, pool, "org/httpjira", "timeout bug"))
		item := claim(t, h, "org/httpjira")
		complete(t, h, item, "product_suspect", 200)
		body := map[string]string{"lease_token": item.LeaseToken, "summary": "Timeout bug", "description": "Failure account"}
		if w := call(h.Defect, item.ID, "", body); w.Code != 502 {
			t.Fatalf("timeout outcome: %s", w.Body.String())
		}
		if w := call(h.Defect, item.ID, "", body); w.Code != 200 || creates.Load() != 1 {
			t.Fatalf("timeout duplication: %s", w.Body.String())
		}
	})

	t.Run("closed uncertain older Jira does not consume new observation", func(t *testing.T) {
		f := &fakeJira{marker: map[string]*Issue{}, fail: true}
		h.Jira = f
		enq(t, h, seed(t, pool, "org/recovered", "recover bug"))
		item := claim(t, h, "org/recovered")
		complete(t, h, item, "product_suspect", 200)
		body := map[string]string{"lease_token": item.LeaseToken, "summary": "Recovered bug", "description": "Failure account"}
		if w := call(h.Defect, item.ID, "", body); w.Code != 502 {
			t.Fatal("expected uncertain create")
		}
		var pending string
		if err := pool.QueryRow(context.Background(), `SELECT id::text FROM triage_defect_submissions WHERE repair_id=$1 AND state='uncertain'`, item.ID).Scan(&pending); err != nil {
			t.Fatal(err)
		}
		f.marker[submissionLabel(pending)] = &Issue{Key: "MM-1", URL: "https://jira.example/browse/MM-1"}
		f.fail = false
		item = enq(t, h, seed(t, pool, "org/recovered", "recover bug"))
		body["lease_token"] = item.LeaseToken
		if w := call(h.Defect, item.ID, "", body); w.Code != 201 || f.creates != 2 {
			t.Fatalf("closed recovered ticket consumed newer failure: %s", w.Body.String())
		}
		// A new sample linked to an unresolved ticket cannot later reuse itself to
		// create another ticket after that ticket closes.
		item = enq(t, h, seed(t, pool, "org/recovered", "recover bug"))
		body["lease_token"] = item.LeaseToken
		if w := call(h.Defect, item.ID, "", body); w.Code != 200 {
			t.Fatal("new sample did not dedup")
		}
		w := httptest.NewRecorder()
		h.ListDefects(w, httptest.NewRequest(http.MethodGet, "/?repository=org/recovered", nil))
		var events struct {
			Items []DefectItem `json:"items"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &events); err != nil {
			t.Fatal(err)
		}
		if len(events.Items) != 3 || events.Items[0].ReportGroupID == "" {
			t.Fatalf("caught observations collapsed into submissions: %s", w.Body.String())
		}

		f.open = nil
		if w := call(h.Defect, item.ID, "", body); w.Code != 409 || f.creates != 2 {
			t.Fatalf("handled sample recreated closed ticket: %s", w.Body.String())
		}
	})

	t.Run("product suspect exclusive Jira dedup closed recurrence and uncertainty", func(t *testing.T) { testDefectState(t, h, pool) })
}

type fakeJira struct {
	mu       sync.Mutex
	open     *Issue
	marker   map[string]*Issue
	creates  int
	fail     bool
	indexLag bool
}

func (f *fakeJira) FindUnresolved(context.Context, string) (*Issue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.indexLag {
		return nil, nil
	}
	return f.open, nil
}
func (f *fakeJira) FindSubmission(_ context.Context, s string) (*Issue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.marker[s], nil
}
func (f *fakeJira) IsUnresolved(_ context.Context, key string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.open != nil && f.open.Key == key, nil
}
func (f *fakeJira) Create(_ context.Context, _, marker, _, _ string) (*Issue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.creates++
	issue := &Issue{Key: fmt.Sprintf("MM-%d", f.creates), URL: fmt.Sprintf("https://jira.example/browse/MM-%d", f.creates)}
	if f.fail {
		return nil, errors.New("timeout")
	}
	f.open = issue
	f.marker[marker] = issue
	return issue, nil
}
func testDefectState(t *testing.T, h *Handlers, pool *pgxpool.Pool) {
	f := &fakeJira{marker: map[string]*Issue{}}
	h.Jira = f
	fixture := seed(t, pool, "org/defect", "product bug")
	enq(t, h, fixture)
	item := claim(t, h, "org/defect")
	body := map[string]string{"lease_token": item.LeaseToken, "summary": "Product regression", "description": "Reproduced with unchanged test against pinned image."}
	if w := call(h.Defect, item.ID, "", body); w.Code != 409 {
		t.Fatal("defect accepted before product outcome")
	}
	complete(t, h, item, "product_suspect", 200)
	if w := call(h.Complete, item.ID, "", map[string]string{"lease_token": item.LeaseToken, "outcome": "repair_pr", "account": "try repair", "pr_url": "https://github.com/org/defect/pull/1", "evidence_url": "https://github.com/org/defect/actions/runs/1"}); w.Code != 409 {
		t.Fatal("product suspect converted to test repair")
	}
	if w := call(h.Defect, item.ID, "org/other", body); w.Code != 403 {
		t.Fatal("cross repo defect accepted")
	}
	if w := call(h.Defect, item.ID, "", body); w.Code != 201 {
		t.Fatalf("defect create %s", w.Body.String())
	}
	f.indexLag = true
	if w := call(h.Defect, item.ID, "", body); w.Code != 200 || f.creates != 1 {
		t.Fatal("index lag caused duplicate")
	}
	f.open = nil
	f.indexLag = false
	if w := call(h.Defect, item.ID, "", body); w.Code != 409 || f.creates != 1 {
		t.Fatal("same evidence recreated closed ticket")
	}
	newer := seed(t, pool, "org/defect", "product bug")
	renewed := enq(t, h, newer)
	if renewed.State != "product_suspect" || renewed.LeaseToken == item.LeaseToken || renewed.LeaseToken == "" {
		t.Fatal("new product observation did not rotate fenced token")
	}
	if w := call(h.Defect, item.ID, "", body); w.Code != 409 {
		t.Fatal("old product token not fenced")
	}
	body["lease_token"] = renewed.LeaseToken
	if old := call(h.Enqueue, "", "", map[string]string{"report_group_id": fixture.group, "stable_key": fixture.key, "owner": "QA", "ticket": "MM-100"}); old.Code != 409 {
		t.Fatal("old master observation replayed")
	}
	if same := enq(t, h, newer); same.LeaseToken != renewed.LeaseToken {
		t.Fatal("same group rotated token")
	}
	item = renewed
	if w := call(h.Defect, item.ID, "", body); w.Code != 201 || f.creates != 2 {
		t.Fatalf("closed issue suppressed recurrence: %s", w.Body.String())
	}
	f.open = nil
	renewed = enq(t, h, seed(t, pool, "org/defect", "product bug"))
	body["lease_token"] = renewed.LeaseToken
	f.fail = true
	if w := call(h.Defect, item.ID, "", body); w.Code != 502 || f.creates != 3 {
		t.Fatalf("failed create not uncertain: %s", w.Body.String())
	}
	var pending string
	if e := pool.QueryRow(context.Background(), `SELECT id::text FROM triage_defect_submissions WHERE repair_id=$1 AND state='uncertain'`, item.ID).Scan(&pending); e != nil {
		t.Fatal(e)
	}
	for range 3 {
		if w := call(h.Defect, item.ID, "", body); w.Code != 409 || f.creates != 3 {
			t.Fatal("uncertain submission duplicated")
		}
	}
	f.marker[submissionLabel(pending)] = &Issue{Key: "MM-3", URL: "https://jira.example/browse/MM-3"}
	if w := call(h.Defect, item.ID, "", body); w.Code != 200 || f.creates != 3 {
		t.Fatal("uncertain submission did not reconcile")
	}
	// Simulate process death after the intent commit but before recording HTTP result.
	sqlExec(t, pool, `UPDATE triage_defect_submissions SET state='submitting',jira_key=NULL,jira_url=NULL WHERE id=$1`, pending)
	delete(f.marker, submissionLabel(pending))
	if w := call(h.Defect, item.ID, "", body); w.Code != 409 || f.creates != 3 {
		t.Fatal("crashed submitting intent blindly retried")
	}
	w := httptest.NewRecorder()
	h.ListDefects(w, httptest.NewRequest(http.MethodGet, "/?repository=org/defect", nil))
	if w.Code != 200 || strings.Contains(w.Body.String(), "resolved_at") || strings.Contains(w.Body.String(), item.LeaseToken) {
		t.Fatalf("invalid defect public receipt %s", w.Body.String())
	}
}

type blockingJira struct {
	*fakeJira
	started chan struct{}
	release chan struct{}
}

func (b *blockingJira) Create(ctx context.Context, test, marker, summary, description string) (*Issue, error) {
	b.started <- struct{}{}
	select {
	case <-b.release:
		return b.fakeJira.Create(ctx, test, marker, summary, description)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
