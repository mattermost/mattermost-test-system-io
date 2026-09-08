//go:build e2e

package db

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/mattermost/mattermost-test-system-io/apps/server/migrations"
)

func database(t *testing.T, version uint) (*pgxpool.Pool, *migrate.Migrate) {
	t.Helper()
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, "postgres:18.3", tcpostgres.WithDatabase("tsio"),
		tcpostgres.WithUsername("tsio"), tcpostgres.WithPassword("tsio"), tcpostgres.BasicWaitStrategies())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	url, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, strings.Replace(url, "postgres://", "pgx5://", 1))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = m.Close() })
	if err := m.Migrate(version); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool, m
}

func execSQL(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func TestStableKeyBackfill(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy_generated_%v", legacy), func(t *testing.T) {
			pool, m := database(t, 26)
			execSQL(t, pool, `WITH g AS (INSERT INTO report_groups(framework,name,repository,branch,commit_sha)
			 VALUES('playwright','smoke','mattermost/mattermost','master','old') RETURNING id),
			 r AS (INSERT INTO reports(report_group_id,name) SELECT id,'smoke' FROM g RETURNING id),
			 s AS (INSERT INTO suites(report_id,title,file,ordinal) SELECT id,'suite','a.spec.ts',0 FROM r RETURNING id)
			 INSERT INTO test_cases(suite_id,title,full_title,status,ordinal)
			 SELECT s.id,v.title,v.full_title,'passed',v.ordinal FROM s CROSS JOIN (VALUES
			 ('MM-T123_4 renamed','no identifier here',0),('MM-T123_4 again','XMM-T999 false prefix',1),
			 ('plain title','plain title',2)) v(title,full_title,ordinal)`)
			if legacy {
				for _, name := range []string{"testdata/legacy_27.sql", "testdata/legacy_28.sql"} {
					body, err := os.ReadFile(name)
					if err != nil {
						t.Fatal(err)
					}
					execSQL(t, pool, string(body))
				}
				if err := m.Force(28); err != nil {
					t.Fatal(err)
				}
			}
			var before []string
			rows, err := pool.Query(context.Background(), `SELECT ctid::text FROM test_cases ORDER BY ordinal`)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var v string
				if err := rows.Scan(&v); err != nil {
					t.Fatal(err)
				}
				before = append(before, v)
			}
			rows.Close()
			if err := m.Up(); err != nil {
				t.Fatal(err)
			}
			rows, err = pool.Query(context.Background(), `SELECT ctid::text FROM test_cases ORDER BY ordinal`)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; rows.Next(); i++ {
				var v string
				if err := rows.Scan(&v); err != nil {
					t.Fatal(err)
				}
				if v != before[i] {
					t.Fatalf("migration rewrote row %d: %s => %s", i, before[i], v)
				}
			}
			rows.Close()
			if err := runStableKeyBackfill(context.Background(), pool, 1, 0); err != nil {
				t.Fatal(err)
			}
			var matching, unknown int
			if err := pool.QueryRow(context.Background(), `SELECT count(*) FILTER (WHERE stable_key='MM-T123_4'),
			 count(*) FILTER (WHERE project IS NULL) FROM test_cases`).Scan(&matching, &unknown); err != nil {
				t.Fatal(err)
			}
			if matching != 2 || unknown != 3 {
				t.Fatalf("backfill identity mismatch: matching=%d project-unknown=%d", matching, unknown)
			}
			// Interrupting a concurrent build leaves an invalid index. Re-running
			// must repair it, not treat IF NOT EXISTS as proof that it is usable.
			execSQL(t, pool, `DROP INDEX test_cases_stable_key_idx`)
			if _, err := pool.Exec(context.Background(), `CREATE UNIQUE INDEX CONCURRENTLY test_cases_stable_key_idx ON test_cases(stable_key)`); err == nil {
				t.Fatal("expected duplicate-key concurrent build to fail")
			}
			if err := runStableKeyBackfill(context.Background(), pool, 1, 0); err != nil {
				t.Fatal(err)
			}
			var valid bool
			if err := pool.QueryRow(context.Background(), `SELECT indisvalid FROM pg_index WHERE indexrelid='test_cases_stable_key_idx'::regclass`).Scan(&valid); err != nil {
				t.Fatal(err)
			}
			if !valid {
				t.Fatal("invalid index survived rerun")
			}
			execSQL(t, pool, `UPDATE test_cases SET project='chrome' WHERE ordinal=0`)
			var key string
			if err := pool.QueryRow(context.Background(), `SELECT stable_key FROM test_cases WHERE ordinal=0`).Scan(&key); err != nil {
				t.Fatal(err)
			}
			if key != "chrome :: MM-T123_4" {
				t.Fatalf("upgraded trigger returned %q", key)
			}
		})
	}
}

type observationFixture struct {
	run, attempt, name, key, branch string
	framework                       string
	pr                              *int
	age                             time.Duration
	statuses                        []string
	complete                        bool
}

func addObservation(t *testing.T, pool *pgxpool.Pool, f observationFixture) {
	t.Helper()
	if f.framework == "" {
		f.framework = "playwright"
	}
	if f.branch == "" {
		f.branch = "master"
	}
	if f.attempt == "" {
		f.attempt = "1"
	}
	status := "completed"
	expected := 1
	if !f.complete {
		status = "incomplete"
		expected = 2
	}
	var groupID, reportID, suiteID string
	if err := pool.QueryRow(context.Background(), `INSERT INTO report_groups(framework,name,repository,branch,commit_sha,
	 gh_run_id,gh_run_attempt,gh_pr_number,created_at,status,total_reports_expected)
	 VALUES($9,$1,'mattermost/mattermost',$2,$3,$3,$4,$5,$6,$7,$8) RETURNING id`,
		f.name, f.branch, f.run, f.attempt, f.pr, time.Now().Add(-f.age), status, expected, f.framework).Scan(&groupID); err != nil {
		t.Fatal(err)
	}
	if len(f.statuses) == 0 {
		return
	}
	if err := pool.QueryRow(context.Background(), `INSERT INTO reports(report_group_id,name,status) VALUES($1,$2,'complete') RETURNING id`, groupID, f.name).Scan(&reportID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `INSERT INTO suites(report_id,title,file,ordinal) VALUES($1,'suite','spec.ts',0) RETURNING id`, reportID).Scan(&suiteID); err != nil {
		t.Fatal(err)
	}
	for i, status := range f.statuses {
		execSQL(t, pool, `INSERT INTO test_cases(suite_id,title,full_title,external_test_id,project,status,ordinal)
	 VALUES($1,$2,$2,$2,'chrome',$3,$4)`, suiteID, f.key, status, i)
	}
}

func TestBaseline_MattermostSuiteMapping(t *testing.T) {
	pool, _ := database(t, 30)
	pr := 123
	for _, framework := range []string{"playwright", "cypress"} {
		for _, edition := range []string{"enterprise", "fips"} {
			name := framework + "-full-" + edition
			addObservation(t, pool, observationFixture{framework: framework, run: name + "-master", name: name + "-master", key: "MM-T1", age: 24 * time.Hour, statuses: []string{"failed"}, complete: true})
			addObservation(t, pool, observationFixture{framework: framework, run: name + "-pr", name: name, key: "MM-T1", pr: &pr, age: time.Hour, statuses: []string{"failed"}, complete: true})
		}
	}
	// A FIPS failure cannot explain an enterprise failure of the same key.
	addObservation(t, pool, observationFixture{run: "clean-enterprise", name: "playwright-full-enterprise-master", key: "MM-T2", age: 24 * time.Hour, statuses: []string{"passed"}, complete: true})
	addObservation(t, pool, observationFixture{run: "failed-fips", name: "playwright-full-fips-master", key: "MM-T2", age: 24 * time.Hour, statuses: []string{"failed"}, complete: true})
	addObservation(t, pool, observationFixture{run: "unexplained-enterprise", name: "playwright-full-enterprise", key: "MM-T2", pr: &pr, age: time.Hour, statuses: []string{"failed"}, complete: true})
	raw, err := readBaseline(context.Background(), pool, "mattermost/mattermost", "master", 30, 1)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Policy []struct {
			Failed     int `json:"observed_failed_pr_run_attempts"`
			Candidates int `json:"policy_candidates"`
		} `json:"policy_replay"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Policy) != 3 {
		t.Fatal(string(raw))
	}
	for _, r := range result.Policy {
		if r.Failed != 5 || r.Candidates != 4 {
			t.Fatalf("Mattermost suite mapping or edition isolation failed: %s", raw)
		}
	}
	// --branch can select a different trusted branch. The master-specific
	// mapping must not replace that branch's existing exact-name behavior.
	addObservation(t, pool, observationFixture{run: "release-base", branch: "release-test", name: "playwright-full-enterprise", key: "MM-T3", age: 24 * time.Hour, statuses: []string{"failed"}, complete: true})
	addObservation(t, pool, observationFixture{run: "release-pr", name: "playwright-full-enterprise", key: "MM-T3", pr: &pr, age: time.Hour, statuses: []string{"failed"}, complete: true})
	raw, err = readBaseline(context.Background(), pool, "mattermost/mattermost", "release-test", 30, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	for _, r := range result.Policy {
		if r.Candidates != 1 {
			t.Fatalf("non-master baseline lost exact-name behavior: %s", raw)
		}
	}
}

func TestBaseline_RetestSurvivorIsNotARedRun(t *testing.T) {
	pool, _ := database(t, 30)
	pr := 123
	addObservation(t, pool, observationFixture{run: "master-failed", name: "smoke", key: "MM-T1", age: 48 * time.Hour, statuses: []string{"failed"}, complete: true})
	for _, f := range []observationFixture{
		{run: "master-survivor", name: "smoke", key: "MM-T1", age: 24 * time.Hour, statuses: []string{"failed"}, complete: true},
		{run: "pr-survivor", name: "smoke", key: "MM-T1", pr: &pr, age: time.Hour, statuses: []string{"failed"}, complete: true},
	} {
		addObservation(t, pool, f)
		// The original shard failed outright; a second complete shard reran
		// the same test successfully. Stored shard rollups differ, but the
		// report-group outcome must be flaky and must not count as PR red.
		execSQL(t, pool, `UPDATE test_cases tc SET attempts=1, attempts_failed=1, run_failed=true
		 FROM suites s, reports r, report_groups g
		 WHERE tc.suite_id=s.id AND s.report_id=r.id AND r.report_group_id=g.id AND g.gh_run_id=$1`, f.run)
		execSQL(t, pool, `UPDATE report_groups SET total_reports_expected=2 WHERE gh_run_id=$1`, f.run)
		var reportID, suiteID string
		if err := pool.QueryRow(context.Background(), `INSERT INTO reports(report_group_id,name,status)
		 SELECT id,'retest','complete' FROM report_groups WHERE gh_run_id=$1 RETURNING id`, f.run).Scan(&reportID); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(context.Background(), `INSERT INTO suites(report_id,title,file,ordinal)
		 VALUES($1,'suite','spec.ts',0) RETURNING id`, reportID).Scan(&suiteID); err != nil {
			t.Fatal(err)
		}
		execSQL(t, pool, `INSERT INTO test_cases(suite_id,title,full_title,external_test_id,project,status,ordinal,attempts,attempts_failed,run_failed)
		 VALUES($1,'MM-T1','MM-T1','MM-T1','chrome','passed',0,1,0,false)`, suiteID)
	}
	raw, err := readBaseline(context.Background(), pool, "mattermost/mattermost", "master", 30, 1)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Policy []struct {
			Failed int `json:"observed_failed_pr_run_attempts"`
		} `json:"policy_replay"`
		Rates []struct {
			Failed int `json:"failed"`
			Flaky  int `json:"retry_survivors"`
		} `json:"master_test_rates_by_suite"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Policy) != 3 || len(result.Rates) != 1 || result.Rates[0].Failed != 1 || result.Rates[0].Flaky != 1 {
		t.Fatalf("retest survivor changed raw master rate: %s", raw)
	}
	for _, r := range result.Policy {
		if r.Failed != 0 {
			t.Fatalf("retest survivor counted as a failed PR run: %s", raw)
		}
	}
}

func TestBaseline(t *testing.T) {
	pool, _ := database(t, 30)
	pr := 123
	add := func(f observationFixture) { addObservation(t, pool, f) }
	// Same test in different configurations must not share a baseline.
	for i := 0; i < 5; i++ {
		add(observationFixture{run: fmt.Sprintf("master-%d", i), name: "smoke", key: "MM-T1", age: time.Duration(10-i) * 24 * time.Hour, statuses: []string{"failed"}, complete: true})
		add(observationFixture{run: fmt.Sprintf("master-%d", i), name: "enterprise", key: "MM-T1", age: time.Duration(10-i) * 24 * time.Hour, statuses: []string{"passed"}, complete: true})
	}
	add(observationFixture{run: "master-clean", name: "smoke", key: "MM-T1", age: 11 * 24 * time.Hour, statuses: []string{"passed"}, complete: true})
	add(observationFixture{run: "p1", attempt: "1", name: "smoke", key: "MM-T1", pr: &pr, age: 3 * time.Hour, statuses: []string{"failed"}, complete: true})
	add(observationFixture{run: "p1", attempt: "2", name: "smoke", key: "MM-T1", pr: &pr, age: 2 * time.Hour, statuses: []string{"failed"}, complete: true})
	add(observationFixture{run: "p1", attempt: "2", name: "enterprise", key: "MM-T1", pr: &pr, age: 2 * time.Hour, statuses: []string{"failed"}, complete: true})
	add(observationFixture{run: "p2", name: "smoke", key: "MM-T1", pr: &pr, age: time.Hour, statuses: []string{"failed"}, complete: true})
	add(observationFixture{run: "p2", name: "missing-worker", pr: &pr, age: time.Hour, complete: false})
	add(observationFixture{run: "green-retry", name: "smoke", key: "MM-T1", pr: &pr, age: time.Hour, statuses: []string{"failed", "passed"}, complete: true})
	// An old intermittent issue reappearing inside the window is not an arrival.
	add(observationFixture{run: "old-clean", name: "old", key: "MM-T2", age: 45 * 24 * time.Hour, statuses: []string{"passed"}, complete: true})
	add(observationFixture{run: "old-fail", name: "old", key: "MM-T2", age: 40 * 24 * time.Hour, statuses: []string{"failed"}, complete: true})
	add(observationFixture{run: "old-reappears", name: "old", key: "MM-T2", age: 24 * time.Hour, statuses: []string{"failed"}, complete: true})
	// A separate suite's newer pass must not hide smoke's latest failure.
	add(observationFixture{run: "latest-other", name: "other", key: "MM-T3", age: time.Minute, statuses: []string{"passed"}, complete: true})
	raw, err := readBaseline(context.Background(), pool, "mattermost/mattermost", "master", 30, 5)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Measurement string `json:"measurement"`
		Policy      []struct {
			Failed     int `json:"observed_failed_pr_run_attempts"`
			Candidates int `json:"policy_candidates"`
		} `json:"policy_replay"`
		Coverage struct {
			Missing    int `json:"groups_without_test_rows"`
			Incomplete int `json:"incomplete_or_unknown_ingestion_groups"`
		} `json:"coverage"`
		Fanout struct {
			Attempts int `json:"registered_pr_run_attempts"`
			Max      int `json:"max"`
		} `json:"pr_group_fanout"`
		Patterns []struct {
			Name       string `json:"name"`
			Persistent int    `json:"persistent_failure_candidates"`
		} `json:"current_failure_patterns_by_suite"`
		Arrivals []struct {
			Name string `json:"name"`
		} `json:"observed_arrivals_by_suite"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result.Measurement != "stored_test_observations_and_policy_replay" {
		t.Fatal(string(raw))
	}
	for _, r := range result.Policy {
		if r.Failed != 3 || r.Candidates != 1 {
			t.Fatalf("retry/attempt/suite/coverage replay defect: %s", raw)
		}
	}
	if len(result.Policy) != 3 || result.Coverage.Missing != 1 || result.Coverage.Incomplete != 1 || result.Fanout.Attempts != 4 || result.Fanout.Max != 2 {
		t.Fatalf("coverage mismatch: %s", raw)
	}
	found := false
	for _, p := range result.Patterns {
		if p.Name == "smoke" && p.Persistent == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("latest per-suite failure hidden: %s", raw)
	}
	for _, a := range result.Arrivals {
		if a.Name == "old" {
			t.Fatalf("old failure counted as arrival: %s", raw)
		}
	}
}
