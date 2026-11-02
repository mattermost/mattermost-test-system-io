//go:build e2e
// +build e2e

package reportse2e

import (
	"context"
	"crypto/md5" //nolint:gosec // matches the corpus's md5() commit SHAs; not security
	"fmt"
	"net/url"
	"os"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/mattermost/mattermost-test-system-io/apps/server/internal/auth/policy"
	"github.com/mattermost/mattermost-test-system-io/apps/server/tests/e2e/testenv"
)

// Corpus shape, from mattermost's Playwright lanes: ~400 spec files and ~1,300 tests per run, spread
// over 20 shards, several runs a day per lane.
const (
	benchLanes          = 10
	benchRunsPerLane    = 360 // 60 days at 6 runs a day
	benchShards         = 20
	benchFilesPerShard  = 20
	benchTestsPerFile   = 3
	benchRequests       = 20
	benchBudgetTypical  = 200 * time.Millisecond // p95, 50 failing tests
	benchBudgetMassFail = time.Second            // p95, 500 failing tests
)

// TestHistorySummaryBenchmark loads a production-scale corpus and times the summary endpoint. It takes
// minutes and a few GB of Docker disk, so it runs only when asked:
//
//	TSIO_BENCH=1 go test -tags=e2e -timeout 30m -run HistorySummaryBenchmark -v ./tests/e2e/reports/
func TestHistorySummaryBenchmark(t *testing.T) {
	if os.Getenv("TSIO_BENCH") == "" {
		t.Skip("production-scale benchmark; set TSIO_BENCH=1 to run it")
	}
	env := testenv.Start(t)
	ctx := context.Background()
	viewer := env.IssueAPIKeyWithRole(t, "bench", policy.RoleViewer)

	start := time.Now()
	for _, step := range []string{
		`INSERT INTO report_groups (framework, name, repository, branch, commit_sha, gh_run_id, gh_run_attempt, status, created_at)
		 SELECT 'playwright', 'lane-' || l, 'mattermost/mattermost', 'master', md5(r::text), 'run-' || r, '1', 'completed',
		        now() - r * interval '4 hours' - l * interval '1 minute'
		 FROM generate_series(0, ` + strconv.Itoa(benchRunsPerLane-1) + `) r, generate_series(0, ` + strconv.Itoa(benchLanes-1) + `) l`,
		`INSERT INTO reports (report_group_id, name, gh_job_name, status)
		 SELECT g.id, 'chrome-' || k, 'chrome-' || k, 'complete'
		 FROM report_groups g, generate_series(1, ` + strconv.Itoa(benchShards) + `) k`,
		`INSERT INTO suites (report_id, title, file, ordinal)
		 SELECT r.id, 'Suite ' || f, 'specs/f' || f || '.spec.ts', j
		 FROM reports r, generate_series(1, ` + strconv.Itoa(benchFilesPerShard) + `) j,
		      LATERAL (SELECT (split_part(r.name, '-', 2)::int - 1) * ` + strconv.Itoa(benchFilesPerShard) + ` + j AS f) x`,
		// About 1% of tries fail and 1% are flaky, so every test has some history of each.
		`INSERT INTO test_cases (suite_id, title, full_title, status, retry_count, ordinal)
		 SELECT s.id, 'test ' || t, s.title || ' > test ' || t,
		        CASE abs(hashtext(s.id::text || t)) % 100 WHEN 0 THEN 'failed' WHEN 1 THEN 'flaky' ELSE 'passed' END, 0, t
		 FROM suites s, generate_series(1, ` + strconv.Itoa(benchTestsPerFile) + `) t`,
		`VACUUM ANALYZE`,
	} {
		if _, err := env.Pool.Exec(ctx, step); err != nil {
			t.Fatalf("load corpus: %v", err)
		}
	}
	var groups, suites, cases int
	if err := env.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM report_groups), (SELECT count(*) FROM suites), (SELECT count(*) FROM test_cases)`).
		Scan(&groups, &suites, &cases); err != nil {
		t.Fatal(err)
	}
	t.Logf("corpus: %d groups, %d suites, %d test cases, loaded in %s", groups, suites, cases, time.Since(start).Round(time.Second))

	for _, tc := range []struct {
		name    string
		failing int
		budget  time.Duration
	}{
		{"typical run, 50 failing tests", 50, benchBudgetTypical},
		{"mass failure, 500 failing tests", 500, benchBudgetMassFail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The newest run of lane-0 fails tc.failing tests, one per spec file in turn.
			if _, err := env.Pool.Exec(ctx, `
				UPDATE test_cases tc SET status = CASE WHEN tc.id IN (
					SELECT tc2.id FROM test_cases tc2
					JOIN suites s ON s.id = tc2.suite_id
					JOIN reports r ON r.id = s.report_id
					JOIN report_groups g ON g.id = r.report_group_id
					WHERE g.name = 'lane-0' AND g.gh_run_id = 'run-0'
					ORDER BY tc2.ordinal, s.file LIMIT $1) THEN 'failed' ELSE 'passed' END
				FROM suites s, reports r, report_groups g
				WHERE tc.suite_id = s.id AND s.report_id = r.id AND r.report_group_id = g.id
				  AND g.name = 'lane-0' AND g.gh_run_id = 'run-0'`, tc.failing); err != nil {
				t.Fatal(err)
			}
			params := url.Values{
				"repository": {"mattermost"}, "branch": {"master"}, "name": {"lane-0"},
				"commit": {md5Hex(0)}, "gh_run_id": {"run-0"}, "gh_run_attempt": {"1"},
				"compare_names": {"lane-1"},
			}
			var took []time.Duration
			// Sequential, and 40 requests in all: inside the key's concurrency and per-minute limits.
			for range benchRequests {
				begin := time.Now()
				code, out, _ := getSummary(t, env, viewer, params)
				took = append(took, time.Since(begin))
				if code != 200 || out.TotalTests != tc.failing {
					t.Fatalf("status %d, %d tests; want 200 and %d", code, out.TotalTests, tc.failing)
				}
			}
			slices.Sort(took)
			p50, p95 := took[len(took)/2], took[len(took)*95/100]
			t.Logf("%d requests: p50 %s, p95 %s, max %s", len(took), p50.Round(time.Millisecond), p95.Round(time.Millisecond), took[len(took)-1].Round(time.Millisecond))
			if p95 > tc.budget {
				t.Errorf("p95 %s is over the %s budget", p95, tc.budget)
			}
		})
	}

	// The plan of the widest read — case rows of 40 lane groups for 50 spec files — with bound
	// parameters, as the handler sends them.
	var groupIDs []string
	if err := env.Pool.QueryRow(ctx, `
		SELECT array_agg(id::text) FROM (SELECT id FROM report_groups WHERE name = 'lane-0' ORDER BY created_at DESC LIMIT 40) g`).
		Scan(&groupIDs); err != nil {
		t.Fatal(err)
	}
	files := make([]string, 50)
	for i := range files {
		files[i] = fmt.Sprintf("specs/f%d.spec.ts", i+1)
	}
	rows, err := env.Pool.Query(ctx, `
		EXPLAIN (ANALYZE, BUFFERS)
		SELECT rp.report_group_id, rp.id, s.file, COALESCE(s.title, '') || ' › ' || tc.title, tc.status
		FROM reports rp
		JOIN suites s ON s.report_id = rp.id
		JOIN test_cases tc ON tc.suite_id = s.id
		WHERE rp.report_group_id = ANY($1::uuid[])
		  AND ($2::text[] IS NULL OR s.file = ANY($2))`, groupIDs, files)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		t.Log(line)
	}
}

// md5Hex is Postgres's md5(n::text), the corpus's commit SHA for run n.
func md5Hex(n int) string {
	return fmt.Sprintf("%x", md5.Sum([]byte(strconv.Itoa(n))))
}
