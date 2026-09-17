//go:build e2e

package identity_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/db"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/identity"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/ingest"
	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/orchestration"
)

func startDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, "postgres:18.3", tcpostgres.WithDatabase("tsio"), tcpostgres.WithUsername("tsio"), tcpostgres.WithPassword("tsio"), tcpostgres.BasicWaitStrategies())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	url, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(url); err != nil {
		t.Fatal(err)
	}
	pool, err := db.NewPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestIdentityPipeline(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	var groupID, reportID uuid.UUID
	err := pool.QueryRow(ctx, `INSERT INTO report_groups(framework,name,repository,branch,commit_sha,environment_metadata) VALUES('detox','mobile-main-detox-ios','mattermost/mobile','main','sha','{"lane":"ios"}') RETURNING id`).Scan(&groupID)
	if err != nil {
		t.Fatal(err)
	}
	err = pool.QueryRow(ctx, `INSERT INTO reports(report_group_id,name) VALUES($1,'detox') RETURNING id`, groupID).Scan(&reportID)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"testResults":[{"testFilePath":"/runner/mobile/detox/e2e/a.js","testResults":[{"ancestorTitles":["suite"],"title":"MM-T1 - works","fullName":"suite MM-T1 - works","status":"failed","duration":7,"failureMessages":["Error 123ms\n at /repo/detox/e2e/a.js:42:4"]}]},{"testFilePath":"ci/ios.stub","testResults":[{"title":"CI infrastructure failure","fullName":"CI infrastructure failure","status":"failed"}]}]}`)
	seq := 0
	suites, start, end := ingest.Extract("detox", payload, &seq)
	if _, err := ingest.Consolidate(ctx, pool, reportID, suites, start, end); err != nil {
		t.Fatal(err)
	}
	var identities, observations, linked, infra int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM test_identities),(SELECT count(*) FROM test_observations),(SELECT count(*) FROM test_cases WHERE identity_id IS NOT NULL),(SELECT count(*) FROM test_observations WHERE is_infra_stub)`).Scan(&identities, &observations, &linked, &infra); err != nil {
		t.Fatal(err)
	}
	if identities != 2 || observations != 2 || linked != 2 || infra != 1 {
		t.Fatalf("counts identities=%d facts=%d linked=%d infra=%d", identities, observations, linked, infra)
	}
	var branch, lane, locus, excerpt string
	if err := pool.QueryRow(ctx, `SELECT branch_kind,lane,failure_locus,error_excerpt FROM test_observations WHERE NOT is_infra_stub`).Scan(&branch, &lane, &locus, &excerpt); err != nil {
		t.Fatal(err)
	}
	if branch != "trunk" || lane != "ios" || locus != "detox/e2e/a.js:42" || excerpt != "Error #" {
		t.Fatalf("metadata %q %q %q %q", branch, lane, locus, excerpt)
	}
	// Concurrent backfills must preserve the append-only history and stable source IDs.
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Go(func() { _, err := identity.Backfill(ctx, pool, "mattermost/mobile", time.Unix(0, 0)); errs <- err })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM test_observations`).Scan(&observations); err != nil {
		t.Fatal(err)
	}
	if observations != 2 {
		t.Fatalf("backfill duplicated facts: %d", observations)
	}
	// Enrichment rolls back together with its owning transaction.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE test_cases SET full_title='MM-T99 rolled back' WHERE title='MM-T1 - works'`); err != nil {
		t.Fatal(err)
	}
	if err := identity.EnrichReport(ctx, tx, reportID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM test_identities`).Scan(&identities); err != nil {
		t.Fatal(err)
	}
	if identities != 2 {
		t.Fatal("identity escaped rollback")
	}

	t.Run("orchestration and upload share facts", func(t *testing.T) {
		store := &orchestration.Store{Pool: pool}
		subject := "test"
		ci := orchestration.CompositeIdentity{Repository: "mattermost/mattermost", CommitSHA: "pwsha", GHRunID: "1", GHRunAttempt: "1", Name: "playwright-full-enterprise", Branch: "master", Framework: "playwright"}
		opts := orchestration.BeginRunOptions{TotalReportsExpected: 1, RetestOnFail: true, RetestBudget: 1, BranchKind: "trunk", BaseRef: "master", BaseSHA: "base", EnvironmentMetadata: json.RawMessage(`{"lane":"enterprise"}`)}
		_, _, seed, err := store.BeginRun(ctx, ci, opts, []string{"e2e-tests/playwright/a.spec.ts"}, orchestration.OwnerInfo{OIDCSubject: &subject})
		if err != nil {
			t.Fatal(err)
		}
		worker := orchestration.WorkerIdentity{GHJobName: "worker", GHJobID: "100"}
		if _, _, _, err := store.AtomicCheckout(ctx, ci, worker, 1); err != nil {
			t.Fatal(err)
		}
		cases := json.RawMessage(`[{"title":"MM-T2 works","full_title":"[chromium] MM-T2 works","status":"failed","retry_count":1,"duration_ms":10,"error_message":"Error 123ms","error_stack":"at /repo/e2e-tests/playwright/a.spec.ts:4:2"}]`)
		result := []orchestration.SpecResult{{SpecPath: "e2e-tests/playwright/a.spec.ts", Status: "failed", TestCases: cases}}
		if _, err := store.RecordCompletion(ctx, ci, worker, result); err != nil {
			t.Fatal(err)
		}
		if _, err := store.RecordCompletion(ctx, ci, worker, result); err != nil {
			t.Fatal(err)
		}
		worker2 := orchestration.WorkerIdentity{GHJobName: "worker2", GHJobID: "101"}
		if _, _, _, err := store.AtomicRetestCheckout(ctx, ci, worker2, 1); err != nil {
			t.Fatal(err)
		}
		result[0].Status = "passed"
		result[0].TestCases = json.RawMessage(`[{"title":"MM-T2 works","full_title":"[chromium] MM-T2 works","status":"passed","retry_count":0}]`)
		if _, err := store.RecordCompletion(ctx, ci, worker2, result); err != nil {
			t.Fatal(err)
		}
		var report uuid.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO reports(report_group_id,name) VALUES($1,'pw') RETURNING id`, seed.ID).Scan(&report); err != nil {
			t.Fatal(err)
		}
		file := "e2e-tests/playwright/a.spec.ts"
		if _, err := ingest.Consolidate(ctx, pool, report, []ingest.ExtractedSuite{{Title: "suite", FilePath: &file, Cases: []ingest.ExtractedCase{{Title: "MM-T2 works", FullTitle: "[chromium] MM-T2 works", Status: "passed"}}}}, nil, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := identity.Backfill(ctx, pool, ci.Repository, time.Unix(0, 0)); err != nil {
			t.Fatal(err)
		}
		var n, maxIndex int
		if err := pool.QueryRow(ctx, `SELECT count(*),max(attempt_index) FROM test_observations WHERE report_group_id=$1`, seed.ID).Scan(&n, &maxIndex); err != nil {
			t.Fatal(err)
		}
		if n != 2 || maxIndex != 1 {
			t.Fatalf("expected fail/pass retest only, facts=%d max index=%d", n, maxIndex)
		}
		var allLinked bool
		if err := pool.QueryRow(ctx, `SELECT bool_and(identity_id IS NOT NULL) FROM test_cases c JOIN suites s ON s.id=c.suite_id WHERE s.report_id=$1`, report).Scan(&allLinked); err != nil {
			t.Fatal(err)
		}
		if !allLinked {
			t.Fatal("uploaded cases not linked")
		}
	})
}

// Compile-time assertion documents the transaction interface used by the pipeline.
var _ func(context.Context, pgx.Tx, uuid.UUID) error = identity.EnrichReport

func TestRawPlaywrightRetriesAndProjectPrefixes(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	store := &orchestration.Store{Pool: pool}
	subject := "test"
	ci := orchestration.CompositeIdentity{Repository: "mattermost/mattermost", CommitSHA: "rawretry", GHRunID: "rawretry", GHRunAttempt: "1", Name: "playwright-full-enterprise", Branch: "master", Framework: "playwright"}
	_, _, seed, err := store.BeginRun(ctx, ci, orchestration.BeginRunOptions{TotalReportsExpected: 1}, []string{"e2e-tests/playwright/raw.spec.ts"}, orchestration.OwnerInfo{OIDCSubject: &subject})
	if err != nil {
		t.Fatal(err)
	}
	worker := orchestration.WorkerIdentity{GHJobName: "worker", GHJobID: "rawretry"}
	if _, _, _, err := store.AtomicCheckout(ctx, ci, worker, 1); err != nil {
		t.Fatal(err)
	}
	payload := json.RawMessage(`[{"title":"MM-T3 works","full_title":"[chromium] [admin] MM-T3 works","status":"failed","retry_count":0,"duration_ms":10,"error_message":"expected visible","error_stack":"at e2e-tests/playwright/raw.spec.ts:8:2"},{"title":"MM-T3 works","full_title":"[chromium] [admin] MM-T3 works","status":"passed","retry_count":1,"duration_ms":20},{"title":"MM-T3 works","full_title":"[chromium] [guest] MM-T3 works","status":"passed","retry_count":0,"duration_ms":5}]`)
	result := []orchestration.SpecResult{{SpecPath: "e2e-tests/playwright/raw.spec.ts", Status: "passed", TestCases: payload}}
	if _, err := store.RecordCompletion(ctx, ci, worker, result); err != nil {
		t.Fatal(err)
	}
	if _, err := identity.Backfill(ctx, pool, ci.Repository, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM test_observations WHERE report_group_id=$1`, seed.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("distinct semantic suites collapsed: %d", n)
	}
	var status, excerpt, locus string
	var retry int
	var duration int64
	key := identity.StableKey(ci.Repository, "playwright", "e2e-tests/playwright/raw.spec.ts", "[chromium] [admin] MM-T3 works")
	if err := pool.QueryRow(ctx, `SELECT o.status,o.retry_count,o.duration_ms,o.error_excerpt,o.failure_locus FROM test_observations o JOIN test_identities i ON i.id=o.identity_id WHERE i.stable_key=$1`, key).Scan(&status, &retry, &duration, &excerpt, &locus); err != nil {
		t.Fatal(err)
	}
	if status != "flaky" || retry != 1 || duration != 30 || excerpt != "expected visible" || locus != "e2e-tests/playwright/raw.spec.ts:8" {
		t.Fatalf("lost retry evidence: %s %d %d %s %s", status, retry, duration, excerpt, locus)
	}
}

func TestConcurrentGroupsWithOppositeIdentityOrder(t *testing.T) {
	pool := startDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	reports := make([]uuid.UUID, 2)
	for i := range reports {
		var g uuid.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO report_groups(framework,name,repository,branch,commit_sha) VALUES('detox',$1,'concurrent','main','sha') RETURNING id`, uuid.NewString()).Scan(&g); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `INSERT INTO reports(report_group_id,name) VALUES($1,'job') RETURNING id`, g).Scan(&reports[i]); err != nil {
			t.Fatal(err)
		}
	}
	start := make(chan struct{})
	errs := make(chan error, 2)
	for i, r := range reports {
		go func() {
			<-start
			titles := []string{"MM-T1 alpha", "MM-T2 beta"}
			if i == 1 {
				titles[0], titles[1] = titles[1], titles[0]
			}
			file := "detox/e2e/shared.js"
			cases := []ingest.ExtractedCase{}
			for _, title := range titles {
				cases = append(cases, ingest.ExtractedCase{Title: title, FullTitle: title, Status: "passed"})
			}
			_, err := ingest.Consolidate(ctx, pool, r, []ingest.ExtractedSuite{{Title: "suite", FilePath: &file, Cases: cases}}, nil, nil)
			errs <- err
		}()
	}
	close(start)
	for range reports {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM test_observations`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Fatalf("lost concurrent evidence: %d", n)
	}
}
